#!/usr/bin/env python3
"""Owned local execution and fail-closed native graph regressions."""
import contextlib
import io
import json
import os
from pathlib import Path
import signal
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import specs
import user_flows as uf
from specs_lib import local_verification as local
from test_specs import fixture

ROOT = Path(__file__).resolve().parents[1]
NOTE = 'Reviewed the actual isolated fixture behavior and its explicit assertions.'


class LocalVerificationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.root = self.directory / 'repository'
        self.root.mkdir()
        for name, content in fixture().items():
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(content)
        self.command = [sys.executable, 'scripts/test_app.py']
        catalog_path = self.root / uf.CATALOG
        catalog = json.loads(catalog_path.read_text())
        for suite in catalog['suites'].values():
            suite['commands'] = [self.command, self.command]
        catalog_path.write_text(json.dumps(catalog))
        (self.root / 'scripts/test_app.py').write_text('def test_perform(): pass\n')
        uf.git(self.root, 'init', '-q')
        uf.git(self.root, 'add', '.')
        uf.git(self.root, '-c', 'user.name=Test', '-c', 'user.email=test@example.invalid', 'commit', '-qm', 'Fixture')
        self.base = uf.revision(self.root, 'HEAD')
        (self.root / '.git/info/exclude').write_text('bin/\n')
        (self.root / 'app.py').write_text('def perform():\n    return 2\n')
        self.affected = specs.analyze(uf.snapshot(self.root, self.base), uf.snapshot(self.root), self.base)[1]
        self.review = self.directory / 'review.json'
        self.review.write_text(json.dumps({subject: {'kind': 'refactor', **dict.fromkeys(
            ('description', 'implementation', 'tests'), NOTE)} for subject in self.affected}))
        self.receipts = [self.root / specs.RECORDS / (subject + '.json') for subject in self.affected]
        self.step = local.Step('correctness', self.root, tuple(self.command))
        self.check = local.Step('specifications', self.root, ('python3', 'scripts/specs.py', 'check', '--base', self.base))
        # Isolated fixtures retain selected tools without live host launcher paths.
        tool_directories = [str(Path(shutil.which(name)).resolve(strict=True).parent)
                            for name in ('go', 'node', 'npm', 'python3', 'bash', 'gcc', 'g++', 'git', 'make', 'ldconfig', 'readelf')
                            if shutil.which(name)]
        stable_path = os.pathsep.join(dict.fromkeys([*tool_directories,
            '/usr/local/sbin', '/usr/local/bin', '/usr/sbin', '/usr/bin', '/sbin', '/bin']))
        self.env = patch.dict(os.environ, {'CODE_FACTORY_REAL_CODEX': '0', 'PATH': stable_path}, clear=False)
        self.env.start()
        for name in (*local.MAKE_CARRIERS, 'FLOW_BASE', 'FLOW_ARGS', 'LD_PRELOAD', 'LD_AUDIT', 'LD_LIBRARY_PATH'):
            os.environ.pop(name, None)
        self.addCleanup(self.env.stop)
        self.python_runtime = self.directory / 'python-runtime'
        self.python_runtime.mkdir()
        (self.python_runtime / 'dependency.py').write_text('VALUE = 7\n')
        # Most fixtures exercise coordinator transitions, with an explicit small
        # dependency tree. Dedicated inventory controls use the real discovery.
        self.discover_python = local.python_inputs
        self.discover_native = local.native_library_inputs
        native = patch.object(local, 'native_library_inputs', return_value=set())
        native.start()
        self.addCleanup(native.stop)
        resources = patch.object(local, 'python_inputs', return_value=({self.python_runtime}, set()))
        resources.start()
        self.addCleanup(resources.stop)

    def run_fixture_python(self, code, **options):
        wrapper = ('from pathlib import Path as FixturePath\nfrom unittest.mock import patch\n'
                   'from specs_lib import local_verification as fixture_local\n'
                   'with patch.object(fixture_local,"python_inputs",return_value=({FixturePath('
                   + repr(str(self.python_runtime)) + ')},set())),patch.object(fixture_local,"native_library_inputs",return_value=set()):\n exec(' + repr(code) + ')\n')
        return subprocess.run([sys.executable, '-c', wrapper], **options)

    def run_owned(self, runner, plan=None, base=None):
        with patch.object(local, 'native_plan', side_effect=lambda root, target, base:
                          [] if target == 'local-verification-prepare' else plan or [self.step, self.check]), contextlib.redirect_stdout(io.StringIO()):
            return local.verify(self.root, base or self.base, self.review, runner=runner,
                                summary=self.directory / 'summary.json')

    def run_alias_command(self, command, **options):
        return subprocess.run(command, check=True, capture_output=True, timeout=options['timeout'])

    def test_identical_commands_execute_once_per_invocation_and_real_receipts_check(self):
        calls = []
        def run(command, **options):
            calls.append((tuple(command), options['cwd']))
            if command[0] == 'python3':
                self.assertEqual(specs.main(['check', '--root', str(self.root), '--base', self.base]), 0)
            return subprocess.CompletedProcess(command, 0)
        result = self.run_owned(run)
        self.assertTrue(result['verified'])
        self.assertEqual(calls.count((tuple(self.command), self.root)), 1)
        self.assertTrue(all(path.is_file() for path in self.receipts))
        self.run_owned(run)
        self.assertEqual(calls.count((tuple(self.command), self.root)), 2)
        summary = json.loads((self.directory / 'summary.json').read_text())
        self.assertTrue(summary['success'])
        self.assertEqual(summary['commands'], 3)
        cache_row, = [row for row in summary['phases'] if row['phase'] == 'python-cache-prepare']
        self.assertGreater(cache_row['entries'], 0)
        self.assertGreater(cache_row['bytes'], 0)
        self.assertEqual(set(cache_row), {'phase', 'seconds', 'exit_code', 'entries', 'bytes'})
        self.assertEqual(summary['source']['head'], self.base)
        self.assertEqual(summary['source']['inputs_sha256'], local.source_identity(self.root))
        self.assertEqual(summary['prepared_inputs_sha256'], local.artifact_identity(self.root))
        self.assertEqual(summary['comparison']['base'], self.base)
        for value in (summary['environment_sha256'], *summary['toolchains'].values()):
            self.assertRegex(value, r'^[0-9a-f]{64}$')
        self.assertNotIn(str(self.root), json.dumps(summary))
        self.assertNotIn('CODE_FACTORY_REAL_CODEX', json.dumps(summary))

    def test_source_helper_untracked_environment_and_review_mutations_fail(self):
        for mutation in ('source', 'helper', 'untracked', 'environment', 'review'):
            with self.subTest(mutation=mutation):
                # Each case receives an independent fixture and invocation.
                case = LocalVerificationTests('test_identical_commands_execute_once_per_invocation_and_real_receipts_check')
                case.setUp()
                try:
                    helper = case.root / 'bin/helper'
                    helper.parent.mkdir()
                    helper.write_text('prepared helper')
                    original_env = dict(os.environ)
                    def run(command, **options):
                        if mutation == 'source':
                            (case.root / 'app.py').write_text('changed')
                        elif mutation == 'helper':
                            helper.write_text('changed helper')
                        elif mutation == 'untracked':
                            (case.root / 'new-input').write_text('new fixture')
                        elif mutation == 'environment':
                            os.environ['OWNED_NEGATIVE_CONTROL'] = 'changed'
                        elif mutation == 'review':
                            case.review.write_text('{}')
                        return subprocess.CompletedProcess(command, 0)
                    try:
                        with self.assertRaises(uf.FlowError):
                            case.run_owned(run)
                    finally:
                        os.environ.clear()
                        os.environ.update(original_env)
                    self.assertTrue(all(not path.exists() for path in case.receipts))
                finally:
                    case.doCleanups()

    def test_moving_comparison_is_rejected_without_changing_head_or_inputs(self):
        tree = uf.git(self.root, 'rev-parse', self.base + '^{tree}').decode().strip()
        replacement = uf.git(self.root, '-c', 'user.name=Test', '-c', 'user.email=test@example.invalid',
                             'commit-tree', tree, '-p', self.base, '-m', 'Alternate comparison').decode().strip()
        uf.git(self.root, 'branch', 'moving', self.base)
        original_source = local.source_identity(self.root)
        def move(command, **options):
            uf.git(self.root, 'update-ref', 'refs/heads/moving', replacement)
            return subprocess.CompletedProcess(command, 0)
        with self.assertRaisesRegex(uf.FlowError, 'Review decisions or comparison changed'):
            self.run_owned(move, base='moving')
        self.assertEqual(uf.revision(self.root, 'HEAD'), self.base)
        self.assertEqual(local.source_identity(self.root), original_source)
        self.assertEqual(uf.revision(self.root, 'moving'), replacement)
        self.assertTrue(all(not path.exists() for path in self.receipts))

    def test_failed_and_cancelled_reruns_remove_previous_success_and_late_receipts(self):
        for status in ('failure', 'interrupt', 'late-failure', 'late-interrupt'):
            with self.subTest(status=status):
                self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))
                self.assertTrue(all(path.exists() for path in self.receipts))
                def run(command, **options):
                    late = command == list(self.check.command)
                    if status.startswith('late') != late:
                        return subprocess.CompletedProcess(command, 0)
                    if status.endswith('interrupt'):
                        raise KeyboardInterrupt
                    return subprocess.CompletedProcess(command, 9)
                with self.assertRaises(KeyboardInterrupt if status.endswith('interrupt') else uf.FlowError):
                    self.run_owned(run)
                self.assertTrue(all(not path.exists() for path in self.receipts))
                self.assertFalse(json.loads((self.directory / 'summary.json').read_text())['success'])

    def test_review_validation_precedes_make_and_does_not_destroy_prior_success(self):
        self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))
        self.review.write_text('{"malformed":"review"}')
        with patch.object(local, 'native_plan', side_effect=AssertionError('Make must not run')):
            with self.assertRaises(uf.FlowError):
                local.verify(self.root, self.base, self.review)
        self.assertTrue(all(path.exists() for path in self.receipts))

    def test_real_verifier_subprocess_runs_once_and_observes_fixture_environment(self):
        launches = self.directory / 'launches'
        (self.root / 'scripts/test_app.py').write_text(
            'def test_perform(): pass\nimport os,pathlib\n'
            + 'assert os.environ["CODE_FACTORY_REAL_CODEX"] == "0"\n'
            + 'with pathlib.Path(' + repr(str(launches)) + ').open("a") as output: output.write("executed\\n")\n')
        affected = specs.analyze(uf.snapshot(self.root, self.base), uf.snapshot(self.root), self.base)[1]
        self.review.write_text(json.dumps({subject: {'kind': 'refactor', **dict.fromkeys(
            ('description', 'implementation', 'tests'), NOTE)} for subject in affected}))
        def run(command, **options):
            if command == list(self.check.command):
                return subprocess.CompletedProcess(command, specs.main(
                    ['check', '--root', str(self.root), '--base', self.base]))
            return uf.run_verification(command, **options)
        self.run_owned(run)
        self.assertEqual(launches.read_text(), 'executed\n')
        self.run_owned(run)
        self.assertEqual(launches.read_text(), 'executed\nexecuted\n')

    def test_real_subprocess_source_and_helper_mutations_invalidate_receipts(self):
        for target in ('app.py', 'bin/helper'):
            with self.subTest(target=target):
                case = LocalVerificationTests('test_identical_commands_execute_once_per_invocation_and_real_receipts_check')
                case.setUp()
                try:
                    path = case.root / target
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_text('def perform():\n    return 3\n' if target == 'app.py' else 'initial input')
                    (case.root / 'scripts/test_app.py').write_text(
                        'def test_perform(): pass\nimport pathlib\n'
                        + 'pathlib.Path(' + repr(target) + ').write_text("mutated input")\n')
                    affected = specs.analyze(uf.snapshot(case.root, case.base), uf.snapshot(case.root), case.base)[1]
                    case.review.write_text(json.dumps({subject: {'kind': 'refactor', **dict.fromkeys(
                        ('description', 'implementation', 'tests'), NOTE)} for subject in affected}))
                    with self.assertRaises(uf.FlowError):
                        case.run_owned(uf.run_verification)
                    self.assertEqual(path.read_text(), 'mutated input')
                    self.assertTrue(all(not path.exists() for path in case.receipts))
                finally:
                    case.doCleanups()

    def test_real_mutate_restore_and_directory_replacement_are_rejected(self):
        for mutation in ('file', 'directory'):
            with self.subTest(mutation=mutation):
                helper = self.root / 'bin/helper'
                helper.parent.mkdir(exist_ok=True)
                helper.write_text('original')
                source = ('def test_perform(): pass\nimport pathlib,os\n'
                          + ('p=pathlib.Path("bin/helper");p.write_text("changed");p.write_text("original")\n'
                             if mutation == 'file' else
                             'os.rename("bin","old-bin");os.rename("old-bin","bin")\n'))
                (self.root / 'scripts/test_app.py').write_text(source)
                affected = specs.analyze(uf.snapshot(self.root, self.base), uf.snapshot(self.root), self.base)[1]
                self.review.write_text(json.dumps({subject: {'kind': 'refactor', **dict.fromkeys(
                    ('description', 'implementation', 'tests'), NOTE)} for subject in affected}))
                with self.assertRaises(uf.FlowError):
                    self.run_owned(uf.run_verification)
                self.assertEqual(helper.read_text(), 'original')
                self.assertTrue(all(not path.exists() for path in self.receipts))

    def test_mutation_monitor_overflow_fails_closed(self):
        from specs_lib.input_watch import InputWatch, EVENT, OVERFLOW
        watch = InputWatch(self.root, local.runtime_output)
        self.addCleanup(watch.close)
        with patch('specs_lib.input_watch.os.read', return_value=EVENT.pack(-1, OVERFLOW, 0, 0)):
            with self.assertRaisesRegex(uf.FlowError, 'lost coverage'):
                watch.check()

    def test_continuous_ignored_mutation_events_have_a_local_drain_bound(self):
        from specs_lib.input_watch import InputWatch, EVENT, MAX_DRAIN_READS
        watch = InputWatch(self.root, local.runtime_output)
        self.addCleanup(watch.close)
        descriptor = next(w for w, path in watch.paths.items() if ('.', None) in path)
        name = b'verification-results\0'
        name += b'\0' * (-len(name) % 4)
        record = EVENT.pack(descriptor, 0x100, 0, len(name)) + name
        with patch('specs_lib.input_watch.os.read', return_value=record) as read:
            with self.assertRaisesRegex(uf.FlowError, 'drain bound'):
                watch.check()
        self.assertEqual(read.call_count, MAX_DRAIN_READS)

    def test_summary_cannot_overwrite_decisions_or_a_reconciliation_receipt(self):
        for summary in [self.review, self.receipts[0]]:
            with self.subTest(summary=summary), patch.object(local, 'native_plan', side_effect=AssertionError('Make must not run')):
                original = self.review.read_bytes()
                with self.assertRaises(uf.FlowError):
                    local.verify(self.root, self.base, self.review, summary=summary)
                self.assertEqual(self.review.read_bytes(), original)

    def test_same_command_in_different_directory_is_not_reused(self):
        calls = []
        session = local.Session(self.root, self.base, [self.step], runner=lambda command, **options:
                                calls.append(options['cwd']) or subprocess.CompletedProcess(command, 0))
        self.addCleanup(session.close)
        session.freeze()
        session.execute(self.step)
        session.execute(local.Step('correctness', self.root / 'scripts', self.step.command))
        self.assertEqual(calls, [self.root, self.root / 'scripts'])

    def test_native_current_graph_and_aggregate_required_targets(self):
        plan = local.native_plan(ROOT, 'verify-steps', self.base)
        self.assertIn(('go', 'test', '-race', '-count=1', './...'), [step.command for step in plan])
        self.assertIn(('npm', 'test'), [step.command for step in plan])
        build = local.expand(ROOT, ['make', 'build'], self.base)
        self.assertTrue(set(build) <= set(plan))
        performance = local.expand(ROOT, ['make', 'test-performance'], self.base)
        self.assertTrue(set(performance) <= set(plan))
        preparation = local.native_plan(ROOT, 'local-verification-prepare', self.base)
        self.assertIn(('node', 'node_modules/vite/bin/vite.js', 'optimize'), [step.command for step in preparation])
        self.assertTrue(all(step.phase in local.PREPARATION for step in preparation))

    def test_native_owner_plumbing_requires_exact_reviewed_binding_before_make(self):
        source = (ROOT / 'Makefile').read_text()
        self.assertIn('--acquire-make-owner', source)
        for change in (source.replace('--acquire-make-owner', '--unreviewed-owner', 1),
                       source + '\nverify-steps:\n\t$(shell touch unreviewed-marker)\n'):
            with self.subTest(change=change[:40]):
                (self.root / 'Makefile').write_text(change)
                with patch.object(local.subprocess, 'run', side_effect=AssertionError('Make must not execute')):
                    with self.assertRaises(uf.FlowError):
                        local.native_plan(self.root, 'verify-steps', self.base)

    def test_owned_entry_shares_stable_owner_and_rejects_competitor_before_work(self):
        fields = ('CODE_FACTORY_VERIFY_RUN', 'CODE_FACTORY_VERIFY_ROOT',
                  'CODE_FACTORY_VERIFY_TOKEN', 'CODE_FACTORY_VERIFY_TMPDIR', 'TMPDIR')
        previous = {key: os.environ.get(key) for key in fields}
        observed, nested = [], self.directory / 'nested-owner.json'
        (self.root / '.git/info/exclude').write_text('bin/\n.code-factory-verify-*/\n')
        def run(command, **options):
            observed.append(tuple(os.environ.get(key) for key in fields))
            self.assertEqual(os.environ['CODE_FACTORY_VERIFY_ROOT'], str(self.root))
            self.assertTrue(Path(os.environ['CODE_FACTORY_VERIFY_RUN']).is_dir())
            code = ('import json,os\nfrom pathlib import Path\n'
                    'from specs_lib.isolation import managed\n'
                    'with managed(Path(' + repr(str(self.root)) + ')):\n'
                    ' Path(' + repr(str(nested)) + ').write_text(json.dumps({key:os.environ.get(key) for key in '
                    + repr(fields) + '}))\n')
            env = {**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}
            result = uf.run_verification([sys.executable, '-c', code], cwd=self.root)
            self.assertEqual(result.returncode, 0)
            self.assertEqual(json.loads(nested.read_text()), dict(zip(fields, observed[-1])))
            independent = {key: value for key, value in env.items()
                           if not key.startswith('CODE_FACTORY_VERIFY_')}
            result = self.run_fixture_python(code, cwd=self.root,
                                    env=independent, capture_output=True, text=True, timeout=15)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('Verification already owns this checkout', result.stderr)
            return subprocess.CompletedProcess(command, 0)
        with patch.dict(os.environ, {'TMPDIR': str(self.directory), 'PYTHONPATH': str(ROOT / 'scripts')}):
            self.assertTrue(self.run_owned(run)['verified'])
        self.assertEqual(len(set(observed)), 1)
        self.assertEqual({key: os.environ.get(key) for key in fields}, previous)

    def test_only_authenticated_owner_namespace_is_omitted_from_input_guards(self):
        (self.root / '.git/info/exclude').write_text('bin/\n.code-factory-verify-*/\n')
        with patch.dict(os.environ, {'TMPDIR': str(self.root)}), local.managed(self.root):
            session = local.Session(self.root, self.base, [self.step], runner=lambda command, **options:
                                    subprocess.CompletedProcess(command, 0))
            try:
                session.freeze()
                actual = Path(os.environ['TMPDIR']) / 'owned-output'
                actual.write_text('private runtime output')
                session.execute(self.step)
                unrelated = self.root / '.code-factory-verify-untrusted'
                unrelated.mkdir()
                (unrelated / 'input').write_text('unrelated ignored input')
                with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                    session.execute(self.step)
            finally:
                session.close()

    def test_unknown_new_make_command_is_rejected_before_execution(self):
        makefile = self.root / 'Makefile'
        makefile.write_text((ROOT / 'Makefile').read_text() + '\nverify-steps:\n\tpython3 -c "raise SystemExit(99)"\n')
        with self.assertRaisesRegex(uf.FlowError, 'Unrecognized verification recipe'):
            local.native_plan(self.root, 'verify-steps', self.base)

    def test_unsafe_make_constructs_fail_before_native_make(self):
        source = (ROOT / 'Makefile').read_text()
        unsafe = ['\ninclude other.mk\n', '\nSIDE := $(shell touch sentinel)\n',
                  '\nverify-steps: forced\nforced:\n\t+touch sentinel\n',
                  '\nverify-steps: recursive\nrecursive:\n\t$(MAKE) other\n',
                  '\nMakefile: other\n\ttouch sentinel\n',
                  '\nexport EXTRA_ENVIRONMENT := changed\n', '\nPATH := /unknown\n',
                  '\n.ONESHELL:\n']
        for addition in unsafe:
            with self.subTest(addition=addition), patch.object(local.subprocess, 'run', side_effect=AssertionError('Make must not start')):
                (self.root / 'Makefile').write_text(source + addition)
                with self.assertRaises(uf.FlowError):
                    local.native_plan(self.root, 'verify-steps', self.base)

    def test_generated_asset_changes_invalidate_before_reuse(self):
        helper = self.root / 'bin/helper'
        helper.parent.mkdir()
        helper.write_text('original helper')
        calls = []
        session = local.Session(self.root, self.base, [self.step], runner=lambda command, **options:
                                calls.append(command) or subprocess.CompletedProcess(command, 0))
        self.addCleanup(session.close)
        session.freeze()
        session.execute(self.step)
        helper.write_text('changed helper')
        with self.assertRaisesRegex(uf.FlowError, 'Prepared verification inputs changed'):
            session.execute(self.step)
        self.assertEqual(len(calls), 1)

    def test_toolchain_identity_change_is_rejected_without_other_mutations(self):
        calls = []
        session = local.Session(self.root, self.base, [self.step], runner=lambda command, **options:
                                calls.append(command) or subprocess.CompletedProcess(command, 0))
        self.addCleanup(session.close)
        session.freeze()
        session.execute(self.step)
        with patch.object(session, 'tool_identity', return_value='different executable') as identity:
            with self.assertRaisesRegex(uf.FlowError, 'toolchain changed'):
                session.execute(self.step)
        identity.assert_called_once_with()
        self.assertEqual(len(calls), 1)

    def test_external_compiler_helper_mutation_and_restoration_invalidate_reuse(self):
        tools = self.directory / 'external-tools'
        tools.mkdir()
        helper = tools / 'compile'
        helper.write_text('original compiler')
        for restore in (False, True):
            with self.subTest(restore=restore):
                helper.write_text('original compiler')
                calls = []
                with patch.object(local, 'helper_inputs', return_value=({tools}, {helper})):
                    session = local.Session(self.root, self.base, [self.step], runner=lambda command, **options:
                                            calls.append(command) or subprocess.CompletedProcess(command, 0))
                try:
                    session.freeze()
                    session.execute(self.step)
                    before = session.tool_identity()
                    subprocess.run([sys.executable, '-c',
                        'from pathlib import Path; p=Path(' + repr(str(helper)) + '); '
                        'p.write_text("changed compiler"); ' + ('p.write_text("original compiler")' if restore else '')],
                        check=True, timeout=15)
                    self.assertEqual(session.tool_identity() == before, restore)
                    with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                        session.execute(self.step)
                    self.assertEqual(len(calls), 1)
                finally:
                    session.close()

    def test_selected_go_and_gcc_helper_inventory_is_complete_or_fails(self):
        # This compiler/browser control does not require an installed module cache.
        # Actual module selection and admission have separate isolated controls.
        with patch.object(local, 'go_module_inputs', return_value=(set(), set())):
            directories, files = local.helper_inputs(ROOT, ['go', 'npm'])
        selected = json.loads(subprocess.run(['go', 'env', '-json', 'GOROOT', 'GOTOOLDIR', *local.CGO_DEFAULTS], cwd=ROOT,
                              capture_output=True, text=True, check=True, timeout=15).stdout)
        self.assertIn(Path(selected['GOROOT']), directories)
        self.assertIn(Path(shutil.which('npm')).resolve().parents[1], directories)
        compiler = Path(selected['GOTOOLDIR']) / 'compile'
        self.assertTrue(any(compiler.is_relative_to(path) for path in directories))
        self.assertTrue(any(path.name == 'cc1' for path in files))
        self.assertTrue(any(path.name.endswith('-as') or path.name == 'as' for path in files))
        self.assertRegex(local.helper_identity(local.helper_roots(directories), files), r'^[0-9a-f]{64}$')
        with patch.object(local.subprocess, 'run', return_value=subprocess.CompletedProcess(
                [], 0, json.dumps({**selected, 'CC': 'unavailable-compiler', 'CXX': 'g++', 'GOENV': 'off'}), '')):
            with self.assertRaisesRegex(uf.FlowError, 'unsupported helper inventory'):
                local.helper_inputs(ROOT, ['go'])

    def test_absent_tool_config_creation_is_watched_without_telemetry_outputs(self):
        config_root = self.directory / 'go-config'
        config_root.mkdir()
        config = config_root / 'env'
        calls = []
        with patch.object(local, 'helper_inputs', return_value=(set(), {config})):
            session = local.Session(self.root, self.base, [self.step], runner=lambda command, **options:
                                    calls.append(command) or subprocess.CompletedProcess(command, 0))
        try:
            session.freeze()
            session.execute(self.step)
            before = session.tool_identity()
            telemetry = config_root / 'telemetry'
            telemetry.mkdir()
            (telemetry / 'runtime.count').write_text('mutable Go telemetry output')
            session.execute(self.step)
            self.assertEqual(session.tool_identity(), before)
            self.assertEqual(len(calls), 1)
            config.write_text('CC=changed')
            with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                session.execute(self.step)
            self.assertEqual(len(calls), 1)
        finally:
            session.close()

    def test_real_signals_after_reconciliation_invalidate_receipts_and_preserve_status(self):
        self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))
        for number, expected in ((signal.SIGINT, 130), (signal.SIGTERM, 143)):
            with self.subTest(number=number):
                code = (
                    'import os,signal,sys\nfrom pathlib import Path\nfrom unittest.mock import patch\n'
                    'import specs\nfrom specs_lib import local_verification as local\n'
                    'root=Path(' + repr(str(self.root)) + ')\n'
                    'step=local.Step("correctness",root,' + repr(tuple(self.command)) + ')\n'
                    'check=local.Step("specifications",root,' + repr(self.check.command) + ')\n'
                    'original=local.reconcile\n'
                    'def reconcile(*args, **kwargs):\n'
                    ' result=original(*args, **kwargs)\n'
                    ' assert all((root/specs.RECORDS/(subject+".json")).is_file() for subject in result["reconciled"])\n'
                    ' os.kill(os.getpid(), ' + str(number) + ')\n return result\n'
                    'with patch.object(local,"reconcile",reconcile), '
                    'patch.object(local,"native_plan",side_effect=lambda root,target,base: [] if target=="local-verification-prepare" else [step,check]):\n'
                    ' raise SystemExit(local.main(' + repr(['--root', str(self.root), '--base', self.base,
                        '--review', str(self.review), '--summary', str(self.directory / 'signal-summary.json')]) + '))\n')
                result = self.run_fixture_python(code, cwd=ROOT,
                                        env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')},
                                        capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
                self.assertTrue(all(not path.exists() for path in self.receipts))
                self.assertFalse(json.loads((self.directory / 'signal-summary.json').read_text())['success'])

    def test_real_summary_write_failure_preserves_success_and_cancellation_status(self):
        for number, expected in ((None, 0), (signal.SIGINT, 130), (signal.SIGTERM, 143)):
            with self.subTest(number=number):
                code = (
                    'import os,sys,subprocess\nfrom pathlib import Path\nfrom unittest.mock import patch\n'
                    'from specs_lib import local_verification as local\n'
                    'root=Path(' + repr(str(self.root)) + ')\n'
                    'step=local.Step("correctness",root,' + repr(tuple(self.command)) + ')\n'
                    'check=local.Step("specifications",root,' + repr(self.check.command) + ')\n'
                    'def run(command, **options):\n'
                    + (' os.kill(os.getpid(), ' + str(number) + ')\n' if number else '')
                    + ' return subprocess.CompletedProcess(command,0)\n'
                    'with patch.object(local,"native_plan",side_effect=lambda root,target,base: [] if target=="local-verification-prepare" else [step,check]), '
                    'patch.object(local.uf,"run_verification",run):\n'
                    ' raise SystemExit(local.main(' + repr(['--root', str(self.root), '--base', self.base,
                        '--review', str(self.review), '--summary', '/dev/null/summary.json']) + '))\n')
                result = self.run_fixture_python(code, cwd=ROOT,
                                        env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')},
                                        capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
                self.assertIn('outcomes remain authoritative', result.stderr)
                self.assertEqual(all(path.exists() for path in self.receipts), number is None)

    def test_invocation_restores_callers_signal_handlers(self):
        handlers = {number: signal.getsignal(number) for number in (signal.SIGINT, signal.SIGTERM)}
        self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))
        self.assertEqual({number: signal.getsignal(number) for number in handlers}, handlers)
        with self.assertRaises(uf.FlowError):
            self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 9))
        self.assertEqual({number: signal.getsignal(number) for number in handlers}, handlers)


    def test_selected_browser_inventory_and_unsupported_overrides(self):
        roots = local.browser_inputs(ROOT)
        self.assertEqual(len(roots), 3)
        self.assertTrue(any(path.name.startswith('chromium-') for path in roots))
        self.assertTrue(any(path.name.startswith('chromium_headless_shell-') for path in roots))
        self.assertTrue(any(path.name.startswith('ffmpeg-') for path in roots))
        with patch.dict(os.environ, {'PLAYWRIGHT_BROWSERS_PATH': '0'}):
            with self.assertRaisesRegex(uf.FlowError, 'In-package Playwright'):
                local.browser_inputs(ROOT)
        web = self.root / 'web'
        web.mkdir()
        (web / 'playwright.config.ts').write_text('use: { channel: "chrome" }')
        with self.assertRaisesRegex(uf.FlowError, 'Custom Playwright'):
            local.browser_inputs(self.root)
        # A shorthand/spread configuration must not bypass a property-name scan.
        for name in local.BROWSER_CONFIGS:
            (web / name).write_bytes((ROOT / 'web' / name).read_bytes())
        custom = (ROOT / 'web/playwright.config.ts').read_text().replace(
            'export default defineConfig({', 'const channel = "chrome"; const custom = { channel };\nexport default defineConfig({').replace(
            'headless: true }', 'headless: true, ...custom }')
        (web / 'playwright.config.ts').write_text(custom)
        with self.assertRaisesRegex(uf.FlowError, 'Custom Playwright'):
            local.browser_inputs(self.root)

    def test_actual_npm_configuration_context_and_restored_change(self):
        web = self.root / 'web'
        web.mkdir()
        config, global_config = self.directory / 'npmrc', self.directory / 'global-npmrc'
        config.write_text('')
        global_config.write_text('')
        with patch.dict(os.environ, {'npm_config_userconfig': str(config),
                                     'npm_config_globalconfig': str(global_config)}):
            files = local.npm_inputs(self.root)
            self.assertTrue({config, global_config, Path('/bin/sh')} <= files)
            (web / '.npmrc').write_text('script-shell=/usr/bin/true\n')
            with self.assertRaisesRegex(uf.FlowError, 'Custom npm script-shell'):
                local.npm_inputs(self.root)
            (web / '.npmrc').unlink()
            # Prove that changing the actual selected config can suppress a script.
            marker = self.directory / 'npm-ran'
            (web / 'package.json').write_text(json.dumps({'scripts': {'test':
                'node -e "require(\'fs\').writeFileSync(' + repr(str(marker)) + ',\'ran\')"'}}))
            for changed in (config, global_config):
                calls = []
                with patch.object(local, 'helper_inputs', return_value=(set(), files)):
                    session = local.Session(self.root, self.base, [self.step], runner=lambda command, **options:
                                            calls.append(command) or subprocess.CompletedProcess(command, 0))
                try:
                    session.freeze()
                    session.execute(self.step)
                    before = session.tool_identity()
                    code = ('from pathlib import Path\nimport subprocess\np=Path(' + repr(str(changed)) + ')\n'
                            'p.write_text("script-shell=/usr/bin/true\\n")\n'
                            'try: subprocess.run(["npm","test"],cwd=' + repr(str(web)) + ',check=True,timeout=10)\n'
                            'finally: p.write_text("")\n')
                    subprocess.run([sys.executable, '-c', code], check=True, timeout=15, capture_output=True)
                    self.assertFalse(marker.exists())
                    self.assertEqual(session.tool_identity(), before)
                    with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                        session.execute(self.step)
                    self.assertEqual(len(calls), 1)
                finally:
                    session.close()

    def test_browser_root_mutation_restore_and_alias_retarget_reject_reuse(self):
        versions = self.directory / 'browsers'
        versions.mkdir()
        selected, other = versions / 'one', versions / 'two'
        for directory in (selected, other):
            directory.mkdir()
            (directory / 'browser').write_text('original')
        alias = self.directory / 'selected-browser'
        alias.symlink_to(selected, target_is_directory=True)
        for change in ('resource', 'alias'):
            with self.subTest(change=change):
                calls = []
                with patch.object(local, 'helper_inputs', return_value=({alias}, set())):
                    session = local.Session(self.root, self.base, [self.step], runner=lambda command, **options:
                                            calls.append(command) or subprocess.CompletedProcess(command, 0))
                try:
                    session.freeze()
                    session.execute(self.step)
                    before = session.tool_identity()
                    code = ('from pathlib import Path\np=Path(' + repr(str(alias)) + ')\n'
                        + ('f=p/"browser";f.write_text("changed");f.write_text("original")\n' if change == 'resource' else
                           'p.unlink();p.symlink_to(' + repr(str(other)) + ',target_is_directory=True)\n'
                           'p.unlink();p.symlink_to(' + repr(str(selected)) + ',target_is_directory=True)\n'))
                    subprocess.run([sys.executable, '-c', code], check=True, timeout=15)
                    self.assertEqual(session.tool_identity(), before)
                    with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                        session.execute(self.step)
                    self.assertEqual(len(calls), 1)
                finally:
                    session.close()

    def test_intermediate_executable_symlink_switch_and_restore_is_watched(self):
        versions = self.directory / 'python-versions'
        versions.mkdir()
        for name in ('one', 'two'):
            executable = versions / name / 'bin' / 'tool'
            executable.parent.mkdir(parents=True)
            executable.write_text('#!/bin/sh\nprintf ' + name + '\\n')
            executable.chmod(0o755)
        alias = self.directory / 'current'
        alias.symlink_to(versions / 'one', target_is_directory=True)
        executable = alias / 'bin' / 'tool'
        step = local.Step('correctness', self.root, (str(executable),))
        calls = []
        session = local.Session(self.root, self.base, [step], runner=lambda command, **options:
                                calls.append(command) or self.run_alias_command(command, **options))
        try:
            session.freeze()
            session.execute(step)
            before = session.tool_identity()
            code = ('from pathlib import Path\nimport subprocess\np=Path(' + repr(str(alias)) + ')\n'
                    'p.unlink();p.symlink_to(' + repr(str(versions / 'two')) + ',target_is_directory=True)\n'
                    'try: assert subprocess.check_output([' + repr(str(executable)) + ']).startswith(b"two")\n'
                    'finally:\n p.unlink();p.symlink_to(' + repr(str(versions / 'one')) + ',target_is_directory=True)\n')
            subprocess.run([sys.executable, '-c', code], check=True, timeout=15)
            self.assertEqual(session.tool_identity(), before)
            with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                session.execute(step)
            self.assertEqual(len(calls), 1)
        finally:
            session.close()

    def test_real_cleanup_signals_and_close_failure_remove_receipts(self):
        for number, expected, late_failure in ((signal.SIGINT, 130, False), (signal.SIGTERM, 143, False),
                                                (None, 1, False), (signal.SIGINT, 130, True),
                                                (signal.SIGTERM, 143, True), (None, 1, True)):
            with self.subTest(number=number, late_failure=late_failure):
                code = ('import os,signal,subprocess\nfrom pathlib import Path\nfrom unittest.mock import patch\n'
                    'import specs\nfrom specs_lib import local_verification as local\n'
                    'root=Path(' + repr(str(self.root)) + ')\n'
                    'step=local.Step("correctness",root,' + repr(tuple(self.command)) + ')\n'
                    'check=local.Step("specifications",root,' + repr(self.check.command) + ')\n'
                    'original=local.Session.close\n'
                    'def close(self):\n'
                    ' assert all((root/specs.RECORDS/(subject+".json")).exists() for subject in '
                    + repr(list(self.affected)) + ')\n'
                    ' original(self)\n'
                    + (' os.kill(os.getpid(),' + str(number) + ')\n' if number else
                       ' os.close(-1)\n')
                    + 'def run(command, **options):\n return subprocess.CompletedProcess(command,9 if ' + repr(late_failure) + ' and command==list(check.command) else 0)\n'
                    'with patch.object(local.Session,"close",close),patch.object(local.uf,"run_verification",run), '
                    'patch.object(local,"native_plan",side_effect=lambda root,target,base: [] if target=="local-verification-prepare" else [step,check]):\n'
                    ' raise SystemExit(local.main(' + repr(['--root', str(self.root), '--base', self.base,
                    '--review', str(self.review), '--summary', str(self.directory / 'cleanup-summary.json')]) + '))\n')
                result = self.run_fixture_python(code, cwd=ROOT,
                    env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
                self.assertTrue(all(not path.exists() for path in self.receipts))
                self.assertFalse(json.loads((self.directory / 'cleanup-summary.json').read_text())['success'])

    def test_summary_uses_frozen_wall_boundary_before_optional_work(self):
        captured = []
        original = local.write_summary
        def summary(*args):
            captured.append(args[-1])
            # Optional preparation, serialization and destination work cannot resample time.
            with patch.object(local.time, 'monotonic', side_effect=AssertionError('late wall sample')):
                original(*args)
        with patch.object(local, 'write_summary', side_effect=summary):
            self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))
        self.assertEqual(json.loads((self.directory / 'summary.json').read_text())['wall_seconds'], captured[0])


    def test_real_signals_at_prior_cleanup_handler_installation_window(self):
        for number, expected in ((signal.SIGINT, 130), (signal.SIGTERM, 143)):
            with self.subTest(number=number):
                marker = self.directory / 'cleanup-entry-observed'
                marker.unlink(missing_ok=True)
                code = ('import os,signal,sys,inspect,subprocess\nfrom pathlib import Path\nfrom unittest.mock import patch\n'
                    'import specs\nfrom specs_lib import local_verification as local\n'
                    'root=Path(' + repr(str(self.root)) + ')\n'
                    'step=local.Step("correctness",root,' + repr(tuple(self.command)) + ')\n'
                    'check=local.Step("specifications",root,' + repr(self.check.command) + ')\n'
                    'lines,start=inspect.getsourcelines(local.verify_owned)\n'
                    'entry=start+next(i for i,line in enumerate(lines) if "original_error = sys.exception()" in line)\n'
                    'fired=False\n'
                    'def trace(frame,event,arg):\n global fired\n'
                    ' if not fired and event=="line" and frame.f_code is local.verify_owned.__code__ and frame.f_lineno==entry:\n'
                    '  assert all((root/specs.RECORDS/(subject+".json")).exists() for subject in '
                    + repr(list(self.affected)) + ')\n'
                    '  fired=True\n  os.kill(os.getpid(),' + str(number) + ')\n'
                    ' return trace\n'
                    'original=local.Session.close\n'
                    'def close(self):\n assert fired\n original(self)\n Path(' + repr(str(marker)) + ').write_text("closed")\n'
                    'def run(command,**options):\n return subprocess.CompletedProcess(command,9 if command==list(check.command) else 0)\n'
                    'with patch.object(local.Session,"close",close),patch.object(local.uf,"run_verification",run),'
                    'patch.object(local,"native_plan",side_effect=lambda root,target,base: [] if target=="local-verification-prepare" else [step,check]):\n'
                    ' sys.settrace(trace)\n'
                    ' try: raise SystemExit(local.main(' + repr(['--root', str(self.root), '--base', self.base,
                        '--review', str(self.review), '--summary', str(self.directory / 'entry-summary.json')]) + '))\n'
                    ' finally: sys.settrace(None)\n')
                result = self.run_fixture_python(code, cwd=ROOT,
                    env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
                self.assertEqual(marker.read_text(), 'closed')
                self.assertTrue(all(not path.exists() for path in self.receipts))
                self.assertFalse(json.loads((self.directory / 'entry-summary.json').read_text())['success'])

    def test_pending_outer_cancellation_prevents_new_work_and_reuse(self):
        for reuse in (False, True):
            calls, accepted = [], False
            session = local.Session(self.root, self.base, [self.step], runner=lambda command, **options:
                                    calls.append(command) or subprocess.CompletedProcess(command, 0))
            try:
                session.freeze()
                with self.assertRaises(KeyboardInterrupt):
                    with local.invocation_signals() as cancellation:
                        session.guard = cancellation.check
                        if reuse:
                            session.execute(self.step)
                        os.kill(os.getpid(), signal.SIGINT)
                        session.execute(self.step)
                        accepted = True
                self.assertFalse(accepted)
                self.assertEqual(len(calls), int(reuse))
            finally:
                session.close()


    def test_selected_go_compiler_helper_and_resource_aliases_are_preserved(self):
        run = subprocess.run
        settings = json.loads(run(['go', 'env', '-json', 'GOROOT', 'GOTOOLDIR', 'CC', 'CXX', 'GOENV', *local.CGO_DEFAULTS],
                                  capture_output=True, text=True, check=True, timeout=15).stdout)
        aliases, admitted, roots = {}, set(), set()
        def alias_for(path):
            path = Path(path).resolve(strict=True)
            if path.parent not in aliases:
                alias = self.directory / ('selected-root-' + str(len(aliases)))
                alias.symlink_to(path.parent, target_is_directory=True)
                aliases[path.parent] = alias
            return aliases[path.parent] / path.name
        go_root = self.directory / 'selected-go'
        go_root.symlink_to(settings['GOROOT'], target_is_directory=True)
        selected = {**settings, 'GOROOT': str(go_root),
                    'GOTOOLDIR': str(go_root / Path(settings['GOTOOLDIR']).relative_to(settings['GOROOT']))}
        for key in ('CC', 'CXX'):
            compiler = self.directory / ('selected-' + key)
            compiler.symlink_to(shutil.which(settings[key]))
            selected[key] = str(compiler)
            admitted.add(compiler)
        def discovery(command, **options):
            if command[:2] == ['go', 'env']:
                return subprocess.CompletedProcess(command, 0, json.dumps(selected), '')
            if command[:2] == ['go', 'list'] or '-E' in command:
                return run(command, **options)
            result = run(command, **options)
            name = result.stdout.strip()
            actual = shutil.which(name) if not Path(name).is_absolute() else name
            alias = alias_for(actual)
            if command[-1] == '-print-libgcc-file-name':
                roots.add(alias.parent)
            admitted.add(alias)
            return subprocess.CompletedProcess(command, 0, str(alias) + '\n', '')
        with patch.object(local.subprocess, 'run', side_effect=discovery), \
                patch.object(local, 'go_module_inputs', return_value=(set(), set())):
            directories, files = local.helper_inputs(ROOT, ['go'])
        self.assertIn(go_root, directories)
        self.assertTrue(roots <= directories)
        self.assertTrue(admitted <= files)
        self.assertRegex(local.helper_identity(local.helper_roots(directories), files), r'^[0-9a-f]{64}$')

    def test_watch_close_failure_still_closes_remaining_real_descriptors(self):
        from specs_lib.input_watch import InputWatch
        session = local.Session(self.root, self.base, [self.step])
        extra = InputWatch(self.root, local.source_output)
        session.helper_watches.append(extra)
        session.freeze()
        artifact = session.artifact_watch
        os.close(session.watch.fd)
        try:
            with self.assertRaises(OSError):
                session.close()
            self.assertEqual(extra.fd, -1)
            self.assertEqual(artifact.fd, -1)
        finally:
            session.watch.fd = -1
            session.close()


    def test_go_overlay_and_workspace_configuration_reject_before_module_discovery(self):
        run = subprocess.run
        settings = json.loads(run(['go', 'env', '-json', 'GOROOT', 'GOTOOLDIR', 'CC', 'CXX', 'GOENV',
                                   'GOFLAGS', 'GOWORK'], cwd=ROOT, capture_output=True, text=True,
                                   check=True, timeout=15).stdout)
        for override in ({'GOFLAGS': '-overlay=/external/overlay.json'}, {'GOWORK': '/external/go.work'}):
            with self.subTest(override=override), patch.object(local.subprocess, 'run', return_value=
                    subprocess.CompletedProcess([], 0, json.dumps({**settings, **override}), '')), \
                    patch.object(local, 'go_module_inputs', side_effect=AssertionError('must reject before discovery')):
                with self.assertRaisesRegex(uf.FlowError, 'Custom Go flags'):
                    local.helper_inputs(ROOT, ['go'])

    def test_selected_python_search_covers_installed_dependencies_and_alias_data(self):
        directories, files = self.discover_python(ROOT, sys.executable)
        import jsonschema
        import yaml
        for module in (jsonschema, yaml):
            self.assertTrue(any(Path(module.__file__).is_relative_to(path) for path in directories))
        packages = self.directory / 'python-packages'
        packages.mkdir()
        data = self.directory / 'package-data'
        data.mkdir()
        (data / 'schema.json').write_text('{}')
        alias = packages / 'data'
        alias.symlink_to(data, target_is_directory=True)
        with patch.dict(os.environ, {'PYTHONPATH': str(packages)}):
            selected, _ = self.discover_python(ROOT, sys.executable)
        self.assertIn(packages, selected)
        roots = local.helper_roots({packages})
        self.assertIn(alias, roots)
        self.assertRegex(local.helper_identity(roots, set()), r'^[0-9a-f]{64}$')

    def test_real_go_and_python_dependency_source_restoration_rejects_evidence(self):
        dependency = self.directory / 'go-dependency'
        dependency.mkdir()
        (dependency / 'go.mod').write_text('module example.invalid/dependency\n\ngo 1.26.0\n')
        source = dependency / 'value.go'
        source.write_text('package dependency\nconst Value = 7\n')
        alias = self.directory / 'selected-go-dependency'
        alias.symlink_to(dependency, target_is_directory=True)
        go_project = self.directory / 'go-project'
        go_project.mkdir()
        (go_project / 'go.mod').write_text('module example.invalid/main\n\ngo 1.26.0\n'
            'require example.invalid/dependency v0.0.0\nreplace example.invalid/dependency => ' + str(alias) + '\n')
        (go_project / 'main_test.go').write_text('package main\nimport ("testing"; d "example.invalid/dependency")\n'
            'func TestDependency(t *testing.T) { if d.Value != 7 { t.Fatal(d.Value) } }\n')
        missing_embed = go_project / 'not-prepared.go'
        missing_embed.write_text('package main\nimport _ "embed"\n//go:embed not-prepared\nvar content string\n')
        go_directories, go_files = local.go_module_inputs(go_project)
        missing_embed.unlink()
        self.assertIn(alias, go_directories)
        self.assertIn(alias / 'go.mod', go_files)
        packages = self.directory / 'python-dependencies'
        packages.mkdir()
        python_source = packages / 'selected_dependency.py'
        python_source.write_text('VALUE = 7\n')
        for kind, target, selected in [('go', source, (go_directories, go_files)),
                                       ('python', python_source, ({packages}, set()))]:
            for restore in (False, True):
                with self.subTest(kind=kind, restore=restore):
                    before = target.read_bytes()
                    calls = []
                    def run(command, **options):
                        calls.append(command)
                        if kind == 'go':
                            subprocess.run(['go', 'test', '-count=1', './...'], cwd=go_project,
                                           check=True, capture_output=True, timeout=30)
                        else:
                            with patch.dict(os.environ, {'PYTHONPATH': str(packages)}):
                                discovered, _ = self.discover_python(self.root, sys.executable)
                                self.assertIn(packages, discovered)
                                subprocess.run([sys.executable, '-c', 'import selected_dependency; assert selected_dependency.VALUE == 7'],
                                               cwd=self.root, check=True, capture_output=True, timeout=15)
                        subprocess.run([sys.executable, '-c', 'from pathlib import Path; p=Path(' + repr(str(target)) + '); '
                            'p.write_bytes(' + repr(before + b'\n') + '); '
                            + ('p.write_bytes(' + repr(before) + ')' if restore else '')], check=True, timeout=15)
                        return subprocess.CompletedProcess(command, 0)
                    try:
                        with patch.object(local, 'helper_inputs', return_value=selected):
                            with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                                self.run_owned(run)
                        self.assertEqual(len(calls), 1)
                        self.assertTrue(all(not path.exists() for path in self.receipts))
                    finally:
                        target.write_bytes(before)

    def test_real_late_summary_and_owner_signals_invalidate_exact_publications(self):
        for window in ('summary', 'owner'):
            for number, expected in ((signal.SIGINT, 130), (signal.SIGTERM, 143)):
                with self.subTest(window=window, number=number):
                    summary = self.directory / 'late-summary.json'
                    code = ('import os,signal,sys,inspect,subprocess\nfrom pathlib import Path\nfrom unittest.mock import patch\n'
                        'import specs\nfrom specs_lib import local_verification as local\nfrom specs_lib import isolation\n'
                        'root=Path(' + repr(str(self.root)) + ')\n'
                        'step=local.Step("correctness",root,' + repr(tuple(self.command)) + ')\n'
                        'check=local.Step("specifications",root,' + repr(self.check.command) + ')\n'
                        'closed=False\nfired=False\noriginal=local.Session.close\n'
                        'def close(self):\n global closed\n original(self)\n closed=True\n'
                        'target=' + ('local.write_summary' if window == 'summary' else 'isolation.managed.__wrapped__') + '\n'
                        'lines,start=inspect.getsourcelines(target)\n'
                        'entry=start+next(i for i,line in enumerate(lines) if ' +
                        repr('temporary.replace(summary)' if window == 'summary' else 'for signum, handler in handlers.items():') + ' in line)\n'
                        'def trace(frame,event,arg):\n global fired\n'
                        ' if closed and not fired and event=="line" and frame.f_code is target.__code__ and frame.f_lineno==entry:\n'
                        '  assert all((root/specs.RECORDS/(subject+".json")).is_file() for subject in ' + repr(list(self.affected)) + ')\n'
                        '  fired=True\n  os.kill(os.getpid(),' + str(number) + ')\n'
                        ' return trace\n'
                        'with patch.object(local.Session,"close",close),patch.object(local,"native_plan",side_effect=lambda root,target,base: [] if target=="local-verification-prepare" else [step,check]):\n'
                        ' sys.settrace(trace)\n'
                        ' try: status=local.main(' + repr(['--root', str(self.root), '--base', self.base, '--review', str(self.review), '--summary', str(summary)]) + ')\n'
                        ' finally: sys.settrace(None)\n assert fired\n raise SystemExit(status)\n')
                    result = self.run_fixture_python(code, cwd=ROOT,
                        env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, capture_output=True, text=True, timeout=30)
                    self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
                    self.assertTrue(all(not path.exists() or path.read_bytes() == b'' for path in self.receipts))
                    self.assertTrue(not summary.exists() or summary.read_bytes() == b'')

    def test_late_rollback_preserves_subsequent_owner_atomic_receipts(self):
        original = local.Publications.invalidate
        fresh = {}
        def replace_then_invalidate(publications):
            child = ('import specs\nraise SystemExit(specs.main(' + repr(['reconcile', '--root', str(self.root),
                '--base', self.base, '--review', str(self.review)]) + '))')
            subprocess.run([sys.executable, '-c', child], cwd=ROOT,
                env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, check=True, capture_output=True, timeout=30)
            for path in self.receipts:
                fresh[path] = path.read_bytes()
                self.assertTrue(all(os.fstat(fd).st_ino != path.stat().st_ino for fd in publications.descriptors))
            original(publications)
        managed = local.managed
        for failure in ('signal', 'cache'):
            @contextlib.contextmanager
            def late_owner(root):
                with managed(root): yield
                if failure == 'signal':
                    os.kill(os.getpid(), signal.SIGINT)
                else:
                    entry = next(Path(sys.pycache_prefix).rglob('*.pyc'))
                    original_bytes = entry.read_bytes()
                    entry.write_bytes(b'poison')
                    entry.write_bytes(original_bytes)
            with self.subTest(failure=failure), patch.object(local, 'managed', late_owner), \
                    patch.object(local.Publications, 'invalidate', replace_then_invalidate):
                with self.assertRaises(KeyboardInterrupt if failure == 'signal' else uf.FlowError):
                    self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))
            self.assertTrue(fresh)
            self.assertEqual({path: path.read_bytes() for path in self.receipts}, fresh)
            self.assertEqual(specs.main(['check', '--root', str(self.root), '--base', self.base]), 0)



    def test_cold_module_cache_rejects_before_execution_without_source_changes(self):
        cache = self.directory / 'cold-modules'
        cache.mkdir()
        manifest = self.root / 'go.mod'
        sums = self.root / 'go.sum'
        manifest.write_text('module example.invalid/cold\n\ngo 1.23.0\nrequire golang.org/x/time v0.16.0\n')
        sums.write_text('')
        before = manifest.read_bytes(), sums.read_bytes()
        calls = []
        with patch.dict(os.environ, {'GOMODCACHE': str(cache), 'GOTOOLCHAIN': 'local', 'GOFLAGS': '', 'GOWORK': 'off'}):
            with self.assertRaisesRegex(uf.FlowError, 'go mod download all before review'):
                local.Session(self.root, self.base, [local.Step('correctness', self.root, ('go', 'test', './...'))],
                    runner=lambda *args, **kwargs: calls.append(args))
        self.assertEqual(calls, [])
        self.assertEqual((manifest.read_bytes(), sums.read_bytes()), before)

    def test_private_python_bytecode_ignores_default_cache_and_uses_source(self):
        packages = self.directory / 'selected-python-packages'
        packages.mkdir()
        module = packages / 'selected_input.py'
        module.write_text('VALUE = 999\n')
        import py_compile
        with patch.object(sys, 'pycache_prefix', None):
            py_compile.compile(str(module), doraise=True, invalidation_mode=py_compile.PycInvalidationMode.UNCHECKED_HASH)
        module.write_text('VALUE = 7\n')
        (packages / 'py_compile.py').write_text('raise AssertionError("prewarm imported arbitrary package")\n')
        calls = []
        def run(command, **options):
            calls.append(command)
            self.assertEqual(sys.pycache_prefix, os.environ['PYTHONPYCACHEPREFIX'])
            self.assertTrue(sys.dont_write_bytecode)
            prefix = Path(sys.pycache_prefix)
            self.assertTrue(prefix.is_dir())
            before = {str(path.relative_to(prefix)): path.read_bytes() for path in prefix.rglob('*') if path.is_file()}
            self.assertTrue(any('selected_input.' in name for name in before))
            for flags in [[], ['-O']]:
                # -O has no prepared entry: compile source without writing a cache.
                probe = subprocess.run([sys.executable, *flags, '-c',
                    'import sys; import selected_input; assert selected_input.VALUE == 7; '
                    'assert sys.dont_write_bytecode; print(sys.pycache_prefix)'],
                    cwd=self.root, capture_output=True, text=True, check=True, timeout=15)
                self.assertEqual(probe.stdout.strip(), sys.pycache_prefix)
            self.assertEqual({str(path.relative_to(prefix)): path.read_bytes() for path in prefix.rglob('*') if path.is_file()}, before)
            (packages / '__pycache__' / 'unused.pyc').write_bytes(b'unused output')
            return subprocess.CompletedProcess(command, 0)
        with patch.dict(os.environ, {'PYTHONPATH': str(packages)}), \
                patch.object(local, 'helper_inputs', return_value=({packages}, set())):
            self.run_owned(run)
        self.assertEqual(len(calls), 2)
        self.assertTrue(all(path.exists() for path in self.receipts))


    def test_prepared_python_cache_poison_and_restore_reject_before_reuse(self):
        for restore in (False, True):
            calls = []
            def poison(command, **options):
                calls.append(command)
                subprocess.run([sys.executable, '-c',
                    'import os; from pathlib import Path; '
                    'p=next(Path(os.environ["PYTHONPYCACHEPREFIX"]).rglob("*.pyc")); '
                    'original=p.read_bytes(); p.write_bytes(b"poison"); '
                    + ('p.write_bytes(original)' if restore else 'None')],
                    cwd=self.root, check=True, timeout=15)
                return subprocess.CompletedProcess(command, 0)
            with self.subTest(restore=restore), self.assertRaisesRegex(uf.FlowError, 'changed'):
                self.run_owned(poison)
            self.assertEqual(len(calls), 1)
            self.assertTrue(all(not path.exists() for path in self.receipts))

    def test_python_cache_late_summary_and_owner_teardown_mutations_reject_success(self):
        real_summary, real_managed = local.write_summary, local.managed
        def poison():
            entry = next(Path(sys.pycache_prefix).rglob('*.pyc'))
            original = entry.read_bytes()
            entry.write_bytes(b'poison')
            entry.write_bytes(original)
        def summary(*arguments, **options):
            real_summary(*arguments, **options)
            poison()
        @contextlib.contextmanager
        def owner(root):
            with real_managed(root): yield
            poison()
        for target, replacement in [('write_summary', summary), ('managed', owner)]:
            with self.subTest(window=target), patch.object(local, target, replacement), \
                    self.assertRaisesRegex(uf.FlowError, 'changed'):
                self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))
            self.assertTrue(all(not path.exists() or path.read_bytes() == b'' for path in self.receipts))
            summary_path = self.directory / 'summary.json'
            self.assertEqual(summary_path.read_bytes(), b'')

    def test_checkout_temporary_parent_rejects_before_owned_work(self):
        calls = []
        with patch.dict(os.environ, {'TMPDIR': str(self.root)}), \
                self.assertRaisesRegex(uf.FlowError, 'temporary parent outside the checkout'):
            self.run_owned(lambda command, **options: calls.append(command))
        self.assertEqual(calls, [])

    def test_python_cache_preparation_failure_restores_policy_and_closes_binding(self):
        previous = (os.environ.get('PYTHONPYCACHEPREFIX'), sys.pycache_prefix,
                    os.environ.get('PYTHONDONTWRITEBYTECODE'), sys.dont_write_bytecode)
        with self.assertRaises(subprocess.TimeoutExpired):
            with local.python_source_imports() as cache:
                descriptor, directory = cache.descriptor, cache.directory
                with patch.object(local.subprocess, 'run', side_effect=subprocess.TimeoutExpired('compile', 120)):
                    cache.prepare(self.root, set(), set())
        self.assertFalse(directory.exists())
        with self.assertRaises(OSError): os.fstat(descriptor)
        self.assertEqual((os.environ.get('PYTHONPYCACHEPREFIX'), sys.pycache_prefix,
                          os.environ.get('PYTHONDONTWRITEBYTECODE'), sys.dont_write_bytecode), previous)

    def test_python_cache_descriptor_loss_rejects_and_restores_context(self):
        previous = os.environ.get('PYTHONPYCACHEPREFIX'), sys.pycache_prefix
        with self.assertRaisesRegex(uf.FlowError, 'binding changed'):
            with local.python_source_imports() as cache:
                descriptor = cache.descriptor
                moved = cache.directory.with_name(cache.directory.name + '-moved')
                cache.directory.rename(moved)
                try: cache()
                finally: moved.rename(cache.directory)
        with self.assertRaises(OSError): os.fstat(descriptor)
        replacement = None
        try:
            with contextlib.redirect_stderr(io.StringIO()), self.assertRaisesRegex(uf.FlowError, 'binding changed'):
                with local.python_source_imports() as guard:
                    descriptor = int(Path(sys.pycache_prefix).name)
                    self.assertFalse(os.get_inheritable(descriptor))
                    os.close(descriptor)
                    replacement = os.open(self.root / 'app.py', os.O_RDONLY)
                    if replacement != descriptor:
                        os.dup2(replacement, descriptor, inheritable=False)
                        os.close(replacement)
                        replacement = descriptor
                    guard()
            self.assertTrue(os.fstat(replacement))
        finally:
            if replacement is not None:
                os.close(replacement)
        self.assertEqual((os.environ.get('PYTHONPYCACHEPREFIX'), sys.pycache_prefix), previous)
        calls = []
        def close_binding(command, **options):
            calls.append(command)
            os.close(int(Path(sys.pycache_prefix).name))
            return subprocess.CompletedProcess(command, 0)
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaisesRegex(uf.FlowError, 'binding was closed'):
            self.run_owned(close_binding)
        self.assertEqual(len(calls), 1)
        self.assertTrue(all(not path.exists() for path in self.receipts))
        self.assertEqual((os.environ.get('PYTHONPYCACHEPREFIX'), sys.pycache_prefix), previous)


    def test_only_exact_maintained_long_suites_receive_bounded_local_deadlines(self):
        commands = [('python3', 'scripts/test_ci.py'),
                    ('python3', '-m', 'unittest', 'discover', '-s', 'scripts', '-p', 'test_specs*.py')]
        for command in commands:
            step = local.Step('required-suite', self.root, command)
            self.assertEqual(local.command_timeout(self.root, step), 900)
            self.assertEqual(local.command_timeout(self.root, step, 1.5), 1.5)
            self.assertEqual(local.command_timeout(self.root, step, 1200), 900)
            other = local.Step('required-suite', self.directory, command)
            self.assertEqual(local.command_timeout(self.root, other), 600)
            self.assertEqual(local.command_timeout(self.root, other, 12), 12)
        for command in [commands[0] + ('--extra',), ('python3', 'scripts/custom.py'),
                        (sys.executable, 'scripts/test_ci.py'), commands[1][:-1] + ('other*.py',), self.step.command]:
            self.assertEqual(local.command_timeout(self.root, local.Step('required-suite', self.root, command)), 600)
        for requested in (0, -1, True, float('inf'), float('nan')):
            with self.subTest(requested=requested), self.assertRaises(uf.FlowError):
                local.command_timeout(self.root, self.step, requested)
        observed = []
        session = local.Session(self.root, self.base, [local.Step('tooling-ci', self.root, commands[0])], timeout=2,
            runner=lambda command, **options: observed.append(options['timeout']) or subprocess.CompletedProcess(command, 0))
        try:
            session.freeze()
            session.execute(local.Step('tooling-ci', self.root, commands[0]))
        finally:
            session.close()
        self.assertEqual(observed, [2])

    def test_partial_receipt_publication_failure_removes_all_selected_success(self):
        replace = Path.replace
        published = []
        def fail_second(path, target):
            if str(target).endswith('.json') and Path(target).parent == self.root / specs.RECORDS:
                if published:
                    raise OSError('partial receipt publication control')
                result = replace(path, target)
                published.append(target)
                return result
            return replace(path, target)
        with patch.object(Path, 'replace', fail_second):
            with self.assertRaisesRegex(OSError, 'partial receipt publication'):
                self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))
        self.assertEqual(len(published), 1)
        self.assertTrue(all(not path.exists() for path in self.receipts))

    def test_helper_rediscovery_rejects_real_goenv_switch_before_watches(self):
        config = self.directory / 'go-env'
        compilers = [self.directory / ('compiler-' + name) for name in ('first', 'second')]
        for compiler in compilers:
            compiler.symlink_to(shutil.which('gcc'))
        config.write_text('CC=' + str(compilers[0]) + '\n')
        watches, calls = [], []
        original_watch = local.InputWatch
        def watch(*args, **options):
            if not watches:
                config.write_text('CC=' + str(compilers[1]) + '\n')
            selected = original_watch(*args, **options)
            watches.append(selected)
            return selected
        step = local.Step('correctness', self.root, ('go', 'version'))
        with patch.dict(os.environ, {'GOENV': str(config), 'CC': '', 'CXX': '', 'GOFLAGS': '', 'GOWORK': 'off'}), \
                patch.object(local, 'go_module_inputs', return_value=(set(), set())), \
                patch.object(local, 'InputWatch', side_effect=watch):
            self.assertEqual(subprocess.check_output(['go', 'env', 'CC'], text=True, timeout=15).strip(), str(compilers[0]))
            session = None
            try:
                with self.assertRaisesRegex(uf.FlowError, 'selection changed during discovery'):
                    session = local.Session(self.root, self.base, [step], runner=lambda command, **options: calls.append(command))
            finally:
                if session is not None:
                    session.close()
            self.assertEqual(subprocess.check_output(['go', 'env', 'CC'], text=True, timeout=15).strip(), str(compilers[1]))
        self.assertTrue(watches)
        self.assertTrue(all(watch.fd == -1 for watch in watches))
        self.assertEqual(calls, [])

    def test_node_startup_source_rejects_before_discovery_or_required_work(self):
        preload, marker = self.directory / 'preload.cjs', self.directory / 'node-started'
        preload.write_text('require("fs").writeFileSync(' + json.dumps(str(marker)) + ', "loaded");\n')
        calls = []
        step = local.Step('correctness', self.root, ('npm', '--version'))
        with patch.dict(os.environ, {'NODE_OPTIONS': '--require=' + str(preload)}):
            with self.assertRaisesRegex(uf.FlowError, 'NODE_OPTIONS startup inputs are unsupported'):
                self.run_owned(lambda command, **options: calls.append(command), [step, self.check])
        self.assertEqual(calls, [])
        self.assertFalse(marker.exists())
        self.assertTrue(all(not path.exists() for path in self.receipts))

    def test_effective_cgo_flags_reject_environment_and_goenv_external_inputs(self):
        header, config = self.directory / 'selected.h', self.directory / 'go-env'
        header.write_text('/* valid external compiler input */\n')
        step = local.Step('correctness', self.root, ('go', 'version'))
        for source in ('environment', 'goenv'):
            for name in local.CGO_DEFAULTS:
                with self.subTest(source=source, name=name), patch.dict(os.environ):
                    for flag in local.CGO_DEFAULTS:
                        os.environ.pop(flag, None)
                    value = ('-Wl,' + str(header)) if name == 'CGO_LDFLAGS' else '-include ' + str(header)
                    config.write_text(name + '=' + value + '\n' if source == 'goenv' else '')
                    os.environ['GOENV'] = str(config)
                    os.environ['GOFLAGS'], os.environ['GOWORK'] = '', 'off'
                    if source == 'environment':
                        os.environ[name] = value
                    self.assertEqual(subprocess.check_output(['go', 'env', name], text=True, timeout=15).strip(), value)
                    calls = []
                    with patch.object(local, 'go_module_inputs') as modules:
                        with self.assertRaisesRegex(uf.FlowError, 'effective cgo compiler flags are unsupported'):
                            self.run_owned(lambda command, **options: calls.append(command), [step, self.check])
                        modules.assert_not_called()
                    self.assertEqual(calls, [])
                    self.assertTrue(all(not path.exists() for path in self.receipts))
            for name in ('CC', 'CXX'):
                with self.subTest(source=source, compiler=name), patch.dict(os.environ):
                    value = ('gcc' if name == 'CC' else 'g++') + ' -include ' + str(header)
                    os.environ.pop(name, None)
                    config.write_text(name + '=' + value + '\n' if source == 'goenv' else '')
                    os.environ['GOENV'] = str(config)
                    if source == 'environment':
                        os.environ[name] = value
                    self.assertEqual(subprocess.check_output(['go', 'env', name], text=True, timeout=15).strip(), value)
                    with self.assertRaisesRegex(uf.FlowError, 'unsupported helper inventory'):
                        local.helper_inputs(self.root, ['go'])
        for source in ('environment', 'goenv'):
            with self.subTest(defaults=source), patch.dict(os.environ):
                for name in local.CGO_DEFAULTS:
                    os.environ.pop(name, None)
                config.write_text(''.join(name + '=' + ' '.join(flags) + '\n' for name, flags in local.CGO_DEFAULTS.items())
                                  if source == 'goenv' else '')
                os.environ['GOENV'] = str(config)
                if source == 'environment':
                    os.environ.update({name: ' '.join(flags) for name, flags in local.CGO_DEFAULTS.items()})
                with patch.object(local, 'go_module_inputs', return_value=(set(), set())):
                    directories, files = local.helper_inputs(self.root, ['go'])
                self.assertTrue(any((directory / 'pthread.h').is_file() for directory in directories))
                self.assertTrue(files)

    def test_effective_npm_node_options_reject_environment_and_config_preloads(self):
        preload, marker = self.directory / 'npm-preload.cjs', self.directory / 'npm-started'
        preload.write_text('require("fs").writeFileSync(' + json.dumps(str(marker)) + ', "loaded");\n')
        config = self.directory / 'npmrc'
        web = self.root / 'web'
        web.mkdir(exist_ok=True)
        ignore = self.root / '.git/info/exclude'
        ignore.write_text(ignore.read_text() + 'web/.npmrc\n')
        value = '--require=' + str(preload)
        step = local.Step('correctness', self.root, ('npm', '--version'))
        for source in ('environment', 'config', 'web-config'):
            with self.subTest(source=source), patch.dict(os.environ):
                for name in ('NODE_OPTIONS', 'npm_config_node_options', 'NPM_CONFIG_NODE_OPTIONS'):
                    os.environ.pop(name, None)
                os.environ['npm_config_userconfig'] = str(config)
                config.write_text('node-options=' + value + '\n' if source == 'config' else '')
                (web / '.npmrc').write_text('node-options=' + value + '\n' if source == 'web-config' else '')
                if source == 'environment':
                    os.environ['npm_config_node_options'] = value
                effective = subprocess.check_output(['npm', 'config', 'get', 'node-options'],
                                                   cwd=web if source == 'web-config' else self.root, text=True, timeout=15).strip()
                self.assertEqual(effective, value)
                calls = []
                with self.assertRaisesRegex(uf.FlowError, 'npm node-options startup inputs are unsupported'):
                    self.run_owned(lambda command, **options: calls.append(command), [step, self.check])
                self.assertEqual(calls, [])
                self.assertFalse(marker.exists())
                self.assertTrue(all(not path.exists() for path in self.receipts))

    def test_gcc_search_overrides_reject_before_compiler_discovery(self):
        for name in local.GCC_CARRIERS:
            with self.subTest(name=name), patch.dict(os.environ, {name: str(self.directory)}):
                with patch.object(local.subprocess, 'run') as run:
                    with self.assertRaisesRegex(uf.FlowError, 'GCC include, library or helper search contexts'):
                        local.helper_inputs(self.root, ['go'])
                    run.assert_not_called()

    def test_gcc_header_search_roots_preserve_aliases_and_guard_restoration_and_creation(self):
        directories, absent = local.gcc_include_inputs(ROOT, shutil.which('gcc'), 'c')
        self.assertTrue(any((directory / 'pthread.h').is_file() for directory in directories))
        self.assertTrue(all(not path.exists() for path in absent))
        headers = self.directory / 'headers'
        headers.mkdir()
        header = headers / 'control.h'
        header.write_text('int selected_header_control;\n')
        alias, missing = self.directory / 'header-alias', self.directory / 'missing-headers'
        alias.symlink_to(headers, target_is_directory=True)
        duplicate = self.directory / 'duplicate-header-alias'
        duplicate.symlink_to(headers, target_is_directory=True)
        other = self.directory / 'other-headers'
        other.mkdir()
        (other / 'only-duplicate.h').write_text('int selected_duplicate_control;\n')
        compiler = self.directory / 'gcc-header-control'
        import shlex
        compiler.write_text('#!/bin/sh\nexec ' + shlex.quote(shutil.which('gcc')) + ' -nostdinc -I' +
                            shlex.quote(str(alias)) + ' -I' + shlex.quote(str(duplicate)) +
                            ' -I' + shlex.quote(str(missing)) + ' "$@"\n')
        compiler.chmod(0o755)
        directories, absent = local.gcc_include_inputs(self.root, compiler, 'c')
        self.assertEqual(directories, {alias, duplicate})
        self.assertEqual(absent, {missing})
        selected = subprocess.run([str(compiler), '-E', '-include', 'control.h', '-'], input='',
                                  text=True, capture_output=True, check=True, timeout=15)
        self.assertIn('int selected_header_control;', selected.stdout)
        for change in ('restore', 'create', 'duplicate', 'duplicate-restored'):
            with self.subTest(change=change), patch.object(local, 'helper_inputs', return_value=(directories, absent)):
                session = local.Session(self.root, self.base, [self.step],
                    runner=lambda command, **options: subprocess.CompletedProcess(command, 0))
                try:
                    session.freeze()
                    session.execute(self.step)
                    before = session.tool_identity()
                    if change == 'restore':
                        original = header.read_bytes()
                        header.write_text('changed')
                        header.write_bytes(original)
                        self.assertEqual(session.tool_identity(), before)
                    elif change == 'create':
                        missing.mkdir()
                    else:
                        duplicate.unlink()
                        duplicate.symlink_to(other, target_is_directory=True)
                        selected = subprocess.run([str(compiler), '-E', '-include', 'only-duplicate.h', '-'], input='',
                                                  text=True, capture_output=True, check=True, timeout=15)
                        self.assertIn('int selected_duplicate_control;', selected.stdout)
                        if change == 'duplicate-restored':
                            duplicate.unlink()
                            duplicate.symlink_to(headers, target_is_directory=True)
                            self.assertEqual(session.tool_identity(), before)
                    with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                        session.execute(self.step)
                finally:
                    session.close()
                    if missing.exists():
                        missing.rmdir()
                    if duplicate.resolve() != headers:
                        duplicate.unlink()
                        duplicate.symlink_to(headers, target_is_directory=True)

    def test_alias_runner_timeout_reaps_child_and_closes_session(self):
        step = local.Step('correctness', self.root, (sys.executable, '-c', 'import time; time.sleep(30)'))
        session = local.Session(self.root, self.base, [step], runner=self.run_alias_command, timeout=0.05)
        started = time.monotonic()
        try:
            session.freeze()
            with self.assertRaises(subprocess.TimeoutExpired) as failure:
                session.execute(step)
            self.assertEqual(failure.exception.timeout, 0.05)
            self.assertEqual(session.completed, {})
        finally:
            session.close()
        self.assertLess(time.monotonic() - started, 5)
        self.assertEqual(session.watch.fd, -1)
        self.assertEqual(session.artifact_watch.fd, -1)

    def test_default_sigterm_after_owner_restoration_and_final_cache_check_rolls_back(self):
        for window, preserve in ((window, preserve) for window in ('owner-restored', 'final-cache') for preserve in (False, True)):
            with self.subTest(window=window, preserve=preserve):
                summary = self.directory / 'restored-handler-summary.json'
                code = ('import os,signal,sys,inspect,subprocess\nfrom pathlib import Path\nfrom unittest.mock import patch\n'
                    'import specs\nfrom specs_lib import local_verification as local\nfrom specs_lib import isolation\n'
                    'signal.signal(signal.SIGTERM,signal.SIG_DFL)\n'
                    'root=Path(' + repr(str(self.root)) + ')\n'
                    'step=local.Step("correctness",root,' + repr(tuple(self.command)) + ')\n'
                    'check=local.Step("specifications",root,' + repr(self.check.command) + ')\n'
                    'closed=False\nfired=False\noriginal=local.Session.close\n'
                    'def close(self):\n global closed\n original(self)\n closed=True\n'
                    'original_invalidate=local.Publications.invalidate\n'
                    'def invalidate(publications):\n'
                    ' fresh={}\n'
                    ' if ' + repr(preserve) + ':\n'
                    '  subprocess.run([sys.executable,"scripts/specs.py","reconcile","--root",str(root),"--base",' + repr(self.base) + ',"--review",' + repr(str(self.review)) + '],cwd=' + repr(str(ROOT)) + ',check=True,capture_output=True,timeout=30)\n'
                    '  fresh={root/specs.RECORDS/(subject+".json"):(root/specs.RECORDS/(subject+".json")).read_bytes() for subject in ' + repr(list(self.affected)) + '}\n'
                    '  assert all(os.fstat(fd).st_ino!=path.stat().st_ino for fd in publications.descriptors for path in fresh)\n'
                    ' original_invalidate(publications)\n'
                    ' assert all(path.read_bytes()==content for path,content in fresh.items())\n'
                    'target=' + ('isolation.isolated.__wrapped__' if window == 'owner-restored' else 'local.verify') + '\n'
                    'lines,start=inspect.getsourcelines(target)\n'
                    'entry=start+' + ('max(i for i,line in enumerate(lines) if "check_interruption()" in line)' if window == 'owner-restored' else
                                      'next(i for i,line in enumerate(lines) if "imports_guard()" in line)') + '\n'
                    'def trace(frame,event,arg):\n global fired\n'
                    ' if closed and not fired and event=="line" and frame.f_code is target.__code__ and frame.f_lineno==entry:\n'
                    '  assert all((root/specs.RECORDS/(subject+".json")).is_file() for subject in ' + repr(list(self.affected)) + ')\n'
                    '  assert Path(' + repr(str(summary)) + ').is_file()\n'
                    '  fired=True\n  os.kill(os.getpid(),signal.SIGTERM)\n'
                    ' return trace\n'
                    'with patch.object(local.Session,"close",close),patch.object(local.Publications,"invalidate",invalidate),patch.object(local,"native_plan",side_effect=lambda root,target,base: [] if target=="local-verification-prepare" else [step,check]):\n'
                    ' sys.settrace(trace)\n'
                    ' try: status=local.main(' + repr(['--root', str(self.root), '--base', self.base, '--review', str(self.review), '--summary', str(summary)]) + ')\n'
                    ' except SystemExit as error: status=error.code\n'
                    ' finally: sys.settrace(None)\n'
                    ' assert fired\n assert signal.getsignal(signal.SIGTERM)==signal.SIG_DFL\n raise SystemExit(status)\n')
                result = self.run_fixture_python(code, cwd=ROOT,
                    env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, 143, result.stdout + result.stderr)
                if preserve:
                    self.assertEqual(specs.main(['check', '--root', str(self.root), '--base', self.base]), 0)
                else:
                    self.assertTrue(all(not path.exists() or path.read_bytes() == b'' for path in self.receipts))
                self.assertTrue(not summary.exists() or summary.read_bytes() == b'')

    def test_default_sigterm_at_exception_handoff_and_rollback_keeps_outputs_invalid(self):
        for window in ('except-entry', 'rollback-call', 'rollback-body'):
            with self.subTest(window=window):
                summary = self.directory / 'exception-handoff-summary.json'
                code = ('import os,signal,sys,inspect,subprocess,contextlib\nfrom pathlib import Path\nfrom unittest.mock import patch\n'
                    'import specs\nfrom specs_lib import local_verification as local\n'
                    'signal.signal(signal.SIGTERM,signal.SIG_DFL)\n'
                    'root=Path(' + repr(str(self.root)) + ')\n'
                    'step=local.Step("correctness",root,' + repr(tuple(self.command)) + ')\n'
                    'check=local.Step("specifications",root,' + repr(self.check.command) + ')\n'
                    'original=local.managed\nfired=False\n'
                    '@contextlib.contextmanager\ndef late(root):\n'
                    ' with original(root): yield\n'
                    ' entry=next(Path(sys.pycache_prefix).rglob("*.pyc"))\n'
                    ' data=entry.read_bytes()\n entry.write_bytes(b"changed")\n entry.write_bytes(data)\n'
                    'target=' + ('local.Publications.invalidate' if window == 'rollback-body' else 'local.verify') + '\n'
                    'lines,start=inspect.getsourcelines(target)\n'
                    'entry=start+next(i for i,line in enumerate(lines) if ' + repr(
                        'except BaseException as original_error:' if window == 'except-entry' else
                        'publications.invalidate()' if window == 'rollback-call' else 'for descriptor in self.descriptors:') + ' in line)\n'
                    'def trace(frame,event,arg):\n global fired\n'
                    ' if not fired and event=="line" and frame.f_code is target.__code__ and frame.f_lineno==entry:\n'
                    '  assert callable(signal.getsignal(signal.SIGTERM))\n'
                    '  assert all((root/specs.RECORDS/(subject+".json")).is_file() for subject in ' + repr(list(self.affected)) + ')\n'
                    '  assert Path(' + repr(str(summary)) + ').is_file()\n'
                    '  fired=True\n  os.kill(os.getpid(),signal.SIGTERM)\n'
                    ' return trace\n'
                    'with patch.object(local,"managed",late),patch.object(local,"native_plan",side_effect=lambda root,target,base: [] if target=="local-verification-prepare" else [step,check]):\n'
                    ' sys.settrace(trace)\n'
                    ' try: status=local.main(' + repr(['--root', str(self.root), '--base', self.base, '--review', str(self.review), '--summary', str(summary)]) + ')\n'
                    ' except SystemExit as error: status=error.code\n'
                    ' finally: sys.settrace(None)\n'
                    ' assert fired\n assert signal.getsignal(signal.SIGTERM)==signal.SIG_DFL\n raise SystemExit(status)\n')
                result = self.run_fixture_python(code, cwd=ROOT,
                    env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, 143, result.stdout + result.stderr)
                self.assertTrue(all(not path.exists() or path.read_bytes() == b'' for path in self.receipts))
                self.assertTrue(not summary.exists() or summary.read_bytes() == b'')

    def test_checked_signal_exit_and_cache_disposal_precede_publication_release(self):
        for window in ('checked-exit', 'cache-disposal', 'cache-disposal-error', 'publication-release'):
            with self.subTest(window=window):
                summary = self.directory / 'outcome-boundary-summary.json'
                code = ('import os,signal,sys,inspect,subprocess,tempfile,contextlib\nfrom pathlib import Path\nfrom unittest.mock import patch\n'
                    'import specs\nfrom specs_lib import local_verification as local\n'
                    'signal.signal(signal.SIGTERM,signal.SIG_DFL)\n'
                    'root=Path(' + repr(str(self.root)) + ')\n'
                    'step=local.Step("correctness",root,' + repr(tuple(self.command)) + ')\n'
                    'check=local.Step("specifications",root,' + repr(self.check.command) + ')\n'
                    'fired=False\ncaptured=[]\ncache_paths=[]\noriginal_cleanup=tempfile.TemporaryDirectory.cleanup\n'
                    'original_capture=local.Publications.capture\noriginal_cache=local.PythonCache.__init__\n'
                    'def capture(publications,path):\n original_capture(publications,path)\n captured[:]=publications.descriptors\n'
                    'def cache_init(cache,path):\n original_cache(cache,path)\n cache_paths.append(cache.directory)\n'
                    'def cleanup(self):\n'
                    ' original_cleanup(self)\n'
                    ' if Path(self.name).name.startswith("python-import-cache-"):\n'
                    '  raise OSError("cache disposal control")\n'
                    'target=' + ('local.invocation_signals.__wrapped__' if window == 'checked-exit' else
                                'local.Publications.close' if window == 'publication-release' else 'local.PythonCache.close') + '\n'
                    'lines,start=inspect.getsourcelines(target)\n'
                    'entry=start+next(i for i,line in enumerate(lines) if ' + repr(
                        'cancellation.check()' if window == 'checked-exit' else 'error = None' if window == 'publication-release' else 'try:') + ' in line)\n'
                    'def trace(frame,event,arg):\n global fired\n'
                    ' if not fired and event=="line" and frame.f_code is target.__code__ and frame.f_lineno==entry:\n'
                    '  if ' + repr(window == 'checked-exit') + ' and (not cache_paths or any(path.exists() for path in cache_paths)): return trace\n'
                    '  if ' + repr(window == 'cache-disposal-error') + ': return trace\n'
                    '  assert captured and all(os.fstat(fd).st_ino for fd in captured)\n'
                    '  if ' + repr(window in ('checked-exit', 'publication-release')) + ': assert cache_paths and all(not path.exists() for path in cache_paths)\n'
                    '  assert all((root/specs.RECORDS/(subject+".json")).is_file() for subject in ' + repr(list(self.affected)) + ')\n'
                    '  assert Path(' + repr(str(summary)) + ').is_file()\n'
                    '  assert ' + ('signal.getsignal(signal.SIGTERM)==signal.SIG_DFL' if window == 'publication-release' else
                                   'callable(signal.getsignal(signal.SIGTERM))') + '\n'
                    '  fired=True\n  os.kill(os.getpid(),signal.SIGTERM)\n'
                    ' return trace\n'
                    'with patch.object(local.Publications,"capture",capture),patch.object(local.PythonCache,"__init__",cache_init),patch.object(local,"native_plan",side_effect=lambda root,target,base: [] if target=="local-verification-prepare" else [step,check]):\n'
                    ' with patch.object(tempfile.TemporaryDirectory,"cleanup",cleanup) if ' + repr(window == 'cache-disposal-error') + ' else contextlib.nullcontext():\n'
                    '  sys.settrace(trace)\n'
                    '  try: status=local.main(' + repr(['--root', str(self.root), '--base', self.base, '--review', str(self.review), '--summary', str(summary)]) + ')\n'
                    '  except SystemExit as error: status=error.code\n'
                    '  finally: sys.settrace(None)\n'
                    '  assert fired or ' + repr(window == 'cache-disposal-error') + '\n'
                    '  assert signal.getsignal(signal.SIGTERM)==signal.SIG_DFL\n raise SystemExit(status)\n')
                result = self.run_fixture_python(code, cwd=ROOT,
                    env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, -signal.SIGTERM if window == 'publication-release' else
                                 1 if window == 'cache-disposal-error' else 143, result.stdout + result.stderr)
                if window == 'publication-release':
                    self.assertTrue(json.loads(summary.read_text())['success'])
                    self.assertEqual(specs.main(['check', '--root', str(self.root), '--base', self.base]), 0)
                else:
                    self.assertTrue(all(not path.exists() or path.read_bytes() == b'' for path in self.receipts))
                    self.assertTrue(not summary.exists() or summary.read_bytes() == b'')

    def test_publication_capture_failure_rolls_back_and_release_attempts_all_descriptors(self):
        capture = local.Publications.capture
        captured = []
        def fail_after_capture(publications, path):
            capture(publications, path)
            captured.extend(publications.descriptors)
            raise OSError('capture control')
        with patch.object(local.Publications, 'capture', fail_after_capture):
            with self.assertRaisesRegex(OSError, 'capture control'):
                self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))
        self.assertTrue(all(not path.exists() for path in self.receipts))
        for descriptor in captured:
            with self.assertRaises(OSError):
                os.fstat(descriptor)
        publications = local.Publications()
        paths = [self.directory / 'publication-one', self.directory / 'publication-two']
        for path in paths:
            path.write_text('owned')
            publications.capture(path)
        descriptors = list(publications.descriptors)
        os.close(descriptors[0])
        with self.assertRaises(OSError):
            publications.close()
        with self.assertRaises(OSError):
            os.fstat(descriptors[1])


    def test_elf_loader_selectors_reject_before_discovery_and_allow_empty_values(self):
        selectors = ('LD_PRELOAD', 'LD_AUDIT', 'LD_LIBRARY_PATH')
        for name in selectors:
            with self.subTest(name=name), patch.dict(os.environ, {name: str(self.directory / 'external-code')}):
                with patch.object(local.subprocess, 'run') as discover:
                    with self.assertRaisesRegex(uf.FlowError, 'ELF loader inputs are unsupported'):
                        local.helper_inputs(self.root, [])
                    discover.assert_not_called()
                with patch.object(local, 'managed') as owner, patch.object(local, 'native_plan') as plan:
                    with self.assertRaisesRegex(uf.FlowError, 'ELF loader inputs are unsupported'):
                        local.verify(self.root, self.base, self.review)
                    owner.assert_not_called()
                    plan.assert_not_called()
        with patch.dict(os.environ, dict.fromkeys(selectors, '')):
            self.assertEqual(local.helper_inputs(self.root, []), (set(), set()))
            self.assertTrue(self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))['verified'])

    def test_terminal_digest_restored_source_helper_and_artifact_writes_invalidate(self):
        for kind in ('source', 'helper', 'artifact'):
            with self.subTest(kind=kind):
                case = LocalVerificationTests('test_identical_commands_execute_once_per_invocation_and_real_receipts_check')
                case.setUp()
                try:
                    target = (case.root / 'app.py' if kind == 'source' else
                              case.python_runtime / 'dependency.py' if kind == 'helper' else case.root / 'bin/helper')
                    target.parent.mkdir(exist_ok=True)
                    if kind == 'artifact':
                        target.write_text('prepared helper')
                    before = target.read_bytes()
                    terminal = False
                    fired = False
                    versions = local.Session.tool_versions
                    digest = local.artifact_identity
                    def final_versions(session):
                        nonlocal terminal
                        result = versions(session)
                        if session.prepared is not None:
                            terminal = True
                        return result
                    def restore_during_terminal_digest(root):
                        nonlocal fired
                        result = digest(root)
                        if terminal and not fired:
                            self.assertTrue(all(path.exists() for path in case.receipts))
                            fired = True
                            target.write_bytes(b'changed during terminal traversal')
                            target.write_bytes(before)
                            self.assertEqual(digest(root), result)
                        return result
                    with patch.object(local.Session, 'tool_versions', final_versions), \
                            patch.object(local, 'artifact_identity', restore_during_terminal_digest):
                        with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                            case.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))
                    self.assertTrue(fired)
                    self.assertEqual(target.read_bytes(), before)
                    self.assertTrue(all(not path.exists() for path in case.receipts))
                    self.assertFalse(json.loads((case.directory / 'summary.json').read_text())['success'])
                finally:
                    case.doCleanups()

    def test_final_watch_drain_invalidates_success_and_preserves_error_and_cancellation(self):
        for outcome in ('success', 'error', 'cancel'):
            with self.subTest(outcome=outcome):
                target = self.python_runtime / 'dependency.py'
                before = target.read_bytes()
                closed = []
                original = local.Session.close
                def restored_before_close(session):
                    watches = [session.watch, *session.helper_watches, session.artifact_watch]
                    descriptors = [watch.fd for watch in watches if watch is not None and watch.fd >= 0]
                    target.write_bytes(b'late cleanup mutation')
                    target.write_bytes(before)
                    try:
                        original(session)
                    finally:
                        for descriptor in descriptors:
                            with self.assertRaises(OSError):
                                os.fstat(descriptor)
                        closed.extend(descriptors)
                def run(command, **options):
                    if command == list(self.check.command):
                        if outcome == 'cancel':
                            raise KeyboardInterrupt
                        if outcome == 'error':
                            return subprocess.CompletedProcess(command, 9)
                    return subprocess.CompletedProcess(command, 0)
                expected = KeyboardInterrupt if outcome == 'cancel' else uf.FlowError
                message = 'Required local verification failed' if outcome == 'error' else 'inputs changed'
                with patch.object(local.Session, 'close', restored_before_close):
                    with self.assertRaises(expected) if outcome == 'cancel' else self.assertRaisesRegex(expected, message):
                        self.run_owned(run)
                self.assertTrue(closed)
                self.assertEqual(target.read_bytes(), before)
                self.assertTrue(all(not path.exists() for path in self.receipts))
                self.assertFalse(json.loads((self.directory / 'summary.json').read_text())['success'])

        monitors = []
        target = self.python_runtime / 'dependency.py'
        def failed_admission(session):
            monitors.extend([session.watch, *session.helper_watches])
            before = target.read_bytes()
            target.write_bytes(b'admission cleanup event')
            target.write_bytes(before)
            raise RuntimeError('original helper admission failure')
        with patch.object(local.Session, 'tool_identity', failed_admission):
            with self.assertRaisesRegex(RuntimeError, 'original helper admission failure'):
                local.Session(self.root, self.base, [self.step])
        self.assertTrue(monitors)
        self.assertTrue(all(watch.fd == -1 for watch in monitors))

    def test_independent_cache_watch_close_failure_and_pending_event_roll_back_publications(self):
        for failure in ('descriptor', 'event'):
            for outcome in ('success', 'error', 'cancel'):
                with self.subTest(failure=failure, outcome=outcome):
                    original = local.PythonCache.close
                    captures = []
                    directories = []
                    descriptors = []
                    capture = local.Publications.capture
                    def record(publications, path):
                        capture(publications, path)
                        captures[:] = publications.descriptors
                    def failed_cache_close(cache):
                        directories.append(cache.directory)
                        descriptors.append(cache.descriptor)
                        self.assertTrue(cache.watch is not None)
                        if outcome == 'success':
                            self.assertTrue(all(path.exists() for path in self.receipts))
                            self.assertTrue(captures)
                            self.assertTrue(all(os.fstat(fd).st_ino for fd in captures))
                        if failure == 'descriptor':
                            os.close(cache.watch.fd)
                        else:
                            entry = next(cache.directory.rglob('*.pyc'))
                            content = entry.read_bytes()
                            entry.write_bytes(b'late restored cache write')
                            entry.write_bytes(content)
                        original(cache)
                    def run(command, **options):
                        if command == list(self.check.command):
                            if outcome == 'cancel':
                                raise KeyboardInterrupt
                            if outcome == 'error':
                                return subprocess.CompletedProcess(command, 9)
                        return subprocess.CompletedProcess(command, 0)
                    expected = (KeyboardInterrupt if outcome == 'cancel' else uf.FlowError if
                                outcome == 'error' or failure == 'event' else OSError)
                    with patch.object(local.PythonCache, 'close', failed_cache_close), \
                            patch.object(local.Publications, 'capture', record):
                        with self.assertRaises(expected) as raised:
                            self.run_owned(run)
                    if outcome == 'error':
                        self.assertIn('Required local verification failed', str(raised.exception))
                    self.assertTrue(directories)
                    self.assertTrue(all(not directory.exists() for directory in directories))
                    for descriptor in descriptors + captures:
                        with self.assertRaises(OSError):
                            os.fstat(descriptor)
                    self.assertTrue(all(not path.exists() or path.read_bytes() == b'' for path in self.receipts))
                    summary = self.directory / 'summary.json'
                    if outcome == 'success':
                        self.assertTrue(not summary.exists() or summary.read_bytes() == b'')
                    elif summary.exists() and summary.read_bytes():
                        self.assertFalse(json.loads(summary.read_text())['success'])


    def test_watch_installation_covers_child_created_at_parent_watch_boundary(self):
        original = local.InputWatch.add
        package = self.python_runtime / 'new-package'
        target = package / 'dependency.py'
        marker = self.directory / 'consumed'
        created = False
        def add(watch, path, *args, **options):
            nonlocal created
            if not created and watch.root == self.python_runtime and path == self.python_runtime:
                created = True
                package.mkdir()
                target.write_text('VALUE = 7\n')
            return original(watch, path, *args, **options)
        calls = []
        def run(command, **options):
            calls.append(command)
            code = ('from pathlib import Path\np=Path(' + repr(str(target)) + ')\n'
                    'before=p.read_bytes()\ntry:\n p.write_text("VALUE = 9\\n")\n'
                    ' scope={}\n exec(p.read_text(),scope)\n Path(' + repr(str(marker)) +
                    ').write_text(str(scope["VALUE"]))\nfinally: p.write_bytes(before)\n')
            return uf.run_verification([sys.executable, '-c', code], cwd=options['cwd'], timeout=10)
        with patch.object(local.InputWatch, 'add', add):
            with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                self.run_owned(run)
        self.assertTrue(created)
        self.assertEqual(marker.read_text(), '9')
        self.assertEqual(target.read_text(), 'VALUE = 7\n')
        self.assertEqual(len(calls), 1)
        self.assertTrue(all(not path.exists() for path in self.receipts))

    def test_runner_cancellation_survives_cleanup_cache_guard_event(self):
        for number, expected in ((signal.SIGINT, 130), (signal.SIGTERM, 143)):
            with self.subTest(number=number):
                child = ('import os,signal,time\nfrom pathlib import Path\n'
                         'entry=next(Path(os.environ["PYTHONPYCACHEPREFIX"]).rglob("*.pyc"))\n'
                         'before=entry.read_bytes()\nentry.write_bytes(b"restored cache write")\n'
                         'entry.write_bytes(before)\nos.kill(os.getppid(),' + str(number) + ')\ntime.sleep(30)\n')
                code = ('import sys\nfrom pathlib import Path\nfrom unittest.mock import patch\n'
                        'from specs_lib import local_verification as local\n'
                        'root=Path(' + repr(str(self.root)) + ')\n'
                        'step=local.Step("correctness",root,' + repr((sys.executable, '-c', child)) + ')\n'
                        'check=local.Step("specifications",root,' + repr(self.check.command) + ')\n'
                        'with patch.object(local,"native_plan",side_effect=lambda root,target,base: [] if target=="local-verification-prepare" else [step,check]):\n'
                        ' raise SystemExit(local.main(' + repr(['--root', str(self.root), '--base', self.base,
                            '--review', str(self.review), '--summary', str(self.directory / 'summary.json')]) + '))\n')
                result = self.run_fixture_python(code, cwd=ROOT,
                    env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
                self.assertTrue(all(not path.exists() or path.read_bytes() == b'' for path in self.receipts))
                summary = self.directory / 'summary.json'
                self.assertTrue(not summary.exists() or not summary.read_bytes() or not json.loads(summary.read_text())['success'])

    def test_earlier_path_shadow_creation_and_removal_rejects_zero_result(self):
        early, tools = self.directory / 'early', self.directory / 'tools'
        early.mkdir()
        tools.mkdir()
        selected = tools / 'fixture-check'
        selected.write_text('#!/bin/sh\nexit 19\n')
        selected.chmod(0o755)
        shadow, marker = early / 'fixture-check', self.directory / 'shadow-ran'
        step = local.Step('correctness', self.root, ('fixture-check',))
        def run(command, **options):
            shadow.write_text('#!/bin/sh\nprintf shadow > ' + shlex.quote(str(marker)) + '\nrm -- "$0"\nexit 0\n')
            shadow.chmod(0o755)
            result = uf.run_verification(command, **options)
            self.assertEqual(result.returncode, 0)
            self.assertFalse(shadow.exists())
            return result
        with patch.dict(os.environ, {'PATH': str(early) + ':' + str(tools) + ':' + os.environ['PATH']}):
            session = local.Session(self.root, self.base, [step], runner=run)
            try:
                with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                    session.execute(step)
                self.assertEqual(marker.read_text(), 'shadow')
                self.assertEqual(session.completed, {})
            finally:
                with contextlib.suppress(uf.FlowError):
                    session.close()
        for context in ('', '.', 'relative', '/usr/bin:'):
            with self.subTest(context=context), patch.dict(os.environ, {'PATH': context}):
                with self.assertRaisesRegex(uf.FlowError, 'PATH'):
                    local.executable_search_inputs()

    def test_bash_startup_source_rejects_before_owned_work_and_empty_is_admitted(self):
        startup, marker = self.directory / 'startup.sh', self.directory / 'startup-ran'
        startup.write_text('printf startup > ' + shlex.quote(str(marker)) + '\n')
        result = subprocess.run(['/bin/bash', '--noprofile', '--norc', '-c', ':'],
                                env={**os.environ, 'BASH_ENV': str(startup)}, capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(marker.read_text(), 'startup')
        marker.unlink()
        with patch.dict(os.environ, {'BASH_ENV': str(startup)}), \
                patch.object(local, 'managed', side_effect=AssertionError('ownership must not start')), \
                patch.object(local, 'helper_inputs', side_effect=AssertionError('discovery must not start')):
            with self.assertRaisesRegex(uf.FlowError, 'Bash startup'):
                self.run_owned(lambda *args, **options: self.fail('required work must not start'))
        self.assertFalse(marker.exists())
        with patch.dict(os.environ, {'BASH_ENV': ''}):
            self.assertTrue(self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))['verified'])


    def test_cleanup_cache_guard_preserves_original_error_and_rejects_success(self):
        for outcome in ('success', 'error'):
            with self.subTest(outcome=outcome):
                original = local.Session.close
                def close(session):
                    original(session)
                    entry = next(Path(sys.pycache_prefix).rglob('*.pyc'))
                    before = entry.read_bytes()
                    entry.write_bytes(b'cleanup validation event')
                    entry.write_bytes(before)
                def run(command, **options):
                    return subprocess.CompletedProcess(command, 9 if outcome == 'error' else 0)
                with patch.object(local.Session, 'close', close):
                    with self.assertRaisesRegex(uf.FlowError, 'Required local verification failed' if outcome == 'error' else 'inputs changed'):
                        self.run_owned(run)
                self.assertTrue(all(not path.exists() or path.read_bytes() == b'' for path in self.receipts))

    def test_native_library_inventory_real_abi_and_unsupported_loader_contexts(self):
        directories = set()
        files = self.discover_native(directories, set(), ['python3', 'node', 'gcc', 'git', 'make'])
        self.assertIn(local.SYSTEM_LIBRARIES, directories)
        self.assertIn(local.SYSTEM_LOADER, files)
        self.assertIn(Path('/etc/ld.so.cache'), files)
        self.assertIn(Path('/usr/lib/x86_64-linux-gnu/libc.so.6'), files)
        for name in ('LD_DEBUG', 'LD_ORIGIN_PATH', 'GLIBC_TUNABLES'):
            with self.subTest(name=name), patch.dict(os.environ, {name: 'unsupported'}), \
                    patch.object(local.subprocess, 'run', side_effect=AssertionError('probe must not execute')):
                with self.assertRaisesRegex(uf.FlowError, 'loader'):
                    local.validate_loader_environment()

    def test_native_library_real_load_restoration_uncached_creation_and_custom_elf_reject(self):
        libraries = self.directory / 'native-libraries'
        libraries.mkdir()
        source = self.directory / 'native.c'
        selected, replacement = libraries / 'libfixture.so', self.directory / 'replacement.so'
        for value, output in ((7, selected), (9, replacement)):
            source.write_text('int fixture_value(void) { return ' + str(value) + '; }\n')
            subprocess.run(['/usr/bin/gcc', '-shared', '-fPIC', str(source), '-o', str(output)], check=True,
                           capture_output=True, timeout=15)
        probe = self.directory / 'dlopen-probe'
        source.write_text('#include <stdio.h>\n#include <dlfcn.h>\n'
                          'int main(int argc, char **argv) { void *p = dlopen(argv[1], RTLD_NOW); '
                          'if (!p) return 21; int (*f)(void) = dlsym(p, "fixture_value"); '
                          'if (!f) return 22; printf("%d", f()); return 0; }\n')
        subprocess.run(['/usr/bin/gcc', str(source), '-o', str(probe), '-ldl', '-Wl,-rpath,' + str(libraries)],
                       check=True, capture_output=True, timeout=15)
        original_run = local.subprocess.run
        cache = '1 libs found in cache fixture\n\tlibfixture.so (libc6,x86-64) => ' + str(selected) + '\n'
        def discovery(command, **options):
            if command[1:] == ['-p']:
                return subprocess.CompletedProcess(command, 0, cache, '')
            return original_run(command, **options)
        marker = self.directory / 'loaded-value'
        dangling = self.directory / 'missing-native.so'
        (libraries / 'libdangling.so').symlink_to(dangling)
        for change in ('restored', 'uncached', 'dangling'):
            with self.subTest(change=change):
                def run(command, **options):
                    target = selected if change == 'restored' else dangling if change == 'dangling' else libraries / 'uncached.so'
                    before = target.read_bytes() if target.exists() else None
                    try:
                        target.write_bytes(replacement.read_bytes())
                        with marker.open('w') as log:
                            return uf.run_verification([str(probe), 'libdangling.so' if change == 'dangling' else target.name],
                                                       cwd=options['cwd'], timeout=10, log_stream=log)
                    finally:
                        if before is None:
                            target.unlink()
                        else:
                            target.write_bytes(before)
                with patch.object(local, 'SYSTEM_LIBRARIES', libraries), \
                        patch.object(local, 'LIBRARY_LOOKUPS', (libraries,)), \
                        patch.object(local, 'native_library_inputs', side_effect=lambda directories, files, executables:
                            {probe} | self.discover_native(directories, files | {probe}, executables)), \
                        patch.object(local.subprocess, 'run', discovery):
                    with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                        self.run_owned(run)
                self.assertEqual(marker.read_text(), '9')
                self.assertTrue(all(not path.exists() for path in self.receipts))
        outside = self.directory / 'unrepresented'
        outside.mkdir()
        custom = self.directory / 'custom.so'
        source.write_text('int fixture_value(void) { return 7; }\n')
        subprocess.run(['/usr/bin/gcc', '-shared', '-fPIC', str(source), '-o', str(custom),
                        '-Wl,-rpath,' + str(outside)], check=True, capture_output=True, timeout=15)
        alternate = self.directory / 'alternate'
        source.write_text('int main(void) { return 0; }\n')
        subprocess.run(['/usr/bin/gcc', str(source), '-o', str(alternate), '-Wl,--dynamic-linker=/unsupported/ld.so'],
                       check=True, capture_output=True, timeout=15)
        with patch.object(local, 'SYSTEM_LIBRARIES', libraries), patch.object(local, 'LIBRARY_LOOKUPS', (libraries,)), \
                patch.object(local.subprocess, 'run', discovery):
            with self.assertRaisesRegex(uf.FlowError, 'outside inventoried'):
                self.discover_native(set(), {custom}, ['python3'])
            with self.assertRaisesRegex(uf.FlowError, 'Alternate ELF'):
                self.discover_native(set(), set(), [str(alternate)])
            real, aliases = self.directory / 'a-native-real', self.directory / 'z-native-aliases'
            real.mkdir()
            (aliases / 'deep').mkdir(parents=True)
            (aliases / 'lookup').mkdir()
            library = real / 'liborigin.so'
            source.write_text('int fixture_value(void) { return 7; }\n')
            subprocess.run(['/usr/bin/gcc', '-shared', '-fPIC', str(source), '-o', str(library),
                            '-Wl,-rpath,$ORIGIN/../lookup'], check=True, capture_output=True, timeout=15)
            alias = aliases / 'deep/liborigin.so'
            alias.symlink_to(library)
            self.assertFalse((self.directory / 'lookup').exists())
            with self.assertRaisesRegex(uf.FlowError, 'outside inventoried'):
                self.discover_native(set(), {alias}, ['python3'])


if __name__ == '__main__':
    unittest.main()
