#!/usr/bin/env python3
"""Cancellation received while a timed-out verifier is being cleaned up."""
import contextlib
import ctypes
import io
import json
import os
import pathlib
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import specs
import user_flows as uf
from test_specs import fixture


VERIFIER = '''def test_perform(): pass
import json, os, pathlib, signal, subprocess, sys, time
state = pathlib.Path(os.environ['RUNNER_STATE'])
if os.environ['RUNNER_MODE'] == 'success':
    raise SystemExit(0)
def terminating(signum, frame):
    (state / 'cleanup-ready').touch()
signal.signal(signal.SIGTERM, terminating)
child = subprocess.Popen([sys.executable, '-c',
    "import pathlib,signal,sys,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); pathlib.Path(sys.argv[1]).touch(); time.sleep(30)",
    str(state / 'child-ready')])
while not (state / 'child-ready').exists():
    time.sleep(.001)
(state / 'pids.json').write_text(json.dumps([os.getpid(), child.pid]))
while True:
    time.sleep(30)
'''


def probe(state, signum):
    # Only this short-lived test process adopts and reaps its verifier's children.
    if ctypes.CDLL(None, use_errno=True).prctl(36, 1, 0, 0, 0):
        raise OSError(ctypes.get_errno(), 'test subreaper setup failed')
    check = unittest.TestCase()
    root = state / 'repository'
    root.mkdir()
    files = fixture()
    catalog = uf.load(files, uf.CATALOG)
    command = [sys.executable, 'scripts/test_app.py']
    for suite in catalog['suites'].values():
        suite['commands'] = [command]
    files[uf.CATALOG] = json.dumps(catalog).encode()
    files['scripts/test_app.py'] = VERIFIER.encode()
    for name, content in files.items():
        path = root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)
    uf.git(root, 'init', '-q')
    uf.git(root, 'add', '.')
    uf.git(root, '-c', 'user.name=Test', '-c', 'user.email=test@example.invalid', 'commit', '-qm', 'Fixture')
    base = uf.revision(root, 'HEAD')
    (root / 'app.py').write_text('def perform():\n    return 2\n')
    affected = specs.analyze(uf.snapshot(root, base), uf.snapshot(root), base)[1]
    review = state / 'review.json'
    review.write_text(json.dumps({subject: {'kind': 'refactor', **dict.fromkeys(
        ('description', 'implementation', 'tests'), 'Reviewed preserved behavior and actual fixture assertions.')}
        for subject in affected}))
    receipt = root / specs.RECORDS / 'flow-example.json'
    old_handlers = {s: signal.getsignal(s) for s in (signal.SIGINT, signal.SIGTERM)}
    real_run, real_killpg = uf.run_verification, os.killpg

    def reconcile():
        return uf.main(['reconcile', '--root', str(root), '--base', base,
                        '--review', str(review), '--json'])

    def short_run(*args, **kwargs):
        return real_run(*args, **kwargs, timeout=0.4)

    def synchronized_term(pid, delivered):
        real_killpg(pid, delivered)
        if delivered == signal.SIGTERM:
            # Keep cleanup in progress until the independent controller has
            # observed the verifier's TERM handler and sent the real signal.
            # The deadline only bounds a failed test; it does not trigger it.
            deadline = time.monotonic() + 5
            while not (state / 'signal-sent').exists():
                check.assertLess(time.monotonic(), deadline, 'controller did not release cleanup')
                time.sleep(.001)

    cancellation = None
    try:
        with patch.dict(os.environ, {'RUNNER_STATE': str(state), 'RUNNER_MODE': 'success'}):
            with contextlib.redirect_stdout(io.StringIO()):
                check.assertEqual(reconcile(), 0)
            check.assertTrue(receipt.exists(), 'prior successful receipt missing')
            os.environ['RUNNER_MODE'] = 'cleanup'
            output = io.StringIO()
            try:
                with (state / 'errors.log').open('w') as errors, \
                        patch('user_flows.run_verification', side_effect=short_run), \
                        patch('user_flows.os.killpg', side_effect=synchronized_term), \
                        contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
                    reconcile()
            except (KeyboardInterrupt, SystemExit) as error:
                cancellation = error
        check.assertEqual({s: signal.getsignal(s) for s in old_handlers}, old_handlers)
        diagnostic = output.getvalue() + (state / 'errors.log').read_text()
        if signum == signal.SIGINT:
            check.assertIsInstance(cancellation, KeyboardInterrupt, diagnostic)
        else:
            check.assertIsInstance(cancellation, SystemExit, diagnostic)
            check.assertEqual(cancellation.code, 128 + signum)
        check.assertFalse(receipt.exists(), 'cancelled rerun retained prior success')
        check.assertEqual(list((root / specs.RECORDS).glob('*.json')), [])
        leader, child = json.loads((state / 'pids.json').read_text())
        with check.assertRaises(ProcessLookupError):
            os.kill(leader, 0)
        deadline = time.monotonic() + 2
        while os.waitpid(child, os.WNOHANG) == (0, 0):
            check.assertLess(time.monotonic(), deadline, 'owned descendant survived cleanup')
            time.sleep(.01)
        with check.assertRaises(ProcessLookupError):
            os.kill(child, 0)
        print('cleanup cancellation preserved; handlers restored; owned group reaped; no receipts', flush=True)
    finally:
        # An assertion failure must still reap only this probe's owned children.
        if (state / 'pids.json').exists():
            leader, child = json.loads((state / 'pids.json').read_text())
            for pid in (leader, child):
                try:
                    os.waitid(os.P_PID, pid, os.WEXITED | os.WNOHANG | os.WNOWAIT)
                    if os.getpgid(pid) == leader:
                        real_killpg(leader, signal.SIGKILL)
                    os.waitpid(pid, 0)
                except (ChildProcessError, ProcessLookupError):
                    pass
    if cancellation is not None:
        raise cancellation


class CleanupCancellationTests(unittest.TestCase):
    def run_probe(self, signum, expected):
        with tempfile.TemporaryDirectory() as directory:
            state = pathlib.Path(directory)
            process = subprocess.Popen([sys.executable, __file__, '--probe', directory, str(signum)],
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                deadline = time.monotonic() + 8
                while not (state / 'cleanup-ready').exists():
                    self.assertIsNone(process.poll(), 'probe exited before verifier cleanup')
                    self.assertLess(time.monotonic(), deadline, 'verifier did not enter timeout cleanup')
                    time.sleep(.001)
                os.kill(process.pid, signum)
                (state / 'signal-sent').touch()
                stdout, stderr = process.communicate(timeout=8)
                self.assertIn(process.returncode, expected, stdout + stderr)
                self.assertIn('cleanup cancellation preserved; handlers restored; owned group reaped; no receipts', stdout)
            finally:
                (state / 'signal-sent').touch()
                if process.poll() is None:
                    # The runner owns this subprocess PID. Its handler completes
                    # group cleanup before exiting, including on test failures.
                    os.kill(process.pid, signal.SIGTERM)
                process.communicate(timeout=8)

    def test_sigint_during_timeout_cleanup_preserves_cancellation(self):
        self.run_probe(signal.SIGINT, (130, -signal.SIGINT))

    def test_sigterm_during_timeout_cleanup_preserves_cancellation(self):
        self.run_probe(signal.SIGTERM, (143,))


if __name__ == '__main__':
    if len(sys.argv) == 4 and sys.argv[1] == '--probe':
        probe(pathlib.Path(sys.argv[2]), int(sys.argv[3]))
    else:
        unittest.main()
