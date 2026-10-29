#!/usr/bin/env python3
"""Qualify correctness-file concurrency; never runs the launch-performance suite."""
import argparse
from collections import Counter
import json
import math
import os
from pathlib import Path
import platform
import select
import signal
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]


def specifications(report):
    def walk(suite):
        yield from suite.get("specs", [])
        for child in suite.get("suites", []):
            yield from walk(child)
    return list(walk(report))


def inventory(report):
    return Counter(spec["title"] for spec in specifications(report))


def validate_run(report, expected):
    if report.get("errors") or inventory(report) != expected:
        raise ValueError("Discovery errors or changed correctness test inventory")
    for spec in specifications(report):
        tests = spec["tests"]
        if len(tests) != 1:
            raise ValueError("Expected exactly one project execution per scenario")
        test = tests[0]
        results = test["results"]
        if (test["expectedStatus"] != "passed" or test["status"] != "expected"
                or len(results) != 1 or results[0]["status"] != "passed"
                or results[0]["retry"] != 0):
            raise ValueError("Every scenario must pass once without skips or retries")


SAMPLE_INTERVAL_SECONDS = 0.05
TERM_GRACE_SECONDS = 0.2
CLEANUP_TIMEOUT_SECONDS = 2


def process_stat(path):
    fields = path.read_text().rsplit(")", 1)[1].split()
    return int(fields[1]), int(fields[3]), int(fields[19]), int(fields[21])


