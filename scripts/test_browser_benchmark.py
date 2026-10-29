import copy
from contextlib import redirect_stdout
import io
import json
import unittest
from pathlib import Path
import subprocess
import sys
import tempfile
from unittest.mock import patch

import benchmark_browser as benchmark

from benchmark_browser import discover, inventory, validate_run

import ctypes
import os
import signal
import time
from unittest import mock
from benchmark_browser import WorkloadMemory, measure

WORKLOAD = r"""
import json, os, pathlib, signal, subprocess, sys, time
state = pathlib.Path(sys.argv[1])
signal.signal(signal.SIGTERM, signal.SIG_IGN)
leaf = "import pathlib,signal,sys,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); pathlib.Path(sys.argv[1]).touch(); time.sleep(30)"
a = subprocess.Popen([sys.executable, '-c', leaf, str(state / 'same-ready')])
b = subprocess.Popen([sys.executable, '-c', leaf, str(state / 'escaped-ready')], start_new_session=True)
(state / 'pids.json').write_text(json.dumps([os.getpid(), a.pid, b.pid]))
while not (state / 'allow-exit').exists():
    time.sleep(.01)
"""
def cancellation_probe(state, signum, phase):
    # This isolated harness alone adopts orphaned fixture children for reaping.
    if ctypes.CDLL(None, use_errno=True).prctl(36, 1, 0, 0, 0):
        raise OSError(ctypes.get_errno(), "test subreaper setup failed")
    check = unittest.TestCase()
    prior = {s: signal.getsignal(s) for s in (signal.SIGINT, signal.SIGTERM)}
    sentinel = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(30)'])
    real_owned, real_killpg = WorkloadMemory.owned_processes, os.killpg
    real_popen = subprocess.Popen

    def observed(memory):
        processes = real_owned(memory)
        # Simulate a remembered PID now belonging to a different process.
        memory.known.add((sentinel.pid, -1))
        if (state / 'same-ready').exists() and (state / 'escaped-ready').exists():
            pids = json.loads((state / 'pids.json').read_text())
            if set(pids) <= processes.keys():
                (state / 'observed').touch()
                if phase == 'stalled':
                    # Deliberately break cooperation inside the production call.
                    # Its recording handlers cannot unwind this stalled hook.
                    while True:
                        time.sleep(1)
        return processes

    def synchronized_term(pid, delivered):
        real_killpg(pid, delivered)
        if phase == 'cleanup' and delivered == signal.SIGTERM:
            (state / 'cleanup-ready').touch()
            deadline = time.monotonic() + 5
            while not (state / 'signal-sent').exists():
                check.assertLess(time.monotonic(), deadline, 'controller did not release cleanup')
                time.sleep(.001)

    def synchronized_launch(*args, **kwargs):
        process = real_popen(*args, **kwargs)
        if phase == 'launch':
            # The real child exists, but Popen has not returned its handle to
            # measure. Deliver a real signal while only its recording handler
            # can preserve ownership for the finally path that follows.
            deadline = time.monotonic() + 5
            while not ((state / 'same-ready').exists() and (state / 'escaped-ready').exists()):
                check.assertLess(time.monotonic(), deadline, 'launch fixture not ready')
                time.sleep(.001)
            (state / 'launch-ready').touch()
            while not (state / 'signal-sent').exists():
                check.assertLess(time.monotonic(), deadline, 'controller did not interrupt launch')
                time.sleep(.001)
        return process

    output = state / 'qualification'
    report = {'suites': [{'specs': [{'title': 'fixture'}]}]}
    cancellation = None
    try:
        with mock.patch.object(benchmark, 'discover', return_value=report), \
                mock.patch.object(benchmark.subprocess, 'check_output', return_value='fixture'), \
                mock.patch.object(benchmark, 'measure', side_effect=lambda command, **kw:
                                  measure([sys.executable, '-c', WORKLOAD, str(state)], **kw)), \
                mock.patch.object(WorkloadMemory, 'owned_processes', observed), \
                mock.patch.object(benchmark.subprocess, 'Popen', side_effect=synchronized_launch), \
                mock.patch.object(benchmark.os, 'killpg', side_effect=synchronized_term), \
                mock.patch.object(sys, 'argv', ['benchmark', '--output', str(output)]):
            try:
                benchmark.main()
            except (KeyboardInterrupt, SystemExit) as error:
                cancellation = error
        if signum == signal.SIGINT:
            check.assertIsInstance(cancellation, KeyboardInterrupt)
        else:
            check.assertIsInstance(cancellation, SystemExit)
            check.assertEqual(cancellation.code, 143)
        check.assertEqual({s: signal.getsignal(s) for s in prior}, prior)
        summary = json.loads((output / 'summary.json').read_text())
        check.assertEqual(len(summary['runs']), 1)
        check.assertEqual(summary['runs'][0]['outcome'], 'interrupted')
        check.assertEqual(summary['runs'][0]['exit_code'], 128 + signum)
        check.assertIsNone(sentinel.poll(), 'unrelated sentinel was terminated')
        leader, *children = json.loads((state / 'pids.json').read_text())
        with check.assertRaises(ChildProcessError):
            os.waitpid(leader, os.WNOHANG)
        for pid in children:
            deadline = time.monotonic() + 2
            while os.waitpid(pid, os.WNOHANG) == (0, 0):
                check.assertLess(time.monotonic(), deadline, 'owned child survived cleanup')
                time.sleep(.01)
        print('bounded cleanup; handlers restored; sentinel alive; interrupted summary retained', flush=True)
    finally:
        sentinel.kill()
        sentinel.wait(timeout=2)
        if (state / 'pids.json').exists():
            # Only unreaped children of this harness can authorize fallback
            # signals; this never trusts a formerly owned numeric PID alone.
            for pid in json.loads((state / 'pids.json').read_text()):
                try:
                    os.waitid(os.P_PID, pid, os.WEXITED | os.WNOHANG | os.WNOWAIT)
                    os.kill(pid, signal.SIGKILL)
                    os.waitpid(pid, 0)
                except (ChildProcessError, ProcessLookupError):
                    pass
    if cancellation is not None:
        raise cancellation
