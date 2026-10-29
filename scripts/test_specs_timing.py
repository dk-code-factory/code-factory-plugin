"""Check pipeline evidence without touching an enclosing verification journal."""
import contextlib
import io
import json
import os
from pathlib import Path
import select
import signal
import socket
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import Mock, patch

import ci
import verification_timing as timing


MODULE_DIRECTORY = Path(__file__).resolve().parent
WORKING_IDENTITY = timing.identity
COMPARISON = timing.comparison


def cleanup_process(process, timeout=5):
    """Reap a probe gracefully; pin its direct children before group fallback."""
    if process.poll() is None:
        process.terminate()
    try:
        process.communicate(timeout=timeout)
        return
    except subprocess.TimeoutExpired:
        pass
    # Stop the wrapper before inspecting children: it cannot reap a group leader
    # while we signal that owned group, so its numeric ID cannot be reused.
    if process.poll() is None:
        process.send_signal(signal.SIGSTOP)
        state = Path('/proc') / str(process.pid)
        deadline = time.monotonic() + 1
        while process.poll() is None:
            try:
                status = state.joinpath('status').read_text()
            except FileNotFoundError:
                break
            if any(line.startswith('State:') and line.split()[1] in ('T', 't')
                   for line in status.splitlines()):
                children = state.joinpath('task', str(process.pid), 'children').read_text()
                for child in map(int, children.split()):
                    try:
                        if os.getpgid(child) == child and os.getsid(child) == child:
                            os.killpg(child, signal.SIGKILL)
                        else:
                            os.kill(child, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                break
            if time.monotonic() >= deadline:
                break
            time.sleep(.01)
        process.send_signal(signal.SIGCONT)
    try:
        process.communicate(timeout=2)
    except subprocess.TimeoutExpired:
        process.kill()
        process.communicate(timeout=2)


class VerificationTimingTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        environment = {key: value for key, value in os.environ.items()
                       if not key.startswith(('CODE_FACTORY_TIMING_', 'GITHUB_')) and
                       key not in ('FLOW_ARGS', 'CI_BOOTSTRAP_SHA')}
        for manager in (patch.dict(os.environ, environment, clear=True),
                        patch.object(timing, 'ROOT', self.root),
                        patch.object(timing, 'identity', return_value={
                            'head': 'a' * 40, 'inputs_sha256': 'b' * 64, 'dirty': False}),
                        patch.object(timing, 'comparison', return_value={'status': 'unavailable'})):
            manager.start()
            self.addCleanup(manager.stop)

    def journal(self, clock=lambda: 100.0, hosted=False):
        return timing.Journal.create(clock=clock, hosted=hosted)

    def data(self, journal):
        return json.loads(journal.path.read_text())

    def test_success_and_failure_leave_distinct_unstarted_phases(self):
        successful = self.journal()
        for phase in timing.PLAN:
            successful.stop(successful.start(phase), 0)
        successful.finish('success')
        self.assertEqual(self.data(successful)['outcome'], 'success')
        self.assertTrue(all(row['outcome'] == 'success' for row in self.data(successful)['phases']))

        failed = self.journal()
        failed.stop(failed.start('specifications'), 0)
        failed.stop(failed.start('build'), 17)
        failed.finish('failure')
        data = self.data(failed)
        self.assertEqual(data['outcome'], 'failure')
        by_phase = {row['phase']: row for row in data['phases']}
        self.assertEqual(by_phase['specifications']['outcome'], 'success')
        self.assertEqual(by_phase['build']['outcome'], 'failure')
        self.assertEqual(by_phase['build']['exit_code'], 17)
        for row in data['phases']:
            if row['phase'] in ('specifications', 'build'):
                continue
            self.assertIsNone(row['start_seconds'])
            self.assertIsNone(row['end_seconds'])
            self.assertIsNone(row['seconds'])
            self.assertIsNone(row['exit_code'])

    def test_injected_clock_records_overlap_parentage_and_wall_time(self):
        ticks = iter([10, 11, 12, 13, 15, 16, 18, 20])
        journal = self.journal(clock=lambda: next(ticks))
        parent = journal.start('build')
        first = journal.start('frontend', parent)
        second = journal.start('daemon', parent)
        journal.stop(first, 0)
        journal.stop(second, 0)
        journal.stop(parent, 0)
        journal.finish('success')
        data = self.data(journal)
        rows = {row['id']: row for row in data['phases']}
        self.assertEqual((rows[parent]['start_seconds'], rows[parent]['end_seconds'],
                          rows[parent]['seconds']), (1, 8, 7))
        self.assertEqual((rows[first]['parent'], rows[second]['parent']), (parent, parent))
        self.assertEqual((rows[first]['start_seconds'], rows[first]['end_seconds']), (2, 5))
        self.assertEqual((rows[second]['start_seconds'], rows[second]['end_seconds']), (3, 6))
        self.assertEqual(data['wall_seconds'], 10)
        self.assertEqual(data['outcome'], 'incomplete')
        self.assertIn('do not sum', data['aggregation'])

    def test_processes_do_not_lose_concurrent_updates(self):
        journal = self.journal(clock=timing.time.monotonic)
        parent = journal.start('build')
        source = '''import sys, time
from verification_timing import Journal
journal = Journal(sys.argv[1])
for unused in range(12):
    identifier = journal.start('frontend', sys.argv[2])
    time.sleep(0.001)
    journal.stop(identifier, 0)
'''
        processes = []
        for _ in range(4):
            process = subprocess.Popen([sys.executable, '-c', source, str(journal.path), parent],
                                       cwd=MODULE_DIRECTORY, env=dict(os.environ),
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            self.addCleanup(cleanup_process, process)
            processes.append(process)
        for process in processes:
            output, errors = process.communicate(timeout=15)
            self.assertEqual(process.returncode, 0, errors.decode())
            self.assertEqual(output, b'')
        rows = self.data(journal)['phases']
        nested = [row for row in rows if row['parent'] == parent]
        self.assertEqual(len(nested), 48)
        self.assertEqual(len({row['id'] for row in rows}), len(rows))
        self.assertTrue(all(row['outcome'] == 'success' for row in nested))

    def cancellation_probe(self):
        journal = self.journal(clock=timing.time.monotonic)
        listener = socket.socket()
        self.addCleanup(listener.close)
        listener.bind(('127.0.0.1', 0))
        listener.listen(1)
        child = ("import os,socket,time; "
                 "connection=socket.create_connection(('127.0.0.1', int(os.environ['TIMING_READY_PORT']))); "
                 "connection.sendall(str(os.getpid()).encode()); connection.close(); time.sleep(60)")
        source = '''import os, signal, sys
import verification_timing as timing
timing.ROOT = __import__('pathlib').Path(sys.argv[2])
signal.signal(signal.SIGTERM, lambda number, frame: sys.exit(128 + number))
journal = timing.Journal(sys.argv[1])
try:
    timing.execute('build', [sys.executable, '-c', sys.argv[3]], journal=journal, owned=True)
finally:
    journal.finish('interrupted')
'''
        environment = {**os.environ, 'TIMING_READY_PORT': str(listener.getsockname()[1])}
        process = subprocess.Popen([sys.executable, '-c', source, str(journal.path), str(self.root), child],
                                   cwd=MODULE_DIRECTORY, env=environment,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.addCleanup(cleanup_process, process)
        ready, _, _ = select.select([listener], [], [], 8)
        self.assertTrue(ready, 'child did not report readiness')
        connection, _ = listener.accept()
        with connection:
            connection.settimeout(2)
            child_pid = int(connection.recv(32))
        return journal, process, child_pid

    def test_partial_evidence_precedes_owned_runner_cancellation(self):
        journal, process, child_pid = self.cancellation_probe()
        running = next(row for row in self.data(journal)['phases'] if row['phase'] == 'build')
        self.assertEqual(running['outcome'], 'running')
        self.assertIsNone(running['seconds'])
        process.send_signal(signal.SIGTERM)
        _, errors = process.communicate(timeout=8)
        self.assertEqual(process.returncode, 143, errors.decode())
        with self.assertRaises(ProcessLookupError):
            os.kill(child_pid, 0)
        data = self.data(journal)
        completed = next(row for row in data['phases'] if row['phase'] == 'build')
        self.assertEqual((data['outcome'], completed['outcome'], completed['exit_code']),
                         ('interrupted', 'interrupted', 143))
        self.assertIsNotNone(completed['seconds'])
        self.assertTrue(all(row['outcome'] == 'unstarted' for row in data['phases']
                            if row['phase'] != 'build'))

    def test_failed_cancellation_probe_assertion_and_stopped_wrapper_are_cleaned(self):
        for stopped in (False, True):
            with self.subTest(stopped=stopped):
                journal, process, child_pid = self.cancellation_probe()
                if stopped:
                    process.send_signal(signal.SIGSTOP)
                with self.assertRaisesRegex(AssertionError, 'forced readiness assertion'):
                    try:
                        self.fail('forced readiness assertion')
                    finally:
                        cleanup_process(process, timeout=.1 if stopped else 5)
                self.assertEqual(process.returncode, 143)
                with self.assertRaises(ProcessLookupError):
                    os.kill(child_pid, 0)
                self.assertEqual(self.data(journal)['outcome'], 'interrupted')

    def test_writer_cleanup_reaps_after_an_early_assertion_failure(self):
        process = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)'],
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.addCleanup(cleanup_process, process)
        with self.assertRaisesRegex(AssertionError, 'forced writer assertion'):
            try:
                self.fail('forced writer assertion')
            finally:
                cleanup_process(process)
        self.assertIsNotNone(process.returncode)
        with self.assertRaises(ChildProcessError):
            os.waitpid(process.pid, os.WNOHANG)

    def test_owned_runner_normalizes_child_signal_without_losing_raw_status(self):
        journal = self.journal(clock=timing.time.monotonic)
        result = timing.execute('build', [sys.executable, '-c',
            'import os,signal; os.kill(os.getpid(), signal.SIGTERM)'], journal=journal, owned=True)
        self.assertEqual(result, 143)
        row = next(row for row in self.data(journal)['phases'] if row['phase'] == 'build')
        self.assertEqual((row['outcome'], row['exit_code']), ('interrupted', -15))

    def test_artifact_contains_allowlisted_metadata_and_is_bounded(self):
        secret = '/private/home/sensitive-token'
        with patch.dict(os.environ, {'CODE_FACTORY_TIMING_CACHE': secret, 'OWNER_TOKEN': secret}):
            journal = self.journal()
            output = 'go1.24.2 ' + secret + '\n'
            with patch.object(timing.subprocess, 'check_output', return_value=output):
                journal.refresh()
            runner = Mock(return_value=subprocess.CompletedProcess([], 7))
            self.assertEqual(timing.execute('build', ['private-command', secret],
                                           journal=journal, owned=True, runner=runner), 7)
        serialized = journal.path.read_text()
        self.assertNotIn(secret, serialized)
        self.assertNotIn('private-command', serialized)
        self.assertNotIn('OWNER_TOKEN', serialized)
        self.assertNotIn(str(self.root), serialized)
        data = self.data(journal)
        self.assertEqual(data['cache_condition'], 'unspecified')
        self.assertTrue(all(value == '1.24.2' for value in data['toolchains'].values()))
        with self.assertRaises(ValueError):
            journal.start(secret)
        while len(self.data(journal)['phases']) < 256:
            journal.stop(journal.start('frontend'), 0)
        with self.assertRaises(ValueError):
            journal.start('frontend')
        self.assertEqual(len(self.data(journal)['phases']), 256)
        self.assertLess(journal.path.stat().st_size, 128 * 1024)

    def test_working_identity_tracks_inputs_and_ignores_journal_outputs(self):
        def git(*arguments):
            return subprocess.check_output(['git', *arguments], cwd=self.root,
                                           stderr=subprocess.DEVNULL).decode().strip()
        git('init', '-b', 'main')
        git('config', 'user.name', 'Fixture')
        git('config', 'user.email', 'fixture@example.invalid')
        (self.root / '.gitignore').write_text('/verification-results/\n')
        source = self.root / 'source.txt'
        source.write_text('initial source\n')
        git('add', '.')
        git('commit', '-m', 'Fixture input')
        baseline = WORKING_IDENTITY()
        self.assertFalse(baseline['dirty'])
        self.assertEqual(baseline['head'], git('rev-parse', 'HEAD'))
        with patch.object(timing, 'identity', side_effect=WORKING_IDENTITY):
            journal = self.journal()
        journal.stop(journal.start('build'), 0)
        self.assertEqual(WORKING_IDENTITY(), baseline)
        source.write_text('changed source\n')
        tracked = WORKING_IDENTITY()
        self.assertTrue(tracked['dirty'])
        self.assertNotEqual(tracked['inputs_sha256'], baseline['inputs_sha256'])
        private = self.root / 'private-filename.txt'
        private.write_text('/private/home/credential\n')
        untracked = WORKING_IDENTITY()
        self.assertNotEqual(untracked['inputs_sha256'], tracked['inputs_sha256'])
        self.assertEqual(untracked['head'], baseline['head'])
        self.assertEqual(self.data(journal)['source'], baseline)
        self.assertNotIn('private-filename', journal.path.read_text())
        self.assertNotIn('/private/home/credential', json.dumps(untracked))

    def test_identity_hashes_exact_whitespace_prefixed_tracked_and_untracked_names(self):
        for kind in ('tracked', 'untracked'):
            for prefix in (' ', '\t', '\n', ' \t\n'):
                with self.subTest(kind=kind, prefix=repr(prefix)):
                    root = self.root / (kind + '-' + prefix.encode().hex())
                    root.mkdir()
                    def git(*arguments):
                        return subprocess.check_output(['git', *arguments], cwd=root,
                                                       stderr=subprocess.DEVNULL)
                    git('init', '-b', 'main')
                    git('config', 'user.name', 'Fixture')
                    git('config', 'user.email', 'fixture@example.invalid')
                    (root / '.gitignore').write_text('/verification-results/\n')
                    (root / 'z-control').write_text('fixed source\n')
                    name = prefix + 'private-input' + prefix
                    source = root / name
                    if kind == 'tracked':
                        source.write_text('committed source\n')
                    git('add', '.')
                    git('commit', '-m', 'Fixture names')
                    head = git('rev-parse', 'HEAD').decode().strip()
                    source.write_text('identity-secret-body: first dirty content\n')
                    with patch.object(timing, 'ROOT', root):
                        paths = timing.git('ls-files', '-z', '--cached', '--others', '--exclude-standard')
                        self.assertTrue(paths.startswith(os.fsencode(name) + b'\0'), paths)
                        first = WORKING_IDENTITY()
                        self.assertTrue(first['dirty'])
                        self.assertEqual(first['head'], head)
                        with patch.object(timing, 'identity', side_effect=WORKING_IDENTITY):
                            journal = self.journal()
                        journal.stop(journal.start('build'), 0)
                        self.assertEqual(WORKING_IDENTITY(), first)
                        source.write_text('identity-secret-body: second dirty content\n')
                        second = WORKING_IDENTITY()
                        self.assertTrue(second['dirty'])
                        self.assertEqual(second['head'], head)
                        self.assertNotEqual(first['inputs_sha256'], second['inputs_sha256'])
                        self.assertEqual(self.data(journal)['source'], first)
                        serialized = journal.path.read_text()
                        for private in ('private-input', 'identity-secret-body', str(root)):
                            self.assertNotIn(private, serialized)
                        self.assertEqual(set(second), {'head', 'inputs_sha256', 'dirty'})

    def test_identity_frames_binary_contents_and_record_boundaries(self):
        self.git_history(root_only=True)
        def git(*arguments):
            return subprocess.check_output(['git', *arguments], cwd=self.root,
                                           stderr=subprocess.DEVNULL)
        a, b = self.root / 'a', self.root / 'b'
        a.write_bytes(b'baseline')
        b.write_bytes(b'Y')
        a.chmod(0o644)
        b.chmod(0o644)
        git('add', '.')
        git('commit', '-m', 'Fixture framing')
        git('rm', '--', 'b')
        a.write_bytes(b'X\0b\x000\0Y')
        first = WORKING_IDENTITY()
        self.assertNotIn(b'b', timing.git('ls-files', '-z', '--cached', '--others').split(b'\0'))
        with patch.object(timing, 'identity', side_effect=WORKING_IDENTITY):
            journal = self.journal()
        a.write_bytes(b'X')
        b.write_bytes(b'Y')
        b.chmod(0o644)
        git('add', '--', 'b')
        second = WORKING_IDENTITY()
        self.assertIn(b'b', timing.git('ls-files', '-z', '--cached', '--others').split(b'\0'))
        self.assertEqual(first['head'], second['head'])
        self.assertTrue(first['dirty'])
        self.assertTrue(second['dirty'])
        self.assertNotEqual(first['inputs_sha256'], second['inputs_sha256'])
        self.assertEqual(self.data(journal)['source'], first)
        self.assertEqual(set(first), {'head', 'inputs_sha256', 'dirty'})

    def test_identity_distinguishes_file_link_absence_and_executable_mode(self):
        self.git_history(root_only=True)
        entry = self.root / 'entry'
        entry.write_bytes(b'absent')
        entry.chmod(0o644)
        regular = WORKING_IDENTITY()['inputs_sha256']
        subprocess.check_call(['git', 'add', '--', 'entry'], cwd=self.root,
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.assertEqual(WORKING_IDENTITY()['inputs_sha256'], regular)
        entry.chmod(0o755)
        executable = WORKING_IDENTITY()['inputs_sha256']
        entry.chmod(0o644)
        self.assertEqual(WORKING_IDENTITY()['inputs_sha256'], regular)
        entry.unlink()
        entry.symlink_to('absent')
        link = WORKING_IDENTITY()['inputs_sha256']
        entry.unlink()
        missing = WORKING_IDENTITY()['inputs_sha256']
        self.assertEqual(len({regular, executable, link, missing}), 4)

    def test_git_text_identities_and_comparisons_remain_normalized(self):
        base, head = self.git_history()
        self.assertEqual(WORKING_IDENTITY()['head'], head)
        self.assertFalse(WORKING_IDENTITY()['dirty'])
        for suffix, merge_base in (('', False), (' --merge-base', True)):
            with self.subTest(merge_base=merge_base):
                result = COMPARISON('--base ' + base + ' --head ' + head + suffix)
                self.assertEqual(result, {'status': 'resolved', 'base': base, 'head': head,
                                         'selection': 'commit', 'merge_base': merge_base})

    def test_instrumentation_failure_preserves_owned_runner_status_and_privacy(self):
        journal = Mock()
        journal.start.side_effect = OSError('/private/home/secret argv=credential')
        runner = Mock(return_value=subprocess.CompletedProcess([], 29))
        errors = io.StringIO()
        with contextlib.redirect_stderr(errors):
            result = timing.execute('build', ['irrelevant'], journal=journal, owned=True, runner=runner)
        self.assertEqual(result, 29)
        self.assertEqual(runner.call_args.kwargs['timeout'], float('inf'))
        journal.stop.assert_not_called()
        self.assertNotIn('/private', errors.getvalue())
        self.assertNotIn('credential', errors.getvalue())
        journal.start.side_effect = None
        journal.start.return_value = 'phase-id'
        journal.stop.side_effect = OSError('/private/home/secret argv=credential')
        with contextlib.redirect_stderr(errors):
            self.assertEqual(timing.execute('build', ['irrelevant'], journal=journal,
                                           owned=True, runner=runner), 29)
        self.assertNotIn('/private', errors.getvalue())
        self.assertNotIn('credential', errors.getvalue())
        with patch.object(timing, 'Journal') as journals, \
                patch('specs_lib.processes.run_verification', return_value=subprocess.CompletedProcess([], 23)) as owned, \
                patch.object(timing.subprocess, 'call', return_value=99) as plain, \
                contextlib.redirect_stderr(io.StringIO()):
            journals.create.side_effect = OSError('unwritable evidence')
            self.assertEqual(timing.verify(), 23)
        owned.assert_called_once()
        plain.assert_not_called()
        journal.path = self.root / 'journal.json'
        journal.finish.side_effect = OSError('/private/home/secret')
        with patch.object(timing, 'selected', return_value=journal), \
                patch('specs_lib.processes.run_verification', return_value=subprocess.CompletedProcess([], 31)), \
                contextlib.redirect_stderr(errors), contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(timing.verify(), 31)
        self.assertNotIn('/private', errors.getvalue())

    def test_local_orchestration_keeps_order_and_stops_at_failure(self):
        # The timing boundary delegates the whole graph once; Make owns ordering.
        argv = ['make', '--no-keep-going', 'verify-steps', 'FLOW_ARGS=--base fixture']
        for status in (0, 19):
            with self.subTest(status=status):
                journal = Mock(path=self.root / 'journal.json')
                with patch.object(timing, 'selected', return_value=journal), \
                        patch('specs_lib.processes.run_verification',
                              return_value=subprocess.CompletedProcess(argv, status)) as runner, \
                        patch.dict(os.environ, {'CODE_FACTORY_REAL_CODEX': '1'}), \
                        contextlib.redirect_stdout(io.StringIO()):
                    self.assertEqual(timing.verify(argv), status)
                    self.assertEqual(os.environ['CODE_FACTORY_REAL_CODEX'], '0')
                runner.assert_called_once_with(argv, cwd=self.root, timeout=float('inf'))
                journal.finish.assert_called_once_with('success' if status == 0 else 'failure')

    def test_malformed_comparison_metadata_cannot_stop_bootstrap_worker(self):
        arguments = ['make', '--no-keep-going', 'verify-bootstrap-steps']
        errors = io.StringIO()
        with patch.object(timing, 'comparison', side_effect=COMPARISON), \
                patch.dict(os.environ, {'FLOW_ARGS': '--base /private/token --head'}), \
                patch('specs_lib.processes.run_verification',
                      return_value=subprocess.CompletedProcess(arguments, 19)) as runner, \
                contextlib.redirect_stderr(errors), contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(timing.verify(arguments), 19)
        runner.assert_called_once_with(arguments, cwd=self.root, timeout=float('inf'))
        journal, = (self.root / 'verification-results').glob('*.json')
        data = json.loads(journal.read_text())
        self.assertEqual(data['comparison'], {'status': 'unavailable'})
        self.assertEqual(data['outcome'], 'failure')
        self.assertNotIn('/private/token', journal.read_text())
        self.assertEqual(errors.getvalue(), '')

    def git_history(self, *, root_only=False):
        def git(*arguments):
            return subprocess.check_output(['git', *arguments], cwd=self.root,
                                           stderr=subprocess.DEVNULL).decode().strip()
        git('init', '-b', 'main')
        git('config', 'user.name', 'Fixture')
        git('config', 'user.email', 'fixture@example.invalid')
        (self.root / '.gitignore').write_text('/verification-results/\n')
        git('add', '.')
        git('commit', '-m', 'Fixture root')
        first = git('rev-parse', 'HEAD')
        if root_only:
            return first, first
        (self.root / 'source.txt').write_text('Second source snapshot\n')
        git('add', '.')
        git('commit', '-m', 'Fixture child')
        return first, git('rev-parse', 'HEAD')

    def test_manual_setup_failure_retains_existing_parent_comparison(self):
        base, head = self.git_history()
        event = self.root / 'event.json'
        event.write_text('{}')
        exported = self.root / 'github-env'
        environment = {'GITHUB_EVENT_PATH': str(event), 'GITHUB_ENV': str(exported),
                       'GITHUB_EVENT_NAME': 'workflow_dispatch', 'GITHUB_REF': 'refs/heads/main',
                       'GITHUB_ACTIONS': 'true'}
        with patch.dict(os.environ, environment), patch.object(ci, 'ROOT', self.root), \
                patch.object(timing, 'identity', side_effect=WORKING_IDENTITY), \
                patch.object(timing, 'toolchains', return_value={'python': '3.12.0'}):
            timing.hosted_action('hosted-start')
            additions = dict(line.split('=', 1) for line in exported.read_text().splitlines())
            with patch.dict(os.environ, additions):
                journal = timing.selected()
                initial = self.data(journal)
                self.assertEqual(initial['comparison'], {'status': 'resolved', 'base': base,
                    'head': head, 'bootstrap': False, 'selection': 'commit'})
                self.assertEqual(initial['comparison'], {'status': 'resolved',
                    **ci.comparison({}, 'workflow_dispatch', '', 'refs/heads/main'),
                    'selection': 'commit'})
                self.assertEqual(initial['source']['head'], head)
                timing.hosted_action('setup-finish', 'failure')
                timing.hosted_action('hosted-finish', 'failure')
        data = self.data(journal)
        self.assertEqual(data['comparison'], initial['comparison'])
        self.assertEqual(data['outcome'], 'failure')
        self.assertEqual(data['phases'][0]['outcome'], 'failure')
        self.assertTrue(all(row['outcome'] == 'unstarted' for row in data['phases'][1:]))

    def test_manual_root_metadata_preserves_existing_bootstrap_guard(self):
        _, head = self.git_history(root_only=True)
        event = self.root / 'event.json'
        event.write_text('{}')
        for pin, ref, accepted in (('', 'refs/heads/main', False),
                                   (head, 'refs/heads/main', True),
                                   (head, 'refs/heads/feature', False)):
            with self.subTest(pin=pin, ref=ref), patch.dict(os.environ, {
                    'GITHUB_EVENT_PATH': str(event), 'GITHUB_EVENT_NAME': 'workflow_dispatch',
                    'GITHUB_REF': ref, 'CI_BOOTSTRAP_SHA': pin}), \
                    patch.object(ci, 'ROOT', self.root), patch.object(ci, 'git', wraps=ci.git) as git:
                result = timing.hosted_comparison()
                self.assertEqual(result, {'status': 'resolved', 'base': None, 'head': head,
                    'bootstrap': True, 'selection': 'commit'} if accepted else {'status': 'unavailable'})
                self.assertNotIn(unittest.mock.call('rev-parse', 'HEAD^'), git.call_args_list)

    def test_hosted_manual_metadata_errors_are_private_and_unavailable(self):
        event = self.root / 'private-event.json'
        event.write_text('{"private /owner/secret": malformed}')
        errors = io.StringIO()
        with patch.dict(os.environ, {'GITHUB_EVENT_PATH': str(event),
                'GITHUB_EVENT_NAME': 'workflow_dispatch', 'GITHUB_REF': 'refs/heads/main'}), \
                contextlib.redirect_stderr(errors):
            self.assertEqual(timing.hosted_comparison(), {'status': 'unavailable'})
        self.assertEqual(errors.getvalue(), '')

    def test_rejected_manual_feature_retains_only_partial_timing_evidence(self):
        self.git_history()
        event = self.root / 'event.json'
        event.write_text('{}')
        exported = self.root / 'github-env'
        environment = {'GITHUB_EVENT_PATH': str(event), 'GITHUB_ENV': str(exported),
                       'GITHUB_EVENT_NAME': 'workflow_dispatch', 'GITHUB_REF': 'refs/heads/feature',
                       'GITHUB_ACTIONS': 'true'}
        with patch.dict(os.environ, environment), patch.object(ci, 'ROOT', self.root), \
                patch.object(timing, 'toolchains', return_value={}):
            timing.hosted_action('hosted-start')
            additions = dict(line.split('=', 1) for line in exported.read_text().splitlines())
            with patch.dict(os.environ, additions), patch.object(ci, 'run_verification') as verify, \
                    patch.object(ci, 'artifact_manifest') as manifest, \
                    patch.object(sys, 'argv', ['ci', 'verify']), \
                    contextlib.redirect_stderr(io.StringIO()):
                timing.hosted_action('setup-finish', 'success')
                with self.assertRaises(SystemExit) as rejected:
                    ci.main()
                self.assertEqual(rejected.exception.code, 1)
                verify.assert_not_called()
                manifest.assert_not_called()
                timing.hosted_action('hosted-finish', 'failure')
                journal = timing.selected()
        data = self.data(journal)
        self.assertEqual(data['comparison'], {'status': 'unavailable'})
        self.assertEqual(data['outcome'], 'failure')
        self.assertEqual(data['phases'][0]['outcome'], 'success')
        self.assertTrue(all(row['outcome'] == 'unstarted' for row in data['phases'][1:]))
        self.assertFalse((self.root / 'dist').exists())

    def test_local_metadata_signals_finalize_before_worker_starts(self):
        for number, expected in ((signal.SIGINT, 130), (signal.SIGTERM, 143)):
            with self.subTest(signal=number):
                root = self.root / str(number)
                root.mkdir()
                tools = root / 'tools'
                tools.mkdir()
                listener = socket.socket()
                self.addCleanup(listener.close)
                listener.bind(('127.0.0.1', 0))
                listener.listen(1)
                version = tools / 'go'
                version.write_text('#!' + sys.executable + '\n' +
                    "import os,socket,time\n" +
                    "connection=socket.create_connection(('127.0.0.1', int(os.environ['METADATA_READY_PORT'])))\n" +
                    "connection.sendall(str(os.getpid()).encode()); connection.close(); time.sleep(60)\n")
                version.chmod(0o755)
                marker = root / 'worker-started'
                source = '''import pathlib,signal,sys
import verification_timing as timing
timing.ROOT = pathlib.Path(sys.argv[1])
timing.identity = lambda: {'head': 'a'*40, 'inputs_sha256': 'b'*64, 'dirty': False}
signal.signal(signal.SIGTERM, lambda number, frame: sys.exit(128 + number))
try:
    sys.exit(timing.verify([sys.executable, '-c',
        'import pathlib,sys; pathlib.Path(sys.argv[1]).touch()', sys.argv[2]]))
except KeyboardInterrupt:
    sys.exit(130)
'''
                environment = {**os.environ, 'PATH': str(tools) + os.pathsep + os.environ['PATH'],
                               'METADATA_READY_PORT': str(listener.getsockname()[1])}
                process = subprocess.Popen([sys.executable, '-c', source, str(root), str(marker)],
                    cwd=MODULE_DIRECTORY, env=environment, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                self.addCleanup(cleanup_process, process)
                ready, _, _ = select.select([listener], [], [], 10)
                self.assertTrue(ready, 'metadata command did not report readiness')
                connection, _ = listener.accept()
                with connection:
                    connection.settimeout(2)
                    version_pid = int(connection.recv(32))
                version_fd = os.pidfd_open(version_pid)
                def cleanup_version(version_fd=version_fd):
                    try:
                        signal.pidfd_send_signal(version_fd, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    finally:
                        os.close(version_fd)
                self.addCleanup(cleanup_version)
                journal, = (root / 'verification-results').glob('*.json')
                running = json.loads(journal.read_text())
                self.assertEqual(running['outcome'], 'running')
                self.assertFalse(marker.exists())
                process.send_signal(number)
                _, errors = process.communicate(timeout=8)
                self.assertEqual(process.returncode, expected, errors.decode())
                # The stdlib terminates the version child; asynchronous zombie
                # reaping after SIGINT is outside the owned worker graph contract.
                exited, _, _ = select.select([version_fd], [], [], 2)
                self.assertTrue(exited, 'metadata version command remained running')
                completed = json.loads(journal.read_text())
                self.assertEqual(completed['outcome'], 'interrupted')
                self.assertIsNotNone(completed['wall_seconds'])
                self.assertTrue(all(row['outcome'] == 'unstarted' and row['seconds'] is None
                                    for row in completed['phases']))
                self.assertFalse(marker.exists())

    def test_hosted_setup_failure_keeps_initial_event_identity(self):
        event = self.root / 'event.json'
        event.write_text(json.dumps({'pull_request': {
            'base': {'sha': 'c' * 40}, 'head': {'sha': 'a' * 40}}}))
        exported = self.root / 'github-env'
        environment = {'GITHUB_EVENT_PATH': str(event), 'GITHUB_ENV': str(exported),
                       'GITHUB_ACTIONS': 'true'}
        with patch.dict(os.environ, environment), patch.object(timing, 'toolchains', return_value={'python': '3.12.0'}):
            with patch.object(sys, 'argv', ['timing', 'hosted-start']):
                self.assertEqual(timing.main(), 0)
            additions = dict(line.split('=', 1) for line in exported.read_text().splitlines())
            with patch.dict(os.environ, additions):
                journal = timing.selected()
                initial = self.data(journal)
                self.assertEqual(initial['comparison']['base'], 'c' * 40)
                self.assertEqual(initial['source']['head'], 'a' * 40)
                self.assertEqual(initial['phases'][0]['outcome'], 'running')
                with patch.object(sys, 'argv', ['timing', 'setup-finish', '--outcome', 'failure']):
                    self.assertEqual(timing.main(), 0)
                with patch.object(sys, 'argv', ['timing', 'hosted-finish', '--outcome', 'failure']):
                    self.assertEqual(timing.main(), 0)
        data = self.data(journal)
        self.assertEqual(data['comparison'], initial['comparison'])
        self.assertEqual(data['source'], initial['source'])
        self.assertEqual(data['outcome'], 'failure')
        self.assertEqual(data['phases'][0]['outcome'], 'failure')
        self.assertTrue(all(row['outcome'] == 'unstarted' for row in data['phases'][1:]))


if __name__ == '__main__':
    unittest.main()
