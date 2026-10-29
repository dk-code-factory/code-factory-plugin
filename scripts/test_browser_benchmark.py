import copy
import ctypes
import json
import os
import signal
import time
import unittest
from pathlib import Path
import subprocess
import sys
import tempfile
from unittest import mock

import benchmark_browser as benchmark
from benchmark_browser import WorkloadMemory, discover, inventory, measure, validate_run


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

    output = state / 'qualification'
    report = {'suites': [{'specs': [{'title': 'fixture'}]}]}
    cancellation = None
    try:
        with mock.patch.object(benchmark, 'discover', return_value=report), \
                mock.patch.object(benchmark.subprocess, 'check_output', return_value='fixture'), \
                mock.patch.object(benchmark, 'measure', side_effect=lambda command, **kw:
                                  measure([sys.executable, '-c', WORKLOAD, str(state)], **kw)), \
                mock.patch.object(WorkloadMemory, 'owned_processes', observed), \
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
        ready('observed')
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

    @unittest.skipUnless(sys.platform == "linux", "Linux /proc measurement")
    def test_memory_read_failure_rejects_partial_measurement(self):
        with mock.patch.object(WorkloadMemory, "sample", side_effect=[None, PermissionError("denied")]):
            with self.assertRaisesRegex(RuntimeError, "memory collection failed: denied"):
                measure([sys.executable, "-c", "import time; time.sleep(0.1)"])


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
            for phase in ('measurement', 'cleanup'):
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


if __name__ == "__main__":
    if len(sys.argv) == 5 and sys.argv[1] == "--signal-case":
        signal_case(Path(sys.argv[2]), int(sys.argv[3]), sys.argv[4])
    elif len(sys.argv) == 5 and sys.argv[1] == "--signal-probe":
        cancellation_probe(Path(sys.argv[2]), int(sys.argv[3]), sys.argv[4])
    else:
        unittest.main()