def reap_probe_children(timeout=2):
    """Independent emergency cleanup inside an isolated subreaper controller."""
    deadline = time.monotonic() + timeout
    killed = set()
    while True:
        # Kill only our direct, unreaped children. Orphaned grandchildren become
        # direct children after their parent dies, even across session boundaries.
        children = Path(f'/proc/self/task/{os.getpid()}/children').read_text().split()
        if not children:
            return killed
        for text in children:
            pid = int(text)
            try:
                os.waitid(os.P_PID, pid, os.WEXITED | os.WNOHANG | os.WNOWAIT)
                # Being our unreaped child pins this PID through the signal.
                os.kill(pid, signal.SIGKILL)
                killed.add(pid)
                os.waitpid(pid, os.WNOHANG)
            except (ChildProcessError, ProcessLookupError):
                pass
        if time.monotonic() >= deadline:
            raise AssertionError('emergency probe cleanup exceeded its deadline')
        time.sleep(.005)
def signal_case(state, signum, phase):
    # Keep adoption and emergency cleanup out of unittest's shared process.
    if ctypes.CDLL(None, use_errno=True).prctl(36, 1, 0, 0, 0):
        raise OSError(ctypes.get_errno(), 'controller subreaper setup failed')
    check = unittest.TestCase()
    stalled = phase == 'stalled'
    process = subprocess.Popen([sys.executable, __file__, '--signal-probe',
                                str(state), str(signum), phase],
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    started = time.monotonic()
    timed_out = False
    try:
        def ready(name):
            while not (state / name).exists():
                check.assertIsNone(process.poll(), 'probe exited before readiness')
                check.assertLess(time.monotonic() - started, 8, 'probe not ready')
                time.sleep(.005)
        ready('launch-ready' if phase == 'launch' else 'observed')
        if phase == 'cleanup':
            (state / 'allow-exit').touch()
            ready('cleanup-ready')
        os.kill(process.pid, signum)
        (state / 'signal-sent').touch()
        try:
            stdout, stderr = process.communicate(timeout=.1 if stalled else 8)
        except subprocess.TimeoutExpired:
            timed_out = True
            if not stalled:
                raise
        else:
            check.assertFalse(stalled, 'forced stall unexpectedly cooperated')
            check.assertIn(process.returncode, (128 + signum, -signum), stdout + stderr)
            check.assertIn('bounded cleanup; handlers restored; sentinel alive; interrupted summary retained', stdout)
            print(stdout, end='')
    finally:
        (state / 'signal-sent').touch()
        if process.poll() is None:
            process.terminate()
        try:
            process.communicate(timeout=.1 if stalled else 8)
        except subprocess.TimeoutExpired:
            # Independence matters: the code under test may never handle TERM.
            # The controller kills/reaps its own children without calling it.
            pass
        finally:
            killed = reap_probe_children()
            process.communicate(timeout=2)
    if stalled:
        check.assertTrue(timed_out)
        check.assertLess(time.monotonic() - started, 5)
        check.assertIn(process.pid, killed)
        check.assertTrue(set(json.loads((state / 'pids.json').read_text())) <= killed)
        check.assertEqual(Path(f'/proc/self/task/{os.getpid()}/children').read_text().strip(), '')
        print('forced timeout recovered; probe and all adopted fixtures killed and reaped', flush=True)


def read_failure_probe(state, stalled):
    check = unittest.TestCase()
    read_fd, write_fd = os.pipe()
    samples = 0

    def sample():
        nonlocal samples
        samples += 1
        if samples == 2:
            # The child cannot finish before this injected read failure.
            os.write(write_fd, b"x")
            raise PermissionError("denied")

    try:
        with mock.patch.object(WorkloadMemory, "sample", side_effect=(lambda: None) if stalled else sample):
            with check.assertRaisesRegex(RuntimeError, "memory collection failed: denied"):
                measure([sys.executable, "-c", f"import os,pathlib; p=pathlib.Path({str(state / 'child.tmp')!r}); p.write_text(str(os.getpid())); p.rename({str(state / 'child.pid')!r}); os.read({read_fd}, 1)"],
                        pass_fds=(read_fd,))
        check.assertEqual(samples, 2)
        print("second-sample read failure rejected", flush=True)
    finally:
        os.close(write_fd)
        os.close(read_fd)

def read_failure_case(state, stalled):
    # Independently own and reap the probe and its pipe-blocked child even if
    # the production sampling callback or cleanup never makes progress.
    if ctypes.CDLL(None, use_errno=True).prctl(36, 1, 0, 0, 0):
        raise OSError(ctypes.get_errno(), 'controller subreaper setup failed')
    check = unittest.TestCase()
    process = subprocess.Popen([sys.executable, __file__, '--read-failure-probe',
                                str(state), str(int(stalled))],
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    timed_out = False
    try:
        if stalled:
            deadline = time.monotonic() + 8
            while not (state / 'child.pid').exists():
                check.assertIsNone(process.poll(), 'probe exited before pipe child readiness')
                check.assertLess(time.monotonic(), deadline, 'pipe child did not become ready')
                time.sleep(.005)
        try:
            stdout, stderr = process.communicate(timeout=.1 if stalled else 8)
        except subprocess.TimeoutExpired:
            timed_out = True
            if not stalled:
                raise
        else:
            check.assertFalse(stalled, 'missing callback unexpectedly completed')
            check.assertEqual(process.returncode, 0, stdout + stderr)
            check.assertIn('second-sample read failure rejected', stdout)
            print(stdout, end='')
    finally:
        # No production cancellation callback is needed for this fallback.
        killed = reap_probe_children()
        stdout, stderr = process.communicate(timeout=2)
        if timed_out:
            (state / 'probe.stdout').write_text(stdout)
            (state / 'probe.stderr').write_text(stderr)
    if stalled:
        check.assertTrue(timed_out)
        check.assertIn(process.pid, killed)
        check.assertIn(int((state / 'child.pid').read_text()), killed)
        check.assertEqual(Path(f'/proc/self/task/{os.getpid()}/children').read_text().strip(), '')
        print('missing callback bounded; probe and pipe child killed and reaped', flush=True)



class BrowserBenchmarkTests(unittest.TestCase):
    def report(self):
        return {"suites": [{"specs": [{"title": "scenario", "tests": [{
            "expectedStatus": "passed", "status": "expected",
            "results": [{"status": "passed", "retry": 0}],
        }]}]}], "errors": []}

    def test_file_move_preserves_inventory_and_duplicates_count(self):
        report = self.report()
        expected = inventory(report)
        moved = {"suites": [{"suites": report["suites"]}], "errors": []}
        validate_run(moved, expected)
        moved["suites"].append(copy.deepcopy(report["suites"][0]))
        with self.assertRaises(ValueError):
            validate_run(moved, expected)

    def test_failure_skip_retry_and_missing_test_cannot_pass(self):
        for status in ("failed", "skipped", "timedOut", "interrupted"):
            report = self.report()
            expected = inventory(report)
            report["suites"][0]["specs"][0]["tests"][0]["results"][0]["status"] = status
            with self.assertRaises(ValueError):
                validate_run(report, expected)
        for change in ("retry", "missing", "error"):
            report = self.report()
            expected = inventory(report)
            test = report["suites"][0]["specs"][0]["tests"][0]
            if change == "retry":
                test["results"][0]["retry"] = 1
            elif change == "missing":
                report["suites"] = []
            else:
                report["errors"] = [{"message": "discovery failed"}]
            with self.assertRaises(ValueError):
                validate_run(report, expected)


    def test_report_requires_one_project_and_result_and_expected_status(self):
        for change in ("zero_projects", "multiple_projects", "zero_results", "multiple_results",
                       "expected_failure", "unexpected_status"):
            with self.subTest(change=change):
                report = self.report()
                expected = inventory(report)
                spec = report["suites"][0]["specs"][0]
                test = spec["tests"][0]
                if change == "zero_projects":
                    spec["tests"] = []
                elif change == "multiple_projects":
                    spec["tests"].append(copy.deepcopy(test))
                elif change == "zero_results":
                    test["results"] = []
                elif change == "multiple_results":
                    test["results"].append(copy.deepcopy(test["results"][0]))
                elif change == "expected_failure":
                    test["expectedStatus"] = "failed"
                else:
                    test["status"] = "unexpected"
                self.assertEqual(inventory(report), expected, "execution cardinality must not change titles")
                with self.assertRaises(ValueError):
                    validate_run(report, expected)

    def test_report_rejects_extra_distinct_title(self):
        report = self.report()
        expected = inventory(report)
        extra = copy.deepcopy(report["suites"][0]["specs"][0])
        extra["title"] = "unexpected scenario"
        report["suites"][0]["specs"].append(extra)
        with self.assertRaises(ValueError):
            validate_run(report, expected)


    def test_nonzero_discovery_retains_both_diagnostics(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            with self.assertRaisesRegex(RuntimeError, "discovery exited 3") as raised:
                discover([sys.executable, "-c",
                          "import sys; print('partial report'); print('missing import', file=sys.stderr); sys.exit(3)"],
                         output)
            self.assertEqual((output / "discovery.json").read_text(), "partial report\n")
            self.assertEqual((output / "discovery.stderr.log").read_text(), "missing import\n")
            self.assertIn(str(output / "discovery.json"), str(raised.exception))
            self.assertIn(str(output / "discovery.stderr.log"), str(raised.exception))


    def run_main(self, output, *, metrics="1.25 512", report=None, exit_code=0,
                 discovery=None, discovery_exit=0, baseline=None, measurement_error=None):
        calls = []
        valid = self.report()
        discovery = valid if discovery is None else discovery
        argv = ["benchmark", "--output", str(output)]
        if baseline is not None:
            baseline_path = output.parent / "baseline.json"
            baseline_path.write_text(baseline if isinstance(baseline, str) else json.dumps(baseline))
            argv.extend(["--baseline-inventory", str(baseline_path)])

        def run(command, *, stdout, stderr, cwd, env):
            self.assertEqual(env["CODE_FACTORY_REAL_CODEX"], "0")
            self.assertEqual(command.count("--retries=0"), 1)
            if "--list" in command:
                stdout.write(discovery if isinstance(discovery, str) else json.dumps(discovery))
                stderr.write("discovery diagnostic")
                return subprocess.CompletedProcess(command, discovery_exit)
            else:
                calls.append(int(next(arg.split("=")[1] for arg in command if arg.startswith("--workers="))))
                sample = len(calls)
                raw_report = report if sample == 2 and report is not None else valid
                stdout.write(raw_report if isinstance(raw_report, str) else json.dumps(raw_report))
                stderr.write(f"sample {sample} diagnostic")
                Path(command[command.index("-o") + 1]).write_text(metrics if sample == 2 else "1.25 512")
                if sample == 2:
                    return subprocess.CompletedProcess(command, exit_code)
            return subprocess.CompletedProcess(command, 0)

        def identity(command, **kwargs):
            if command[:2] == ["git", "diff"]:
                return b""
            return "fixture-version\n"

        def measurement(command, **kwargs):
            result = run(command, **kwargs)
            if len(calls) == 2 and measurement_error is not None:
                if callable(measurement_error):
                    measurement_error()
                else:
                    raise RuntimeError(measurement_error)
            return result.returncode, {"peak_workload_rss_kib": 1024,
                                      "memory_samples": 2, "max_sampled_processes": 2,
                                      "max_sample_gap_seconds": .05}

        failure = None
        with patch.object(sys, "argv", argv), \
                patch.object(benchmark.subprocess, "run", side_effect=run), \
                patch.object(benchmark.subprocess, "check_output", side_effect=identity), \
                patch.object(benchmark, "measure", side_effect=measurement), \
                redirect_stdout(io.StringIO()):
            try:
                benchmark.main()
            except (ValueError, RuntimeError) as error:
                failure = error
        return calls, failure

    def test_main_runs_complete_worker_order_and_accepts_zero_metrics(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "evidence"
            calls, failure = self.run_main(output, metrics="0 0")
            self.assertIsNone(failure)
            self.assertEqual(calls, [1, 2, 2, 1])
            summary = json.loads((output / "summary.json").read_text())
            self.assertEqual([run["outcome"] for run in summary["runs"]], ["passed"] * 4)
            self.assertEqual(summary["version"], 2)
            self.assertGreater(summary["runs"][1]["peak_workload_rss_kib"], 0)
            self.assertEqual(summary["runs"][1]["seconds"], 0)
            self.assertEqual(summary["runs"][1]["max_process_rss_kib"], 0)
            self.assertEqual(json.loads((output / "inventory.json").read_text()), ["scenario"])
            for sample, workers in enumerate(calls, 1):
                run_dir = output / f"run-{sample}-workers-{workers}"
                self.assertEqual(json.loads((run_dir / "report.json").read_text()), self.report())
                self.assertEqual((run_dir / "stderr.log").read_text(), f"sample {sample} diagnostic")

    def test_main_stops_on_execution_report_or_metric_failure_and_retains_evidence(self):
        bad_report = self.report()
        bad_report["suites"][0]["specs"][0]["tests"][0]["results"][0]["status"] = "skipped"
        cases = [{"exit_code": 19}, {"report": bad_report}, {"report": "invalid JSON"},
                 {"report": {"suites": [], "errors": []}}]
        cases += [{"metrics": value} for value in
                  ("", "1", "1 2 3", "bad 100", "1 bad", "nan 100", "inf 100",
                   "-inf 100", "-1 100", "1 -100")]
        for options in cases:
            with self.subTest(options=options), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / "evidence"
                calls, failure = self.run_main(output, **options)
                self.assertIsInstance(failure, (ValueError, RuntimeError))
                self.assertEqual(calls, [1, 2], "failed sample must stop later invocations")
                summary = json.loads((output / "summary.json").read_text(),
                                     parse_constant=lambda value: self.fail(f"Nonstandard JSON number: {value}"))
                self.assertEqual([run["outcome"] for run in summary["runs"]], ["passed", "failed"])
                self.assertEqual(summary["runs"][1]["exit_code"], options.get("exit_code", 0))
                self.assertNotIn("seconds", summary["runs"][1])
                self.assertNotIn("max_process_rss_kib", summary["runs"][1])
                self.assertEqual(sorted(path.name for path in output.glob("run-*")),
                                 ["run-1-workers-1", "run-2-workers-2"])
                failed = output / "run-2-workers-2"
                expected_report = options.get("report", self.report())
                self.assertEqual((failed / "report.json").read_text(),
                                 expected_report if isinstance(expected_report, str) else json.dumps(expected_report))
                self.assertEqual((failed / "stderr.log").read_text(), "sample 2 diagnostic")
                self.assertEqual((failed / "time.txt").read_text(), options.get("metrics", "1.25 512"))
                self.assertEqual((output / "discovery.stderr.log").read_text(), "discovery diagnostic")
                self.assertEqual(json.loads((output / "discovery.json").read_text()), self.report())


    def test_main_accepts_matching_baseline_inventory(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "evidence"
            # File/suite nesting may differ without changing the title multiset.
            baseline = {"suites": [{"suites": self.report()["suites"]}], "errors": []}
            calls, failure = self.run_main(output, baseline=baseline)
            self.assertIsNone(failure)
            self.assertEqual(calls, [1, 2, 2, 1])
            self.assertTrue(all(run["outcome"] == "passed"
                                for run in json.loads((output / "summary.json").read_text())["runs"]))

    def test_main_rejects_discovery_and_baseline_before_any_sample(self):
        errors = self.report()
        errors["errors"] = [{"message": "discovery error"}]
        mismatch = self.report()
        mismatch["suites"][0]["specs"][0]["title"] = "different scenario"
        duplicate = self.report()
        duplicate["suites"].append(copy.deepcopy(duplicate["suites"][0]))
        empty = {"suites": [], "errors": []}
        cases = [{"discovery": empty}, {"discovery": errors},
                 {"discovery": "malformed discovery"}, {"discovery_exit": 3},
                 {"baseline": mismatch}, {"baseline": duplicate},
                 {"baseline": empty}, {"baseline": errors}, {"baseline": "malformed baseline"}]
        for options in cases:
            with self.subTest(options=options), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / "evidence"
                calls, failure = self.run_main(output, **options)
                self.assertIsInstance(failure, (ValueError, RuntimeError))
                self.assertEqual(calls, [], "invalid inventory must prevent every sample")
                self.assertEqual(sorted(path.name for path in output.iterdir()),
                                 ["discovery.json", "discovery.stderr.log"])
                discovery = options.get("discovery", self.report())
                self.assertEqual((output / "discovery.json").read_text(),
                                 discovery if isinstance(discovery, str) else json.dumps(discovery))
                self.assertEqual((output / "discovery.stderr.log").read_text(), "discovery diagnostic")


    @unittest.skipUnless(sys.platform == "linux", "Linux /proc measurement")
    def test_concurrent_process_hierarchy_memory_includes_new_sessions(self):
        # Four simultaneous 48 MiB allocations, including a new-session child
        # and its grandchild, distinguish workload RSS from the largest process.
        leaf = "import time; allocation=bytearray(48*1024*1024); time.sleep(0.8)"
        child = ("import subprocess,sys; allocation=bytearray(48*1024*1024); "
                 f"subprocess.run([sys.executable, '-c', {leaf!r}])")
        root = ("import subprocess,sys; allocation=bytearray(48*1024*1024); "
                f"a=subprocess.Popen([sys.executable, '-c', {child!r}], start_new_session=True); "
                f"b=subprocess.Popen([sys.executable, '-c', {leaf!r}]); a.wait(); b.wait()")
        status, metrics = measure([sys.executable, "-c", root],
                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.assertEqual(status, 0)
        self.assertGreater(metrics["peak_workload_rss_kib"], 180 * 1024)
        self.assertGreaterEqual(metrics["max_sampled_processes"], 4)
        self.assertGreater(metrics["memory_samples"], 2)
        self.assertGreater(metrics["max_sample_gap_seconds"], 0)

    def test_memory_requires_root_children_and_positive_rss(self):
        for root_seen, peak, count in ((False, 100, 2), (True, 0, 2), (True, 100, 1)):
            memory = WorkloadMemory(123)
            memory.root_seen, memory.peak_kib, memory.max_processes = root_seen, peak, count
            with self.assertRaisesRegex(RuntimeError, "Memory coverage requires"):
                memory.metrics()
        with tempfile.TemporaryDirectory() as directory:
            memory = WorkloadMemory(123, Path(directory))
            with self.assertRaisesRegex(RuntimeError, "No workload processes"):
                memory.sample()
            (Path(directory) / "123").mkdir()
            (Path(directory) / "123/stat").write_text("invalid stat")
            with self.assertRaises(IndexError):
                memory.sample()

    def test_reused_or_exited_parent_never_adopts_or_signals_unrelated_children(self):
        for seed in ("session", "remembered"):
            for race in ("replaced_before_open", "exited_before_open", "exited_during_child_read",
                         "replaced_during_child_read"):
                with self.subTest(seed=seed, race=race), tempfile.TemporaryDirectory() as directory:
                    proc = Path(directory)
                    paths = [proc / str(pid) for pid in (100, 200, 300, 400)]
                    for path in paths:
                        path.mkdir()
                    rows = {100: (1, 100, 10, 1),
                            200: (100, 100 if seed == "session" else 800, 20, 1),
                            300: (200, 900, 40, 900), 400: (300, 900, 50, 900)}
                    exited = set()
                    child_reads = 0
                    process = mock.Mock(pid=100)

                    def read(path):
                        nonlocal child_reads
                        self.assertFalse(process.wait.called, "session leader must remain unreaped during capture")
                        pid = int(path.parent.name)
                        if pid == 300:
                            child_reads += 1
                            trigger = 1 if race.endswith("before_open") else 2
                            if child_reads == trigger:
                                exited.add((200, rows[200][2]))
                                if race.startswith("replaced"):
                                    rows[200] = (1, 800, 30, 1)
                        return rows[pid]

                    class Poll:
                        def __init__(self):
                            self.handles = set()

                        def register(self, handle, events):
                            self.handles.add(handle)

                        def unregister(self, handle):
                            self.handles.remove(handle)

                        def poll(self, timeout):
                            return [(handle, benchmark.select.POLLIN) for handle in self.handles
                                    if timeout or (handle // 1000, handle % 1000) in exited]

                    memory = WorkloadMemory(100, proc)
                    if seed == "remembered":
                        memory.known.add((200, 20))
                    with mock.patch.object(Path, 'iterdir', return_value=paths), \
                            mock.patch.object(benchmark, 'process_stat', side_effect=read), \
                            mock.patch.object(benchmark.os, 'pidfd_open', side_effect=lambda pid: pid * 1000 + rows[pid][2]), \
                            mock.patch.object(benchmark.os, 'close'), \
                            mock.patch.object(benchmark.select, 'poll', Poll), \
                            mock.patch.object(benchmark.os, 'killpg') as group_signal, \
                            mock.patch.object(benchmark.signal, 'pidfd_send_signal') as target_signal, \
                            mock.patch.object(benchmark.time, 'sleep'):
                        owned = memory.owned_processes()
                        self.assertNotIn(300, owned)
                        self.assertNotIn(400, owned)
                        self.assertNotIn((300, 40), memory.known)
                        self.assertNotIn((400, 50), memory.known)
                        benchmark.cleanup_workload(process, memory)
                        self.assertNotIn((300, 40), memory.known, "cleanup capture must not contaminate known identities")
                        self.assertNotIn((400, 50), memory.known)
                        self.assertFalse(any(call.args[0] // 1000 in (300, 400) for call in target_signal.call_args_list),
                                         "neither TERM nor KILL may target the unrelated replacements")
                        self.assertEqual(group_signal.call_args_list,
                                         [mock.call(100, signal.SIGTERM), mock.call(100, signal.SIGKILL)])
                        process.wait.assert_called_once()

    def test_verified_cross_session_child_stays_known_after_reparenting(self):
        with tempfile.TemporaryDirectory() as directory:
            proc = Path(directory)
            paths = [proc / str(pid) for pid in (100, 200, 300)]
            for path in paths:
                path.mkdir()
            rows = {100: (1, 100, 10, 1), 200: (100, 100, 20, 1), 300: (200, 900, 40, 1)}
            memory = WorkloadMemory(100, proc)
            poller = mock.Mock()
            poller.poll.return_value = []
            with mock.patch.object(Path, 'iterdir', return_value=paths), \
                    mock.patch.object(benchmark, 'process_stat', side_effect=lambda path: rows[int(path.parent.name)]), \
                    mock.patch.object(benchmark.os, 'pidfd_open', return_value=77), \
                    mock.patch.object(benchmark.os, 'close') as close, \
                    mock.patch.object(benchmark.select, 'poll', return_value=poller):
                self.assertIn(300, memory.owned_processes())
                self.assertIn((300, 40), memory.known)
                rows[200] = (1, 800, 30, 1)
                rows[300] = (1, 900, 40, 1)
                self.assertIn(300, memory.owned_processes())
                close.assert_called_once_with(77)

    @unittest.skipUnless(sys.platform == "linux", "Linux /proc measurement")
    def test_memory_read_failure_rejects_partial_measurement(self):
        self.assertIn('second-sample read failure rejected', self.run_read_failure_case(False))

    def run_read_failure_case(self, stalled):
        with tempfile.TemporaryDirectory() as directory:
            result = subprocess.run([sys.executable, __file__, '--read-failure-case',
                                     directory, str(int(stalled))],
                                    capture_output=True, text=True, timeout=15)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            return result.stdout

    @unittest.skipUnless(sys.platform == "linux", "Linux /proc measurement")
    def test_missing_sampling_callback_is_bounded_and_reaps_pipe_child(self):
        sentinel = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(30)'])
        try:
            self.assertIn('missing callback bounded; probe and pipe child killed and reaped',
                          self.run_read_failure_case(True))
            self.assertIsNone(sentinel.poll(), 'fallback killed unrelated controller sibling')
        finally:
            sentinel.kill()
            sentinel.wait(timeout=2)

    def run_signal_case(self, signum, phase):
        with tempfile.TemporaryDirectory() as directory:
            result = subprocess.run([sys.executable, __file__, '--signal-case',
                                     directory, str(signum), phase],
                                    capture_output=True, text=True, timeout=25)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            return result.stdout

    @unittest.skipUnless(sys.platform == "linux", "Linux ownership and signals")
    def test_real_signals_clean_owned_hierarchy_and_retain_interruption(self):
        for signum in (signal.SIGINT, signal.SIGTERM):
            for phase in ('launch', 'measurement', 'cleanup'):
                with self.subTest(signal=signum, phase=phase):
                    self.assertIn('interrupted summary retained', self.run_signal_case(signum, phase))

    @unittest.skipUnless(sys.platform == "linux", "Linux ownership and signals")
    def test_stalled_probe_fallback_kills_and_reaps_only_owned_children(self):
        sentinel = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(30)'])
        try:
            self.assertIn('forced timeout recovered; probe and all adopted fixtures killed and reaped',
                          self.run_signal_case(signal.SIGTERM, 'stalled'))
            self.assertIsNone(sentinel.poll(), 'fallback killed unrelated controller sibling')
        finally:
            sentinel.kill()
            sentinel.wait(timeout=2)

    def test_cleanup_targeting_failure_is_not_accepted(self):
        process = mock.Mock(pid=123)
        memory = mock.Mock(known={(456, 789)})
        with mock.patch.object(benchmark.os, 'pidfd_open', side_effect=PermissionError('denied')), \
                mock.patch.object(benchmark.os, 'killpg') as group_signal, \
                mock.patch.object(benchmark.time, 'sleep'):
            with self.assertRaisesRegex(RuntimeError, 'Cannot safely target descendant 456: denied'):
                benchmark.cleanup_workload(process, memory)
        self.assertEqual(group_signal.call_args_list,
                         [mock.call(123, signal.SIGTERM), mock.call(123, signal.SIGKILL)])
        self.assertLessEqual(process.wait.call_args.kwargs['timeout'], benchmark.CLEANUP_TIMEOUT_SECONDS)

    def test_unavailable_safe_targeting_fails_before_launch(self):
        with mock.patch.object(benchmark.os, 'pidfd_open', side_effect=PermissionError('denied')), \
                mock.patch.object(benchmark.subprocess, 'Popen') as launch:
            with self.assertRaisesRegex(RuntimeError, 'requires Linux pidfd support: denied'):
                measure(['unused'])
            launch.assert_not_called()

    def test_main_stops_on_memory_failure_and_retains_diagnostics(self):
        def missing_coverage():
            WorkloadMemory(123).metrics()

        for error in (missing_coverage, "Workload memory collection failed: denied"):
            with self.subTest(error=error), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / "evidence"
                calls, failure = self.run_main(output, measurement_error=error)
                self.assertIsInstance(failure, RuntimeError)
                self.assertEqual(calls, [1, 2])
                summary = json.loads((output / "summary.json").read_text())
                self.assertEqual([entry["outcome"] for entry in summary["runs"]], ["passed", "failed"])
                self.assertIn("error", summary["runs"][1])
                self.assertNotIn("peak_workload_rss_kib", summary["runs"][1])
                self.assertEqual(len(list(output.glob("run-*"))), 2)
                failed = output / "run-2-workers-2"
                self.assertEqual(json.loads((failed / "report.json").read_text()), self.report())
                self.assertEqual((failed / "stderr.log").read_text(), "sample 2 diagnostic")
                self.assertEqual((failed / "time.txt").read_text(), "1.25 512")

    def test_cleanup_deadline_failures_stop_main_and_close_owned_handles(self):
        for boundary in ("descendants", "leader"):
            with self.subTest(boundary=boundary), tempfile.TemporaryDirectory() as directory:
                process = mock.Mock(pid=123)
                memory = mock.Mock(known={(456, 789)}, proc=Path(directory))
                if boundary == "leader":
                    process.wait.side_effect = subprocess.TimeoutExpired(["fixture"], 2)

                def exhaust_cleanup():
                    with mock.patch.object(benchmark.os, 'pidfd_open', return_value=77), \
                            mock.patch.object(benchmark, 'process_stat', return_value=(123, 123, 789, 1)), \
                            mock.patch.object(benchmark.os, 'killpg') as group_signal, \
                            mock.patch.object(benchmark.signal, 'pidfd_send_signal') as target_signal, \
                            mock.patch.object(benchmark.os, 'close') as close, \
                            mock.patch.object(benchmark.time, 'sleep'), \
                            mock.patch.object(benchmark.time, 'monotonic', side_effect=[0, 3, 3]), \
                            mock.patch.object(benchmark.select, 'poll'):
                        try:
                            benchmark.cleanup_workload(process, memory)
                        finally:
                            self.assertEqual(group_signal.call_args_list,
                                             [mock.call(123, signal.SIGTERM), mock.call(123, signal.SIGKILL)])
                            self.assertEqual(target_signal.call_args_list,
                                             [mock.call(77, signal.SIGTERM), mock.call(77, signal.SIGKILL)])
                            close.assert_called_once_with(77)
                            process.wait.assert_called_once_with(timeout=0)

                output = Path(directory) / "evidence"
                calls, failure = self.run_main(output, measurement_error=exhaust_cleanup)
                self.assertIsInstance(failure, RuntimeError)
                self.assertIn("Owned descendants exceeded the cleanup deadline", str(failure))
                if boundary == "leader":
                    self.assertIn("Owned command exceeded the cleanup deadline", str(failure))
                self.assertEqual(calls, [1, 2])
                summary = json.loads((output / "summary.json").read_text())
                self.assertEqual([entry["outcome"] for entry in summary["runs"]], ["passed", "failed"])
                self.assertIn("cleanup deadline", summary["runs"][1]["error"])
                self.assertNotIn("peak_workload_rss_kib", summary["runs"][1])


if __name__ == "__main__":
    if len(sys.argv) == 4 and sys.argv[1] == "--read-failure-probe":
        read_failure_probe(Path(sys.argv[2]), bool(int(sys.argv[3])))
    elif len(sys.argv) == 4 and sys.argv[1] == "--read-failure-case":
        read_failure_case(Path(sys.argv[2]), bool(int(sys.argv[3])))
    elif len(sys.argv) == 5 and sys.argv[1] == "--signal-case":
        signal_case(Path(sys.argv[2]), int(sys.argv[3]), sys.argv[4])
    elif len(sys.argv) == 5 and sys.argv[1] == "--signal-probe":
        cancellation_probe(Path(sys.argv[2]), int(sys.argv[3]), sys.argv[4])
    else:
        unittest.main()
