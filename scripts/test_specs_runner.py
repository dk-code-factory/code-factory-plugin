#!/usr/bin/env python3
"""Real, isolated verifier process-group and cancellation regressions."""
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

import user_flows as uf
import specs
from test_specs import fixture


VERIFIER = '''def test_perform(): pass
import json, os, pathlib, signal, subprocess, sys, time
state = pathlib.Path(os.environ['RUNNER_STATE'])
with (state / 'launches').open('a') as log:
    log.write('launched\\n')
print('verifier stdout', flush=True)
print('verifier stderr', file=sys.stderr, flush=True)
(state / 'arguments.json').write_text(json.dumps([sys.argv[1:], os.environ['CODE_FACTORY_REAL_CODEX']]))
mode = os.environ.get('RUNNER_MODE', 'success')
if mode == 'success':
    raise SystemExit(0)
signal.signal(signal.SIGTERM, signal.SIG_IGN)
child = subprocess.Popen([sys.executable, '-c',
    "import pathlib,signal,sys,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); pathlib.Path(sys.argv[1]).touch(); time.sleep(30)",
    str(state / 'ready')])
while not (state / 'ready').exists():
    time.sleep(.001)
(state / 'pids.json').write_text(json.dumps([os.getpid(), child.pid]))
if mode in ('interrupt', 'terminate'):
    os.kill(os.getppid(), signal.SIGINT if mode == 'interrupt' else signal.SIGTERM)
if mode == 'nonzero':
    raise SystemExit(7)
if mode == 'success-descendant':
    raise SystemExit(0)
time.sleep(30)
'''


