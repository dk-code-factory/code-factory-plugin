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


GRACE_VERIFIER = '''import os,pathlib,signal,subprocess,sys,time
state=pathlib.Path(sys.argv[1]);mode=sys.argv[2]
child=subprocess.Popen([sys.executable,'-c',"""import pathlib,signal,sys,time
state=pathlib.Path(sys.argv[1]);stopped=[False]
signal.signal(signal.SIGTERM,lambda *args:stopped.__setitem__(0,True))
(state/'child-ready').touch()
deadline=time.monotonic()+5
while not stopped[0]:
    if time.monotonic()>=deadline:raise RuntimeError('cleanup did not receive TERM')
    time.sleep(.001)
(state/'cleanup-started').touch()
time.sleep(.3)
(state/'cleanup-finished').touch()
""",str(state)])
deadline=time.monotonic()+5
while not (state/'child-ready').exists():
    if child.poll() is not None or time.monotonic()>=deadline:raise RuntimeError('child not ready')
    time.sleep(.001)
(state/'leader-ready').touch()
if mode=='signal':os.kill(os.getpid(),signal.SIGKILL)
if mode.startswith('cleanup-'):raise SystemExit(0)
time.sleep(5)
'''


def grace_probe(mode):
    # Adopt only this isolated probe's descendants, including a cleanup child
    # whose direct leader has exited. The unittest process never becomes subreaper.
    if ctypes.CDLL(None, use_errno=True).prctl(36, 1, 0, 0, 0):
        raise OSError(ctypes.get_errno(), 'test subreaper setup failed')
    from specs_lib import processes
    check = unittest.TestCase()
    with tempfile.TemporaryDirectory() as directory:
        state = pathlib.Path(directory)
        real_waitid, real_killpg = os.waitid, os.killpg
        injected = False
        handlers = {s: signal.getsignal(s) for s in (signal.SIGINT, signal.SIGTERM)}
        def waitid(*args):
            nonlocal injected
            if mode == 'wait-error' and not injected and (state / 'leader-ready').exists():
                injected = True
                raise OSError('controlled wait observation failure')
            return real_waitid(*args)
        def killpg(pid, signum):
            nonlocal injected
            real_killpg(pid, signum)
            if mode.startswith('cleanup-') and signum == signal.SIGTERM and not injected:
                injected = True
                os.kill(os.getpid(), signal.SIGINT if mode == 'cleanup-int' else signal.SIGTERM)
        try:
            error, result = None, None
            with patch.object(processes.os, 'waitid', side_effect=waitid), \
                    patch.object(processes.os, 'killpg', side_effect=killpg):
                try:
                    result = processes.run_verification(
                        [sys.executable, '-c', GRACE_VERIFIER, str(state), mode],
                        cwd=state, timeout=2, termination_grace=.8)
                except (KeyboardInterrupt, SystemExit, OSError, subprocess.TimeoutExpired) as caught:
                    error = caught
            if mode == 'signal':
                check.assertIsNone(error)
                check.assertEqual(result.returncode, -signal.SIGKILL)
            elif mode == 'timeout':
                check.assertIsInstance(error, subprocess.TimeoutExpired)
            elif mode == 'wait-error':
                check.assertIsInstance(error, OSError)
                check.assertIn('controlled wait', str(error))
            elif mode == 'cleanup-int':
                check.assertIsInstance(error, KeyboardInterrupt)
            else:
                check.assertIsInstance(error, SystemExit)
                check.assertEqual(error.code, 143)
            check.assertTrue((state / 'cleanup-started').exists())
            check.assertTrue((state / 'cleanup-finished').exists(), mode + ' truncated cleanup')
            check.assertEqual({s: signal.getsignal(s) for s in handlers}, handlers)
            print(mode + ': delayed cleanup finished; outcome and handlers preserved', flush=True)
        finally:
            deadline = time.monotonic() + 3
            children = pathlib.Path(f'/proc/{os.getpid()}/task/{os.getpid()}/children')
            while children.read_text().strip():
                check.assertLess(time.monotonic(), deadline, 'probe could not reap owned children')
                for pid in map(int, children.read_text().split()):
                    os.kill(pid, signal.SIGKILL)
                    while os.waitpid(pid, os.WNOHANG) == (0, 0):
                        check.assertLess(time.monotonic(), deadline, 'probe child survived KILL')
                        time.sleep(.001)


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
    def test_failure_and_cleanup_cancellation_retain_grace_after_leader_exit(self):
        for mode in ('signal', 'timeout', 'wait-error', 'cleanup-int', 'cleanup-term'):
            with self.subTest(mode=mode):
                result = subprocess.run([sys.executable, __file__, '--grace-probe', mode],
                                        capture_output=True, text=True, timeout=10)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn('delayed cleanup finished; outcome and handlers preserved', result.stdout)

    def test_normal_exit_does_not_wait_for_cancellation_grace(self):
        for status in (0, 7):
            with self.subTest(status=status):
                started = time.monotonic()
                result = uf.run_verification([sys.executable, '-c', f'raise SystemExit({status})'],
                                             cwd=pathlib.Path.cwd(), termination_grace=2)
                self.assertEqual(result.returncode, status)
                self.assertLess(time.monotonic() - started, 1.5)

    def test_descriptor_passthrough_is_explicit_and_preserves_default_calls(self):
        from specs_lib import processes
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            read, write = os.pipe()
            unrelated_read, unrelated_write = os.pipe()
            descriptors = (read, write, unrelated_read, unrelated_write)
            for fd in descriptors:
                self.addCleanup(os.close, fd)
                os.set_inheritable(fd, True)
            output = root / 'descriptor-state.json'
            source = '''import json,os,pathlib,sys
states=[]
for fd in map(int,sys.argv[2:]):
    try:os.fstat(fd);states.append(True)
    except OSError:states.append(False)
pathlib.Path(sys.argv[1]).write_text(json.dumps({'open':states,'fixture':os.environ['CODE_FACTORY_REAL_CODEX']}))
sys.exit(37)
'''
            command = [sys.executable, '-c', source, str(output), *map(str, descriptors)]
            handlers = {s: signal.getsignal(s) for s in (signal.SIGINT, signal.SIGTERM)}
            for passed in ((), (read, write)):
                with self.subTest(pass_fds=passed), \
                        patch.dict(os.environ, {'CODE_FACTORY_REAL_CODEX': '1'}), \
                        patch.object(processes.subprocess, 'Popen', wraps=subprocess.Popen) as spawn:
                    options = {'pass_fds': passed} if passed else {}
                    result = uf.run_verification(command, cwd=root, timeout=5, **options)
                    self.assertEqual(result.returncode, 37)
                    data = json.loads(output.read_text())
                    self.assertEqual(data['fixture'], '0')
                    self.assertEqual(data['open'], [bool(passed), bool(passed), False, False])
                    if passed:
                        self.assertEqual(spawn.call_args.kwargs['pass_fds'], passed)
                    else:
                        self.assertNotIn('pass_fds', spawn.call_args.kwargs)
                    self.assertEqual({s: signal.getsignal(s) for s in handlers}, handlers)

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
    if len(sys.argv) == 3 and sys.argv[1] == '--grace-probe':
        grace_probe(sys.argv[2])
    elif len(sys.argv) == 3 and sys.argv[1] == '--probe':
        probe(sys.argv[2])
    else:
        unittest.main()
