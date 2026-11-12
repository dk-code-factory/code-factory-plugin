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

PROBE_SUPERVISOR = '''import ctypes,os,signal,subprocess,sys,time
from pathlib import Path
if ctypes.CDLL(None,use_errno=True).prctl(36,1,0,0,0):
    raise OSError(ctypes.get_errno(),'probe subreaper setup failed')
interruption=None
def interrupted(number,frame):
    # Recording cannot skip child assignment or the first cleanup instruction.
    global interruption
    interruption=number
signal.signal(signal.SIGTERM,interrupted)
signal.signal(signal.SIGINT,interrupted)
process=None
status=1
try:
    process=subprocess.Popen([sys.executable,'-c',sys.argv[1]],start_new_session=True)
    deadline=time.monotonic()+float(sys.argv[2])
    while interruption is None:
        remaining=deadline-time.monotonic()
        if remaining<=0:
            Path(sys.argv[3]).touch()
            break
        try:
            status=process.wait(timeout=min(.05,remaining))
            break
        except subprocess.TimeoutExpired:
            pass
finally:
    # Let the coordinator unwind its own verifier groups before escalation.
    signal.signal(signal.SIGTERM,signal.SIG_IGN)
    signal.signal(signal.SIGINT,signal.SIG_IGN)
    if process is not None and process.poll() is None:
        process.terminate()
        try: process.wait(timeout=.5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=1)
    # A killed coordinator cannot run its finally blocks. Adopt and reap every
    # remaining descendant, including children that started separate sessions.
    children=Path(f'/proc/{os.getpid()}/task/{os.getpid()}/children')
    deadline=time.monotonic()+3
    while children.read_text().strip():
        for pid in map(int,children.read_text().split()):
            try: os.kill(pid,signal.SIGKILL)
            except ProcessLookupError: pass
            os.waitpid(pid,os.WNOHANG)
        if time.monotonic()>=deadline:
            raise RuntimeError('probe descendant cleanup exceeded its deadline')
        time.sleep(.001)
if interruption is not None:
    raise SystemExit(128+interruption)
if status<0:
    if -status not in (signal.SIGKILL,signal.SIGSTOP):
        signal.signal(-status,signal.SIG_DFL)
    os.kill(os.getpid(),-status)
raise SystemExit(status)
'''


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
        # Keep selected installations and distro helpers, without adding unrelated
        # host launcher directories whose earlier candidates may be unleaseable.
        stable_path = os.pathsep.join(dict.fromkeys([*tool_directories,
            '/usr/sbin', '/usr/bin', '/sbin', '/bin']))
        self.env = patch.dict(os.environ, {'CODE_FACTORY_REAL_CODEX': '0', 'PATH': stable_path}, clear=False)
        self.env.start()
        for name in (*local.MAKE_CARRIERS, 'FLOW_BASE', 'FLOW_ARGS', 'GITHUB_ACTIONS',
                     'LD_PRELOAD', 'LD_AUDIT', 'LD_LIBRARY_PATH'):
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
        timeout = options.pop('timeout')
        check = options.pop('check', False)
        with tempfile.TemporaryDirectory(dir=self.directory) as state:
            expired = Path(state) / 'timeout'
            command = [sys.executable, '-c', PROBE_SUPERVISOR, wrapper, str(timeout), str(expired)]
            # The supervisor owns the deadline; killing only the coordinator at
            # an outer run() deadline would abandon its detached verifier groups.
            with subprocess.Popen(command, **{key: value for key, value in options.items()
                    if key != 'capture_output'}, **({'stdout': subprocess.PIPE, 'stderr': subprocess.PIPE}
                    if options.get('capture_output') else {})) as process:
                try:
                    stdout, stderr = process.communicate()
                except BaseException:
                    process.terminate()
                    process.communicate(timeout=6)
                    raise
                result = subprocess.CompletedProcess([sys.executable, '-c', wrapper], process.returncode, stdout, stderr)
            if expired.exists():
                raise subprocess.TimeoutExpired(result.args, timeout, output=stdout, stderr=stderr)
            if check:
                result.check_returncode()
            return result

    def run_owned(self, runner, plan=None, base=None):
        with patch.object(local, 'native_plan', side_effect=lambda root, target, base:
                          [] if target == 'local-verification-prepare' else plan or [self.step, self.check]), contextlib.redirect_stdout(io.StringIO()):
            return local.verify(self.root, base or self.base, self.review, runner=runner,
                                summary=self.directory / 'summary.json')

    def run_alias_command(self, command, **options):
        return subprocess.run(command, check=True, capture_output=True, timeout=options['timeout'])

    def test_hosted_marker_rejects_entrypoint_before_required_execution(self):
        self.assertNotIn('GITHUB_ACTIONS', os.environ)
        calls = []
        with patch.dict(os.environ, {'GITHUB_ACTIONS': 'true'}):
            with self.assertRaisesRegex(uf.FlowError, 'not a hosted CI entrypoint'):
                self.run_owned(lambda command, **options: calls.append(command))
        self.assertEqual(calls, [])
        self.assertTrue(all(not path.exists() for path in self.receipts))

    def test_probe_timeout_reaps_detached_verifier_descendants_before_fixture_cleanup(self):
        pids = self.directory / 'probe-descendants.json'
        child = ('import os,signal,subprocess,sys,time,json\nfrom pathlib import Path\n'
                 'signal.signal(signal.SIGTERM,signal.SIG_IGN)\n'
                 'nested=subprocess.Popen([sys.executable,"-c",'
                 '"import signal,time;signal.signal(signal.SIGTERM,signal.SIG_IGN);time.sleep(30)"],start_new_session=True)\n'
                 'Path(' + repr(str(pids)) + ').write_text(json.dumps([os.getpid(),nested.pid]))\n'
                 'time.sleep(30)\n')
        code = ('import signal,sys\nfrom specs_lib import processes\n'
                'processes.run_verification([sys.executable,"-c",' + repr(child) + '],cwd=' +
                repr(str(self.root)) + ',timeout=30)\n')
        with self.assertRaises(subprocess.TimeoutExpired) as raised:
            self.run_fixture_python(code, cwd=ROOT,
                env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')},
                capture_output=True, text=True, timeout=2)
        self.assertEqual(raised.exception.timeout, 2)
        self.assertTrue(pids.exists(), raised.exception.stderr)
        for pid in json.loads(pids.read_text()):
            self.assertFalse(Path('/proc', str(pid)).exists(), 'probe left a live or unreaped descendant')
        self.assertTrue(self.root.is_dir())

    def test_probe_cleanup_entry_interrupt_reaps_detached_descendants(self):
        marker = self.directory / 'cleanup-entry-pids.json'
        observation = self.directory / 'cleanup-entry-observation.json'
        expired = self.directory / 'cleanup-entry-expired'
        child = ('import json,os,signal,subprocess,sys,time\nfrom pathlib import Path\n'
                 'signal.signal(signal.SIGTERM,signal.SIG_IGN)\n'
                 'nested=subprocess.Popen([sys.executable,"-c",'
                 '"import signal,time;signal.signal(signal.SIGTERM,signal.SIG_IGN);time.sleep(30)"],'
                 'start_new_session=True)\nPath(' + repr(str(marker)) +
                 ').write_text(json.dumps([os.getpid(),nested.pid]))\nos.write(1,b"R")\ntime.sleep(30)\n')
        cleanup_line = next(index for index, line in enumerate(PROBE_SUPERVISOR.splitlines(), 1)
                            if 'signal.signal(signal.SIGTERM,signal.SIG_IGN)' in line)
        traced = ('import os,select,signal,subprocess,sys\nfrom pathlib import Path\n'
                  'body=sys.argv[1];line=int(sys.argv[2])\n'
                  'original_popen=subprocess.Popen\n'
                  'def ready_child(*args,**options):\n'
                  ' process=original_popen(*args,**options,stdout=subprocess.PIPE)\n'
                  ' try:\n'
                  '  assert select.select([process.stdout],[],[],3)[0],"child readiness exceeded deadline"\n'
                  '  assert os.read(process.stdout.fileno(),1)==b"R","child exited before readiness"\n'
                  ' finally: process.stdout.close()\n'
                  ' return process\n'
                  'subprocess.Popen=ready_child\n'
                  'sys.argv=["supervisor",sys.argv[3],".5",sys.argv[4]]\n'
                  'def trace(frame,event,arg):\n'
                  ' if event=="line" and frame.f_code.co_filename=="inner-supervisor" and frame.f_lineno==line:\n'
                  '  sys.settrace(None);os.kill(os.getpid(),signal.SIGINT)\n'
                  ' return trace\nsys.settrace(trace)\n'
                  'exec(compile(body,"inner-supervisor","exec"))\n')
        # The outer supervisor adopts and cleans any survivors even if this
        # assertion fails after an inner supervisor regresses.
        code = ('import json,subprocess,sys\nfrom pathlib import Path\n'
                'result=subprocess.run([sys.executable,"-c",' + repr(traced) + ',' +
                repr(PROBE_SUPERVISOR) + ',' + repr(str(cleanup_line)) + ',' + repr(child) + ',' +
                repr(str(expired)) + '],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=6)\n'
                'pids=json.loads(Path(' + repr(str(marker)) + ').read_text())\n'
                'alive=[pid for pid in pids if Path(f"/proc/{pid}").exists()]\n'
                'Path(' + repr(str(observation)) + ').write_text(json.dumps({"exit":result.returncode,"alive":alive}))\n'
                'assert result.returncode==130 and not alive,(result.returncode,alive)\n')
        result = self.run_fixture_python(code, cwd=ROOT,
            env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')},
            capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(observation.read_text()), {'exit': 130, 'alive': []})
        self.assertTrue(expired.exists())
        for pid in json.loads(marker.read_text()):
            self.assertFalse(Path('/proc', str(pid)).exists())

    def test_nested_python_cache_name_source_and_legacy_modules_are_monitored(self):
        import py_compile
        for location in (self.python_runtime, self.root):
            package = location / 'fixture_package'
            nested = package / '__pycache__'
            nested.mkdir(parents=True)
            (package / '__init__.py').write_text('')
            for kind in ('source', 'legacy'):
                with self.subTest(location=str(location), kind=kind):
                    source = nested / ('policy_' + kind + '.py')
                    source.write_text('VALUE = 7\n')
                    target = source
                    replacement = b'VALUE = 9\n'
                    if kind == 'legacy':
                        target = source.with_suffix('.pyc')
                        py_compile.compile(str(source), cfile=str(target), doraise=True)
                        source.write_bytes(replacement)
                        prepared = self.directory / 'replacement.pyc'
                        py_compile.compile(str(source), cfile=str(prepared), doraise=True)
                        replacement = prepared.read_bytes()
                        source.unlink()
                    before = target.read_bytes()
                    marker = self.directory / 'nested-python-consumed'
                    probe = ('import importlib\nfrom pathlib import Path\n'
                             'target=Path(' + repr(str(target)) + ')\nbefore=target.read_bytes()\n'
                             'try:\n target.write_bytes(' + repr(replacement) + ')\n'
                             ' value=importlib.import_module("fixture_package.__pycache__.policy_' + kind + '").VALUE\n'
                             ' Path(' + repr(str(marker)) + ').write_text(str(value))\n'
                             'finally: target.write_bytes(before)\n')
                    calls = []
                    def run(command, **options):
                        calls.append(command)
                        return uf.run_verification([sys.executable, '-c', probe], **options)
                    with patch.dict(os.environ, {'PYTHONPATH': str(location)}):
                        if location == self.python_runtime:
                            with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                                self.run_owned(run)
                        else:
                            # The generic fixture catalog does not map this
                            # added package; exercise its artifact/session guard
                            # directly without weakening catalog validation.
                            with local.python_source_imports(self.directory) as cache:
                                session = local.Session(self.root, self.base, [self.step], runner=run)
                                try:
                                    cache.prepare(self.root, session.helper_directories, session.helper_files)
                                    session.freeze()
                                    with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                                        session.execute(self.step)
                                    self.assertEqual(session.completed, {})
                                finally:
                                    with contextlib.suppress(uf.FlowError):
                                        session.close()
                    self.assertEqual(marker.read_text(), '9')
                    self.assertEqual(target.read_bytes(), before)
                    self.assertEqual(len(calls), 1)
                    self.assertTrue(all(not path.exists() for path in self.receipts))

    def test_linked_worktree_private_refs_reject_restored_vcs_revision_changes(self):
        linked = self.directory / 'linked'
        uf.git(self.root, 'worktree', 'add', '--detach', str(linked), self.base)
        tree = uf.git(self.root, 'rev-parse', 'HEAD^{tree}').decode().strip()
        alternate = uf.git(self.root, '-c', 'user.name=Test', '-c', 'user.email=test@example.invalid',
            'commit-tree', tree, '-p', self.base, '-m', 'Alternate stamp').decode().strip()
        selected = 'refs/worktree/current'
        uf.git(linked, 'update-ref', selected, self.base)
        uf.git(linked, 'symbolic-ref', 'HEAD', selected)
        gitdir = Path(uf.git(linked, 'rev-parse', '--absolute-git-dir').decode().strip())
        directories, _ = local.git_inputs(linked)
        self.assertIn(gitdir / 'refs', directories)
        command = local.Step('correctness', linked, tuple(self.command))
        consumed = []
        def run(argv, **options):
            uf.git(linked, 'update-ref', selected, alternate)
            try:
                consumed.append(uf.git(linked, 'log', '-1', '--format=%H').decode().strip())
            finally:
                uf.git(linked, 'update-ref', selected, self.base)
            return subprocess.CompletedProcess(argv, 0)
        session = local.Session(linked, self.base, [command], runner=run)
        try:
            session.freeze()
            with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                session.execute(command)
            self.assertEqual(consumed, [alternate])
            self.assertEqual(uf.revision(linked, 'HEAD'), self.base)
            self.assertEqual(session.completed, {})
        finally:
            with contextlib.suppress(uf.FlowError):
                session.close()

    def test_elf_discovery_fifo_substitution_rejects_without_blocking(self):
        target = self.directory / 'discovery-input'
        target.write_bytes(b'not ELF')
        code = ('import os\nfrom pathlib import Path\nfrom unittest.mock import patch\n'
                'from specs_lib import local_verification as local\n'
                'target=Path(' + repr(str(target)) + ')\nopening=os.open\nopened=[]\n'
                'def substitute(path,flags,*args,**options):\n'
                ' if Path(path)==target:\n  target.unlink()\n  os.mkfifo(target)\n'
                ' descriptor=opening(path,flags,*args,**options)\n'
                ' if Path(path)==target: opened.append(descriptor)\n'
                ' return descriptor\n'
                'with patch.object(local.os,"open",side_effect=substitute):\n'
                ' try: local.native_library_inputs(set(),{target},["python3"])\n'
                ' except local.uf.FlowError as error:\n  assert "Prepared input changed while opening" in str(error),str(error)\n'
                ' else: raise AssertionError("FIFO discovery was accepted")\n'
                'assert opened\n'
                'for descriptor in opened:\n'
                ' try: os.fstat(descriptor)\n'
                ' except OSError: pass\n'
                ' else: raise AssertionError("discovery descriptor leaked")\n')
        # run_fixture_python stubs discovery for ordinary coordinator fixtures;
        # restore the real function only for this isolated discovery regression.
        code = 'import importlib\nimport specs_lib.local_verification as m\nimportlib.reload(m)\n' + code
        result = self.run_fixture_python(code, cwd=ROOT,
            env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')},
            capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    @contextlib.contextmanager
    def independent_fixture(self):
        case = LocalVerificationTests('test_identical_commands_execute_once_per_invocation_and_real_receipts_check')
        try:
            case.setUp()
            yield case
        finally:
            case.doCleanups()

    def test_independent_fixture_cleans_setup_failure_body_failure_and_success(self):
        from unittest.mock import _patch
        setup, mkdir = LocalVerificationTests.setUp, Path.mkdir
        unrelated = patch.dict(os.environ, {'INDEPENDENT_FIXTURE_CALLER': 'retained'})
        unrelated.start()
        self.addCleanup(unrelated.stop)
        for outcome in ('setup', 'body', 'success'):
            cases, stopped = [], []
            failure = OSError('injected independent fixture failure')
            def acquire(case):
                cases.append(case)
                register = case.addCleanup
                def register_owned(function, *args, **options):
                    if getattr(case, 'env', None) is not None and function == case.env.stop:
                        def stop_owned():
                            stopped.append(case.env)
                            function(*args, **options)
                        register(stop_owned)
                    else:
                        register(function, *args, **options)
                with patch.object(case, 'addCleanup', register_owned):
                    setup(case)
            def make_directory(path, *args, **options):
                if path.name == 'python-runtime':
                    case = cases[-1]
                    self.assertIn(case.env, _patch._active_patches)
                    self.assertNotIn('FLOW_BASE', os.environ)
                    if outcome == 'setup':
                        raise failure
                return mkdir(path, *args, **options)
            with self.subTest(outcome=outcome), patch.dict(os.environ, {'FLOW_BASE': 'caller selection'}):
                environment, active = dict(os.environ), tuple(_patch._active_patches)
                expected = self.assertRaises(OSError) if outcome != 'success' else contextlib.nullcontext()
                with patch.object(LocalVerificationTests, 'setUp', acquire), \
                        patch.object(Path, 'mkdir', make_directory), expected as caught:
                    with self.independent_fixture() as case:
                        self.assertIs(case, cases[-1])
                        self.assertIn(case.env, _patch._active_patches)
                        if outcome == 'body':
                            raise failure
                if outcome != 'success':
                    self.assertIs(caught.exception, failure)
                self.assertEqual(stopped, [cases[-1].env])
                self.assertNotIn(cases[-1].env, _patch._active_patches)
                self.assertEqual(tuple(_patch._active_patches), active)
                self.assertEqual(dict(os.environ), environment)
                self.assertFalse(cases[-1].directory.exists())

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

    def test_preparation_schedule_preserves_native_order_and_bound_context_without_duplicate_scans(self):
        catalog = json.loads((ROOT / uf.CATALOG).read_text())
        suites = {name for flow in catalog['flows'] for name in flow['suites']} | {'tooling'}
        commands = sorted({tuple(command) for name in suites for command in catalog['suites'][name]['commands']})
        steps = (local.native_plan(ROOT, 'local-verification-prepare', self.base) +
                 local.native_plan(ROOT, 'verify-steps', self.base) +
                 [step for command in commands for step in local.expand(ROOT, command, self.base)])
        preparation = [step for step in steps if step.phase in local.PREPARATION]
        expected = list(dict.fromkeys((step.cwd, step.command) for step in preparation))
        self.assertEqual((len(preparation), len(expected)), (27, 8))
        contexts = []
        def exercise(deduplicate, change=False):
            calls, scans = [], []
            session = local.Session.__new__(local.Session)
            session.root, session.base, session.head = ROOT, self.base, 'fixed-head'
            session.environment = dict(os.environ)
            contexts.append(session.environment)
            session.source, session.tools, session.prepared, session.timeout = 'source', 'tools', None, None
            session.go_cache = None
            session.rows, session.completed = [], {}
            session.guard = session.check_watches = lambda: None
            session.tool_identity = lambda: scans.append(True) or 'tools'
            def run(command, **options):
                calls.append((options['cwd'], tuple(command)))
                if change:
                    os.environ['PREPARATION_CONTEXT'] = 'changed'
                return subprocess.CompletedProcess(command, 0)
            session.runner = run
            with patch.object(local, 'source_identity', return_value='source'), \
                    patch.object(uf, 'revision', return_value='fixed-head'), contextlib.redirect_stdout(io.StringIO()):
                if deduplicate:
                    local.prepare_once(session, steps)
                else:
                    for step in preparation:
                        session.execute(step)
            self.assertEqual(calls, expected)
            return len(scans)
        with patch.dict(os.environ, {'PREPARATION_CONTEXT': 'first'}):
            self.assertEqual(exercise(False), 35)
            self.assertEqual(exercise(True), 16)
        with patch.dict(os.environ, {'PREPARATION_CONTEXT': 'second'}):
            self.assertEqual(exercise(True), 16)
            with self.assertRaisesRegex(uf.FlowError, 'environment or toolchain changed'):
                exercise(True, change=True)
        self.assertNotEqual(contexts[0], contexts[2])
        self.assertTrue(all(item['PREPARATION_CONTEXT'] == 'first' for item in contexts[:2]))

    def test_regular_input_hashes_stream_large_files_with_bounded_reads(self):
        import hashlib
        target = self.directory / 'large-input'
        fdopen = os.fdopen
        buffers = []
        class Stream:
            def __init__(self, source):
                self.source = source
            def __enter__(self):
                self.source.__enter__()
                return self
            def __exit__(self, *args):
                return self.source.__exit__(*args)
            def readable(self):
                return True
            def readinto(self, buffer):
                buffers.append(len(buffer))
                self.assert_bounded(buffer)
                return self.source.readinto(buffer)
            def assert_bounded(self, buffer):
                if not 0 < len(buffer) <= 1024 * 1024:
                    raise AssertionError('Input hashing allocated an unbounded read buffer')
            def read(self, *args):
                raise AssertionError('Input hashing read the entire file at once')
        for payload in (b'', b'bounded input\0' * 200000 + b'tail'):
            with self.subTest(bytes=len(payload)):
                target.write_bytes(payload)
                buffers.clear()
                with patch.object(local.os, 'fdopen', side_effect=lambda *args, **kwargs:
                                  Stream(fdopen(*args, **kwargs))):
                    self.assertEqual(local.artifact_digest(target, target.lstat()), hashlib.sha256(payload).digest())
                    helper = local.helper_identity(set(), {target})
                self.assertTrue(buffers)
                self.assertEqual(helper, local.helper_identity(set(), {target}))
                if payload:
                    self.assertGreater(len(buffers), 2)

    def test_helper_identity_memo_is_single_traversal_preserves_rows_and_closes_opened_files(self):
        import hashlib
        target = self.directory / 'native-input'
        payload = b'fixed native input\n'
        target.write_bytes(payload)
        hardlink, alias = self.directory / 'hardlink', self.directory / 'alias'
        os.link(target, hardlink)
        alias.symlink_to(target)
        paths = {target, hardlink, alias}
        expected_rows = []
        for path in sorted(paths):
            edges, resolved = local.resolution_chain(path)
            expected_rows.extend((str(parent / name), os.readlink(parent / name))
                                 for parent, name in edges if (parent / name).is_symlink())
            expected_rows.append((str(path), str(resolved), path.lstat().st_mode,
                                  resolved.stat().st_mode, hashlib.sha256(payload).digest()))
        expected = local.digest_rows(expected_rows)
        raw_fdopen, raw_fstat, raw_open, raw_close = os.fdopen, os.fstat, os.open, os.close
        reads, opened, per_open = [], [], [0]
        read_hook, open_hook, stat_hook, close_failure = [None], [None], [None], [False]
        class Stream:
            def __init__(self, stream):
                self.stream = stream
            def __enter__(self):
                self.stream.__enter__()
                return self
            def __exit__(self, *args):
                return self.stream.__exit__(*args)
            def readable(self):
                return self.stream.readable()
            def readinto(self, buffer):
                result = self.stream.readinto(buffer)
                if result:
                    reads.append(raw_fstat(self.stream.fileno()).st_ino)
                    if read_hook[0] is not None:
                        read_hook[0]()
                return result
        def fdopen(descriptor, *args, **options):
            opened.append(descriptor)
            per_open[0] = 0
            return Stream(raw_fdopen(descriptor, *args, **options))
        def fstat(descriptor):
            per_open[0] += 1
            if stat_hook[0] is not None:
                stat_hook[0](descriptor)
            return raw_fstat(descriptor)
        def opening(path, flags, *args, **options):
            if open_hook[0] is not None:
                hook, open_hook[0] = open_hook[0], None
                hook()
            return raw_open(path, flags, *args, **options)
        def closing(descriptor):
            raw_close(descriptor)
            if close_failure[0]:
                raise OSError('secondary close failure')
        def restored_write():
            before = target.stat()
            target.write_bytes(b'changed input\n')
            target.write_bytes(payload)
            os.utime(target, ns=(before.st_atime_ns, before.st_mtime_ns))
        def assert_closed():
            for descriptor in set(opened):
                with self.assertRaises(OSError):
                    raw_fstat(descriptor)
        with patch.object(local.os, 'fdopen', side_effect=fdopen), \
                patch.object(local.os, 'fstat', side_effect=fstat), \
                patch.object(local.os, 'open', side_effect=opening), \
                patch.object(local.os, 'close', side_effect=closing):
            self.assertEqual(local.helper_identity(set(), paths), expected)
            self.assertEqual(len(reads), 1)
            self.assertEqual(len(opened), 3)
            assert_closed()
            self.assertEqual(local.helper_identity(set(), paths), expected)
            self.assertEqual(len(reads), 2)
            distinct = self.directory / 'different-inode'
            distinct.write_bytes(payload)
            local.helper_identity(set(), {target, distinct})
            self.assertEqual(len(reads), 4)
            self.assertNotEqual(target.stat().st_ino, distinct.stat().st_ino)
            read_hook[0] = restored_write
            with self.assertRaisesRegex(uf.FlowError, 'changed during traversal'):
                local.helper_identity(set(), paths)
            read_hook[0] = None
            assert_closed()
            count = len(opened)
            stat_hook[0] = lambda descriptor: restored_write() if len(opened) == count + 2 and per_open[0] == 2 else None
            with self.assertRaisesRegex(uf.FlowError, 'changed during traversal'):
                local.helper_identity(set(), paths)
            stat_hook[0] = None
            assert_closed()
            for kind in ('regular', 'symlink'):
                replacement = self.directory / ('replacement-' + kind)
                replacement.write_bytes(payload)
                def replace():
                    target.unlink()
                    if kind == 'regular':
                        replacement.replace(target)
                    else:
                        target.symlink_to(replacement)
                open_hook[0] = replace
                with self.assertRaises((uf.FlowError, OSError)):
                    local.helper_identity(set(), {target})
                assert_closed()
                target.unlink()
                target.write_bytes(payload)
            code = ('import os\nfrom pathlib import Path\nfrom unittest.mock import patch\n'
                'from specs_lib import local_verification as local\n'
                'target=Path(' + repr(str(target)) + ')\n'
                'opening=os.open\nopened=[]\n'
                'def substitute(path,flags,*args,**options):\n'
                ' target.unlink();os.mkfifo(target)\n'
                ' fd=opening(path,flags,*args,**options);opened.append(fd);return fd\n'
                'with patch.object(local.os,"open",side_effect=substitute):\n'
                ' try: local.helper_identity(set(),{target})\n'
                ' except local.uf.FlowError: pass\n'
                ' else: raise AssertionError("FIFO admitted")\n'
                'assert len(opened)==1\n'
                'try: os.fstat(opened[0])\n'
                'except OSError: pass\n'
                'else: raise AssertionError("descriptor leaked")\n')
            subprocess.run([sys.executable, '-B', '-c', code], check=True, capture_output=True,
                           env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, timeout=5)
            target.unlink()
            target.write_bytes(payload)
            close_failure[0] = True
            for error in (OSError('original read failure'), KeyboardInterrupt()):
                def fail():
                    raise error
                read_hook[0] = fail
                with self.assertRaises(type(error)) as caught:
                    local.helper_identity(set(), {target})
                self.assertIs(caught.exception, error)
                assert_closed()
            read_hook[0] = None
            stat_error = OSError('original stat failure')
            def fail_stat(descriptor):
                raise stat_error
            stat_hook[0] = fail_stat
            with self.assertRaises(OSError) as caught:
                local.helper_identity(set(), {target})
            self.assertIs(caught.exception, stat_error)
            stat_hook[0] = None
            assert_closed()
            hash_error = ValueError('original hash failure')
            with patch.object(local.hashlib, 'sha256', side_effect=hash_error):
                with self.assertRaises(ValueError) as caught:
                    local.helper_identity(set(), {target})
            self.assertIs(caught.exception, hash_error)
            assert_closed()
            with self.assertRaisesRegex(OSError, 'secondary close failure'):
                local.helper_identity(set(), {target})
            assert_closed()
            cancellation = local.InvocationCancellation()
            previous = signal.getsignal(signal.SIGINT)
            read_hook[0] = lambda: os.kill(os.getpid(), signal.SIGINT)
            with self.assertRaises(KeyboardInterrupt):
                with local.invocation_signals(cancellation):
                    local.helper_identity(set(), {target})
            self.assertEqual(cancellation.number, signal.SIGINT)
            self.assertEqual(signal.getsignal(signal.SIGINT), previous)
            assert_closed()

    def test_source_helper_untracked_environment_and_review_mutations_fail(self):
        for mutation in ('source', 'helper', 'untracked', 'environment', 'review'):
            with self.subTest(mutation=mutation), self.independent_fixture() as case:
                # Each case receives an independent fixture and invocation.
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

    def test_restored_git_revision_consumed_by_real_go_build_rejects_evidence(self):
        catalog_path = self.root / uf.CATALOG
        catalog = json.loads(catalog_path.read_text())
        catalog['exclusions'].extend({'glob': name, 'reason': 'Standalone VCS stamp probe; no fixture product behavior'}
                                     for name in ('go.mod', 'main.go'))
        catalog_path.write_text(json.dumps(catalog))
        (self.root / 'go.mod').write_text('module example.invalid/stamped\n\ngo 1.26.0\n')
        (self.root / 'main.go').write_text('package main\nimport ("fmt"; "runtime/debug")\n'
            'func main() { b, _ := debug.ReadBuildInfo(); for _, s := range b.Settings {'
            ' if s.Key == "vcs.revision" { fmt.Print(s.Value) } } }\n')
        uf.git(self.root, 'add', 'go.mod', 'main.go', uf.CATALOG)
        uf.git(self.root, '-c', 'user.name=Test', '-c', 'user.email=test@example.invalid',
               'commit', '-qm', 'Add stamped fixture')
        self.base = uf.revision(self.root, 'HEAD')
        self.check = local.Step('specifications', self.root,
            ('python3', 'scripts/specs.py', 'check', '--base', self.base))
        tree = uf.git(self.root, 'rev-parse', 'HEAD^{tree}').decode().strip()
        alternate = uf.git(self.root, '-c', 'user.name=Test', '-c', 'user.email=test@example.invalid',
            'commit-tree', tree, '-p', self.base, '-m', 'Same tree, different revision').decode().strip()
        self.assertNotEqual(alternate, self.base)
        build = local.Step('build', self.root, ('go', 'build', '-o', 'bin/stamped', '.'))
        plan = [build, self.step, self.check]
        before = local.source_identity(self.root)
        stamps, calls = [], []
        previous = os.environ.get('GIT_OPTIONAL_LOCKS')
        def run(command, **options):
            calls.append(command)
            self.assertEqual(os.environ['GIT_OPTIONAL_LOCKS'], '0')
            if command == list(build.command):
                compilation = subprocess.run(command, cwd=self.root, capture_output=True, timeout=60,
                    text=True, env={**os.environ, 'GOMODCACHE': str(self.directory / 'go-modules')})
                self.assertEqual(compilation.returncode, 0, compilation.stdout + compilation.stderr)
                stamps.append(subprocess.check_output([str(self.root / 'bin/stamped')], text=True, timeout=10))
            elif command == list(self.check.command):
                self.assertEqual(specs.main(['check', '--root', str(self.root), '--base', self.base]), 0)
            return subprocess.CompletedProcess(command, 0)
        with patch.object(local, 'helper_inputs', return_value=({self.python_runtime}, set())):
            self.assertTrue(self.run_owned(run, plan=plan)['verified'])
        self.assertEqual(stamps, [self.base])
        self.assertTrue(all(path.exists() for path in self.receipts))
        self.assertEqual(os.environ.get('GIT_OPTIONAL_LOCKS'), previous)
        calls.clear()
        def changed(command, **options):
            if command != list(build.command):
                return run(command, **options)
            try:
                uf.git(self.root, 'update-ref', 'HEAD', alternate)
                return run(command, **options)
            finally:
                uf.git(self.root, 'update-ref', 'HEAD', self.base)
        with patch.object(local, 'helper_inputs', return_value=({self.python_runtime}, set())):
            with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                self.run_owned(changed, plan=plan)
        self.assertEqual(stamps, [self.base, alternate])
        self.assertEqual(calls, [list(build.command)])
        self.assertEqual(uf.revision(self.root, 'HEAD'), self.base)
        self.assertEqual(local.source_identity(self.root), before)
        self.assertEqual(os.environ.get('GIT_OPTIONAL_LOCKS'), previous)
        self.assertTrue(all(not path.exists() for path in self.receipts))
        self.assertFalse(json.loads((self.directory / 'summary.json').read_text())['success'])

    def test_git_status_inputs_and_unsupported_contexts_have_bounded_coverage(self):
        directories, files = local.git_inputs(self.root)
        self.assertTrue({self.root / '.git/refs', self.root / '.git/objects'} <= directories)
        for name in ('HEAD', 'index', 'config', 'packed-refs', 'shallow', 'info/exclude', 'info/attributes'):
            self.assertIn(self.root / '.git' / name, files)
        system_attributes = subprocess.check_output(['git', 'var', 'GIT_ATTR_SYSTEM'],
            cwd=self.root, text=True, timeout=15).strip()
        self.assertIn(Path(system_attributes), files)
        (self.root / '.git/info/attributes').write_text('')
        for name in ('index', 'config', 'info/exclude', 'info/attributes'):
            with self.subTest(name=name):
                path = self.root / '.git' / name
                before = path.read_bytes()
                session = local.Session(self.root, self.base, [self.step])
                try:
                    path.write_bytes(before + b'\n')
                    path.write_bytes(before)
                    with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                        session.assert_current()
                finally:
                    path.write_bytes(before)
                    with contextlib.suppress(uf.FlowError):
                        session.close()
                self.assertTrue(all(watch.fd == -1 for watch in [session.watch, *session.helper_watches]))
        tree = uf.git(self.root, 'rev-parse', 'HEAD^{tree}').decode().strip()
        alternate = uf.git(self.root, '-c', 'user.name=Test', '-c', 'user.email=test@example.invalid',
            'commit-tree', tree, '-p', self.base, '-m', 'Metadata fixture').decode().strip()
        selected_ref = 'refs/heads/__pycache__/current'
        uf.git(self.root, 'update-ref', selected_ref, self.base)
        with local.python_source_imports(self.directory):
            session = local.Session(self.root, self.base, [self.step],
                runner=lambda command, **options: subprocess.CompletedProcess(command, 0))
            try:
                session.execute(self.step)
                uf.git(self.root, 'update-ref', selected_ref, alternate)
                uf.git(self.root, 'update-ref', selected_ref, self.base)
                self.assertEqual(uf.revision(self.root, selected_ref), self.base)
                with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                    session.execute(self.step)
            finally:
                with contextlib.suppress(uf.FlowError):
                    session.close()
            self.assertTrue(all(watch.fd == -1 for watch in [session.watch, *session.helper_watches]))
        config = self.root / '.git/config'
        attributes = self.root / '.gitattributes'
        source = self.root / 'app.py'
        before_config, before_source = config.read_bytes(), source.read_bytes()
        marker, helper = self.directory / 'filter-consumed', self.directory / 'clean-filter'
        helper.write_text('#!/bin/sh\nprintf consumed >> ' + shlex.quote(str(marker)) + '\ncat\n')
        helper.chmod(0o755)
        try:
            uf.git(self.root, 'config', 'filter.probe.clean', str(helper))
            attributes.write_text('app.py filter=probe\n')
            source.write_bytes(before_source.replace(b'return 2', b'return 9'))
            self.assertEqual(source.stat().st_size, len(before_source))
            os.utime(source, (time.time() + 2, time.time() + 2))
            env = {**os.environ, 'GIT_OPTIONAL_LOCKS': '0'}
            subprocess.run(['git', 'status', '--porcelain'], cwd=self.root, env=env,
                           check=True, capture_output=True, timeout=15)
            self.assertTrue(marker.exists())
            with self.assertRaisesRegex(uf.FlowError, 'Selected Git clean/process filters'):
                local.git_inputs(self.root)
            for token, special in [('set', 'filter'), ('unset', '-filter'), ('unspecified', '!filter')]:
                uf.git(self.root, 'config', 'filter.' + token + '.clean', str(helper))
                attributes.write_text('app.py filter=' + token + '\n')
                marker.unlink(missing_ok=True)
                os.utime(source, (time.time() + 2, time.time() + 2))
                literal = subprocess.check_output(['git', 'check-attr', '-z', 'filter', '--', 'app.py'],
                    cwd=self.root, timeout=15)
                self.assertEqual(literal, b'app.py\0filter\0' + token.encode() + b'\0')
                subprocess.run(['git', 'status', '--porcelain'], cwd=self.root, env=env,
                               check=True, capture_output=True, timeout=15)
                self.assertTrue(marker.exists())
                with self.assertRaisesRegex(uf.FlowError, 'Reserved Git filter driver names'):
                    local.git_inputs(self.root)
                attributes.write_text('app.py ' + special + '\n')
                marker.unlink()
                sentinel = subprocess.check_output(['git', 'check-attr', '-z', 'filter', '--', 'app.py'],
                    cwd=self.root, timeout=15)
                self.assertEqual(sentinel, literal)
                subprocess.run(['git', 'status', '--porcelain'], cwd=self.root, env=env,
                               check=True, capture_output=True, timeout=15)
                self.assertFalse(marker.exists())
                with self.assertRaisesRegex(uf.FlowError, 'Reserved Git filter driver names'):
                    local.git_inputs(self.root)
                uf.git(self.root, 'config', '--remove-section', 'filter.' + token)
                uf.git(self.root, 'config', 'filter.' + token + '.process', str(helper))
                with self.assertRaisesRegex(uf.FlowError, 'Reserved Git filter driver names'):
                    local.git_inputs(self.root)
                uf.git(self.root, 'config', '--remove-section', 'filter.' + token)
            attributes.write_text('app.py -filter\n')
            marker.unlink(missing_ok=True)
            subprocess.run(['git', 'status', '--porcelain'], cwd=self.root, env=env,
                           check=True, capture_output=True, timeout=15)
            local.git_inputs(self.root)
            self.assertFalse(marker.exists())
        finally:
            attributes.unlink(missing_ok=True)
            source.write_bytes(before_source)
            config.write_bytes(before_config)
        for key in ('core.fsmonitor', 'core.excludesFile', 'core.attributesFile', 'include.path', 'extensions.refStorage'):
            config = self.root / '.git/config'
            before = config.read_bytes()
            try:
                uf.git(self.root, 'config', key, str(self.directory / 'external'))
                with self.assertRaises((uf.FlowError, subprocess.CalledProcessError)):
                    local.git_inputs(self.root)
            finally:
                config.write_bytes(before)
        for name in ('GIT_DIR', 'GIT_CONFIG_GLOBAL', 'GIT_ALTERNATE_OBJECT_DIRECTORIES'):
            with self.subTest(name=name), patch.dict(os.environ, {name: str(self.directory)}):
                with self.assertRaisesRegex(uf.FlowError, 'Custom Git input selection'):
                    local.git_inputs(self.root)
        for value in ('0', '1'):
            for outcome in ('success', 'failure', 'cancel'):
                with self.subTest(value=value, outcome=outcome), patch.dict(os.environ, {'GIT_OPTIONAL_LOCKS': value}):
                    def run(command, **options):
                        self.assertEqual(os.environ['GIT_OPTIONAL_LOCKS'], '0')
                        if outcome == 'cancel':
                            raise KeyboardInterrupt
                        return subprocess.CompletedProcess(command, int(outcome == 'failure'))
                    if outcome == 'success':
                        self.run_owned(run)
                    else:
                        with self.assertRaises(KeyboardInterrupt if outcome == 'cancel' else uf.FlowError):
                            self.run_owned(run)
                    self.assertEqual(os.environ['GIT_OPTIONAL_LOCKS'], value)

    def test_esbuild_override_consumes_external_binary_but_rejects_before_ownership(self):
        original = ROOT / 'web/node_modules/@esbuild/linux-x64/bin/esbuild'
        external = self.directory / 'external-esbuild'
        shutil.copyfile(original, external)
        external.chmod(0o755)
        code = ('const e = require(' + json.dumps(str(ROOT / 'web/node_modules/esbuild')) + ');'
                'if (!e.transformSync("let value = 7").code.includes("value = 7")) process.exit(1);')
        with patch.dict(os.environ, {'ESBUILD_BINARY_PATH': str(external)}):
            subprocess.run(['node', '-e', code], check=True, capture_output=True, timeout=15)
            with patch.object(local, 'managed', side_effect=AssertionError('must reject before ownership')), \
                    patch.object(local, 'helper_inputs', side_effect=AssertionError('must reject before discovery')):
                with self.assertRaisesRegex(uf.FlowError, 'ESBUILD_BINARY_PATH'):
                    self.run_owned(lambda command, **options: self.fail('required work must not run'))
        with patch.dict(os.environ, {'ESBUILD_BINARY_PATH': ''}):
            self.assertTrue(self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))['verified'])

    def test_relative_python_search_paths_reject_before_ownership_and_browser_startup(self):
        project = self.directory / 'project'
        root = project / 'repo'
        (root / 'web').mkdir(parents=True)
        for directory in (self.directory / 'outside', project / 'outside'):
            directory.mkdir()
        # The same relative selector means different external roots in the
        # native root command and the maintained npm browser-helper command.
        selected = project / 'outside'
        consumed = self.directory / 'relative-secrets-consumed'
        (selected / 'secrets.py').write_text('from pathlib import Path\nPath(' +
            repr(str(consumed)) + ').write_text("consumed")\nraise SystemExit(0)\n')
        with patch.dict(os.environ, {'PYTHONPATH': '../../outside'}):
            paths = json.loads(subprocess.check_output([sys.executable, '-B', '-c',
                'import json,sys; print(json.dumps(sys.path))'], cwd=root, text=True, timeout=5))
            self.assertIn(str(self.directory / 'outside'), paths)
            self.assertNotIn(str(selected), paths)
            result = subprocess.run([sys.executable, '-B', '-c',
                'import secrets; print("required-browser-body")'], cwd=root / 'web',
                capture_output=True, text=True, timeout=5)
            self.assertEqual(result.returncode, 0)
            self.assertNotIn('required-browser-body', result.stdout)
            self.assertTrue(consumed.exists())
            consumed.unlink()
        for value in ('../../outside', '.', ':', str(selected) + ':'):
            with self.subTest(value=value), patch.dict(os.environ, {'PYTHONPATH': value}), \
                    patch.object(local, 'managed', side_effect=AssertionError('must reject before ownership')), \
                    patch.object(local, 'native_plan', side_effect=AssertionError('must reject before planning')):
                result = subprocess.run([sys.executable, '-B', str(ROOT / 'scripts/verify_local.py'),
                    '--base', 'HEAD', '--review', str(self.review)], cwd=root / 'web',
                    capture_output=True, text=True, timeout=5)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn('Relative or empty PYTHONPATH', result.stderr)
                self.assertFalse(consumed.exists(), 'public CLI imported an unsupported relative module')
                with self.assertRaisesRegex(uf.FlowError, 'Relative or empty PYTHONPATH'):
                    local.verify(self.root, self.base, self.review,
                                 runner=lambda command, **options: self.fail('required work must not run'))
                with self.assertRaisesRegex(uf.FlowError, 'Relative or empty PYTHONPATH'):
                    self.discover_python(self.root, sys.executable)
        for name in ('PYTHONHOME', 'PYTHONUSERBASE'):
            with self.subTest(selector=name), patch.dict(os.environ, {name: '../outside'}), \
                    patch.object(local, 'managed', side_effect=AssertionError('must reject before ownership')):
                with self.assertRaisesRegex(uf.FlowError, 'Relative or empty ' + name):
                    local.verify(self.root, self.base, self.review)
                with self.assertRaisesRegex(uf.FlowError, 'Relative or empty ' + name):
                    self.discover_python(self.root, sys.executable)
        absolute = self.directory / 'absolute-packages'
        absolute.mkdir()
        with patch.dict(os.environ, {'PYTHONPATH': str(absolute)}):
            result = subprocess.run([sys.executable, '-B', str(ROOT / 'scripts/verify_local.py'), '--help'],
                capture_output=True, text=True, timeout=5)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('usage:', result.stdout)
            self.assertIn(absolute, self.discover_python(self.root, sys.executable)[0])
            self.assertTrue(self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))['verified'])
        with patch.dict(os.environ, {'PYTHONPATH': ''}):
            self.assertTrue(self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))['verified'])

    def test_used_transitive_python_sources_in_excluded_search_paths_reject_with_aliases(self):
        import referencing
        with (self.root / '.git/info/exclude').open('a') as output:
            output.write('__pycache__/\n')
        cache = self.root / '__pycache__'
        cache.mkdir()
        normal = self.directory / 'normal-packages'
        normal.mkdir()
        for packages in (cache, normal):
            shutil.copytree(Path(referencing.__file__).parent, packages / 'referencing',
                            ignore=shutil.ignore_patterns('__pycache__'))
            with (packages / 'referencing/__init__.py').open('a') as output:
                output.write('\nSELECTED_SOURCE_VALUE = 9\n')
        alias = self.directory / 'selected-packages'
        alias.symlink_to(cache, target_is_directory=True)
        reverse = cache / 'selected-packages'
        reverse.symlink_to(normal, target_is_directory=True)
        probe = ('import jsonschema,sys; from pathlib import Path; '
                 'assert "referencing" in sys.modules; import referencing; '
                 'assert referencing.SELECTED_SOURCE_VALUE == 9; '
                 'assert Path(jsonschema.__file__).is_relative_to(Path(' + repr(str(Path(__import__('jsonschema').__file__).parent)) + ')); '
                 'print(Path(referencing.__file__).resolve())')
        for selected in (cache, alias, reverse):
            with self.subTest(selected=selected), patch.dict(os.environ, {'PYTHONPATH': str(selected)}):
                result = subprocess.run([sys.executable, '-B', '-c', probe], cwd=self.root,
                                        text=True, check=True, capture_output=True, timeout=15)
                self.assertEqual(result.stdout.strip(), str((selected / 'referencing/__init__.py').resolve()))
                with self.assertRaisesRegex(uf.FlowError, 'Selected Python import search intersects'):
                    self.discover_python(self.root, sys.executable)
                with patch.object(local, 'python_inputs', side_effect=self.discover_python):
                    with self.assertRaisesRegex(uf.FlowError, 'Selected Python import search intersects'):
                        self.run_owned(lambda command, **options: self.fail('required work must not run'))
        with patch.dict(os.environ, {'PYTHONPATH': str(normal)}):
            subprocess.run([sys.executable, '-B', '-c', probe], cwd=self.root,
                           check=True, capture_output=True, timeout=15)
            directories, _ = self.discover_python(self.root, sys.executable)
            self.assertIn(normal, directories)
        (normal / '__pycache__').mkdir(exist_ok=True)
        (normal / '__pycache__/unused.pyc').write_bytes(b'unused automatic cache')
        with patch.dict(os.environ, {'PYTHONPATH': str(normal)}):
            self.assertIn(normal, self.discover_python(self.root, sys.executable)[0])

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
            with self.subTest(target=target), self.independent_fixture() as case:
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
        with self.assertRaisesRegex(uf.FlowError, 'already invalidated'):
            watch.check()

    def test_external_hardlink_restoration_rejects_consumed_temporary_source(self):
        source = self.root / 'app.py'
        alias = self.directory / 'external-alias.py'
        os.link(source, alias)
        original = source.read_bytes()
        consumed = self.directory / 'consumed-source'
        code = ('from pathlib import Path\n'
                'alias=Path(' + repr(str(alias)) + ')\noriginal=alias.read_bytes()\n'
                'try:\n alias.write_bytes(b"temporary source")\n'
                ' Path(' + repr(str(consumed)) + ').write_bytes(Path(' + repr(str(source)) + ').read_bytes())\n'
                'finally: alias.write_bytes(original)\n')
        calls = []
        def run(command, **options):
            calls.append(command)
            return uf.run_verification([sys.executable, '-c', code], **options)
        with self.assertRaises(uf.FlowError):
            self.run_owned(run)
        self.assertEqual(len(calls), 1)
        self.assertEqual(consumed.read_bytes(), b'temporary source')
        self.assertEqual(source.read_bytes(), original)
        self.assertTrue(all(not path.exists() for path in self.receipts))
        import fcntl
        from specs_lib.isolation import checkout_lock_path
        with checkout_lock_path(self.root).open('a+') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)

    def test_prefaulted_writable_mapping_rejects_before_required_commands(self):
        import mmap
        from specs_lib.input_watch import InputWatch, LEASES
        previous = signal.getsignal(signal.SIGIO)
        # tmpfs dirty mappings can change bytes without changing either timestamp.
        for parent in (self.directory, Path('/dev/shm')):
            with self.subTest(parent=str(parent)), tempfile.TemporaryDirectory(dir=parent) as temporary:
                root = Path(temporary)
                path = root / 'input'
                path.write_bytes(b'original')
                with path.open('r+b') as stream:
                    mapping = mmap.mmap(stream.fileno(), 0)
                with mapping:
                    mapping[:] = b'modified'
                    mapping[:] = b'original'
                    with self.assertRaisesRegex(uf.FlowError, 'read lease unavailable'):
                        InputWatch(root, lambda name: False)
                    mapping[:] = b'modified'
                    self.assertEqual(path.read_bytes(), b'modified')
                self.assertEqual(LEASES.files, {})
                self.assertEqual(LEASES.users, 0)
                self.assertEqual(signal.getsignal(signal.SIGIO), previous)
        source = self.root / 'app.py'
        with source.open('r+b') as stream, mmap.mmap(stream.fileno(), 0) as mapping:
            mapping[0] = mapping[0]
            with patch.object(local, 'native_plan', side_effect=AssertionError('must reject before planning')):
                with self.assertRaisesRegex(uf.FlowError, 'read lease unavailable'):
                    local.verify(self.root, self.base, self.review)
        self.assertTrue(all(not path.exists() for path in self.receipts))

    def test_late_shared_mapping_restoration_rejects_while_writer_remains_open(self):
        import mmap
        source = self.root / 'app.py'
        original = source.read_bytes()
        consumed, retained = [], []
        previous = signal.getsignal(signal.SIGIO)
        def run(command, **options):
            stream = source.open('r+b')
            retained.append(stream)
            mapping = mmap.mmap(stream.fileno(), 0)
            retained.append(mapping)
            replacement = b'x' * len(original)
            mapping[:] = replacement
            consumed.append(source.read_bytes())
            mapping[:] = original
            return subprocess.CompletedProcess(command, 0)
        try:
            with self.assertRaises(uf.FlowError):
                self.run_owned(run)
            self.assertEqual(consumed, [b'x' * len(original)])
            self.assertEqual(source.read_bytes(), original)
            self.assertTrue(all(not item.closed for item in retained))
            self.assertTrue(all(not path.exists() for path in self.receipts))
            self.assertEqual(signal.getsignal(signal.SIGIO), previous)
        finally:
            for item in reversed(retained):
                item.close()

    def test_read_leases_share_aliases_and_admit_readonly_and_private_mappings(self):
        import fcntl
        import mmap
        from specs_lib.input_watch import InputWatch, LEASES
        with tempfile.TemporaryDirectory(dir=self.directory) as temporary:
            root = Path(temporary)
            source = root / 'input'
            source.write_bytes(b'original')
            os.link(source, root / 'alias')
            previous = signal.getsignal(signal.SIGIO)
            mask = signal.pthread_sigmask(signal.SIG_BLOCK, set())
            with source.open('rb') as stream, mmap.mmap(stream.fileno(), 0, access=mmap.ACCESS_READ) as readonly, \
                    mmap.mmap(stream.fileno(), 0, access=mmap.ACCESS_COPY) as private:
                first = InputWatch(root, lambda name: False)
                try:
                    second = InputWatch(root, lambda name: False)
                    try:
                        self.assertEqual(len(LEASES.files), 1)
                        entry = next(iter(LEASES.files.values()))
                        self.assertEqual(entry['users'], 2)
                        private[:] = b'modified'
                        self.assertEqual(readonly[:], b'original')
                        first.check()
                        first.close()
                        second.check()
                        self.assertEqual(fcntl.fcntl(entry['fd'], fcntl.F_GETLEASE), fcntl.F_RDLCK)
                        child = os.fork()
                        if child == 0:
                            signal.alarm(3)
                            try:
                                with self.assertRaisesRegex(uf.FlowError, 'inherited across fork'):
                                    InputWatch(root, lambda name: False)
                                second.close()
                            except BaseException:
                                os._exit(1)
                            os._exit(0)
                        _, status = os.waitpid(child, 0)
                        self.assertEqual(os.waitstatus_to_exitcode(status), 0)
                        second.check()
                        self.assertEqual(fcntl.fcntl(entry['fd'], fcntl.F_GETLEASE), fcntl.F_RDLCK)
                        fcntl.fcntl(entry['fd'], fcntl.F_SETLEASE, fcntl.F_UNLCK)
                        with self.assertRaisesRegex(uf.FlowError, 'read lease'):
                            second.check()
                    finally:
                        second.close()
                finally:
                    first.close()
            first = InputWatch(root, lambda name: False)
            try:
                entry = next(iter(LEASES.files.values()))
                close = os.close
                rejected = []
                def close_duplicate(descriptor):
                    duplicate = descriptor != entry['fd'] and os.fstat(descriptor).st_ino == source.stat().st_ino
                    close(descriptor)
                    if duplicate and not rejected:
                        rejected.append(descriptor)
                        raise OSError('injected duplicate close failure')
                with patch('specs_lib.input_watch.os.close', side_effect=close_duplicate):
                    with self.assertRaisesRegex(uf.FlowError, 'could not cover every input'):
                        InputWatch(root, lambda name: False)
                self.assertEqual(len(rejected), 1)
                self.assertEqual(entry['users'], 1)
                self.assertEqual(LEASES.users, 1)
                first.check()
                with self.assertRaises(OSError):
                    os.fstat(rejected[0])
            finally:
                first.close()
            self.assertEqual(LEASES.files, {})
            self.assertEqual(LEASES.users, 0)
            self.assertEqual(signal.getsignal(signal.SIGIO), previous)
            self.assertEqual(signal.pthread_sigmask(signal.SIG_BLOCK, set()), mask)

    def test_unavailable_read_lease_and_reserved_signal_fail_closed_without_leaks(self):
        import errno
        from specs_lib.input_watch import InputWatch, LEASES
        previous = signal.getsignal(signal.SIGIO)
        for condition in ('writer', 'unsupported', 'unsupported-close-error', 'handler', 'blocked'):
            with self.subTest(condition=condition), contextlib.ExitStack() as cleanup:
                if condition == 'writer':
                    cleanup.enter_context((self.root / 'app.py').open('r+b'))
                elif condition.startswith('unsupported'):
                    cleanup.enter_context(patch('specs_lib.input_watch.fcntl.fcntl',
                        side_effect=OSError(errno.EINVAL, 'unsupported fixture filesystem')))
                    if condition == 'unsupported-close-error':
                        close = os.close
                        def close_then_fail(descriptor):
                            close(descriptor)
                            raise OSError('secondary descriptor close failure')
                        cleanup.enter_context(patch('specs_lib.input_watch.os.close', side_effect=close_then_fail))
                elif condition == 'handler':
                    signal.signal(signal.SIGIO, lambda number, frame: None)
                    cleanup.callback(signal.signal, signal.SIGIO, previous)
                else:
                    mask = signal.pthread_sigmask(signal.SIG_BLOCK, {signal.SIGIO})
                    cleanup.callback(signal.pthread_sigmask, signal.SIG_SETMASK, mask)
                with self.assertRaises(uf.FlowError) as failure:
                    InputWatch(self.root, local.source_output)
                if condition.startswith('unsupported'):
                    self.assertIn('read lease unavailable', str(failure.exception))
                self.assertEqual(LEASES.files, {})
                self.assertEqual(LEASES.users, 0)
            self.assertEqual(signal.getsignal(signal.SIGIO), previous)

    def test_read_lease_release_rejects_late_writer_and_restores_signal_after_close_error(self):
        import fcntl
        from specs_lib.input_watch import InputWatch, LEASES
        previous = signal.getsignal(signal.SIGIO)
        close = os.close
        control = fcntl.fcntl
        for failure in ('late-writer', 'release-race', 'close-error'):
            with self.subTest(failure=failure):
                watch = InputWatch(self.root, local.source_output)
                descriptors = [entry['fd'] for entry in LEASES.files.values()]
                fired = []
                source_fd = next(entry['fd'] for entry in LEASES.files.values()
                                 if entry['path'] == self.root / 'app.py')
                def release_lease(descriptor, command, *arguments):
                    if (failure == 'release-race' and descriptor == source_fd and
                            command == fcntl.F_SETLEASE and arguments == (fcntl.F_UNLCK,)):
                        self.assertIn(signal.SIGIO, signal.pthread_sigmask(signal.SIG_BLOCK, set()))
                        with self.assertRaises(BlockingIOError):
                            os.open(self.root / 'app.py', os.O_WRONLY | os.O_NONBLOCK)
                        fired.append(True)
                    return control(descriptor, command, *arguments)
                def release(descriptor):
                    close(descriptor)
                    if descriptor == watch_fd and failure != 'release-race':
                        fired.append(True)
                        if failure == 'late-writer':
                            # Nonblocking break queues SIGIO without waiting for
                            # this close callback to return to Python's handler.
                            mask = signal.pthread_sigmask(signal.SIG_BLOCK, {signal.SIGIO})
                            try:
                                with self.assertRaises(BlockingIOError):
                                    os.open(self.root / 'app.py', os.O_WRONLY | os.O_NONBLOCK)
                            finally:
                                signal.pthread_sigmask(signal.SIG_SETMASK, mask)
                        else:
                            raise OSError('injected closed inotify descriptor')
                watch_fd = watch.fd
                with patch('specs_lib.input_watch.os.close', side_effect=release), \
                        patch('specs_lib.input_watch.fcntl.fcntl', side_effect=release_lease):
                    with self.assertRaises((uf.FlowError, OSError)):
                        watch.close()
                self.assertEqual(fired, [True])
                self.assertEqual(LEASES.files, {})
                self.assertEqual(LEASES.users, 0)
                self.assertEqual(signal.getsignal(signal.SIGIO), previous)
                for descriptor in descriptors:
                    with self.assertRaises(OSError):
                        os.fstat(descriptor)

    def test_read_lease_acquisition_cancellation_releases_ownership_and_preserves_signal(self):
        import fcntl
        from specs_lib.input_watch import InputWatch, LEASES
        control = fcntl.fcntl
        previous = {number: signal.getsignal(number) for number in (signal.SIGIO, signal.SIGINT)}
        acquired = []
        def acquire(descriptor, command, *arguments):
            result = control(descriptor, command, *arguments)
            if command == fcntl.F_SETLEASE and arguments == (fcntl.F_RDLCK,) and not acquired:
                acquired.append(descriptor)
                os.kill(os.getpid(), signal.SIGINT)
            return result
        with patch('specs_lib.input_watch.fcntl.fcntl', side_effect=acquire):
            with self.assertRaises(KeyboardInterrupt):
                InputWatch(self.root, local.source_output)
        self.assertEqual(len(acquired), 1)
        self.assertEqual(LEASES.files, {})
        self.assertEqual(LEASES.users, 0)
        self.assertEqual({number: signal.getsignal(number) for number in previous}, previous)
        with self.assertRaises(OSError):
            os.fstat(acquired[0])

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

    def test_review_watch_rejects_restored_change_before_initial_consumption(self):
        content, fired = local.review_content, False
        original = self.review.read_bytes()
        def read(path):
            nonlocal fired
            if not fired:
                fired = True
                self.review.write_bytes(original + b'\n')
                self.review.write_bytes(original)
            return content(path)
        with patch.object(local, 'review_content', read), self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
            self.run_owned(lambda *args, **options: self.fail('required work must not start'))
        self.assertTrue(fired)
        self.assertEqual(self.review.read_bytes(), original)
        self.assertTrue(all(not path.exists() for path in self.receipts))
        self.assertFalse((self.directory / 'summary.json').exists())

    def test_review_typed_reads_reject_fifo_substitution_and_preserve_aliases(self):
        code = ('import os\nfrom pathlib import Path\nfrom unittest.mock import patch\n'
                'from specs_lib import local_verification as local\n'
                'target=Path(' + repr(str(self.review)) + ')\n'
                'before=target.read_bytes();alias=target.with_name("review-alias.json")\n'
                'alias.symlink_to(target)\n'
                'assert local.review_content(target)==before\n'
                'assert local.review_content(alias)==before\n'
                'text=target.with_name("text-input");text.write_bytes(b"first\\r\\nsecond\\rthird\\n")\n'
                'assert local.input_text(text)==text.read_text()=="first\\nsecond\\nthird\\n"\n'
                'opening=os.open;pathname_open=Path.open;opened=[];fired=False\n'
                'def replace(path):\n global fired\n'
                ' if Path(path)==target and not fired:\n'
                '  assert target.is_file()\n  fired=True\n  target.unlink();os.mkfifo(target)\n'
                'def typed_open(path,flags,*args,**options):\n'
                ' replace(path)\n fd=opening(path,flags,*args,**options);opened.append(fd);return fd\n'
                'def path_open(path,*args,**options):\n'
                ' replace(path)\n return pathname_open(path,*args,**options)\n'
                'with patch.object(local.os,"open",side_effect=typed_open),patch.object(Path,"open",path_open):\n'
                ' try: local.review_content(alias)\n except local.uf.FlowError: pass\n'
                ' else: raise AssertionError("FIFO review admitted")\n'
                'assert fired and opened\n'
                'for fd in opened:\n'
                ' try: os.fstat(fd)\n except OSError: pass\n'
                ' else: raise AssertionError("review descriptor leaked")\n')
        result = subprocess.run([sys.executable, '-B', '-c', code], capture_output=True, text=True,
                                env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, timeout=5)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_review_fifo_replacements_reject_after_work_and_leave_no_success(self):
        code = ('import unittest\nfrom test_specs_local_verification import LocalVerificationTests\n'
                'suite=unittest.TestSuite([LocalVerificationTests("assert_review_fifo_rollback")])\n'
                'raise SystemExit(not unittest.TextTestRunner().run(suite).wasSuccessful())\n')
        self.run_fixture_python(code, cwd=ROOT,
            env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, check=True,
            capture_output=True, text=True, timeout=60)

    def test_source_fifo_between_metadata_and_open_invalidates_receipts(self):
        code = ('import unittest\nfrom test_specs_local_verification import LocalVerificationTests\n'
                'suite=unittest.TestSuite([LocalVerificationTests("assert_source_fifo_rollback")])\n'
                'raise SystemExit(not unittest.TextTestRunner().run(suite).wasSuccessful())\n')
        self.run_fixture_python(code, cwd=ROOT,
            env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, check=True,
            capture_output=True, text=True, timeout=30)

    def assert_source_fifo_rollback(self):
        opening, pathname_open = os.open, Path.open
        for number in (None, signal.SIGINT, signal.SIGTERM):
            with self.subTest(number=number), self.independent_fixture() as case:
                target = case.root / 'app.py'
                ready, fired, opened = False, False, []
                def replace(path):
                    nonlocal fired
                    if ready and not fired and Path(path) == target:
                        self.assertTrue(all(path.is_file() for path in case.receipts))
                        self.assertTrue(target.is_file())
                        target.unlink()
                        os.mkfifo(target)
                        fired = True
                        if number is not None:
                            os.kill(os.getpid(), number)
                def typed_open(path, flags, *args, **options):
                    replace(path)
                    descriptor = opening(path, flags, *args, **options)
                    if fired and Path(path) == target:
                        opened.append(descriptor)
                    return descriptor
                def path_open(path, *args, **options):
                    replace(path)
                    return pathname_open(path, *args, **options)
                def run(command, **options):
                    nonlocal ready
                    if command == list(case.check.command):
                        ready = True
                    return subprocess.CompletedProcess(command, 0)
                expected = (KeyboardInterrupt if number == signal.SIGINT else
                            SystemExit if number == signal.SIGTERM else uf.FlowError)
                with patch.object(local.os, 'open', side_effect=typed_open), \
                        patch.object(Path, 'open', path_open), self.assertRaises(expected) as raised:
                    case.run_owned(run)
                if number == signal.SIGTERM:
                    self.assertEqual(raised.exception.code, 143)
                self.assertTrue(fired and opened)
                for descriptor in opened:
                    with self.assertRaises(OSError):
                        os.fstat(descriptor)
                self.assertTrue(all(not path.exists() for path in case.receipts))
                self.assertFalse(json.loads((case.directory / 'summary.json').read_text())['success'])
                with local.managed(case.root):
                    pass

    def assert_review_fifo_rollback(self):
        original = local.reconcile
        for window in ('reconcile-read', 'receipts', 'final-check'):
            for number in (None, signal.SIGINT, signal.SIGTERM):
                with self.subTest(window=window, number=number), self.independent_fixture() as case:
                    fired, calls = False, []
                    def replace():
                        nonlocal fired
                        self.assertTrue(calls, 'required work must precede replacement')
                        if window != 'reconcile-read':
                            self.assertTrue(all(path.is_file() for path in case.receipts))
                        case.review.unlink()
                        os.mkfifo(case.review)
                        fired = True
                        if number is not None:
                            os.kill(os.getpid(), number)
                    def reconcile(*args, **options):
                        if window == 'reconcile-read':
                            replace()
                        result = original(*args, **options)
                        if window == 'receipts':
                            replace()
                        return result
                    def run(command, **options):
                        calls.append(command)
                        if window == 'final-check' and command == list(case.check.command):
                            replace()
                        return subprocess.CompletedProcess(command, 0)
                    expected = (KeyboardInterrupt if number == signal.SIGINT else
                                SystemExit if number == signal.SIGTERM else uf.FlowError)
                    with patch.object(local, 'reconcile', reconcile), self.assertRaises(expected) as raised:
                        case.run_owned(run)
                    if number == signal.SIGTERM:
                        self.assertEqual(raised.exception.code, 143)
                    self.assertTrue(fired)
                    self.assertTrue(all(not path.exists() for path in case.receipts))
                    self.assertFalse(json.loads((case.directory / 'summary.json').read_text())['success'])
                    # Failure and cancellation must release the original owner.
                    with local.managed(case.root):
                        pass

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
        with contextlib.ExitStack() as cleanup:
            session = local.Session(self.root, self.base, [self.step])
            cleanup.callback(session.close)
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

    def test_watch_fixture_setup_failures_close_every_acquired_descriptor(self):
        from specs_lib.input_watch import InputWatch
        initialize, add = InputWatch.__init__, InputWatch.add
        for failure_at in (2, 3):
            acquired, roots = [], []
            def initialize_watch(watch, root, excluded):
                if root == self.root:
                    roots.append(watch)
                initialize(watch, root, excluded)
            def install(watch, *args, **options):
                if watch.fd not in acquired:
                    acquired.append(watch.fd)
                add(watch, *args, **options)
                if len(roots) == failure_at and watch is roots[-1]:
                    raise uf.FlowError('injected setup watch coverage failure')
            with self.subTest(failure_at=failure_at), \
                    patch.object(InputWatch, '__init__', initialize_watch), \
                    patch.object(InputWatch, 'add', install):
                with self.assertRaisesRegex(uf.FlowError, 'setup watch coverage'):
                    self.test_watch_close_failure_still_closes_remaining_real_descriptors()
            self.assertEqual(len(roots), failure_at)
            self.assertTrue(acquired)
            for descriptor in acquired:
                with self.assertRaises(OSError):
                    os.fstat(descriptor)


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
            conventional = packages / '__pycache__' / ('selected_input.' + sys.implementation.cache_tag + '.pyc')
            subprocess.run([sys.executable, '-P', '-S', '-c',
                'import py_compile; py_compile.compile(' + repr(str(module)) +
                ',cfile=' + repr(str(conventional)) + ',doraise=True)'],
                cwd=self.root, env={**os.environ, 'PYTHONPATH': ''},
                check=True, capture_output=True, timeout=15)
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

    def test_python_cache_typed_reads_reject_replacements_and_close_on_failure(self):
        import hashlib
        with local.python_source_imports(self.directory) as cache:
            target = cache.directory / 'entry.pyc'
            payload = b'prepared cache input\n'
            target.write_bytes(payload)
            expected = local.digest_rows(sorted([
                ('.', cache.directory.lstat().st_mode, b''),
                ('entry.pyc', target.lstat().st_mode, hashlib.sha256(payload).digest())]))
            self.assertEqual(cache.contents(), expected)
            other = self.directory / 'other-cache-input'
            other.write_bytes(payload)
            for kind in ('symlink', 'fifo'):
                target.unlink()
                if kind == 'symlink':
                    target.symlink_to(other)
                else:
                    os.mkfifo(target)
                with self.subTest(admission=kind), patch.object(local.os, 'open') as open_file:
                    with self.assertRaisesRegex(uf.FlowError, 'unsupported entry'):
                        cache.contents()
                    open_file.assert_not_called()
                target.unlink()
                target.write_bytes(payload)
            opening, closing = os.open, os.close
            opened = []
            substitution = [None]
            def record(path, flags):
                self.assertTrue(flags & os.O_NONBLOCK and flags & os.O_NOFOLLOW and flags & os.O_CLOEXEC)
                if substitution[0] is not None:
                    substitute, substitution[0] = substitution[0], None
                    substitute()
                descriptor = opening(path, flags)
                opened.append(descriptor)
                return descriptor
            def assert_closed():
                for descriptor in opened:
                    with self.assertRaises(OSError):
                        os.fstat(descriptor)
            with patch.object(local.os, 'open', side_effect=record):
                self.assertEqual(cache.contents(), expected)
                self.assertEqual(len(opened), 1)
                assert_closed()
                for kind in ('regular', 'symlink'):
                    replacement = self.directory / ('cache-replacement-' + kind)
                    replacement.write_bytes(payload)
                    def substitute():
                        target.unlink()
                        if kind == 'regular':
                            replacement.replace(target)
                        else:
                            target.symlink_to(replacement)
                    substitution[0] = substitute
                    with self.subTest(kind=kind), self.assertRaises((uf.FlowError, OSError)):
                        cache.contents()
                    assert_closed()
                    target.unlink()
                    target.write_bytes(payload)
                def close_error(descriptor):
                    closing(descriptor)
                    raise OSError('secondary cache close failure')
                for failure in (OSError('original cache read failure'), KeyboardInterrupt()):
                    with self.subTest(error=type(failure).__name__), \
                            patch.object(local.os, 'fdopen', side_effect=failure), \
                            patch.object(local.os, 'close', side_effect=close_error):
                        with self.assertRaises(type(failure)) as caught:
                            cache.contents()
                        self.assertIs(caught.exception, failure)
                    assert_closed()
                with patch.object(local.os, 'close', side_effect=close_error):
                    with self.assertRaisesRegex(OSError, 'secondary cache close failure'):
                        cache.contents()
                assert_closed()
                original_hash = local.hashlib.file_digest
                def restored_write(*args, **options):
                    result = original_hash(*args, **options)
                    target.write_bytes(b'changed cache input\n')
                    target.write_bytes(payload)
                    return result
                with patch.object(local.hashlib, 'file_digest', side_effect=restored_write):
                    with self.assertRaisesRegex(uf.FlowError, 'changed during traversal'):
                        cache.contents()
                assert_closed()
                cancellation = local.InvocationCancellation()
                previous = signal.getsignal(signal.SIGINT)
                def interrupt(*args, **options):
                    result = original_hash(*args, **options)
                    os.kill(os.getpid(), signal.SIGINT)
                    return result
                with patch.object(local.hashlib, 'file_digest', side_effect=interrupt):
                    with self.assertRaises(KeyboardInterrupt), local.invocation_signals(cancellation):
                        cache.contents()
                self.assertEqual(cancellation.number, signal.SIGINT)
                self.assertEqual(signal.getsignal(signal.SIGINT), previous)
                assert_closed()

    def test_python_cache_fifo_after_type_checks_rejects_without_blocking(self):
        code = ('import os\nfrom pathlib import Path\nfrom unittest.mock import patch\n'
            'from specs_lib import local_verification as local\n'
            'with local.python_source_imports(Path(' + repr(str(self.directory)) + ')) as cache:\n'
            ' target=cache.directory/"entry.pyc";target.write_bytes(b"prepared cache input")\n'
            ' cache.contents()\n opening=os.open;pathname_open=Path.open;opened=[];fired=False\n'
            ' def replace(path):\n  global fired\n'
            '  if Path(path)==target and not fired:\n'
            '   assert target.is_file()\n   fired=True\n   target.unlink();os.mkfifo(target)\n'
            ' def typed_open(path,flags,*args,**options):\n'
            '  replace(path)\n  fd=opening(path,flags,*args,**options);opened.append(fd);return fd\n'
            ' def path_open(path,*args,**options):\n'
            '  replace(path)\n  return pathname_open(path,*args,**options)\n'
            ' with patch.object(local.os,"open",side_effect=typed_open),patch.object(Path,"open",path_open):\n'
            '  try: cache.contents()\n  except local.uf.FlowError: pass\n'
            '  else: raise AssertionError("FIFO admitted")\n'
            ' assert fired and opened\n'
            ' for fd in opened:\n'
            '  try: os.fstat(fd)\n  except OSError: pass\n'
            '  else: raise AssertionError("cache descriptor leaked")\n')
        subprocess.run([sys.executable, '-B', '-c', code], check=True, capture_output=True,
                       env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, timeout=5)

    def test_python_cache_late_fifo_rejects_outputs_and_preserves_new_owner_replacements(self):
        code = ('import unittest\nfrom test_specs_local_verification import LocalVerificationTests\n'
                'suite=unittest.TestSuite([LocalVerificationTests("assert_late_cache_fifo_rollback")])\n'
                'raise SystemExit(not unittest.TextTestRunner().run(suite).wasSuccessful())\n')
        self.run_fixture_python(code, cwd=ROOT,
            env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, check=True, capture_output=True, timeout=60)

    def assert_late_cache_fifo_rollback(self):
        digest, summary, managed, invalidate = local.artifact_digest, local.write_summary, local.managed, local.Publications.invalidate
        for window in ('summary', 'owner', 'next-owner'):
            ready, fired, fresh = False, False, {}
            summary_path = self.directory / 'summary.json'
            def publish(*args, **options):
                nonlocal ready
                summary(*args, **options)
                if window == 'summary':
                    ready = True
            @contextlib.contextmanager
            def owner(root):
                nonlocal ready
                with managed(root):
                    yield
                if window != 'summary':
                    ready = True
            def substitute(path, expected):
                nonlocal fired
                if ready and not fired and Path(path).is_relative_to(Path(sys.pycache_prefix).resolve()):
                    self.assertTrue(all(receipt.is_file() for receipt in self.receipts))
                    self.assertTrue(json.loads(summary_path.read_text())['success'])
                    fired = True
                    path.unlink()
                    os.mkfifo(path)
                return digest(path, expected)
            def rollback(publications):
                if window == 'next-owner':
                    child = 'import specs\nraise SystemExit(specs.main(' + repr(['reconcile', '--root', str(self.root),
                        '--base', self.base, '--review', str(self.review)]) + '))'
                    environment = {key: value for key, value in os.environ.items() if key != 'PYTHONPYCACHEPREFIX'}
                    subprocess.run([sys.executable, '-B', '-c', child], cwd=ROOT,
                        env={**environment, 'PYTHONPATH': str(ROOT / 'scripts')}, check=True, capture_output=True, timeout=30)
                    temporary = self.directory / 'next-summary.json'
                    temporary.write_bytes(b'next owner summary\n')
                    temporary.replace(summary_path)
                    for path in [*self.receipts, summary_path]:
                        fresh[path] = path.read_bytes()
                        self.assertTrue(all(os.fstat(fd).st_ino != path.stat().st_ino for fd in publications.descriptors))
                invalidate(publications)
            with self.subTest(window=window), patch.object(local, 'artifact_digest', substitute), \
                    patch.object(local, 'write_summary', publish), patch.object(local, 'managed', owner), \
                    patch.object(local.Publications, 'invalidate', rollback), \
                    self.assertRaisesRegex(uf.FlowError, 'changed while opening'):
                self.run_owned(lambda command, **options: subprocess.CompletedProcess(command, 0))
            self.assertTrue(fired)
            if window == 'next-owner':
                self.assertTrue(fresh)
                self.assertEqual({path: path.read_bytes() for path in fresh}, fresh)
                self.assertEqual(specs.main(['check', '--root', str(self.root), '--base', self.base]), 0)
            else:
                self.assertTrue(all(not path.exists() or path.read_bytes() == b'' for path in self.receipts))
                self.assertEqual(summary_path.read_bytes(), b'')

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
                    ('python3', '-m', 'unittest', 'discover', '-s', 'scripts', '-p', 'test_specs*.py'),
                    ('python3', 'scripts/python_suites.py', 'ci'),
                    ('python3', 'scripts/python_suites.py', 'specs')]
        for command in commands:
            step = local.Step('required-suite', self.root, command)
            self.assertEqual(local.command_timeout(self.root, step), 900)
            self.assertEqual(local.command_timeout(self.root, step, 1.5), 1.5)
            self.assertEqual(local.command_timeout(self.root, step, 1200), 900)
            other = local.Step('required-suite', self.directory, command)
            self.assertEqual(local.command_timeout(self.root, other), 600)
            self.assertEqual(local.command_timeout(self.root, other, 12), 12)
        for command in [commands[0] + ('--extra',), ('python3', 'scripts/custom.py'),
                        (sys.executable, 'scripts/test_ci.py'), commands[1][:-1] + ('other*.py',),
                        commands[2] + ('--workers', '2'), commands[3][:-1] + ('custom',), self.step.command]:
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
            with self.subTest(kind=kind), self.independent_fixture() as case:
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
        for cache_kind, outcome in ((kind, result) for kind in ('python', 'go')
                                    for result in ('success', 'error')):
            with self.subTest(cache=cache_kind, outcome=outcome):
                original = local.Session.close
                def close(session):
                    original(session)
                    if cache_kind == 'python':
                        entry = next(Path(sys.pycache_prefix).rglob('*.pyc'))
                        before = entry.read_bytes()
                        entry.write_bytes(b'cleanup validation event')
                        entry.write_bytes(before)
                    else:
                        directory = Path(os.environ['GOCACHE'])
                        before = directory.stat().st_mode
                        directory.chmod(0o755)
                        directory.chmod(before)
                def run(command, **options):
                    entry = Path(os.environ['GOCACHE']) / 'fixture-cache-entry'
                    if cache_kind == 'go' and not entry.exists():
                        entry.write_bytes(b'checked cache output')
                    return subprocess.CompletedProcess(command, 9 if outcome == 'error' else 0)
                with patch.object(local.Session, 'close', close):
                    with self.assertRaisesRegex(uf.FlowError, 'Required local verification failed' if outcome == 'error' else 'inputs changed'):
                        self.run_owned(run)
                self.assertTrue(all(not path.exists() or path.read_bytes() == b'' for path in self.receipts))

    def test_python_native_directory_aliases_admit_loader_inputs_before_execution(self):
        packages, package, components, private, abi = [self.directory / name for name in
            ('package-search', 'native-package', 'native-components', 'private-libraries', 'minimal-abi')]
        for directory in (packages, package, components, private, abi):
            directory.mkdir()
        (packages / 'fixture_native_package').symlink_to(package, target_is_directory=True)
        (package / 'components').symlink_to(components, target_is_directory=True)
        (package / '__init__.py').write_text('from ctypes import CDLL\nfrom pathlib import Path\n'
            'VALUE = CDLL(str(Path(__file__).parent / "components" / "_fixture.so")).alias_value()\n')
        (components / '__init__.py').write_text('')
        source = self.directory / 'alias-native.c'
        library, replacement = private / 'libaliasfixture.so', self.directory / 'alias-replacement.so'
        for value, output in ((7, library), (9, replacement)):
            source.write_text('int fixture_value(void) { return ' + str(value) + '; }\n')
            subprocess.run(['/usr/bin/gcc', '-shared', '-fPIC', '-nostdinc', str(source), '-o', str(output)],
                           check=True, capture_output=True, timeout=15)
        source.write_text('extern int fixture_value(void);\nint alias_value(void) { return fixture_value(); }\n')
        subprocess.run(['/usr/bin/gcc', '-shared', '-fPIC', '-nostdinc', str(source),
                        '-o', str(components / '_fixture.so'), '-L' + str(private), '-laliasfixture',
                        '-Wl,-rpath,' + str(private)], check=True, capture_output=True, timeout=15)
        code = 'import fixture_native_package; print(fixture_native_package.VALUE, end="")'
        environment = {**os.environ, 'PYTHONPATH': str(packages), 'PYTHONDONTWRITEBYTECODE': '1'}
        control = subprocess.run([sys.executable, '-c', code], cwd=self.root, env=environment,
                                 check=True, capture_output=True, text=True, timeout=15)
        self.assertEqual(control.stdout, '7')
        original_run = subprocess.run
        def discovery(command, **options):
            if command[1:] == ['-p']:
                return subprocess.CompletedProcess(command, 0, '0 libs found in cache fixture\n', '')
            return original_run(command, **options)
        calls = []
        def inventory(root, executables, *, include_private=False):
            directories = {packages} | ({private} if include_private else set())
            files = self.discover_native(directories, set(), [])
            return directories, files
        with patch.object(local, 'SYSTEM_LIBRARIES', abi), patch.object(local, 'LIBRARY_LOOKUPS', (abi,)), \
                patch.object(local.subprocess, 'run', discovery), patch.dict(os.environ, {'PYTHONPATH': str(packages)}):
            with patch.object(local, 'helper_inputs', side_effect=inventory):
                with self.assertRaisesRegex(uf.FlowError, 'outside inventoried'):
                    self.run_owned(lambda command, **options: calls.append(command) or subprocess.CompletedProcess(command, 0))
            self.assertEqual(calls, [])
            self.assertTrue(all(not path.exists() for path in self.receipts))
            selected, _ = inventory(self.root, [], include_private=True)
            self.assertIn(packages / 'fixture_native_package', selected)
            self.assertIn(package / 'components', selected)
            marker = self.directory / 'alias-native-value'
            def consume(command, **options):
                before = library.read_bytes()
                try:
                    library.write_bytes(replacement.read_bytes())
                    with marker.open('w') as log:
                        return uf.run_verification([sys.executable, '-c', code], cwd=options['cwd'],
                                                   log_stream=log, timeout=10)
                finally:
                    library.write_bytes(before)
            with patch.object(local, 'helper_inputs', side_effect=lambda root, executables:
                              inventory(root, executables, include_private=True)):
                with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                    self.run_owned(consume)
            self.assertEqual(marker.read_text(), '9')
            self.assertTrue(all(not path.exists() for path in self.receipts))

    def test_native_library_inventory_real_abi_and_unsupported_loader_contexts(self):
        directories = set()
        # Scope the positive control to the maintained distro ABI, rather than
        # unrelated host-installed libraries whose private RUNPATHs must reject.
        abi = self.directory / 'real-abi'
        abi.mkdir()
        libc = abi / 'libc.so.6'
        shutil.copyfile('/usr/lib/x86_64-linux-gnu/libc.so.6', libc)
        original_run = subprocess.run
        def abi_cache(command, **options):
            result = original_run(command, **options)
            if command[1:] != ['-p']:
                return result
            rows = [line for line in result.stdout.splitlines() if '=> ' in line and 'x86-64' in line
                    and Path(line.split('=> ', 1)[1].strip()).resolve() ==
                    Path('/usr/lib/x86_64-linux-gnu/libc.so.6').resolve()]
            self.assertTrue(rows, 'real ldconfig must select the copied distro libc')
            return subprocess.CompletedProcess(result.args, result.returncode,
                '1 libs found in ABI fixture\n\tlibc.so.6 (libc6,x86-64) => ' + str(libc) + '\n', result.stderr)
        source, probe = self.directory / 'abi.c', self.directory / 'abi-probe'
        source.write_text('int main(void) { return 0; }\n')
        subprocess.run(['/usr/bin/gcc', '-nostdinc', str(source), '-o', str(probe), '-Wl,-rpath,' + str(abi)],
                       check=True, capture_output=True, timeout=15)
        selected = subprocess.check_output([str(local.SYSTEM_LOADER), '--list', str(probe)], text=True, timeout=15)
        self.assertIn('libc.so.6 => ' + str(libc), selected)
        subprocess.run([str(probe)], check=True, capture_output=True, timeout=15)
        with patch.object(local, 'SYSTEM_LIBRARIES', abi), patch.object(local, 'LIBRARY_LOOKUPS', (abi,)), \
                patch.object(local.subprocess, 'run', abi_cache):
            files = self.discover_native(directories, {probe}, ['/usr/bin/python3', 'node', 'gcc', 'git', 'make'])
        self.assertIn(abi, directories)
        self.assertIn(local.SYSTEM_LOADER, files)
        self.assertIn(Path('/etc/ld.so.cache'), files)
        self.assertIn(libc, files)
        self.assertEqual(libc.read_bytes(), Path('/usr/lib/x86_64-linux-gnu/libc.so.6').read_bytes())
        private = self.directory / 'unrepresented-native-search'
        private.mkdir()
        source, library = self.directory / 'unrelated.c', self.directory / 'libunrelated.so'
        source.write_text('int unrelated(void) { return 7; }\n')
        subprocess.run(['/usr/bin/gcc', '-shared', '-fPIC', '-nostdinc', str(source), '-o', str(library),
                        '-Wl,-rpath,' + str(private)], check=True, capture_output=True, timeout=15)
        def unrelated_cache(command, **options):
            result = abi_cache(command, **options)
            if command[1:] == ['-p']:
                return subprocess.CompletedProcess(result.args, result.returncode, result.stdout +
                    '\tlibunrelated.so (libc6,x86-64) => ' + str(library) + '\n', result.stderr)
            return result
        with patch.object(local, 'SYSTEM_LIBRARIES', abi), patch.object(local, 'LIBRARY_LOOKUPS', (abi,)), \
                patch.object(local.subprocess, 'run', unrelated_cache):
            with self.assertRaisesRegex(uf.FlowError, 'ELF library search lies outside inventoried inputs'):
                self.discover_native(set(), set(), ['/usr/bin/python3'])
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
        # Inventory this C probe and its libraries. The separate real ABI control
        # covers maintained system launchers, including distro Python.
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
                        patch.object(local, 'native_library_inputs', side_effect=lambda directories, files, _executables:
                            {probe} | self.discover_native(directories, files | {probe}, [str(probe)])), \
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
            # These controls inspect the explicit C fixture, so an unrelated
            # host interpreter RUNPATH cannot mask the intended ELF rejection.
            with self.assertRaisesRegex(uf.FlowError, 'outside inventoried'):
                self.discover_native(set(), {custom}, [])
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
                self.discover_native(set(), {alias}, [])

    def test_owned_go_cache_isolates_archives_and_empties_between_commands(self):
        project = self.directory / 'go-cache-project'
        (project / 'library').mkdir(parents=True)
        (project / 'go.mod').write_text('module fixture.invalid/cachetest\n\ngo 1.23\n')
        (project / 'library/library.go').write_text(
            'package library\nfunc Value() string { return "CACHE_VALUE_7" }\n')
        (project / 'main.go').write_text('package main\nimport "fixture.invalid/cachetest/library"\n'
                                       'func main() { println(library.Value()) }\n')
        external = self.directory / 'external-go-cache'
        external.mkdir()
        config = self.directory / 'go-cache-config'
        config.write_text('GOCACHE=' + str(external) + '\n')
        go_environment = {**os.environ, 'GOENV': 'off', 'GOWORK': 'off',
                          'GOCACHE': str(external), 'GOTOOLCHAIN': 'local'}
        def go(command, **options):
            return subprocess.run(['go', *command], cwd=project, capture_output=True,
                                  text=True, timeout=120, check=True, **options)
        self.assertEqual(go(['run', '.'], env=go_environment).stderr.strip(), 'CACHE_VALUE_7')
        archive_command = ['list', '-export', '-f', '{{.Export}}', './library']
        external_archive = Path(go(archive_command, env=go_environment).stdout.strip())
        original = external_archive.read_bytes()
        replacement = original.replace(b'CACHE_VALUE_7', b'CACHE_VALUE_9')
        self.assertNotEqual(original, replacement)
        self.assertEqual(len(original), len(replacement))
        original_id = go(['tool', 'buildid', str(external_archive)], env=go_environment).stdout
        external_archive.write_bytes(replacement)
        self.assertEqual(go(['tool', 'buildid', str(external_archive)], env=go_environment).stdout,
                         original_id)
        self.assertEqual(go(['run', '-gcflags=-l', '.'], env=go_environment).stderr.strip(), 'CACHE_VALUE_9')
        step = local.Step('static', project, ('go', 'run', '-gcflags=-l', '.'))
        marker = self.directory / 'go-cache-consumed'
        selected, owners = [], []
        def consume(command, **options):
            if command[0] == 'go':
                cache = Path(go(['env', 'GOCACHE']).stdout.strip())
                selected.append(cache)
                owners.append(Path(os.environ['CODE_FACTORY_VERIFY_RUN']) / 'tmp')
                with marker.open('w') as log:
                    return uf.run_verification(command, cwd=project, timeout=120, log_stream=log)
            return subprocess.CompletedProcess(command, 0)
        with patch.object(local, 'helper_inputs', return_value=({self.python_runtime}, set())):
            for source in ('environment', 'goenv'):
                with self.subTest(source=source), patch.dict(os.environ, {
                        'GOENV': str(config), 'GOWORK': 'off', 'GOTOOLCHAIN': 'local'}):
                    os.environ.pop('GOCACHE', None)
                    if source == 'environment':
                        os.environ['GOCACHE'] = str(external)
                    before = dict(os.environ)
                    self.assertEqual(go(['env', 'GOCACHE']).stdout.strip(), str(external))
                    self.assertTrue(self.run_owned(consume, plan=[step, self.check])['verified'])
                    self.assertEqual(marker.read_text().strip(), 'CACHE_VALUE_7')
                    self.assertTrue(selected[-1].is_relative_to(owners[-1]))
                    self.assertEqual(dict(os.environ), before)
                    self.assertTrue(all(path.exists() for path in self.receipts))
            self.assertEqual(len(set(selected)), 2)
            self.assertTrue(all(not path.exists() for path in selected))
            self.assertEqual(external_archive.read_bytes(), replacement)

            alias = self.directory / 'go-archive-alias'
            retained = {}
            transition = local.Step('correctness', self.root, ('python3', '-c', 'pass'))
            def restored(command, **options):
                if command[0] == 'go':
                    result = consume(command, **options)
                    archive = Path(go(archive_command).stdout.strip())
                    retained['path'] = archive
                    retained['bytes'] = archive.read_bytes()
                    self.assertIn(b'CACHE_VALUE_7', retained['bytes'])
                    os.link(archive, alias)
                    return result
                if tuple(command) == transition.command:
                    self.assertFalse(retained['path'].exists())
                    self.assertEqual(list(Path(os.environ['GOCACHE']).iterdir()), [])
                    try:
                        alias.write_bytes(retained['bytes'].replace(b'CACHE_VALUE_7', b'CACHE_VALUE_9'))
                        with marker.open('w') as log:
                            return uf.run_verification(['go', 'run', '-gcflags=-N -l', '.'],
                                cwd=project, timeout=120, log_stream=log)
                    finally:
                        alias.write_bytes(retained['bytes'])
                return subprocess.CompletedProcess(command, 0)
            with patch.dict(os.environ, {'GOENV': 'off', 'GOWORK': 'off', 'GOTOOLCHAIN': 'local'}):
                before = dict(os.environ)
                self.assertTrue(self.run_owned(restored, plan=[step, transition, self.check])['verified'])
                self.assertEqual(dict(os.environ), before)
            self.assertEqual(marker.read_text().strip(), 'CACHE_VALUE_7')
            self.assertEqual(alias.read_bytes(), retained['bytes'])
            self.assertTrue(all(path.exists() for path in self.receipts))
            self.assertTrue(all(not path.exists() for path in selected))

    def test_selected_headless_browser_rejects_external_loader_inputs_before_checks(self):
        script = ("const {registry:{registry}}=require('playwright-core/lib/coreBundle');"
                  "console.log(registry.findExecutable('chromium-headless-shell').executablePath());")
        selected = Path(subprocess.check_output(['node', '-e', script], cwd=ROOT / 'web',
                                                text=True, timeout=15).strip()).name
        self.assertEqual(selected, 'chrome-headless-shell')
        browser = self.directory / 'browser-installation'
        browser.mkdir()
        outside = self.directory / 'outside-browser-libraries'
        outside.mkdir()
        source = self.directory / 'headless.c'
        source.write_text('int main(void) { return 0; }\n')
        for name in ('headless_shell', selected):
            for mode in ('runpath', 'interpreter'):
                with self.subTest(name=name, mode=mode):
                    executable = browser / name
                    flag = ('-Wl,-rpath,' + str(outside) if mode == 'runpath' else
                            '-Wl,--dynamic-linker=/unsupported/browser-loader')
                    subprocess.run(['/usr/bin/gcc', '-nostdinc', str(source), '-o', str(executable), flag],
                                   capture_output=True, check=True, timeout=15)
                    calls = []
                    def inventory(root, executables):
                        directories = {browser}
                        self.discover_native(directories, set(), [])
                        # Admission is the subject here; the real ABI inventory
                        # has its own control. Keep any incorrect acceptance on
                        # this small explicit tree until the fake runner fails it.
                        return {browser}, set()
                    def called(command, **options):
                        calls.append(command)
                        return subprocess.CompletedProcess(command, 0)
                    with patch.object(local, 'helper_inputs', side_effect=inventory):
                        with self.assertRaisesRegex(uf.FlowError,
                                'outside inventoried' if mode == 'runpath' else 'Alternate ELF'):
                            self.run_owned(called)
                    self.assertEqual(calls, [])
                    self.assertTrue(all(not path.exists() for path in self.receipts))
                    executable.unlink()

    def test_effective_go_cache_program_rejects_environment_and_goenv_before_modules(self):
        config = self.directory / 'goenv-cache-program'
        helper = self.directory / 'external-cache-program'
        helper.write_text('#!/bin/sh\nexit 19\n')
        helper.chmod(0o755)
        for source in ('environment', 'goenv'):
            with self.subTest(source=source), patch.dict(os.environ):
                os.environ.pop('GOCACHEPROG', None)
                config.write_text('GOCACHEPROG=' + str(helper) + '\n' if source == 'goenv' else '')
                os.environ['GOENV'] = str(config)
                if source == 'environment':
                    os.environ['GOCACHEPROG'] = str(helper)
                self.assertEqual(subprocess.check_output(['go', 'env', 'GOCACHEPROG'],
                                 text=True, timeout=15).strip(), str(helper))
                with patch.object(local, 'go_module_inputs', side_effect=AssertionError('modules must not start')):
                    with self.assertRaisesRegex(uf.FlowError, 'effective GOCACHEPROG'):
                        local.helper_inputs(self.root, ['go'])
        with patch.dict(os.environ, {'GOENV': str(config), 'GOCACHEPROG': ''}):
            config.write_text('')
            with patch.object(local, 'go_module_inputs', return_value=(set(), set())):
                directories, files = local.helper_inputs(self.root, ['go'])
            self.assertTrue(directories)
            self.assertTrue(files)

    def test_planning_rejects_a_removed_external_make_shadow_before_required_execution(self):
        makefile = self.root / 'Makefile'
        static = '\tpython3 scripts/verification_timing.py command static -- go vet ./...\n'
        makefile.write_text('.PHONY: local-verification-prepare verify-steps\n'
            'local-verification-prepare:\n\tmkdir -p bin\nverify-steps:\n' + static +
            '\tpython3 scripts/verification_timing.py command correctness -- ' +
            sys.executable + ' scripts/test_app.py\n'
            '\tpython3 scripts/verification_timing.py command specifications -- '
            'python3 scripts/specs.py check --base ' + self.base + '\n')
        catalog = json.loads((self.root / uf.CATALOG).read_text())
        catalog['flows'][0]['implementation'].append({'path': 'Makefile', 'purpose': 'Native fixture verification graph.'})
        (self.root / uf.CATALOG).write_text(json.dumps(catalog))
        self.assertEqual(specs.main(['render', '--root', str(self.root)]), 0)
        affected = specs.analyze(uf.snapshot(self.root, self.base), uf.snapshot(self.root), self.base)[1]
        self.review.write_text(json.dumps({subject: {'kind': 'refactor', **dict.fromkeys(
            ('description', 'implementation', 'tests'), NOTE)} for subject in affected}))
        real_make = shutil.which('make')
        directory = self.directory / 'external-planner'
        directory.mkdir()
        shadow = directory / 'make'
        shadow.write_text('#!/bin/sh\ncase " $* " in\n *" verify-steps "*) ' +
            shlex.quote(real_make) + ' "$@" | /usr/bin/sed "/command static -- go vet/d"; '
            '/usr/bin/rm -- "$0" ;;\n *) exec ' + shlex.quote(real_make) + ' "$@" ;;\nesac\n')
        shadow.chmod(0o755)
        native_plan = local.native_plan
        captured, calls = [], []
        def capture(root, target, base):
            result = native_plan(root, target, base)
            if target == 'verify-steps':
                captured.extend(result)
            return result
        self.assertIn(('go', 'vet', './...'), [step.command for step in native_plan(self.root, 'verify-steps', self.base)])
        with patch.dict(os.environ, {'PATH': str(directory) + os.pathsep + os.environ['PATH']}), \
                patch.object(local, 'native_plan', side_effect=capture):
            with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                local.verify(self.root, self.base, self.review,
                             runner=lambda command, **options: calls.append(command))
        self.assertFalse(shadow.exists())
        self.assertTrue(captured)
        self.assertNotIn(('go', 'vet', './...'), [step.command for step in captured])
        self.assertEqual(calls, [])
        self.assertTrue(all(not path.exists() for path in self.receipts))
        self.assertNotIn('go', [Path(step.command[0]).name for step in captured])

    def test_planning_rejects_restored_checkout_ancestor_substitution(self):
        holder = self.directory / 'holder'
        holder.mkdir()
        self.root.rename(holder / 'repository')
        self.root = holder / 'repository'
        self.receipts = [self.root / specs.RECORDS / (subject + '.json') for subject in self.affected]
        makefile = self.root / 'Makefile'
        static = '\tpython3 scripts/verification_timing.py command static -- go vet ./...\n'
        original = ('.PHONY: local-verification-prepare verify-steps\n'
                    'local-verification-prepare:\n\tmkdir -p bin\n'
                    'verify-steps:\n' + static +
                    '\tpython3 scripts/verification_timing.py command specifications -- '
                    'python3 scripts/specs.py check --base ' + self.base + '\n')
        makefile.write_text(original)
        catalog = json.loads((self.root / uf.CATALOG).read_text())
        catalog['flows'][0]['implementation'].append({'path': 'Makefile', 'purpose': 'Native fixture verification graph.'})
        (self.root / uf.CATALOG).write_text(json.dumps(catalog))
        self.assertEqual(specs.main(['render', '--root', str(self.root)]), 0)
        affected = specs.analyze(uf.snapshot(self.root, self.base), uf.snapshot(self.root), self.base)[1]
        self.review.write_text(json.dumps({subject: {'kind': 'refactor', **dict.fromkeys(
            ('description', 'implementation', 'tests'), NOTE)} for subject in affected}))
        alternate, saved = self.directory / 'alternate', self.directory / 'saved-holder'
        shutil.copytree(holder, alternate)
        (alternate / 'repository/Makefile').write_text(original.replace(static, ''))
        native_plan, watch_type = local.native_plan, local.InputWatch
        initial = local.source_identity(self.root)
        inode = self.root.stat().st_ino
        makefile_inode = makefile.stat().st_ino
        self.assertIn(('go', 'vet', './...'), [step.command for step in native_plan(self.root, 'verify-steps', self.base)])
        @contextlib.contextmanager
        def substituted():
            holder.rename(saved)
            alternate.rename(holder)
            try:
                yield
            finally:
                holder.rename(alternate)
                saved.rename(holder)
        for stage in ('planning', 'watch-construction'):
            with self.subTest(stage=stage):
                calls, captured = [], []
                constructed = False
                def construct(root, excluded):
                    nonlocal constructed
                    if stage == 'watch-construction' and root == self.root and not constructed:
                        constructed = True
                        with substituted():
                            return watch_type(root, excluded)
                    return watch_type(root, excluded)
                def capture(root, target, base):
                    if target != 'verify-steps':
                        return native_plan(root, target, base)
                    if stage == 'planning':
                        with substituted():
                            result = native_plan(root, target, base)
                    else:
                        makefile.write_text(original.replace(static, ''))
                        try:
                            result = native_plan(root, target, base)
                        finally:
                            makefile.write_text(original)
                    captured.extend(result)
                    return result
                def run(command, **options):
                    calls.append(command)
                    if command == ['python3', 'scripts/specs.py', 'check', '--base', self.base]:
                        return subprocess.CompletedProcess(command,
                            specs.main(['check', '--root', str(self.root), '--base', self.base]))
                    return subprocess.run(command, capture_output=True, **options)
                with patch.object(local, 'InputWatch', side_effect=construct), \
                        patch.object(local, 'native_plan', side_effect=capture):
                    with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                        local.verify(self.root, self.base, self.review, runner=run)
                self.assertEqual(calls, [])
                if stage == 'planning':
                    self.assertTrue(captured)
                    self.assertNotIn(('go', 'vet', './...'), [step.command for step in captured])
                self.assertEqual(self.root.stat().st_ino, inode)
                self.assertEqual(makefile.stat().st_ino, makefile_inode)
                self.assertEqual(local.source_identity(self.root), initial)
                self.assertEqual(makefile.read_text(), original)
                self.assertTrue(all(not path.exists() for path in self.receipts))

    def test_planning_rejects_a_restored_makefile_before_required_execution(self):
        makefile = self.root / 'Makefile'
        static = '\tpython3 scripts/verification_timing.py command static -- go vet ./...\n'
        original = ('.PHONY: local-verification-prepare verify-steps\n'
                    'local-verification-prepare:\n\tmkdir -p bin\n'
                    'verify-steps:\n' + static +
                    '\tpython3 scripts/verification_timing.py command specifications -- '
                    'python3 scripts/specs.py check --base ' + self.base + '\n')
        makefile.write_text(original)
        catalog = json.loads((self.root / uf.CATALOG).read_text())
        catalog['flows'][0]['implementation'].append({'path': 'Makefile', 'purpose': 'Native fixture verification graph.'})
        (self.root / uf.CATALOG).write_text(json.dumps(catalog))
        self.assertEqual(specs.main(['render', '--root', str(self.root)]), 0)
        affected = specs.analyze(uf.snapshot(self.root, self.base), uf.snapshot(self.root), self.base)[1]
        self.review.write_text(json.dumps({subject: {'kind': 'refactor', **dict.fromkeys(
            ('description', 'implementation', 'tests'), NOTE)} for subject in affected}))
        native_plan = local.native_plan
        captured, calls = [], []
        def transient(root, target, base):
            if target != 'verify-steps':
                return native_plan(root, target, base)
            makefile.write_text(original.replace(static, ''))
            try:
                result = native_plan(root, target, base)
                captured.extend(result)
                return result
            finally:
                makefile.write_text(original)
        self.assertIn(('go', 'vet', './...'), [step.command for step in native_plan(self.root, 'verify-steps', self.base)])
        with patch.object(local, 'native_plan', side_effect=transient):
            with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                local.verify(self.root, self.base, self.review,
                             runner=lambda command, **options: calls.append(command))
        self.assertNotIn(('go', 'vet', './...'), [step.command for step in captured])
        self.assertEqual(makefile.read_text(), original)
        self.assertEqual(calls, [])
        self.assertTrue(all(not path.exists() for path in self.receipts))

    def test_cache_disposal_preserves_runner_errors_and_exceptional_command_timings(self):
        cleanup = tempfile.TemporaryDirectory.cleanup
        errors = [None, KeyboardInterrupt(), SystemExit(143), uf.FlowError('required failure'),
                  subprocess.TimeoutExpired(['fixture'], 1)]
        for prefix, original in ((kind, error) for kind in ('python-import-cache-', 'go-build-cache-')
                                 for error in errors):
            disposed = []
            def fail(command, **options):
                if original is not None:
                    raise original
                return subprocess.CompletedProcess(command, 0)
            def fail_cleanup(temporary):
                cleanup(temporary)
                if Path(temporary.name).name.startswith(prefix):
                    disposed.append(temporary.name)
                    raise OSError('secondary cache disposal failure')
            with self.subTest(cache=prefix, error=type(original).__name__), \
                    patch.object(tempfile.TemporaryDirectory, 'cleanup', fail_cleanup):
                with self.assertRaises(type(original) if original is not None else OSError) as caught:
                    self.run_owned(fail)
            if original is not None:
                self.assertIs(caught.exception, original)
            self.assertTrue(disposed)
            self.assertTrue(all(not Path(path).exists() for path in disposed))
            self.assertTrue(all(not path.exists() or (original is None and path.read_bytes() == b'')
                                for path in self.receipts))
            summary_path = self.directory / 'summary.json'
            if original is None:
                self.assertTrue(not summary_path.exists() or summary_path.read_bytes() == b'')
                continue
            summary = json.loads(summary_path.read_text())
            self.assertFalse(summary['success'])
            self.assertEqual(summary['commands'], len(summary['phases']))
            row, = [row for row in summary['phases'] if row['phase'] == 'correctness']
            self.assertIsNone(row['exit_code'])
            self.assertGreaterEqual(row['seconds'], 0)

    def test_npm_shebang_interpreter_content_changes_reject_results(self):
        interpreter = self.directory / 'env'
        interpreter.write_text('#!/bin/sh\nexec /usr/bin/env "$@"\n')
        interpreter.chmod(0o755)
        script = self.directory / 'npm-like-launcher'
        script.write_text('#!' + str(interpreter) + ' node\nconsole.log("native interpreter ran");\n')
        script.chmod(0o755)
        step = local.Step('frontend-tests', self.root, ('npm', '--version'))
        consumed = []
        def run(command, **options):
            result = uf.run_verification([str(script)], **options)
            consumed.append(result.returncode)
            interpreter.write_text(interpreter.read_text() + '# changed interpreter\n')
            return result
        with patch.object(local, 'NPM_INTERPRETER', str(interpreter)), \
                patch.object(local, 'helper_inputs', return_value=({self.python_runtime}, set())):
            session = local.Session(self.root, self.base, [step], runner=run)
            try:
                session.freeze()
                with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                    session.execute(step)
                self.assertEqual(consumed, [0])
                self.assertEqual(session.completed, {})
            finally:
                with contextlib.suppress(uf.FlowError):
                    session.close()

    def test_npm_fixture_python_content_changes_reject_results(self):
        early = self.directory / 'npm-fixture-helpers'
        early.mkdir()
        python = early / 'python3'
        python.write_text('#!/bin/sh\nexec ' + shlex.quote(shutil.which('python3')) + ' "$@"\n')
        python.chmod(0o755)
        step = local.Step('frontend-tests', self.root, ('npm', 'test'))
        consumed = []
        def run(command, **options):
            result = uf.run_verification(['python3', '-c', 'print("owned npm fixture ran")'], **options)
            consumed.append(result.returncode)
            python.write_text(python.read_text() + '# changed fixture interpreter\n')
            return result
        with patch.dict(os.environ, {'PATH': str(early) + ':' + os.environ['PATH']}), \
                patch.object(local, 'helper_inputs', return_value=({self.python_runtime}, set())):
            session = local.Session(self.root, self.base, [step], runner=run)
            try:
                session.freeze()
                with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                    session.execute(step)
                self.assertEqual(consumed, [0])
                self.assertEqual(session.completed, {})
            finally:
                with contextlib.suppress(uf.FlowError):
                    session.close()

    def test_indirect_setpriv_shadow_rejects_successful_go_fixture_results(self):
        early = self.directory / 'earlier-path'
        early.mkdir()
        real = shutil.which('setpriv')
        self.assertIsNotNone(real)
        marker = self.directory / 'setpriv-shadow-ran'
        shadow = early / 'setpriv'
        step = local.Step('correctness', self.root, ('go', 'test', '-race', '-count=1', './...'))
        def run(command, **options):
            if transient[0]:
                shadow.write_text('#!/bin/sh\nprintf guarded > ' + shlex.quote(str(marker)) +
                                  '\nrm -- "$0"\nexec ' + shlex.quote(real) + ' "$@"\n')
                shadow.chmod(0o755)
            return uf.run_verification(['setpriv', '--no-new-privs', sys.executable,
                                        '-c', 'print("fixture completed")'], **options)
        with patch.dict(os.environ, {'PATH': str(early) + ':' + os.environ['PATH']}), \
                patch.object(local, 'helper_inputs', return_value=({self.python_runtime}, set())):
            transient = [False]
            session = local.Session(self.root, self.base, [step], runner=run)
            try:
                session.freeze()
                session.execute(step)
                self.assertEqual(session.completed[(step.cwd, step.command)]['exit_code'], 0)
            finally:
                session.close()
            transient[0] = True
            session = local.Session(self.root, self.base, [step], runner=run)
            try:
                session.freeze()
                with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                    session.execute(step)
                self.assertEqual(marker.read_text(), 'guarded')
                self.assertFalse(shadow.exists())
                self.assertEqual(session.completed, {})
            finally:
                with contextlib.suppress(uf.FlowError):
                    session.close()

    def test_executable_fifo_substitution_rejects_without_blocking(self):
        target = self.directory / 'launcher'
        target.write_text('#!/bin/sh\nexit 0\n')
        target.chmod(0o755)
        code = ('import os\nfrom pathlib import Path\nfrom unittest.mock import patch\n'
                'from specs_lib import local_verification as local\n'
                'target=Path(' + repr(str(target)) + ')\n'
                'session=local.Session.__new__(local.Session)\n'
                'session.executables=[str(target)]\n'
                'session.helper_directories=session.helper_files=set()\n'
                'session.git_directories=session.git_files=set()\n'
                'assert session.tool_identity()\n'
                'statting=Path.stat;lstating=Path.lstat;opening=os.open;opened=[];changed=False\n'
                'def substitute(path,operation,*args,**options):\n'
                ' global changed\n'
                ' value=operation(path,*args,**options)\n'
                ' if path==target and not changed:\n'
                '  changed=True;target.unlink();os.mkfifo(target);target.chmod(0o755)\n'
                ' return value\n'
                'def record(path,flags,*args,**options):\n'
                ' fd=opening(path,flags,*args,**options);opened.append(fd);return fd\n'
                'with patch.object(Path,"stat",lambda p,*a,**kw:substitute(p,statting,*a,**kw)),\\\n'
                ' patch.object(Path,"lstat",lambda p:substitute(p,lstating)),patch.object(local.os,"open",record):\n'
                ' try: session.tool_identity()\n'
                ' except local.uf.FlowError: pass\n'
                ' else: raise AssertionError("FIFO admitted")\n'
                'assert changed and opened\n'
                'for fd in opened:\n'
                ' try: os.fstat(fd)\n'
                ' except OSError: pass\n'
                ' else: raise AssertionError("descriptor leaked")\n')
        result = subprocess.run([sys.executable, '-B', '-c', code], capture_output=True, text=True,
                                env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, timeout=5)
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_npm_helper_inventory_admits_aliases_and_rejects_different_launchers(self):
        web = self.root / 'web'
        web.mkdir()
        ancestor = self.directory / 'node_modules/.bin'
        ancestor.mkdir(parents=True)
        marker = self.directory / 'npm-helper-selected'
        node, npm = shutil.which('node'), str(Path(shutil.which('npm')).resolve())
        for helper in ('python3', 'node', 'go'):
            with self.subTest(helper=helper):
                real = shutil.which(helper)
                selected = ancestor / helper
                selected.symlink_to(real)
                try:
                    with self.subTest(selection='canonical alias'):
                        self.assertIn(selected, local.npm_inputs(self.root))
                    selected.unlink()
                    selected.write_text('#!/bin/sh\nprintf selected > ' + shlex.quote(str(marker)) +
                                        '\nexec ' + shlex.quote(real) + ' "$@"\n')
                    selected.chmod(0o755)
                    command = helper + (' version' if helper == 'go' else ' --version')
                    (web / 'package.json').write_text(json.dumps({'scripts': {'test': command}}))
                    actual = subprocess.run([node, npm, 'test'], cwd=web, capture_output=True,
                                            text=True, timeout=15)
                    self.assertEqual(actual.returncode, 0, actual.stdout + actual.stderr)
                    self.assertEqual(marker.read_text(), 'selected')
                    with self.subTest(selection='different launcher'), \
                            self.assertRaisesRegex(uf.FlowError, 'Npm helper selection differs'):
                        local.npm_inputs(self.root)
                finally:
                    selected.unlink(missing_ok=True)
                    marker.unlink(missing_ok=True)

    def test_npm_python_alias_virtualenv_selection_and_config_mutations_reject(self):
        web = self.root / 'web'
        web.mkdir()
        ancestor = self.directory / 'node_modules/.bin'
        ancestor.mkdir(parents=True)
        alias = ancestor / 'python3'
        alias.symlink_to(shutil.which('python3'))
        config = ancestor.parent / 'pyvenv.cfg'
        marker = self.directory / 'selected-prefix'
        code = 'from pathlib import Path;import sys;Path(' + repr(str(marker)) + ').write_text(sys.prefix)'
        (web / 'package.json').write_text(json.dumps({'scripts': {'test': 'python3 -c ' + shlex.quote(code)}}))
        configuration = 'home = ' + str(Path(shutil.which('python3')).parent) + '\ninclude-system-site-packages = false\n'
        with patch.dict(os.environ, {'PYTHONPATH': str(ROOT / 'scripts')}), \
                patch.object(local, 'python_inputs', self.discover_python):
            config.write_text(configuration)
            try:
                with self.assertRaisesRegex(uf.FlowError, 'Npm Python import selection differs'):
                    local.npm_inputs(self.root)
            finally:
                config.unlink()
            files = local.npm_inputs(self.root)
        step = local.Step('frontend-tests', web, ('npm', 'test'))
        def run(command, **options):
            config.write_text(configuration)
            try:
                result = uf.run_verification(command, **options)
                self.assertEqual(result.returncode, 0)
                self.assertEqual(Path(marker.read_text()), ancestor.parent)
                return result
            finally:
                config.unlink()
        with patch.object(local, 'helper_inputs', return_value=({self.python_runtime}, files)):
            session = local.Session(self.root, self.base, [step], runner=run)
            try:
                session.freeze()
                with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                    session.execute(step)
                self.assertEqual(session.completed, {})
            finally:
                with contextlib.suppress(uf.FlowError):
                    session.close()

    def test_indirect_npm_helpers_inherited_and_ancestor_shadows_reject_zero_results(self):
        web = self.root / 'web'
        web.mkdir()
        shutil.copyfile(ROOT / 'scripts/verification_run.py', self.root / 'scripts/verification_run.py')
        shutil.copytree(ROOT / 'scripts/specs_lib', self.root / 'scripts/specs_lib',
                        ignore=shutil.ignore_patterns('__pycache__'))
        for name in ('user_flows.py', 'specs.py', 'verification_timing.py'):
            shutil.copyfile(ROOT / 'scripts' / name, self.root / 'scripts' / name)
        scripts = json.loads((ROOT / 'web/package.json').read_text())['scripts']
        (web / 'package.json').write_text(json.dumps({'scripts': {key: scripts[key] for key in ('test', 'test:performance')}}))
        # Keep the maintained Python ownership entrypoint and Node/Go lookup chain,
        # substituting only the browser workload with a dependency-free Go query.
        (web / 'fixture-runner.mjs').write_text('''import { spawn } from "node:child_process";
const child = spawn("go", ["version"], { stdio: "inherit" });
child.on("error", (error) => { console.error(error); process.exitCode = 1; });
child.on("exit", (code, signal) => { process.exitCode = code ?? 143; });
''')
        node, npm = shutil.which('node'), str(Path(shutil.which('npm')).resolve())
        def actual_npm(command, **options):
            # Bypass npm's env-node shebang to observe its child Node lookup itself.
            return uf.run_verification([node, npm, *command[1:]], **options)
        early = self.directory / 'earlier-path'
        ancestor = self.directory / 'node_modules/.bin'
        early.mkdir()
        ancestor.mkdir(parents=True)
        checkout_bins = (self.root / 'node_modules/.bin', web / 'node_modules/.bin')
        for directory in checkout_bins:
            directory.mkdir(parents=True)
        marker = self.directory / 'indirect-npm-helper-ran'
        for helper in ('python3', 'node', 'go'):
            for location in (early, ancestor, *checkout_bins):
                for command in (('npm', 'test'), ('npm', 'run', 'test:performance')):
                    shadow = location / helper
                    def run(command, **options):
                        shadow.write_text('#!/bin/sh\nprintf guarded > ' + shlex.quote(str(marker)) +
                                          '\nrm -- "$0"\nexit 0\n')
                        shadow.chmod(0o755)
                        result = actual_npm(command, **options)
                        self.assertEqual(result.returncode, 0)
                        self.assertFalse(shadow.exists())
                        return result
                    with self.subTest(helper=helper, location=str(location), command=command), \
                            patch.dict(os.environ, {'PATH': str(early) + ':' + os.environ['PATH'],
                                                  'PYTHONPATH': str(ROOT / 'scripts')}), \
                            patch.object(local, 'helper_inputs', return_value=({self.python_runtime}, set())):
                        step = local.Step('performance', web, command)
                        session = local.Session(self.root, self.base, [step], runner=run)
                        try:
                            session.freeze()
                            with self.assertRaisesRegex(uf.FlowError, 'inputs changed'):
                                session.execute(step)
                            self.assertEqual(marker.read_text(), 'guarded')
                            self.assertEqual(session.completed, {})
                        finally:
                            with contextlib.suppress(uf.FlowError):
                                session.close()
                    marker.unlink(missing_ok=True)
        shadow = early / 'python3'
        shadow.write_text('#!/bin/sh\nprintf merged > ' + shlex.quote(str(marker)) + '\nexit 0\n')
        shadow.chmod(0o755)
        for carrier in ('Path', 'path', 'pAtH'):
            with self.subTest(carrier=carrier), patch.dict(os.environ):
                canonical = os.environ.pop('PATH')
                for name in list(os.environ):
                    if name.lower() == 'path':
                        del os.environ[name]
                os.environ[carrier] = str(early)
                os.environ['PATH'] = canonical
                os.environ['PYTHONPATH'] = str(ROOT / 'scripts')
                subprocess.run([node, npm, 'test'], cwd=web, check=True, capture_output=True, timeout=15)
                self.assertEqual(marker.read_text(), 'merged')
                marker.unlink()
                with patch.object(local, 'managed', side_effect=AssertionError('ownership must not start')), \
                        patch.object(local, 'helper_inputs', side_effect=AssertionError('discovery must not start')):
                    with self.assertRaisesRegex(uf.FlowError, 'Noncanonical npm PATH'):
                        self.run_owned(lambda *args, **options: self.fail('required work must not start'))
                self.assertTrue(all(not path.exists() for path in self.receipts))
        shadow.unlink()
        (web / 'package.json').write_text(json.dumps({'name': 'fixture', 'version': '1.0.0',
                                                    'scripts': {key: scripts[key] for key in ('test', 'test:performance')}}))
        (web / 'package-lock.json').write_text(json.dumps({'name': 'fixture', 'version': '1.0.0',
            'lockfileVersion': 3, 'packages': {'': {'name': 'fixture', 'version': '1.0.0'}}}))
        (self.root / '.git/info/exclude').write_text('bin/\nweb/node_modules/\n')
        internal = web / 'node_modules/.bin'
        internal.mkdir(parents=True, exist_ok=True)
        (internal / 'obsolete').write_text('old generated entry\n')
        for helper in ('python3', 'node', 'go'):
            (ancestor / helper).symlink_to(shutil.which(helper))
        files = local.npm_inputs(self.root)
        self.assertTrue({ancestor / helper for helper in ('python3', 'node', 'go')} <= files)
        step = local.Step('correctness', web, ('npm', 'test'))
        prepare = local.Step('npm-install', web, ('npm', 'ci'))
        with patch.dict(os.environ, {'Path': '', 'path': '', 'pAtH': ''}):
            self.assertEqual(local.executable_search_inputs(), {Path(name) for name in os.environ['PATH'].split(':')})
            with patch.object(local, 'helper_inputs', return_value=({self.python_runtime}, files)):
                session = local.Session(self.root, self.base, [prepare, step])
            try:
                session.execute(prepare)
                self.assertFalse((internal / 'obsolete').exists())
                session.freeze()
                session.execute(step)
                session.execute(step)
                self.assertEqual(len(session.rows), 2)
            finally:
                session.close()

    def test_artifact_symlink_chains_reject_every_excluded_target_and_keep_internal_aliases(self):
        package = self.root / 'bin/package'
        package.mkdir(parents=True)
        value = package / 'value.js'
        value.write_text('module.exports = 7;\n')
        entry = package / 'entry.js'
        entry.symlink_to(value.name)
        nested = package / 'deep/value.js'
        nested.parent.mkdir()
        nested.write_text('module.exports = 11;\n')
        (package / 'a-first.js').symlink_to('deep/value.js')
        (package / 'b-second.js').symlink_to(value.name)
        with patch.object(local, 'digest_rows', wraps=local.digest_rows) as digest:
            normal = local.artifact_identity(self.root)
        rows = {row[0]: row for row in digest.call_args.args[0]}
        import hashlib
        for path in (package / 'a-first.js', package / 'b-second.js', entry, nested, value):
            name = path.relative_to(self.root).as_posix()
            content = os.readlink(path) if path.is_symlink() else hashlib.sha256(path.read_bytes()).digest()
            self.assertEqual(rows[name], (name, path.lstat().st_mode, content))
        session = local.Session(self.root, self.base, [self.step])
        try:
            session.freeze()
            self.assertEqual(session.prepared, normal)
            session.assert_current()
        finally:
            session.close()
        private = self.root / '.owned-private'
        omitted = [package / '.git', self.root / 'verification-results',
                   self.root / 'web/node_modules/.vite-temp', private]
        with patch.object(local, 'owned_runtime', return_value=private.relative_to(self.root).as_posix()):
            for directory in omitted:
                with self.subTest(directory=str(directory)):
                    directory.mkdir(parents=True, exist_ok=True)
                    hidden = directory / 'value.js'
                    hidden.write_text('module.exports = 19;\n')
                    entry.unlink()
                    entry.symlink_to(hidden)
                    self.assertEqual(subprocess.check_output(['node', '-p', 'require(' + json.dumps(str(entry)) + ')'],
                                     text=True, timeout=5).strip(), '19')
                    with self.assertRaisesRegex(uf.FlowError, 'Excluded input symlink'):
                        local.artifact_identity(self.root)
                    redirect = directory / 'redirect'
                    redirect.symlink_to(value)
                    entry.unlink()
                    entry.symlink_to(redirect)
                    with self.assertRaisesRegex(uf.FlowError, 'Excluded input symlink'):
                        local.artifact_identity(self.root)
                    redirect.unlink()
            entry.unlink()
            entry.symlink_to(value)
            before = local.artifact_identity(self.root)
            value.write_text('module.exports = 8;\n')
            self.assertNotEqual(local.artifact_identity(self.root), before)

    def test_artifact_typed_open_rejects_fifo_replacement_and_preserves_cleanup_errors(self):
        target = self.root / 'bin/artifact'
        target.parent.mkdir()
        target.write_bytes(b'fixed artifact\n')
        code = ('import os\nfrom pathlib import Path\nfrom unittest.mock import patch\n'
                'from specs_lib import local_verification as local\n'
                'root=Path(' + repr(str(self.root)) + ');target=Path(' + repr(str(target)) + ')\n'
                'opening=os.open\nopened=[]\n'
                'def substitute(path,flags,*args,**options):\n'
                ' if Path(path)==target:\n'
                '  target.unlink();os.mkfifo(target)\n'
                ' fd=opening(path,flags,*args,**options);opened.append(fd);return fd\n'
                'with patch.object(local.os,"open",side_effect=substitute):\n'
                ' try: local.artifact_identity(root)\n'
                ' except local.uf.FlowError: pass\n'
                ' else: raise AssertionError("FIFO admitted")\n'
                'assert opened\n'
                'for fd in opened:\n'
                ' try: os.fstat(fd)\n'
                ' except OSError: pass\n'
                ' else: raise AssertionError("descriptor leaked")\n')
        subprocess.run([sys.executable, '-B', '-c', code], check=True, capture_output=True,
                       env={**os.environ, 'PYTHONPATH': str(ROOT / 'scripts')}, timeout=5)
        target.unlink()
        target.write_bytes(b'fixed artifact\n')
        expected = target.lstat()
        import hashlib
        self.assertEqual(local.artifact_digest(target, expected), hashlib.sha256(target.read_bytes()).digest())
        opening, closing = os.open, os.close
        opened = []
        def record(path, flags):
            descriptor = opening(path, flags)
            self.assertTrue(flags & os.O_NONBLOCK and flags & os.O_NOFOLLOW and flags & os.O_CLOEXEC)
            opened.append(descriptor)
            return descriptor
        def close_error(descriptor):
            closing(descriptor)
            raise OSError('secondary artifact close failure')
        for original in (OSError('original artifact read'), KeyboardInterrupt()):
            with self.subTest(original=type(original).__name__), patch.object(local.os, 'open', side_effect=record), \
                    patch.object(local.os, 'close', side_effect=close_error), \
                    patch.object(local.os, 'fdopen', side_effect=original):
                with self.assertRaises(type(original)) as caught:
                    local.artifact_digest(target, expected)
                self.assertIs(caught.exception, original)
            with self.assertRaises(OSError):
                os.fstat(opened[-1])
        with patch.object(local.os, 'open', side_effect=record), patch.object(local.os, 'close', side_effect=close_error):
            with self.assertRaisesRegex(OSError, 'secondary artifact close failure'):
                local.artifact_digest(target, expected)
        with self.assertRaises(OSError):
            os.fstat(opened[-1])
        replacement = target.with_name('replacement')
        replacement.write_bytes(target.read_bytes())
        def replace(path, flags):
            replacement.replace(target)
            return record(path, flags)
        with patch.object(local.os, 'open', side_effect=replace):
            with self.assertRaisesRegex(uf.FlowError, 'changed while opening'):
                local.artifact_digest(target, expected)
        with self.assertRaises(OSError):
            os.fstat(opened[-1])
        for operation in ('stat', 'hash'):
            original = ValueError('original artifact ' + operation)
            patched = patch.object(local.os, 'fstat', side_effect=original) if operation == 'stat' else \
                patch.object(local.hashlib, 'sha256', side_effect=original)
            with self.subTest(operation=operation), patch.object(local.os, 'open', side_effect=record), \
                    patch.object(local.os, 'close', side_effect=close_error), patched:
                with self.assertRaises(ValueError) as caught:
                    local.artifact_digest(target, target.lstat())
                self.assertIs(caught.exception, original)
            with self.assertRaises(OSError):
                os.fstat(opened[-1])
        hashing = hashlib.file_digest
        payload = target.read_bytes()
        before = target.lstat()
        def restore_during_hash(*args):
            result = hashing(*args)
            target.write_bytes(b'changed artifact\n')
            target.write_bytes(payload)
            os.utime(target, ns=(before.st_atime_ns, before.st_mtime_ns))
            return result
        with patch.object(local.os, 'open', side_effect=record), \
                patch.object(local.hashlib, 'file_digest', side_effect=restore_during_hash):
            with self.assertRaisesRegex(uf.FlowError, 'changed during traversal'):
                local.artifact_digest(target, before)
        with self.assertRaises(OSError):
            os.fstat(opened[-1])
        cancellation = local.InvocationCancellation()
        previous = signal.getsignal(signal.SIGINT)
        hashing = hashlib.file_digest
        def interrupt(*args):
            result = hashing(*args)
            os.kill(os.getpid(), signal.SIGINT)
            return result
        with patch.object(local.os, 'open', side_effect=record), patch.object(local.hashlib, 'file_digest', side_effect=interrupt):
            with self.assertRaises(KeyboardInterrupt):
                with local.invocation_signals(cancellation):
                    local.artifact_digest(target, target.lstat())
        self.assertEqual(cancellation.number, signal.SIGINT)
        self.assertEqual(signal.getsignal(signal.SIGINT), previous)
        with self.assertRaises(OSError):
            os.fstat(opened[-1])


if __name__ == '__main__':
    unittest.main()