def probe(mode):
    # This short-lived harness alone adopts orphaned verifier children. The
    # production runner does not change process-wide subreaper state, and this
    # never affects unittest's process or unrelated children.
    if ctypes.CDLL(None, use_errno=True).prctl(36, 1, 0, 0, 0):
        raise OSError(ctypes.get_errno(), 'test subreaper setup failed')
    check = unittest.TestCase()
    with tempfile.TemporaryDirectory() as directory:
        state = pathlib.Path(directory)
        root = state / 'repository'
        root.mkdir()
        files = fixture()
        catalog = uf.load(files, uf.CATALOG)
        command = [sys.executable, 'scripts/test_app.py', 'literal space', '$(not-a-shell)']
        for suite in catalog['suites'].values():
            suite['commands'] = [command, command]
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
        review = state / 'review.json'
        affected = specs.analyze(uf.snapshot(root, base), uf.snapshot(root), base)[1]
        review.write_text(json.dumps({subject: {'kind': 'refactor', **dict.fromkeys(
            ('description', 'implementation', 'tests'), 'Reviewed the preserved outcome and actual fixture assertions.')}
            for subject in affected}))
        receipt = root / specs.RECORDS / 'flow-example.json'
        old_handlers = {s: signal.getsignal(s) for s in (signal.SIGINT, signal.SIGTERM)}
        def reconcile():
            return uf.main(['reconcile', '--root', str(root), '--base', base,
                            '--review', str(review), '--json'])
        real_run = uf.run_verification
        def short_run(*args, **kwargs):
            return real_run(*args, **kwargs, timeout=0.4)
        cancellation = None
        with patch.dict(os.environ, {'RUNNER_STATE': directory, 'CODE_FACTORY_REAL_CODEX': '1', 'RUNNER_MODE': 'success'}):
            try:
                output = io.StringIO()
                with contextlib.redirect_stdout(output):
                    check.assertEqual(reconcile(), 0)
                check.assertTrue(json.loads(output.getvalue())['reconciled'])
                record = json.loads(receipt.read_text())
                expected = [c for suite in affected['flow-example']['suites'].values() for c in suite['commands']]
                check.assertEqual([r['command'] for r in record['verification']], expected)
                check.assertTrue(all(r['exit_code'] == 0 for r in record['verification']))
                check.assertEqual((state / 'launches').read_text(), 'launched\n')
                check.assertEqual(json.loads((state / 'arguments.json').read_text()), [command[2:], '0'])
                os.environ['RUNNER_MODE'] = mode
                output = io.StringIO()
                try:
                    with (state / 'errors.log').open('w') as errors, patch('user_flows.run_verification', side_effect=short_run), contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
                        status = reconcile()
                except (KeyboardInterrupt, SystemExit) as error:
                    cancellation = error
                if mode == 'terminate':
                    check.assertIsInstance(cancellation, SystemExit)
                    check.assertEqual(cancellation.code, 143)
                elif mode == 'interrupt':
                    check.assertIsInstance(cancellation, KeyboardInterrupt)
                else:
                    check.assertIsNone(cancellation)
                    check.assertEqual(status, 0 if mode == 'success-descendant' else 1)
                    check.assertIsInstance(json.loads(output.getvalue()), dict)
                check.assertEqual(receipt.exists(), mode == 'success-descendant')
                check.assertEqual({s: signal.getsignal(s) for s in old_handlers}, old_handlers)
                logs = (state / 'errors.log').read_text()
                check.assertIn('verifier stdout', logs)
                check.assertIn('verifier stderr', logs)
                leader, child = json.loads((state / 'pids.json').read_text())
                # The direct child has been reaped by the runner. The harness
                # must reap the adopted grandchild within a short deadline.
                with check.assertRaises(ProcessLookupError):
                    os.kill(leader, 0)
                deadline = time.monotonic() + 2
                while os.waitpid(child, os.WNOHANG) == (0, 0):
                    check.assertLess(time.monotonic(), deadline, 'owned descendant survived verification cleanup')
                    time.sleep(.01)
                with check.assertRaises(ProcessLookupError):
                    os.kill(child, 0)
                print('owned verifier group cleaned and receipts checked', flush=True)
            finally:
                # Failures may never leave this harness's inert process group
                # behind. These IDs come only from our spawned verifier.
                if (state / 'pids.json').exists():
                    leader, child = json.loads((state / 'pids.json').read_text())
                    try:
                        # Signal only while an unreaped child of this harness
                        # still pins the recorded group ID against reuse.
                        os.waitid(os.P_PID, child, os.WEXITED | os.WNOHANG | os.WNOWAIT)
                        if os.getpgid(child) == leader:
                            os.killpg(leader, signal.SIGKILL)
                        os.waitpid(child, 0)
                    except (ChildProcessError, ProcessLookupError):
                        pass
        if cancellation is not None:
            raise cancellation


class RunnerTests(unittest.TestCase):
    def run_probe(self, mode, expected):
        result = subprocess.run([sys.executable, __file__, '--probe', mode],
                                capture_output=True, text=True, timeout=10)
        self.assertIn(result.returncode, expected, result.stdout + result.stderr)
        self.assertIn('owned verifier group cleaned and receipts checked', result.stdout)
        self.assertIn('verifier stdout', result.stderr)
        self.assertIn('verifier stderr', result.stderr)

    def test_timeout_kills_owned_descendants_and_invalidates_prior_success(self):
        self.run_probe('timeout', (0,))

    def test_keyboard_interrupt_cleans_owned_descendants_and_writes_no_success(self):
        self.run_probe('interrupt', (130, -signal.SIGINT))

    def test_sigterm_cleans_owned_descendants_and_preserves_cli_exit_status(self):
        self.run_probe('terminate', (143,))

    def test_nonzero_exit_cleans_descendants_and_invalidates_prior_success(self):
        self.run_probe('nonzero', (0,))

    def test_success_cleans_descendants_and_records_exact_argv_fixture_env_and_dedup(self):
        self.run_probe('success-descendant', (0,))


if __name__ == '__main__':
    if len(sys.argv) == 3 and sys.argv[1] == '--probe':
        probe(sys.argv[2])
    else:
        unittest.main()