class WorkloadMemory:
    """Sample an owned session and verified descendants.

    The caller keeps the direct session leader unreaped through the last scan.
    That pins the original session ID, unlike descriptors for other processes.
    """

    def __init__(self, root_pid, proc=Path("/proc")):
        self.root_pid = root_pid
        self.proc = proc
        self.known = set()
        self.samples = 0
        self.peak_kib = 0
        self.max_processes = 0
        self.root_seen = False
        self.last_sample = None
        self.max_gap_seconds = 0.0

    def verified_child(self, pid, row, parent_row):
        """Require a live parent identity throughout the fresh child PPID read."""
        parent, _, started, _ = row
        handle = None
        try:
            handle = os.pidfd_open(parent)
            poller = select.poll()
            poller.register(handle, select.POLLIN)
            if poller.poll(0):
                return None
            before = process_stat(self.proc / str(parent) / "stat")
            if before[2] != parent_row[2]:
                return None
            current = process_stat(self.proc / str(pid) / "stat")
            after = process_stat(self.proc / str(parent) / "stat")
            # A pidfd refers to one process, but does not reserve its numeric
            # PID after exit. Liveness must span the PPID observation; matching
            # the child's own identity alone cannot establish its ancestry.
            if (poller.poll(0) or after[2] != parent_row[2]
                    or current[0] != parent or current[2] != started):
                return None
            return current
        except (FileNotFoundError, ProcessLookupError):
            return None
        finally:
            if handle is not None:
                os.close(handle)

    def owned_processes(self):
        processes = {}
        # Read all visible process metadata to find children that start new sessions.
        # Only ENOENT/ESRCH races are harmless; denied or malformed reads fail closed.
        for path in self.proc.iterdir():
            if not path.name.isdecimal():
                continue
            try:
                processes[int(path.name)] = process_stat(path / "stat")
            except (FileNotFoundError, ProcessLookupError):
                continue
        selected = {pid for pid, (_, session, started, _) in processes.items()
                    if session == self.root_pid or (pid, started) in self.known}
        while True:
            children = {}
            for pid, row in processes.items():
                if pid not in selected and row[0] in selected:
                    current = self.verified_child(pid, row, processes[row[0]])
                    if current is not None:
                        children[pid] = current
            if not children:
                break
            processes.update(children)
            selected.update(children)
        if not selected:
            raise RuntimeError("No workload processes visible in /proc")
        self.known.update((pid, processes[pid][2]) for pid in selected)
        return {pid: processes[pid] for pid in selected}

    def sample(self):
        processes = self.owned_processes()
        self.root_seen |= self.root_pid in processes
        rss = sum(row[3] for row in processes.values())
        if rss < 0:
            raise RuntimeError("Invalid workload RSS in /proc")
        self.peak_kib = max(self.peak_kib, rss * os.sysconf("SC_PAGE_SIZE") // 1024)
        self.max_processes = max(self.max_processes, len(processes))
        self.samples += 1
        now = time.monotonic()
        if self.last_sample is not None:
            self.max_gap_seconds = max(self.max_gap_seconds, now - self.last_sample)
        self.last_sample = now

    def metrics(self):
        if not self.root_seen or not self.peak_kib or self.max_processes < 2:
            raise RuntimeError("Memory coverage requires root and concurrent child samples with positive RSS")
        return {"peak_workload_rss_kib": self.peak_kib,
                "memory_samples": self.samples,
                "max_sampled_processes": self.max_processes,
                "max_sample_gap_seconds": self.max_gap_seconds}


def cleanup_workload(process, memory):
    """Keep the unreaped leader's group pinned; target other members by pidfd."""
    handles, errors = {}, []

    def capture():
        try:
            memory.owned_processes()
        except (OSError, ValueError, IndexError, RuntimeError) as error:
            errors.append(str(error))
        # Retain already observed identities even if the final scan failed.
        for pid, started in memory.known:
            if pid == process.pid or (pid, started) in handles:
                continue
            handle = None
            try:
                handle = os.pidfd_open(pid)
                # Opening precedes revalidation: PID reuse cannot redirect a
                # subsequent signal to a different process after this check.
                if process_stat(memory.proc / str(pid) / "stat")[2] == started:
                    handles[pid, started] = handle
                    handle = None
            except (FileNotFoundError, ProcessLookupError):
                pass
            except (OSError, ValueError, IndexError) as error:
                errors.append(f"Cannot safely target descendant {pid}: {error}")
            finally:
                if handle is not None:
                    os.close(handle)

    def deliver(signum):
        # No other numeric group ID is safe: its leader might already be reaped.
        try:
            os.killpg(process.pid, signum)
        except ProcessLookupError:
            pass
        except OSError as error:
            errors.append(str(error))
        for handle in handles.values():
            try:
                signal.pidfd_send_signal(handle, signum)
            except ProcessLookupError:
                pass
            except OSError as error:
                errors.append(str(error))

    try:
        try:
            capture()
            deliver(signal.SIGTERM)
            time.sleep(TERM_GRACE_SECONDS)
            capture()
        finally:
            # Even failed collection must terminate the pinned original group
            # and every safely captured target. Never use Popen's unbounded exit.
            deliver(signal.SIGKILL)
            deadline = time.monotonic() + CLEANUP_TIMEOUT_SECONDS
            try:
                poller = select.poll()
                for handle in handles.values():
                    poller.register(handle, select.POLLIN)
                pending = set(handles.values())
                while pending:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        errors.append("Owned descendants exceeded the cleanup deadline")
                        break
                    for handle, event in poller.poll(max(1, int(remaining * 1000))):
                        if event & (select.POLLIN | select.POLLHUP):
                            pending.discard(handle)
                            poller.unregister(handle)
            finally:
                try:
                    process.wait(timeout=max(0, deadline - time.monotonic()))
                except subprocess.TimeoutExpired:
                    errors.append("Owned command exceeded the cleanup deadline")
    finally:
        for handle in handles.values():
            os.close(handle)
    if errors:
        raise RuntimeError("Workload cleanup failed: " + "; ".join(errors))


def measure(command, **kwargs):
    if sys.platform != "linux" or not Path("/proc/self/stat").is_file():
        raise RuntimeError("Workload memory collection requires readable Linux /proc")
    try:
        handle = os.pidfd_open(os.getpid())
        try:
            signal.pidfd_send_signal(handle, 0)
        finally:
            os.close(handle)
    except (AttributeError, OSError) as error:
        raise RuntimeError(f"Workload cleanup requires Linux pidfd support: {error}") from error
    interrupted = None
    handlers = {}

    def interrupt(signum, frame):
        nonlocal interrupted
        interrupted = interrupted or signum

    def check_interruption():
        if interrupted == signal.SIGINT:
            raise KeyboardInterrupt
        if interrupted is not None:
            raise SystemExit(128 + interrupted)

    try:
        # Record signals during Popen as well; raising there can lose the PID.
        for signum in (signal.SIGINT, signal.SIGTERM):
            handlers[signum] = signal.signal(signum, interrupt)
        process = subprocess.Popen(command, start_new_session=True, **kwargs)
        memory = WorkloadMemory(process.pid)
        failure = None
        try:
            while True:
                check_interruption()
                # Do not reap until the final group signal, even on normal exit.
                if os.waitid(os.P_PID, process.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT) is not None:
                    break
                if failure is None:
                    try:
                        memory.sample()
                    except (OSError, ValueError, IndexError, RuntimeError) as error:
                        failure = error
                time.sleep(SAMPLE_INTERVAL_SECONDS)
        finally:
            try:
                cleanup_workload(process, memory)
            except RuntimeError as error:
                if not interrupted:
                    raise
                print(str(error), file=kwargs.get("stderr") or sys.stderr, flush=True)
        if failure is not None:
            raise RuntimeError(f"Workload memory collection failed: {failure}") from failure
        return process.returncode, memory.metrics()
    finally:
        for signum, handler in handlers.items():
            signal.signal(signum, handler)
        # Cancellation during cleanup also takes precedence over success/failure.
        check_interruption()


def discover(command, output, **kwargs):
    stdout_path, stderr_path = output / "discovery.json", output / "discovery.stderr.log"
    with stdout_path.open("w") as stdout, stderr_path.open("w") as stderr:
        result = subprocess.run(command, stdout=stdout, stderr=stderr, **kwargs)
    diagnostics = f"stdout: {stdout_path}; stderr: {stderr_path}"
    if result.returncode:
        raise RuntimeError(f"Correctness discovery exited {result.returncode}; {diagnostics}")
    try:
        return json.loads(stdout_path.read_text())
    except ValueError as error:
        raise ValueError(f"Invalid discovery JSON; {diagnostics}: {error}") from error


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--baseline-inventory", type=Path)
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    env = dict(os.environ, CODE_FACTORY_REAL_CODEX="0")
    command = ["npx", "playwright", "test", "--retries=0", "--reporter=json"]
    report = discover(command + ["--list"], output, cwd=ROOT / "web", env=env)
    expected = inventory(report)
    if not expected or report.get("errors"):
        raise ValueError("Empty or invalid correctness inventory")
    if args.baseline_inventory:
        baseline = json.loads(args.baseline_inventory.read_text())
        if baseline.get("errors") or inventory(baseline) != expected:
            raise ValueError("Correctness inventory differs from the baseline")
    (output / "inventory.json").write_text(json.dumps(sorted(expected.elements()), indent=2) + "\n")
    summary = {
        "version": 2,
        "source": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
        "tracked_changes": bool(subprocess.check_output(["git", "diff", "HEAD", "--name-only"], cwd=ROOT)),
        "platform": platform.system(),
        "node": subprocess.check_output(["node", "--version"], text=True).strip(),
        "playwright": subprocess.check_output(["npx", "playwright", "--version"], cwd=ROOT / "web", text=True).strip(),
        "conditions": "prepared dependencies/build; fresh browser contexts and testhost per run; OS/toolchain caches retained",
        "rss_boundary": "GNU time maximum individual-process RSS in KiB, not aggregate process-tree memory",
        "workload_rss_boundary": "Sampled sum of Linux /proc RSS for the new command session plus observed descendants, including descendants starting new sessions; shared pages count per process; processes that exit or escape and reparent between samples may be missed",
        "memory_sample_interval_seconds": SAMPLE_INTERVAL_SECONDS,
        "scenarios": sum(expected.values()),
        "runs": [],
    }
    def save():
        (output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    save()
    # Reversed second pair reduces a consistent cache/order advantage.
    for index, workers in enumerate([1, 2, 2, 1], 1):
        run_dir = output / f"run-{index}-workers-{workers}"
        run_dir.mkdir()
        entry = {"workers": workers, "outcome": "started"}
        summary["runs"].append(entry)
        save()
        metrics = run_dir / "time.txt"
        with (run_dir / "report.json").open("w") as stdout, (run_dir / "stderr.log").open("w") as stderr:
            try:
                exit_code, memory_metrics = measure(
                    ["/usr/bin/time", "-f", "%e %M", "-o", str(metrics),
                     *command, f"--workers={workers}", "--output", str(run_dir / "failures")],
                    cwd=ROOT / "web", env=env, stdout=stdout, stderr=stderr)
            except (KeyboardInterrupt, SystemExit) as error:
                entry.update(outcome="interrupted", exit_code=130 if isinstance(error, KeyboardInterrupt) else error.code)
                save()
                raise
            except RuntimeError as error:
                entry["outcome"] = "failed"
                entry["error"] = str(error)
                save()
                raise RuntimeError(f"{error}; retained {run_dir}") from error
        entry["exit_code"] = exit_code
        entry.update(memory_metrics)
        entry["outcome"] = "failed"
        save()
        if exit_code:
            raise RuntimeError(f"Correctness run {index} failed; retained {run_dir}")
        seconds, rss = metrics.read_text().split()
        seconds, rss = float(seconds), int(rss)
        if not math.isfinite(seconds) or seconds < 0 or rss < 0:
            raise ValueError(f"Invalid correctness metrics; retained {run_dir}")
        entry.update(seconds=seconds, max_process_rss_kib=rss)
        validate_run(json.loads((run_dir / "report.json").read_text()), expected)
        entry["outcome"] = "passed"
        save()
        print(json.dumps(entry), flush=True)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
