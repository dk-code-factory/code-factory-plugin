"""Exercise verification ownership with real children, locks and Git discovery."""
import base64
import json
import os
from pathlib import Path
import select
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import Mock, patch

from specs_lib.isolation import isolated
from test_specs_timing import cleanup_process


def supervise_probe(root, timeout, command, drain_timeout=0):
    """Own only this short-lived supervisor's descendants, across probe death."""
    import ctypes
    if ctypes.CDLL(None, use_errno=True).prctl(36, 1, 0, 0, 0):
        raise OSError(ctypes.get_errno(), 'test supervisor subreaper setup failed')
    def interrupt(signum, frame):
        raise SystemExit(128 + signum)
    for signum in (signal.SIGINT, signal.SIGTERM):
        signal.signal(signum, interrupt)
    outcome, status, process = 'failed', 1, None
    try:
        process = subprocess.Popen(command)
        status = process.wait(timeout=timeout)
        outcome = 'completed'
    except subprocess.TimeoutExpired:
        outcome, status = 'timeout', 124
    finally:
        # A second cancellation cannot interrupt exact-child cleanup. Each PID
        # stays our unreaped child until its final signal; newly orphaned detached
        # descendants are adopted here rather than by the unittest process.
        for signum in (signal.SIGINT, signal.SIGTERM):
            signal.signal(signum, signal.SIG_IGN)
        if drain_timeout:
            # A checker runs Make directly. Stop that exact unreaped child on
            # timeout, then let its detached guardian finish admitted cleanup.
            if process is not None and process.returncode is None:
                process.kill()
                process.wait(timeout=3)
            drain_deadline = time.monotonic() + drain_timeout
            children = Path(f'/proc/{os.getpid()}/task/{os.getpid()}/children')
            while children.read_text().strip() and time.monotonic() < drain_deadline:
                for pid in map(int, children.read_text().split()):
                    os.waitpid(pid, os.WNOHANG)
                time.sleep(.001)
        deadline = time.monotonic() + 3
        children = Path(f'/proc/{os.getpid()}/task/{os.getpid()}/children')
        while children.read_text().strip():
            if time.monotonic() >= deadline:
                raise RuntimeError('supervisor could not reap exact owned children: ' + children.read_text())
            for pid in map(int, children.read_text().split()):
                os.kill(pid, signal.SIGKILL)
                while os.waitpid(pid, os.WNOHANG) == (0, 0):
                    if time.monotonic() >= deadline:
                        raise RuntimeError(f'supervisor could not reap owned child {pid}')
                    time.sleep(.001)
        (root / 'probe-drained.json').write_text(json.dumps({
            'supervisor': os.getpid(), 'probe': process.pid if process else None,
            'outcome': outcome, 'returncode': status, 'children': []}))
    return status if status >= 0 else 128 - status


CANCELLATION_PROBE = r'''
import ctypes,json,os,pathlib,signal,subprocess,sys,time,unittest
from specs_lib.isolation import isolated
check=unittest.TestCase()
root=pathlib.Path(sys.argv[1]); mode=sys.argv[2]
# Reserve five seconds for the final ownership check within the 45-second probe.
probe_deadline=time.monotonic()+40
make_arguments=json.loads(sys.argv[3]) if len(sys.argv)>3 else ['make','-j2','normal-error']
normal_serial=(root/'normal-serial').exists()
if ctypes.CDLL(None,use_errno=True).prctl(36,1,0,0,0):
    raise OSError(ctypes.get_errno(),'test subreaper setup failed')
commands={
    'reconcile': [sys.executable,'-c',
        'from pathlib import Path\nfrom specs_lib.isolation import isolated\n'
        'from specs_lib.processes import run_verification\n'
        'with isolated(Path.cwd()):\n'
        ' run_verification(["npm","--prefix","web","test"],cwd=Path.cwd())'],
    'make': ['make','--no-print-directory','verify'],
    'make-death': ['make','--no-print-directory','verify'],
    'make-direct-helper': ['make','--no-print-directory','direct-helper'],
    'make-complete': ['make','--no-print-directory','background-helper'],
    'make-ready-batch': ['make','--no-print-directory','background-helper'],
    'make-listener-first': ['make','--no-print-directory','background-helper'],
    'make-pending-handshake': ['make','--no-print-directory','background-helper'],
    'make-admitted-handshake': ['make','--no-print-directory','background-helper'],
    'make-imported': ['make','--no-print-directory','imported-helper'],
    'make-sharing-cleanup': ['make','--no-print-directory','imported-helper'],
    'outer-ignore': [sys.executable,'scripts/verification_run.py','--','env','CODE_FACTORY_TIMING_WORKER_CONTEXT=1',sys.executable,'scripts/verification_timing.py','verify','--','sh','-c','cd web && exec npx probe'],
    'hosted': [sys.executable,'scripts/ci.py','verify','--event','event.json'],
    'hosted-make-death': [sys.executable,'scripts/ci.py','verify','--event','event.json'],
    'make-normal-nonzero': make_arguments,
    'make-normal-fatal': make_arguments,
}
with (root/'probe.stdout').open('w') as out,(root/'probe.stderr').open('w') as err:
    process=subprocess.Popen(commands[mode],cwd=root,stdout=out,stderr=err,start_new_session=True)
    try:
        deadline=time.monotonic()+20
        while not (root/'pids.json').exists():
            if process.poll() is not None:
                check.assertTrue((root/'pids.json').exists(),(root/'probe.stderr').read_text())
            check.assertLess(time.monotonic(),deadline,'workload did not become ready')
            time.sleep(.01)
        state=json.loads((root/'pids.json').read_text())
        if mode in ('make','make-death'):
            check.assertGreater(state['lease_sockets'],0,'authenticated exec lost its managed lease')
        # Observe the guardian's 30-second drain plus bounded polling/finalization
        # margin from here. Worker PID death can precede the outer helper's lease
        # exit, so it must not start a new five-second ownership-release budget.
        drain_deadline=min(time.monotonic()+30+5,probe_deadline)
        if mode.startswith('make-normal-'):
            from specs_lib.isolation import process_identity
            runner,identity=state['group_owner'].split(':')
            check.assertEqual(process_identity(int(runner)),identity)
            check.assertEqual(os.getpgid(int(runner)),process.pid)
            check.assertNotEqual(state['group'],process.pid)
            check.assertEqual(os.getsid(state['pids'][0]),state['group'])
            owner=json.loads((pathlib.Path(state['tmpdir']).parent/'owner.json').read_text())
            check.assertEqual(owner['make_pid'],process.pid)
            grants=pathlib.Path(state['tmpdir']).parent/'groups'/f'{runner}-{identity}'
            check.assertTrue(any(json.loads(path.read_text())==owner for path in grants.glob('grant-*.json')))
            if normal_serial:
                check.assertFalse((root/'normal-error-observed').exists())
            else:
                deadline=time.monotonic()+5
                while not (root/'normal-error-observed').exists():
                    check.assertLess(time.monotonic(),deadline,'sibling did not trigger the Make failure')
                    time.sleep(.001)
            check.assertIsNone(process.poll(),'Make exited before its foreground timed worker')
            (root/'release-worker').touch()
            deadline=time.monotonic()+5
            while not (root/'normal-finalization-started').exists():
                check.assertLess(time.monotonic(),deadline,'foreground did not reach finalization')
                time.sleep(.001)
            check.assertIsNone(process.poll(),'Make exited during foreground timing finalization')
            if normal_serial:
                check.assertFalse((root/'normal-error-observed').exists())
            (root/'release-finalization').touch()
        elif mode=='hosted-make-death':
            import fcntl
            from specs_lib.isolation import checkout_lock_path,process_identity
            owner=json.loads((pathlib.Path(state['tmpdir']).parent/'owner.json').read_text())
            check.assertEqual(owner['pid'],process.pid)
            check.assertNotIn('make_pid',owner)
            children=pathlib.Path(f'/proc/{process.pid}/task/{process.pid}/children')
            make_pid,=map(int,children.read_text().split())
            check.assertEqual(os.getpgid(make_pid),make_pid)
            check.assertEqual(os.getsid(make_pid),make_pid)
            check.assertNotEqual(state['group'],make_pid)
            check.assertEqual(os.getsid(state['pids'][0]),state['group'])
            runner,identity=state['group_owner'].split(':')
            check.assertEqual(process_identity(int(runner)),identity)
            check.assertEqual(os.getpgid(int(runner)),make_pid)
            check.assertIsNone(process.poll())
            os.kill(make_pid,signal.SIGKILL)
            # Observe while the ten-second inner cleanup is still required. The
            # finite delay distinguishes it from the old 100 ms enclosing KILL.
            time.sleep(.3)
            with checkout_lock_path(root).open('a+') as lock:
                try:
                    fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
                    held=False
                except BlockingIOError:
                    held=True
            fresh={key:value for key,value in os.environ.items()
                   if not key.startswith('CODE_FACTORY_VERIFY_')}
            contender=subprocess.run([sys.executable,'-c',
                "from pathlib import Path;from specs_lib.isolation import isolated; "
                "context=isolated(Path.cwd());context.__enter__();print('fresh owner admitted')"],
                cwd=root,env=fresh,capture_output=True,text=True,timeout=3)
            journal,=(root/'verification-results').glob('*.json')
            observation={
                'ci_alive':process.poll() is None,'lock_held':held,
                'temporary_exists':pathlib.Path(state['tmpdir']).is_dir(),
                'worker_running':all(pathlib.Path(f'/proc/{pid}/stat').read_text().rsplit(')',1)[1].split()[0]
                                     not in ('Z','X') for pid in state['pids']),
                'contender_rejected':contender.returncode!=0,
                'journal_outcome':json.loads(journal.read_text())['outcome'],
                'make_pid':make_pid,'worker_group':state['group']}
            print('hosted Make-only death observation: '+json.dumps(observation),flush=True)
            check.assertTrue(observation['ci_alive'],observation)
            check.assertTrue(observation['lock_held'],observation)
            check.assertTrue(observation['temporary_exists'],observation)
            check.assertTrue(observation['worker_running'],observation)
            check.assertTrue(observation['contender_rejected'],observation)
            check.assertEqual(observation['journal_outcome'],'running',observation)
        elif mode=='make':
            os.killpg(process.pid,signal.SIGTERM)
        elif mode in ('make-death','make-imported','make-sharing-cleanup'):
            process.kill()
        elif mode in ('make-complete','make-ready-batch','make-listener-first','make-pending-handshake','make-admitted-handshake'):
            pass
        else:
            process.send_signal(signal.SIGTERM)
        status=process.wait(timeout=20)
        if mode.startswith('make-normal-'):
            check.assertTrue((root/'normal-error-observed').exists())
        if mode in ('make-complete','make-ready-batch','make-listener-first','make-pending-handshake','make-admitted-handshake'):
            check.assertEqual(status,0)
        else:
            check.assertNotEqual(status,0)
        deadline=time.monotonic()+(20 if mode in (
            'make-direct-helper','make-complete','make-death','make-ready-batch',
            'make-listener-first','make-pending-handshake','make-admitted-handshake',
            'make-sharing-cleanup') else 3)
        for pid in state['pids']:
            while True:
                try:
                    waited=os.waitpid(pid,os.WNOHANG)
                except ChildProcessError:
                    if not pathlib.Path(f'/proc/{pid}').exists():
                        break
                else:
                    if waited[0]:
                        break
                check.assertLess(time.monotonic(),deadline,f'owned process {pid} survived')
                time.sleep(.01)
            check.assertFalse(pathlib.Path(f'/proc/{pid}').exists())
        deadline=drain_deadline
        while pathlib.Path(state['tmpdir']).parent.exists():
            check.assertLess(time.monotonic(),deadline,'Make guardian did not drain private state')
            time.sleep(.01)
        if mode in ('make-death','make-ready-batch','make-listener-first','make-pending-handshake','make-admitted-handshake','hosted-make-death'):
            observation=json.loads((root/'drain-observation.json').read_text())
            check.assertTrue(observation['temporary_exists'])
            check.assertTrue(observation['lock_held'])
            check.assertTrue(observation['directory_fd_live'])
        if mode in ('make-ready-batch','make-listener-first','make-pending-handshake','make-admitted-handshake'):
            check.assertTrue((root/('handshake-started' if mode=='make-pending-handshake' else
                                   'admission-committed' if mode=='make-admitted-handshake' else 'ready-batch')).exists())
            if mode=='make-admitted-handshake':
                check.assertTrue((root/'second-work').exists())
            else:
                check.assertFalse((root/'second-work').exists())
        if mode=='make-sharing-cleanup':
            observation=json.loads((root/'sharing-cleanup.json').read_text())
            check.assertTrue(observation['shared'])
            check.assertTrue(observation['cleanup_completed'],observation)
            check.assertTrue(observation['new_rejected'])
            check.assertTrue(observation['directory_fd_live'])
        deadline=time.monotonic()+5
        while True:
            try:
                with isolated(root):
                    break
            except RuntimeError as error:
                if 'Verification already owns this checkout' not in str(error):
                    raise
                check.assertLess(time.monotonic(),deadline,'guardian did not release checkout lock')
                time.sleep(.001)
        if mode not in ('reconcile','make-direct-helper','make-complete','make-imported','make-sharing-cleanup'):
            journal,=(root/'verification-results').glob('*.json')
            data=json.loads(journal.read_text())
            check.assertEqual(data['outcome'],'incomplete' if mode.startswith('make-normal-') else 'interrupted')
            if mode.startswith('make-normal-'):
                check.assertTrue((root/'normal-finalization-finished').exists())
            check.assertIsNotNone(data['wall_seconds'])
            check.assertFalse(any(p['outcome']=='running' for p in data['phases']))
        print('owned groups reaped; temporary state removed; evidence finalized',flush=True)
    finally:
        # Only this short-lived probe adopts descendants. Kill/reap direct children
        # before reading again, so numeric PIDs cannot be reused under this owner.
        if process.poll() is None:
            process.kill()
        process.wait(timeout=3)
        deadline=time.monotonic()+3
        children=pathlib.Path(f'/proc/{os.getpid()}/task/{os.getpid()}/children')
        while children.read_text().strip():
            check.assertLess(time.monotonic(),deadline,'probe cleanup could not reap children')
            for pid in map(int,children.read_text().split()):
                os.kill(pid,signal.SIGKILL)
                while os.waitpid(pid,os.WNOHANG)==(0,0):
                    check.assertLess(time.monotonic(),deadline,'child did not exit after KILL')
                    time.sleep(.01)
'''


class ProbeSupervision:
    def cleanup_directory(self):
        if self.probe_drained:
            self.directory.cleanup()
        else:
            self.directory._finalizer.detach()
            raise RuntimeError(f'unresolved probe ownership; retained fixture {self.root}')

    def supervised_probe(self, command, *, env=None, timeout=45, cwd=None,
                         input=None, drain_timeout=0):
        receipt = self.root / 'probe-drained.json'
        receipt.unlink(missing_ok=True)
        self.probe_drained = False
        cancellation, interrupted, process = None, None, None
        handlers = {s: signal.getsignal(s) for s in (signal.SIGINT, signal.SIGTERM)}
        def interrupt(signum, frame):
            nonlocal interrupted
            interrupted = signum
            if process is not None and process.poll() is None:
                process.terminate()
        try:
            for signum in handlers:
                signal.signal(signum, interrupt)
            process = subprocess.Popen([
                sys.executable, str(Path(__file__).resolve()),
                '--supervise-checker' if drain_timeout else '--supervise-probe',
                str(self.root), str(timeout), *command], env=env,
                cwd=cwd, stdin=subprocess.PIPE if input is not None else None,
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            if interrupted is not None and process.poll() is None:
                process.terminate()
            try:
                stdout, stderr = process.communicate(
                    input=input, timeout=timeout + drain_timeout + (8 if drain_timeout else 4))
            except BaseException as error:
                cancellation = error
                for signum in handlers:
                    signal.signal(signum, signal.SIG_IGN)
                if process.poll() is None:
                    process.terminate()
                # Never kill the cleanup owner at the last bound. Missing drain
                # acknowledgement retains its fixture and reports its exact PID.
                try:
                    stdout, stderr = process.communicate(timeout=drain_timeout + (8 if drain_timeout else 4))
                except subprocess.TimeoutExpired as error:
                    raise RuntimeError(f'probe supervisor {process.pid} did not acknowledge cleanup; '
                                       f'retained fixture {self.root}') from error
            if not receipt.exists():
                raise RuntimeError(f'probe supervisor {process.pid} exited without drain acknowledgement; '
                                   f'retained fixture {self.root}: {stderr}')
            evidence = json.loads(receipt.read_text())
            if evidence['supervisor'] != process.pid or evidence['children'] != []:
                raise RuntimeError(f'invalid probe drain acknowledgement for {process.pid}')
            self.probe_drained = True
            if interrupted == signal.SIGINT:
                raise KeyboardInterrupt
            if interrupted is not None:
                raise SystemExit(128 + interrupted)
            if cancellation is not None:
                raise cancellation
            if evidence['outcome'] == 'timeout':
                raise subprocess.TimeoutExpired(command, timeout, output=stdout, stderr=stderr)
            return subprocess.CompletedProcess(command, process.returncode, stdout, stderr)
        finally:
            for signum, handler in handlers.items():
                signal.signal(signum, handler)


class IsolationTests(ProbeSupervision, unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.probe_drained = True
        self.addCleanup(self.cleanup_directory)
        self.root = Path(self.directory.name) / "checkout"
        self.root.mkdir()
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        self.env = dict(os.environ)
        for key in list(self.env):
            if key.startswith("CODE_FACTORY_VERIFY_"):
                self.env.pop(key)
        self.env["PYTHONPATH"] = str(Path(__file__).resolve().parent)

    def probe(self, root, body, *, env=None):
        script = "from specs_lib.isolation import isolated\nfrom pathlib import Path\nimport os,json,sys,time\nwith isolated(Path(sys.argv[1])):\n" + "\n".join("    " + line for line in body.splitlines())
        child_env = dict(env or self.env, PYTHONPATH=str(Path(__file__).resolve().parent))
        child = subprocess.Popen([sys.executable, "-c", script, str(root)], env=child_env,
                                 stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                 stderr=subprocess.PIPE, text=True)
        self.addCleanup(cleanup_process, child)
        return child

    def ready_line(self, child, timeout=5):
        # select followed by readline can still block on partial output.
        deadline = time.monotonic() + timeout
        data = b""
        while b"\n" not in data:
            remaining = deadline - time.monotonic()
            self.assertGreater(remaining, 0, 'child readiness timed out')
            self.assertTrue(select.select([child.stdout], [], [], remaining)[0],
                            'child readiness timed out')
            chunk = os.read(child.stdout.fileno(), 4096)
            self.assertTrue(chunk, 'child exited before readiness')
            data += chunk
        return data.split(b"\n", 1)[0].decode()

    def test_nested_ownership_reuses_private_state_and_restores_environment(self):
        previous = dict(os.environ)
        with isolated(self.root):
            temporary = Path(os.environ["TMPDIR"])
            state = json.loads((temporary.parent / 'owner.json').read_text())
            descriptor_alias = Path(f"/proc/{state['pid']}/fd/{state['directory_fd']}")
            self.assertTrue(descriptor_alias.is_dir())
            self.assertFalse(os.get_inheritable(state['directory_fd']))
            self.assertTrue(temporary.is_dir())
            self.assertNotEqual(temporary, Path(self.directory.name))
            with isolated(self.root):
                self.assertEqual(Path(os.environ["TMPDIR"]), temporary)
            child = self.probe(self.root, "print(os.environ['TMPDIR'])", env=dict(os.environ))
            stdout, stderr = child.communicate(timeout=10)
            self.assertEqual(child.returncode, 0, stderr)
            self.assertEqual(stdout.strip(), str(temporary))
        self.assertFalse(temporary.exists())
        self.assertFalse(descriptor_alias.exists())
        self.assertEqual(dict(os.environ), previous)

    def test_competing_checkout_fails_before_command_and_stale_lock_file_is_safe(self):
        with isolated(self.root):
            child = self.probe(self.root, "print('MUTATED')")
            stdout, stderr = child.communicate(timeout=10)
            self.assertNotEqual(child.returncode, 0)
            self.assertNotIn("MUTATED", stdout)
            self.assertIn("already owns this checkout", stderr)
        # The persistent inode contains no authority; the kernel lock does.
        (self.root / ".git/verification.lock").write_text("stale prior PID")
        with isolated(self.root):
            self.assertTrue(Path(os.environ["TMPDIR"]).is_dir())

    def test_authenticated_borrowed_context_bounds_git_and_restores_native_choice(self):
        subprocess.run(['git', 'init', '-q', self.directory.name], check=True)
        caller = Path(self.directory.name) / 'caller-boundary'
        caller.mkdir()
        for choice in (None, '', str(caller), ':' + str(caller) + '::'):
            with self.subTest(choice=choice), patch.dict(os.environ, {'TMPDIR': self.directory.name}), isolated(self.root):
                temporary = Path(os.environ['TMPDIR'])
                if choice is None:
                    os.environ.pop('GIT_CEILING_DIRECTORIES', None)
                else:
                    os.environ['GIT_CEILING_DIRECTORIES'] = choice
                fixture = Path(tempfile.mkdtemp())
                control = subprocess.run(['git', 'rev-parse', '--show-toplevel'], cwd=fixture,
                                         capture_output=True, text=True)
                self.assertEqual(control.returncode, 0, control.stderr)
                for fail in (False, True):
                    try:
                        with isolated(self.root):
                            expected = str(temporary) + (':' + choice if choice else '')
                            self.assertEqual(os.environ['GIT_CEILING_DIRECTORIES'], expected)
                            with isolated(self.root):
                                self.assertEqual(os.environ['GIT_CEILING_DIRECTORIES'], expected)
                            bounded = subprocess.run(['git', 'rev-parse', '--show-toplevel'],
                                                     cwd=fixture, capture_output=True, text=True)
                            self.assertEqual(bounded.returncode, 128, bounded.stdout + bounded.stderr)
                            if fail:
                                raise RuntimeError('borrowed-body failure')
                    except RuntimeError as error:
                        self.assertTrue(fail)
                        self.assertEqual(str(error), 'borrowed-body failure')
                    self.assertEqual(os.environ.get('GIT_CEILING_DIRECTORIES'), choice)
                self.assertTrue((temporary.parent / 'owner.json').exists())
            self.assertFalse(temporary.exists())

    def test_stale_inherited_run_is_rejected(self):
        with isolated(self.root):
            env = dict(os.environ)
            env["CODE_FACTORY_VERIFY_TOKEN"] = "stale"
            child = self.probe(self.root, "print('MUTATED')", env=env)
            stdout, stderr = child.communicate(timeout=10)
            self.assertNotEqual(child.returncode, 0)
            self.assertNotIn("MUTATED", stdout)
            self.assertIn("Stale or malformed", stderr)

    def test_owner_death_rejects_stale_inheritance_and_fresh_run_reclaims_lock(self):
        owner = self.probe(self.root, "print(json.dumps({key:value for key,value in os.environ.items() if key.startswith('CODE_FACTORY_VERIFY_') or key=='TMPDIR'}),flush=True)\ntime.sleep(30)")
        def cleanup():
            if owner.poll() is None:
                owner.kill()
            owner.communicate(timeout=5)
        self.addCleanup(cleanup)
        inherited = json.loads(self.ready_line(owner))
        abandoned = Path(inherited['CODE_FACTORY_VERIFY_RUN'])
        owner.kill()
        owner.communicate(timeout=5)
        self.assertTrue(abandoned.exists())
        self.addCleanup(shutil.rmtree, abandoned)
        stale = self.probe(self.root, "print('MUTATED')", env=inherited)
        stdout, stderr = stale.communicate(timeout=5)
        self.assertNotEqual(stale.returncode, 0)
        self.assertNotIn('MUTATED', stdout)
        self.assertIn('Stale or malformed', stderr)
        with isolated(self.root):
            self.assertNotEqual(Path(os.environ['CODE_FACTORY_VERIFY_RUN']), abandoned)
        self.assertTrue(abandoned.exists(), 'fresh ownership must not clean another run')

    def test_unreaped_dead_owner_cannot_join_replacement_checkout_owner(self):
        body = "print(json.dumps(dict(os.environ)),flush=True)\nsys.stdin.readline()"
        owner = self.probe(self.root, body)
        inherited = json.loads(self.ready_line(owner))
        abandoned = Path(inherited['CODE_FACTORY_VERIFY_RUN'])
        self.addCleanup(shutil.rmtree, abandoned, True)
        owner.kill()
        deadline = time.monotonic() + 5
        while os.waitid(os.P_PID, owner.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT) is None:
            self.assertLess(time.monotonic(), deadline, 'owner did not exit')
            time.sleep(.01)
        # Keep A unreaped: /proc still exposes its original process identity.
        replacement = self.probe(self.root, body)
        current = json.loads(self.ready_line(replacement))
        self.assertNotEqual(current['CODE_FACTORY_VERIFY_RUN'], str(abandoned))
        stale = self.probe(self.root, "print('MUTATED')", env=inherited)
        stdout, stderr = stale.communicate(timeout=5)
        self.assertNotEqual(stale.returncode, 0)
        self.assertNotIn('MUTATED', stdout)
        self.assertIn('Stale or malformed', stderr)
        replacement.communicate(input='release\n', timeout=5)

    def test_inherited_root_is_required_and_must_match_its_owner(self):
        with isolated(self.root):
            for value in (None, str(self.root / 'wrong')):
                with self.subTest(root=value):
                    inherited = dict(os.environ)
                    inherited.pop('CODE_FACTORY_VERIFY_ROOT', None)
                    if value is not None:
                        inherited['CODE_FACTORY_VERIFY_ROOT'] = value
                    child = self.probe(self.root, "print('MUTATED')", env=inherited)
                    stdout, stderr = child.communicate(timeout=5)
                    self.assertNotEqual(child.returncode, 0)
                    self.assertNotIn('MUTATED', stdout)
                    self.assertIn('Stale or malformed', stderr)

    def test_malformed_owner_roots_cannot_enter_another_checkout(self):
        other = Path(self.directory.name) / 'other'
        other.mkdir()
        with isolated(self.root):
            owner = Path(os.environ['CODE_FACTORY_VERIFY_RUN']) / 'owner.json'
            original = json.loads(owner.read_text())
            inherited = dict(os.environ)
            inherited.pop('CODE_FACTORY_VERIFY_ROOT')
            shapes = [{}, {'root': None}, {'root': ''}, {'root': 'relative'},
                      {'root': []}, {'root': {}}]
            for shape in shapes:
                with self.subTest(root=shape):
                    state = {key: value for key, value in original.items() if key != 'root'}
                    state.update(shape)
                    owner.write_text(json.dumps(state))
                    child = self.probe(other, "print('MUTATED')", env=inherited)
                    stdout, stderr = child.communicate(timeout=5)
                    self.assertNotEqual(child.returncode, 0, stdout + stderr)
                    self.assertNotIn('MUTATED', stdout)
                    self.assertIn('Stale or malformed', stderr)

    def cancellation_entrypoint(self, mode, make_arguments=None, parallel_stress=True, probe_timeout=45):
        source = Path(__file__).resolve().parents[1]
        shutil.copytree(source / 'scripts', self.root / 'scripts',
                        ignore=shutil.ignore_patterns('__pycache__'))
        timing = self.root / 'scripts/verification_timing.py'
        timing.write_text(timing.read_text().replace(
            'def main():', '''def delay_owned_finalization():
    import fcntl
    from specs_lib.isolation import checkout_lock_path
    with checkout_lock_path(ROOT).open('a+') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            held = False
        except BlockingIOError:
            held = True
    try:
        state = json.loads((Path(os.environ['CODE_FACTORY_VERIFY_RUN']) / 'owner.json').read_text())
        directory_live = Path(f"/proc/{state['pid']}/fd/{state['directory_fd']}").is_dir()
    except OSError:
        directory_live = False
    (ROOT / 'drain-observation.json').write_text(json.dumps({
        'temporary_exists': Path(os.environ['TMPDIR']).is_dir(), 'lock_held': held,
        'directory_fd_live': directory_live}))
    if (ROOT / 'normal-delay').exists():
        (ROOT / 'normal-finalization-started').touch()
        deadline = time.monotonic() + 5
        while not (ROOT / 'release-finalization').exists():
            if time.monotonic() >= deadline:
                raise RuntimeError('normal finalization was not released')
            time.sleep(.001)
    else:
        time.sleep(.25)


def main():''').replace(
            'current.finish("interrupted"', 'delay_owned_finalization()\n                current.finish("interrupted"'))
        if mode.startswith('make-normal-'):
            (self.root / 'normal-delay').touch()
            timing.write_text(timing.read_text().replace(
                'current.finish("interrupted" if interrupted else "success" if code == 0 else "failure")',
                'current.finish("interrupted" if interrupted else "success" if code == 0 else "failure"); (ROOT / "normal-finalization-finished").touch()'))
        makefile = (source / 'Makefile').read_text().replace(
            'verify-steps: verification-context specs-check build check test test-performance package test-installer smoke',
            'verify-steps: verification-context\n\tcd web && npm test').replace(
            'hosted-verify-steps: verification-context hosted-prerequisites verify-steps',
            'hosted-verify-steps: verification-context verify-steps').replace(
            'hosted-verify-bootstrap-steps: verification-context hosted-prerequisites verify-bootstrap-steps',
            'hosted-verify-bootstrap-steps: verification-context verify-steps')
        if mode.startswith('make-normal-'):
            # GNU Make 4.3 applies .NOTPARALLEL globally. These synthetic sibling goals
            # must overlap to stimulate failure while a foreground helper lives.
            if parallel_stress:
                makefile = '\n'.join(line for line in makefile.split('\n')
                                     if not line.startswith('.NOTPARALLEL:'))
            else:
                (self.root / 'normal-serial').touch()
            makefile += '''
.DEFAULT_GOAL := normal-error
.PHONY: normal-error normal-alias foreground failing-sibling normal-worker
normal-error: foreground failing-sibling
normal-alias: normal-error
foreground:
	+python3 scripts/verification_timing.py verify -- $(MAKE) --no-print-directory normal-worker
normal-worker:
	cd web && npx probe
'''
            if mode == 'make-normal-fatal':
                makefile += 'failing-sibling:\n\t$(shell python3 normal-error.py)$(error controlled fatal expansion)\n'
            else:
                makefile += 'failing-sibling:\n\tpython3 normal-error.py fail\n'
            (self.root / 'normal-error.py').write_text('''import pathlib,sys,time
root=pathlib.Path.cwd();deadline=time.monotonic()+5
while not (root/'pids.json').exists():
    if time.monotonic()>=deadline:raise RuntimeError('foreground did not become ready')
    time.sleep(.001)
(root/'normal-error-observed').touch()
if len(sys.argv)>1:raise SystemExit(7)
''')
        (self.root / 'Makefile').write_text(makefile)
        with (self.root / 'Makefile').open('a') as output:
            output.write("\ndirect-helper:\n\tcd web && python3 ../scripts/verification_run.py -- sh -c 'cd web && exec npx probe'\n")
            output.write('\nbackground-helper:\n\tpython3 background-launcher.py\n')
            output.write('\nimported-helper:\n\tpython3 imported-runner.py\n')
        (self.root / 'imported-runner.py').write_text('''from pathlib import Path
from specs_lib.processes import run_verification
run_verification(['npm','--prefix','web','test'],cwd=Path.cwd())
''')
        if mode == 'make-sharing-cleanup':
            processes = self.root / 'scripts/specs_lib/processes.py'
            processes.write_text(processes.read_text().replace(
                'os.killpg(process.pid, signal.SIGTERM)',
                "os.killpg(process.pid, signal.SIGTERM)\n                (Path(cwd) / 'group-term-sent').touch()"))
            (self.root / 'imported-runner.py').write_text('''from pathlib import Path
import sys
from specs_lib.processes import run_verification
run_verification([sys.executable,'sharing-cleanup.py'],cwd=Path.cwd(),termination_grace=3)
''')
            (self.root / 'sharing-cleanup.py').write_text('''import json,os,signal,subprocess,sys,time
from pathlib import Path
from specs_lib.isolation import managed,enclosing_managed_group
import verification_timing as timing
root=Path.cwd();cancelled=[False]
with managed(root):
    signal.signal(signal.SIGTERM,lambda number,frame:cancelled.__setitem__(0,True))
    partial=root/'pids.partial'
    partial.write_text(json.dumps({'pids':[os.getpid()],'tmpdir':os.environ['TMPDIR']}))
    partial.rename(root/'pids.json')
    deadline=time.monotonic()+10
    while not cancelled[0]:
        if time.monotonic()>=deadline: raise RuntimeError('cleanup did not receive cancellation')
        time.sleep(.001)
    deadline=time.monotonic()+2
    while not (root/'group-term-sent').exists():
        if time.monotonic()>=deadline: raise RuntimeError('runner did not send group TERM')
        time.sleep(.001)
    shared=enclosing_managed_group()
    expected=os.getpgrp()
    code=None
    try:
        code=timing.execute('build',[sys.executable,'-c',
            "import os,pathlib;pathlib.Path('cleanup-group').write_text(str(os.getpgrp()))"],owned=not shared)
        completed=code==0 and int((root/'cleanup-group').read_text())==expected
    except (OSError,RuntimeError):
        completed=False
    admitted=subprocess.run([sys.executable,'-c',
        "from pathlib import Path;from specs_lib.isolation import managed; "
        "context=managed(Path.cwd());context.__enter__();Path('new-work').touch()"],
        capture_output=True,timeout=2)
    state=json.loads((Path(os.environ['CODE_FACTORY_VERIFY_RUN'])/'owner.json').read_text())
    alias=Path(f"/proc/{state['pid']}/fd/{state['directory_fd']}")
    (root/'sharing-cleanup.json').write_text(json.dumps({'shared':shared,'cleanup_completed':completed,
        'cleanup_exit':code,
        'new_rejected':admitted.returncode!=0 and not (root/'new-work').exists(),
        'directory_fd_live':alias.is_dir()}))
''')
        (self.root / 'background-launcher.py').write_text('''import os,pathlib,subprocess,sys,time
race=pathlib.Path('race-enabled').exists()
command=([sys.executable,'scripts/verification_timing.py','verify','--','make','verify-steps'] if race else
    [sys.executable,'scripts/verification_run.py','--','sh','-c','cd web && exec npx probe'])
process=subprocess.Popen(command)
deadline=time.monotonic()+5
while not pathlib.Path('pids.json').exists():
    if process.poll() is not None or time.monotonic()>=deadline:
        process.terminate()
        process.wait(timeout=15)
        raise RuntimeError('background managed helper did not become ready')
    time.sleep(.01)
if race:
    pathlib.Path('hold-accept').touch()
    second=subprocess.Popen([sys.executable,'-c',
        "from pathlib import Path; from specs_lib.isolation import managed; "
        "context=managed(Path.cwd()); context.__enter__(); Path('second-work').touch(); Path('second-finished').touch()"],
        env={**os.environ,'LEASE_CONNECTED_MARKER':str(pathlib.Path('client-connected').resolve())},
        stderr=subprocess.DEVNULL)
    deadline=time.monotonic()+5
    while not pathlib.Path('client-connected').exists():
        if second.poll() is not None or time.monotonic()>=deadline:
            raise RuntimeError('second managed client did not connect')
        time.sleep(.01)
    if pathlib.Path('pending-handshake').exists() or pathlib.Path('admitted-handshake').exists():
        marker='handshake-started' if pathlib.Path('pending-handshake').exists() else 'admission-committed'
        deadline=time.monotonic()+5
        while not pathlib.Path(marker).exists():
            if time.monotonic()>=deadline: raise RuntimeError('guardian did not start pending handshake')
            time.sleep(.001)
''')
        if mode in ('make-ready-batch', 'make-listener-first', 'make-pending-handshake', 'make-admitted-handshake'):
            (self.root / 'race-enabled').touch()
            owner = self.root / 'scripts/specs_lib/make_owner.py'
            batch = '''ready = events.select(.05)
                    if (root / 'hold-accept').exists() and any(key.data == 'listener' for key, _ in ready):
                        limit = time.monotonic() + 5
                        while not select.select([owner], [], [], 0)[0]:
                            if time.monotonic() >= limit:
                                raise RuntimeError('Make did not exit at the pending-connection barrier')
                            time.sleep(.01)
                        ready = [(events.get_key(owner), selectors.EVENT_READ),
                                 (events.get_key(listener), selectors.EVENT_READ)]
                        (root / 'ready-batch').touch()
                    for item, _ in ready:'''
            if mode == 'make-listener-first':
                batch = batch.replace(
                    "[(events.get_key(owner), selectors.EVENT_READ),\n                                 (events.get_key(listener), selectors.EVENT_READ)]",
                    "[(events.get_key(listener), selectors.EVENT_READ),\n                                 (events.get_key(owner), selectors.EVENT_READ)]")
            owner_source = owner.read_text()
            if mode == 'make-pending-handshake':
                (self.root / 'pending-handshake').touch()
                owner_source = owner_source.replace(
                    'def message(stream, timeout=5):',
                    "def message(stream, timeout=5):\n"
                    "    if isinstance(stream, socket.socket) and Path('pending-handshake').exists():\n"
                    "        Path('handshake-started').touch()")
            elif mode == 'make-admitted-handshake':
                (self.root / 'admitted-handshake').touch()
                owner_source = owner_source.replace(
                    "connection.sendall(b'1')",
                    "if (root / 'hold-accept').exists():\n"
                    "                                    (root / 'admission-committed').touch()\n"
                    "                                    if not select.select([owner], [], [], 5)[0]: raise RuntimeError('Make did not exit after admission')\n"
                    "                                connection.sendall(b'1')")
            else:
                owner_source = owner_source.replace('for item, _ in events.select(.05):', batch)
            owner_source = owner_source.replace(
                "connection.sendall(b'1')",
                "connection.sendall(b'1')\n"
                "                                if (root / 'hold-accept').exists():\n"
                "                                    limit = time.monotonic() + 5\n"
                "                                    while not (root / 'second-finished').exists():\n"
                "                                        if time.monotonic() >= limit: raise RuntimeError('admitted helper did not finish')\n"
                "                                        time.sleep(.001)")
            owner.write_text(owner_source)
            isolation = self.root / 'scripts/specs_lib/isolation.py'
            isolation.write_text(isolation.read_text().replace(
                "connection.sendall(process_identity(os.getpid()).encode() + b'\\n')",
                "if os.environ.get('LEASE_CONNECTED_MARKER'):\n"
                "                    Path(os.environ['LEASE_CONNECTED_MARKER']).touch()\n"
                "                    if Path('pending-handshake').exists():\n"
                "                        import select\n"
                "                        owner = os.pidfd_open(state['make_pid'])\n"
                "                        try:\n"
                "                            if not select.select([owner], [], [], 5)[0]: raise RuntimeError('Make did not exit during handshake')\n"
                "                        finally:\n"
                "                            os.close(owner)\n"
                "                connection.sendall(process_identity(os.getpid()).encode() + b'\\n')"))
        (self.root / 'web').mkdir()
        package = json.loads((source / 'web/package.json').read_text())
        (self.root / 'web/package.json').write_text(json.dumps({'scripts': package['scripts']}))
        tools = self.root / 'tools'
        tools.mkdir()
        npx = tools / 'npx'
        npx.write_text('''#!/usr/bin/python3
import json,os,pathlib,signal,subprocess,sys,time
root=pathlib.Path.cwd().parent
signal.signal(signal.SIGTERM,signal.SIG_IGN)
child=subprocess.Popen([sys.executable,'-c',
    "import pathlib,signal,sys,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); pathlib.Path(sys.argv[1]).touch(); time.sleep(60)",
    str(root/'child-ready')])
deadline=time.monotonic()+5
while not (root/'child-ready').exists():
    if time.monotonic()>=deadline: raise RuntimeError('child did not start')
    time.sleep(.001)
state=root/'pids.partial'
state.write_text(json.dumps({'pids':[os.getpid(),child.pid],'tmpdir':os.environ['TMPDIR'],
    'group':os.getpgrp(),'group_owner':os.environ.get('CODE_FACTORY_VERIFY_GROUP_OWNER'),
    'lease_sockets':sum(path.is_socket() for path in pathlib.Path('/proc/self/fd').iterdir())}))
state.rename(root/'pids.json')
time.sleep(60)
''')
        if mode.startswith('make-normal-'):
            npx.write_text(npx.read_text().replace('time.sleep(60)\n', '''deadline=time.monotonic()+5
while not (root/'release-worker').exists():
    if time.monotonic()>=deadline:raise RuntimeError('worker was not released')
    time.sleep(.001)
child.kill();child.wait(timeout=2)
'''))
        npx.chmod(0o755)
        # Keep the production npm ownership entrypoint, substituting only its
        # browser workload. Retain the authenticated fixture sockets in both
        # launcher and worker, with their original group/session identity.
        (self.root / 'web/fixture-runner.mjs').write_text(
            '''import { spawn } from "node:child_process";
import { readdirSync, fstatSync } from "node:fs";
process.chdir(import.meta.dirname);
const stdio = ["inherit", "inherit", "inherit"];
for (const name of readdirSync("/proc/self/fd")) {
  const fd = Number(name);
  try {
    if (fd > 2 && fstatSync(fd).isSocket()) {
      while (stdio.length <= fd) stdio.push("ignore");
      stdio[fd] = fd;
    }
  } catch (error) { if (error.code !== "EBADF") throw error; }
}
const worker = spawn(''' + json.dumps(str(npx)) + ''',
  ["playwright", "test", ...process.argv.slice(2)], { stdio });
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => worker.kill(signal));
worker.on("error", (error) => { console.error(error); process.exitCode = 1; });
worker.on("exit", (code, signal) => { process.exitCode = code ?? (signal === "SIGINT" ? 130 : 143); });
''')
        subprocess.run(['git', 'add', '.'], cwd=self.root, check=True)
        subprocess.run(['git', '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid',
                        'commit', '-qm', 'Fixture'], cwd=self.root, check=True)
        head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=self.root, text=True).strip()
        (self.root / 'event.json').write_text(json.dumps({'before': '0' * 40, 'after': head}))
        environment = {key: value for key, value in self.env.items()
                       if not key.startswith(('CODE_FACTORY_TIMING_', 'GITHUB_'))
                       and key not in ('MAKEFLAGS', 'MFLAGS', 'MAKELEVEL', 'FLOW_ARGS', 'FLOW_BASE')}
        environment.update(PATH=str(tools) + ':' + os.environ['PATH'],
                           PYTHONPATH=str(self.root / 'scripts'), GITHUB_EVENT_NAME='push',
                           GITHUB_REF='refs/heads/main', CI_BOOTSTRAP_SHA=head)
        result = self.supervised_probe([
            sys.executable, '-c', CANCELLATION_PROBE, str(self.root), mode,
            json.dumps(make_arguments or ['make', '-j2', 'normal-error'])],
            env=environment, timeout=probe_timeout)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr +
                         (self.root / 'probe.stderr').read_text())
        self.assertIn('owned groups reaped; temporary state removed; evidence finalized', result.stdout)
        if mode == 'hosted-make-death':
            print(result.stdout, end='')

    def test_reconciliation_npm_wrapper_cancellation_reaps_term_ignoring_group(self):
        self.cancellation_entrypoint('reconcile')

    def test_probe_timeout_reaps_detached_workers_before_fixture_removal(self):
        unrelated = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)'])
        self.addCleanup(cleanup_process, unrelated)
        fault = CANCELLATION_PROBE.replace(
            "state=json.loads((root/'pids.json').read_text())",
            "state=json.loads((root/'pids.json').read_text())\n"
            "        (root/'timeout-fault-ready').touch()\n"
            "        time.sleep(60)")
        for mode in ('reconcile', 'make', 'hosted'):
            with self.subTest(mode=mode):
                case = IsolationTests()
                case.setUp()
                try:
                    with patch(__name__ + '.CANCELLATION_PROBE', fault):
                        with self.assertRaises(subprocess.TimeoutExpired):
                            case.cancellation_entrypoint(mode, probe_timeout=5)
                    self.assertTrue((case.root / 'timeout-fault-ready').exists())
                    state = json.loads((case.root / 'pids.json').read_text())
                    self.assertTrue(case.root.exists(), 'fixture removed before drain inspection')
                    for pid in state['pids']:
                        self.assertFalse(Path(f'/proc/{pid}').exists(), f'owned worker {pid} survived')
                    self.assertEqual(json.loads((case.root / 'probe-drained.json').read_text())['children'], [])
                    self.assertIsNone(unrelated.poll(), 'supervisor touched an unrelated child')
                finally:
                    self.assertTrue(case.doCleanups(), 'probe fixture cleanup failed')
                self.assertFalse(case.root.exists())

    def test_probe_supervisor_preserves_normal_and_nonzero_status(self):
        for status in (0, 7):
            with self.subTest(status=status):
                result = self.supervised_probe([sys.executable, '-c', f'raise SystemExit({status})'])
                self.assertEqual(result.returncode, status, result.stderr)
                self.assertTrue(self.probe_drained)

    def test_probe_supervisor_drains_after_parent_interrupt_and_wait_timeout(self):
        command = [sys.executable, '-c',
                   "import pathlib,subprocess,sys,time; "
                   "child=subprocess.Popen([sys.executable,'-c',"
                   "\"import os,pathlib,signal,sys,time;signal.signal(signal.SIGTERM,signal.SIG_IGN);"
                   "pathlib.Path(sys.argv[1]).write_text(str(os.getpid()));time.sleep(60)\","
                   "sys.argv[1]],start_new_session=True);time.sleep(60)",
                   str(self.root / 'detached-ready')]
        real_communicate = subprocess.Popen.communicate
        for failure in ('interrupt', 'terminate', 'timeout'):
            with self.subTest(failure=failure):
                ready = self.root / 'detached-ready'
                ready.unlink(missing_ok=True)
                injected = False
                def communicate(process, *args, **kwargs):
                    nonlocal injected
                    if not injected:
                        injected = True
                        deadline = time.monotonic() + 5
                        while not ready.exists():
                            self.assertLess(time.monotonic(), deadline, 'detached worker not ready')
                            time.sleep(.001)
                        if failure == 'timeout':
                            raise subprocess.TimeoutExpired(process.args, kwargs['timeout'])
                        os.kill(os.getpid(), signal.SIGINT if failure == 'interrupt' else signal.SIGTERM)
                    return real_communicate(process, *args, **kwargs)
                expected = {'interrupt': KeyboardInterrupt, 'terminate': SystemExit,
                            'timeout': subprocess.TimeoutExpired}[failure]
                handlers = {s: signal.getsignal(s) for s in (signal.SIGINT, signal.SIGTERM)}
                with patch.object(subprocess.Popen, 'communicate', communicate):
                    with self.assertRaises(expected):
                        self.supervised_probe(command)
                self.assertTrue(self.probe_drained)
                self.assertFalse(Path('/proc/' + ready.read_text()).exists())
                self.assertEqual({s: signal.getsignal(s) for s in handlers}, handlers)

    def test_probe_supervisor_missing_acknowledgement_retains_fixture(self):
        process = Mock(pid=123, returncode=None)
        process.poll.return_value = None
        process.communicate.side_effect = subprocess.TimeoutExpired('supervisor', 4)
        try:
            with patch.object(subprocess, 'Popen', return_value=process):
                with self.assertRaisesRegex(RuntimeError, 'supervisor 123 did not acknowledge cleanup'):
                    self.supervised_probe([sys.executable, '-c', 'pass'], timeout=.01)
            process.terminate.assert_called_once()
            process.kill.assert_not_called()
            self.assertFalse(self.probe_drained)
            with self.assertRaisesRegex(RuntimeError, 'unresolved probe ownership; retained fixture'):
                self.cleanup_directory()
            self.assertTrue(self.root.exists())
        finally:
            # This control spawned no process; ordinary test cleanup is safe.
            self.probe_drained = True

    def test_make_npm_wrapper_cancellation_reaps_group_and_finalizes_timing(self):
        self.cancellation_entrypoint('make')

    def test_direct_hosted_owner_cancellation_reaps_group_and_finalizes_timing(self):
        self.cancellation_entrypoint('hosted')

    def test_normal_make_failure_waits_for_foreground_cleanup_and_timing(self):
        for mode in ('make-normal-nonzero', 'make-normal-fatal'):
            for flags in (['-j1'], ['-j2'], ['-j2', '-k']):
                for goals in ([], ['normal-error'], ['normal-alias']):
                    with self.subTest(mode=mode, flags=flags, goals=goals):
                        case = IsolationTests()
                        try:
                            case.setUp()
                            case.cancellation_entrypoint(mode, ['make', *flags, *goals], parallel_stress=flags != ['-j1'])
                        finally:
                            case.doCleanups()

    def test_direct_hosted_make_death_retains_owner_through_worker_cleanup(self):
        self.cancellation_entrypoint('hosted-make-death')

    def test_native_make_death_drains_managed_cleanup_and_timing(self):
        self.cancellation_entrypoint('make-death')

    def test_native_make_direct_managed_helper_owns_its_command_group(self):
        self.cancellation_entrypoint('make-direct-helper')

    def test_native_make_completion_drains_background_managed_helper(self):
        self.cancellation_entrypoint('make-complete')

    def test_make_death_and_pending_admission_in_same_ready_batch_still_drain(self):
        self.cancellation_entrypoint('make-ready-batch')

    def test_make_death_listener_first_rejects_pending_admission_and_still_drains(self):
        self.cancellation_entrypoint('make-listener-first')

    def test_make_death_during_identity_handshake_rejects_pending_admission(self):
        self.cancellation_entrypoint('make-pending-handshake')

    def test_make_death_after_admission_before_acknowledgement_still_drains(self):
        self.cancellation_entrypoint('make-admitted-handshake')

    def test_make_death_drains_imported_owned_runner_lease(self):
        self.cancellation_entrypoint('make-imported')

    def test_term_ignoring_worker_finishes_before_enclosing_cleanup_deadline(self):
        self.cancellation_entrypoint('outer-ignore')

    def test_admitted_cleanup_keeps_group_after_make_death_without_new_admission(self):
        self.cancellation_entrypoint('make-sharing-cleanup')

    def test_phase_sharing_requires_live_group_and_matching_ownership_origin(self):
        source = Path(__file__).resolve().parents[1]
        shutil.copytree(source / 'scripts', self.root / 'scripts',
                        ignore=shutil.ignore_patterns('__pycache__'))
        observer = self.root / 'group-observer.py'
        observer.write_text("import json,os,sys\nfrom pathlib import Path\n"
                            "Path(sys.argv[1]).write_text(json.dumps({'group':os.getpgrp()}))\n")
        driver = self.root / 'group-driver.py'
        driver.write_text('''import json,os,shutil,sys
from pathlib import Path
from specs_lib.isolation import isolated,process_identity
import verification_timing as timing
root=Path(sys.argv[1]);mode=sys.argv[2]
timing.ROOT=root
expected=os.getpgrp()
if mode=='forged':
    os.environ['CODE_FACTORY_VERIFY_GROUP_OWNER']=str(os.getpid())+':'+process_identity(os.getpid())
if mode=='stale':
    os.environ['CODE_FACTORY_VERIFY_GROUP_OWNER']=os.environ['CODE_FACTORY_VERIFY_GROUP_OWNER'].split(':')[0]+':stale'
previous=Path(os.environ['CODE_FACTORY_VERIFY_RUN'])
context=isolated(root/'other') if mode=='other' else None
if context is not None:
    context.__enter__()
    timing.ROOT=root/'other'
    shutil.copytree(previous/'groups',Path(os.environ['CODE_FACTORY_VERIFY_RUN'])/'groups',dirs_exist_ok=True)
try:
    target=root/(mode+'.json')
    sys.argv=['timing','command','build','--',sys.executable,str(root/'group-observer.py'),str(target)]
    assert timing.main()==0
    data=json.loads(target.read_text())
    data['expected_group']=expected
    target.write_text(json.dumps(data))
finally:
    if context is not None: context.__exit__(None,None,None)
''')
        (self.root / 'other').mkdir()
        body = ("from specs_lib.processes import run_verification\n"
                "root=Path(sys.argv[1]);os.chdir(root)\n"
                "code=" + repr(
                    "import pathlib,subprocess,sys\n"
                    "root=pathlib.Path(sys.argv[1])\n"
                    "for mode in ('shared','detached','forged','stale','other'):\n"
                    " subprocess.run([sys.executable,str(root/'group-driver.py'),str(root),mode],"
                    "check=True,start_new_session=mode=='detached')\n") + "\n"
                "run_verification([sys.executable,'-c',code,str(root)],cwd=root)")
        child = self.probe(self.root, body, env={**self.env, 'PYTHONPATH': str(self.root / 'scripts')})
        stdout, stderr = child.communicate(timeout=15)
        self.assertEqual(child.returncode, 0, stdout + stderr)
        for mode in ('shared', 'detached', 'forged', 'stale', 'other'):
            data = json.loads((self.root / (mode + '.json')).read_text())
            with self.subTest(mode=mode):
                if mode == 'shared':
                    self.assertEqual(data['group'], data['expected_group'])
                else:
                    self.assertNotEqual(data['group'], data['expected_group'])

    def test_startup_signal_defers_until_temporary_directory_can_be_cleaned(self):
        real_temporary = tempfile.TemporaryDirectory
        allocated = []
        def allocate(*args, **kwargs):
            directory = real_temporary(*args, **kwargs)
            allocated.append(Path(directory.name))
            os.kill(os.getpid(), signal.SIGTERM)
            return directory
        previous = dict(os.environ)
        with patch('specs_lib.isolation.tempfile.TemporaryDirectory', side_effect=allocate):
            with self.assertRaises(SystemExit) as stopped:
                with isolated(self.root):
                    self.fail('cancelled startup executed verifier work')
        self.assertEqual(stopped.exception.code, 143)
        self.assertTrue(allocated)
        self.assertTrue(all(not path.exists() for path in allocated))
        self.assertEqual(dict(os.environ), previous)
        with isolated(self.root):
            pass

    def test_unrelated_ancestor_git_marker_is_not_discovered(self):
        ancestor_root = Path(self.directory.name)
        subprocess.run(["git", "init", "-q", str(ancestor_root)], check=True)
        sentinel = ancestor_root / ".git/sentinel"
        sentinel.write_text("unrelated")
        for name in ('ordinary-parent', 'caller:state', 'caller:雪\n'):
            parent = ancestor_root / name
            parent.mkdir()
            with self.subTest(parent=name), patch.dict(os.environ, {"TMPDIR": str(parent)}), isolated(self.root):
                temporary = Path(os.environ['TMPDIR'])
                self.assertEqual(temporary.parent.parent, parent)
                fixture = Path(tempfile.mkdtemp())
                # Prove the ancestor is discoverable, so the ceiling is exercised.
                unbounded = dict(os.environ)
                unbounded.pop("GIT_CEILING_DIRECTORIES", None)
                inherited = subprocess.check_output(["git", "rev-parse", "--show-toplevel"],
                                                     cwd=fixture, env=unbounded, text=True)
                self.assertEqual(Path(inherited.strip()), ancestor_root)
                result = subprocess.run(["git", "rev-parse", "--show-toplevel"], cwd=fixture,
                                        capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                subprocess.run(["git", "init", "-q", str(fixture)], check=True)
                for directory in (fixture, fixture / 'nested'):
                    directory.mkdir(exist_ok=True)
                    result = subprocess.check_output(["git", "rev-parse", "--show-toplevel"],
                                                     cwd=directory, text=True)
                    self.assertEqual(Path(result.rstrip('\n')), fixture)
                result = subprocess.check_output(["git", "rev-parse", "--show-toplevel"],
                                                 cwd=self.root, text=True)
                self.assertEqual(Path(result.rstrip('\n')), self.root)
            self.assertFalse(temporary.exists())
        self.assertEqual(sentinel.read_text(), "unrelated")

    def test_two_worktrees_complete_concurrently_with_distinct_state(self):
        subprocess.run(["git", "-C", str(self.root), "-c", "user.name=Test", "-c",
                        "user.email=test@example.invalid", "commit", "-qm", "fixture", "--allow-empty"], check=True)
        second = Path(self.directory.name) / "second"
        subprocess.run(["git", "-C", str(self.root), "worktree", "add", "-q", "--detach", str(second)], check=True)
        body = "print(json.dumps({'tmp':os.environ['TMPDIR'],'pid':os.getpid()}),flush=True)\nsys.stdin.readline()\nprint('completed')"
        a = self.probe(self.root, body)
        b = self.probe(second, body)
        first, other = json.loads(self.ready_line(a)), json.loads(self.ready_line(b))
        self.assertIsNone(a.poll())
        self.assertIsNone(b.poll())
        self.assertNotEqual(first["tmp"], other["tmp"])
        for process, state in ((a, first), (b, other)):
            stdout, stderr = process.communicate(input='release\n', timeout=10)
            self.assertEqual(process.returncode, 0, stderr)
            self.assertIn("completed", stdout)
            self.assertFalse(Path(state["tmp"]).exists())

    def test_interruption_releases_checkout_and_removes_only_owned_state(self):
        script = """from specs_lib.isolation import isolated
from specs_lib.processes import run_verification
from pathlib import Path
import os,sys
with isolated(Path(sys.argv[1])):
    print(os.environ['TMPDIR'],flush=True)
    command = 'import os,pathlib,sys,time; ready=pathlib.Path(sys.argv[1]); partial=ready.with_suffix(".partial"); partial.write_text(str(os.getpid())); partial.replace(ready); time.sleep(30)'
    run_verification([sys.executable,'-c',command,str(Path(os.environ['TMPDIR'])/'child-ready')],cwd=Path(sys.argv[1]))
"""
        child = subprocess.Popen([sys.executable, "-c", script, str(self.root)], env=self.env,
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.addCleanup(cleanup_process, child)
        temporary = Path(self.ready_line(child))
        # A real child has started after production recording handlers were
        # installed; cancellation must remove both its work and private state.
        deadline = time.monotonic() + 5
        ready = temporary / 'child-ready'
        while not ready.exists():
            self.assertLess(time.monotonic(), deadline)
            time.sleep(.01)
        verifier = int(ready.read_text())
        child.send_signal(signal.SIGTERM)
        child.communicate(timeout=10)
        self.assertEqual(child.returncode, 143)
        self.assertFalse(temporary.exists())
        with self.assertRaises(ProcessLookupError):
            os.kill(verifier, 0)
        self.assertTrue(self.root.exists())
        with isolated(self.root):
            pass

    def test_absent_and_partial_readiness_are_bounded_and_failure_reaps_child(self):
        for output in ('', "print('partial',end='',flush=True)"):
            with self.subTest(output=output):
                child = self.probe(self.root, output + "\ntime.sleep(60)")
                with self.assertRaisesRegex(AssertionError, 'readiness timed out'):
                    try:
                        self.ready_line(child, timeout=.1)
                    finally:
                        cleanup_process(child)
                self.assertIsNotNone(child.returncode)
                with self.assertRaises(ChildProcessError):
                    os.waitpid(child.pid, os.WNOHANG)


class MakeOwnershipCheckerTests(ProbeSupervision, unittest.TestCase):
    """Use the production checker, rather than the command-recording fixture."""

    def setUp(self):
        if getattr(self, '_checker_worker', False):
            return
        from test_specs import ROOT, edit, fixture, write_tree
        self.directory = tempfile.TemporaryDirectory()
        self.probe_drained = True
        self.addCleanup(self.cleanup_directory)
        self.parent = Path(self.directory.name)
        self.root = self.parent / 'checkout'
        self.root.mkdir()
        files = fixture()
        edit(files, 'docs/user-flows/catalog.json', lambda catalog: catalog['exclusions'].extend(
            {'glob': name, 'reason': 'Production entrypoint in the checker fixture.'}
            for name in ('Makefile', '.gitignore', 'scripts/verification_run.py',
                         'scripts/verification_timing.py')))
        write_tree(self.root, files)
        shutil.copytree(ROOT / 'scripts/specs_lib', self.root / 'scripts/specs_lib',
                        ignore=shutil.ignore_patterns('__pycache__'))
        for name in ('specs.py', 'user_flows.py', 'verification_run.py', 'verification_timing.py'):
            shutil.copyfile(ROOT / 'scripts' / name, self.root / 'scripts' / name)
        # Keep real ownership, timing, comparison selection and checker execution.
        # The fixture has no browser/build inputs; its ci graph stops at that gate.
        makefile = (ROOT / 'Makefile').read_text().replace(
            'verify-steps: verification-context specs-check build check test test-performance package test-installer smoke',
            'verify-steps: verification-context specs-check')
        (self.root / 'Makefile').write_text(makefile)
        shutil.copyfile(ROOT / '.gitignore', self.root / '.gitignore')
        self.env = {key: value for key, value in os.environ.items()
                    if not key.startswith(('CODE_FACTORY_VERIFY_', 'CODE_FACTORY_TIMING_'))
                    and key not in ('MAKEFLAGS', 'MFLAGS', 'MAKELEVEL', 'MAKEFILES', 'FLOW_ARGS', 'FLOW_BASE', 'CODE_FACTORY_MAKE_STDIN')}
        self.git('init', '-q', '-b', 'main')
        self.git('config', 'user.name', 'Fixture')
        self.git('config', 'user.email', 'fixture@example.invalid')
        self.commit('Initial checker fixture')
        self.git('checkout', '-qb', 'change')

    def _callTestMethod(self, method):
        if getattr(self, '_checker_worker', False):
            return super()._callTestMethod(method)
        # The parent owns the fixture. The entire test runs beneath an
        # independent subreaper, so direct/interactive subprocesses and failed
        # assertions receive the same drain guarantee as self.make().
        result = self.supervised_probe([
            sys.executable, str(Path(__file__).resolve()), '--checker-worker',
            str(self.root), str(self.parent), method.__name__],
            env=self.env, timeout=180, drain_timeout=35)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def git(self, *args):
        return subprocess.run(['git', *args], cwd=self.root, env=self.env,
                              capture_output=True, text=True, check=True, timeout=10).stdout

    def commit(self, message):
        self.git('add', '.')
        self.git('commit', '-qm', message)

    def make(self, *args, parent=None, input=None, directory=None, timeout=30):
        env = dict(self.env)
        if parent is not None:
            env['TMPDIR'] = str(parent)
        return self.supervised_probe(['make', '--no-print-directory', *args],
                                     cwd=directory or self.root, env=env, input=input,
                                     timeout=timeout, drain_timeout=35)

    def test_selected_makefiles_preserve_comparison_order_includes_and_overrides(self):
        (self.root / 'app.py').write_text('def perform():\n    return 2\n')
        self.commit('Change requiring reconciliation against main')
        first = self.parent / 'first.mk'
        first.write_text('FLOW_ARGS = --base HEAD\n')
        included = self.parent / 'included.mk'
        included.write_text('INCLUDE_VISITS += visit\n')
        second = self.parent / 'second config.mk'
        second.write_text('include ' + str(included) + '\n'
                          'ifneq ($(words $(INCLUDE_VISITS)),1)\n'
                          '$(error included file loaded more than once)\nendif\n'
                          'FLOW_ARGS = --base main --merge-base\n')
        selections = [('-f', 'Makefile', '--file=' + str(first), '--makefile', str(second)),
                      ('-fMakefile', '-f' + str(first), '-sf' + str(second))]
        for selection in selections:
            with self.subTest(selection=selection):
                result = self.make(*selection, 'specs-check')
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn('reconciliation required', result.stdout + result.stderr)
                self.assertNotIn('included file loaded more than once', result.stderr)
                overridden = self.make(*selection, 'specs-check', 'FLOW_ARGS=--base HEAD')
                self.assertEqual(overridden.returncode, 0, overridden.stdout + overridden.stderr)

    def test_supplemental_recipes_and_prerequisites_execute_once_under_ownership(self):
        self.git('init', '-q', str(self.parent))
        observer = self.parent / 'observe.py'
        observer.write_text('''import json,os,sys,subprocess,tempfile
from pathlib import Path
sys.path.insert(0,str(Path.cwd()/'scripts'))
from specs_lib.isolation import isolated
run=os.environ.get('CODE_FACTORY_VERIFY_RUN')
state={'label':sys.argv[1],'run':run,'tmp':os.environ.get('TMPDIR'),
       'ceiling':os.environ.get('GIT_CEILING_DIRECTORIES'),'owned':False}
if run:
    with isolated(Path.cwd()):
        state['owned']=True
        state['managed_ceiling']=os.environ['GIT_CEILING_DIRECTORIES']
        state['bounded']=subprocess.run(['git','rev-parse','--show-toplevel'],
            cwd=tempfile.mkdtemp(),capture_output=True).returncode
state['restored_ceiling']=os.environ.get('GIT_CEILING_DIRECTORIES')
with Path('../executions.jsonl').open('a') as output:
    output.write(json.dumps(state)+'\\n')
''')
        supplemental = self.parent / 'recipes.mk'
        supplemental.write_text(
            '.PHONY: supplemental-preparation audit\n'
            'audit: SHELL := /bin/bash\n'
            'audit: .SHELLFLAGS := -ec\n'
            'plugin-embed audit: supplemental-preparation\n'
            'supplemental-preparation:\n\t@python3 ../observe.py preparation\n'
            'plugin-embed:\n\t@python3 ../observe.py standard\n'
            'audit:\n\t@python3 ../observe.py custom\n')
        alias = self.parent / 'selected Makefile.mk'
        alias.symlink_to(self.root / 'Makefile')
        variants = ('Makefile', './Makefile', str(self.root / 'Makefile'),
                    '../checkout/Makefile', str(alias))
        for selection, flags, ending in ((name, flags, ending) for name in variants
                                        for flags, ending in (((), '\n'), (('-j3',), '\r'),
                                                              (('-k', '-j3'), '\n'))):
            ceiling = self.parent / ('ceiling' + ending)
            ceiling.mkdir(exist_ok=True)
            self.env['GIT_CEILING_DIRECTORIES'] = str(ceiling)
            with self.subTest(selection=selection, flags=flags, ending=ending):
                log = self.parent / 'executions.jsonl'
                log.unlink(missing_ok=True)
                result = self.make(*flags, '-f', selection, '-f', str(supplemental),
                                   'plugin-embed', 'audit', 'specs-check', 'FLOW_ARGS=--base HEAD', parent=self.parent)
                events = [json.loads(line) for line in log.read_text().splitlines()]
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertCountEqual([event['label'] for event in events],
                                      ['preparation', 'standard', 'custom'])
                self.assertTrue(all(event['owned'] for event in events), events)
                self.assertEqual(len({event['run'] for event in events}), 1, events)
                self.assertEqual(len({event['tmp'] for event in events}), 1, events)
                self.assertTrue(all(event['ceiling'] == str(ceiling)
                                    and event['restored_ceiling'] == str(ceiling)
                                    and event['managed_ceiling'] == event['tmp'] + ':' + str(ceiling)
                                    and event['bounded'] == 128 for event in events), events)
                self.assertTrue(all(not Path(event['run']).exists() for event in events))
                self.assertIn('scripts/specs.py check --base HEAD', result.stdout)

    def test_colon_temporary_parent_preserves_native_git_discovery_boundary(self):
        self.git('init', '-q', str(self.parent))
        parent = self.parent / 'caller:雪'
        parent.mkdir()
        observer = self.parent / 'git-boundary.py'
        observer.write_text("""import json,os,subprocess,tempfile
from pathlib import Path
fixture=Path(tempfile.mkdtemp())
ceiling=os.environ['GIT_CEILING_DIRECTORIES'].split(':')[0]
bounded=subprocess.run(['git','rev-parse','--show-toplevel'],cwd=fixture,capture_output=True,text=True)
subprocess.run(['git','init','-q',str(fixture)],check=True)
nested=fixture/'nested'
nested.mkdir()
valid=subprocess.check_output(['git','rev-parse','--show-toplevel'],cwd=nested,text=True).rstrip('\\n')
source=subprocess.check_output(['git','rev-parse','--show-toplevel'],text=True).rstrip('\\n')
Path('../git-boundary.json').write_text(json.dumps({'bounded':bounded.returncode,
    'fixture':str(fixture),'valid':valid,'source':source,'tmp':os.environ['TMPDIR'],
    'ceiling':str(Path(ceiling).resolve())}))
""")
        supplemental = self.parent / 'boundary.mk'
        supplemental.write_text('audit:\n\t@python3 scripts/verification_run.py -- python3 ../git-boundary.py\n')
        result = self.make('-f', str(self.root / 'Makefile'), '-f', str(supplemental),
                           'audit', 'specs-check', 'FLOW_ARGS=--base HEAD', parent=parent)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        observation = json.loads((self.parent / 'git-boundary.json').read_text())
        self.assertNotEqual(observation['bounded'], 0, observation)
        self.assertEqual(observation['valid'], observation['fixture'])
        self.assertEqual(observation['source'], str(self.root))
        self.assertEqual(observation['ceiling'], observation['tmp'])
        self.assertEqual(Path(observation['tmp']).parent.parent, parent)
        self.assertFalse(Path(observation['tmp']).exists())

    def test_git_ceiling_origins_preserve_native_exports_and_real_discovery(self):
        self.git('init', '-q', str(self.parent))
        observer = self.parent / 'ceiling-observe.py'
        observer.write_text("""import json,os,subprocess,sys,tempfile
from pathlib import Path
def discover(path):
    result=subprocess.run(['git','rev-parse','--show-toplevel'],cwd=path,capture_output=True,text=True,timeout=5)
    return {'status':result.returncode,'root':result.stdout.rstrip('\\n')}
fixture=Path(tempfile.mkdtemp()); bounded=discover(fixture)
subprocess.run(['git','init','-q',str(fixture)],check=True,timeout=5)
nested=fixture/'nested'; nested.mkdir()
state={'ceiling':os.environ.get('GIT_CEILING_DIRECTORIES'),'tmp':os.environ['TMPDIR'],
       'caller':discover(os.environ['CALLER_DISCOVERY']),'bounded':bounded,
       'nested':discover(nested),'fixture':str(fixture)}
if os.environ.get('CODE_FACTORY_VERIFY_RUN') and len(sys.argv)==1:
    process=subprocess.Popen([sys.executable,'scripts/verification_run.py','--',sys.executable,
                              __file__,'managed'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    try:
        stdout,stderr=process.communicate(timeout=10)
    finally:
        if process.poll() is None: process.terminate()
        try: process.communicate(timeout=18)
        except subprocess.TimeoutExpired:
            process.kill(); process.communicate(timeout=2)
    if process.returncode: raise RuntimeError(stdout+stderr)
    state['managed']=json.loads(stdout)
    state['restored_ceiling']=os.environ.get('GIT_CEILING_DIRECTORIES')
print(json.dumps(state))
""")
        control = self.parent / 'native.mk'
        control.write_text('audit:\n\t@python3 ../ceiling-observe.py\n')
        supplemental = self.parent / 'ceiling.mk'
        value_file = self.parent / 'ceiling-value'
        (self.parent / 'unchanged-include.mk').write_text('# Existing caller source.\n')
        (self.parent / 'unchanged-output').write_text('Existing caller output.\n')
        for name in ('ordinary', ' dollar$#雪\\path\r\n'):
            boundary = self.parent / name
            boundary.mkdir()
            (boundary / 'empty').mkdir()
            alias = self.parent / ('alias-' + str(len(name)))
            alias.symlink_to(boundary, target_is_directory=True)
            values = [str(boundary), ':' + str(boundary) + '::', str(alias), ':' + str(alias), '']
            if name == 'ordinary':
                values.append(str(boundary) + ':' + ':'.join(['/nonexistent'] * 6000))
            for value in values:
                value_file.write_bytes(os.fsencode(value) + b'\r\n')
                for origin in ('environment', 'command', 'command-unexport', 'exported-recursive',
                               'exported-simple', 'unexported', 'explicit-unexport'):
                    with self.subTest(name=name, value=value, origin=origin):
                        env = dict(self.env, TMPDIR=str(self.parent),
                                   CALLER_DISCOVERY=str(boundary / 'empty'))
                        env.pop('GIT_CEILING_DIRECTORIES', None)
                        definition = ''
                        arguments = []
                        if origin == 'environment':
                            env['GIT_CEILING_DIRECTORIES'] = value
                        elif origin.startswith('command'):
                            arguments = ['GIT_CEILING_DIRECTORIES=$(file <../ceiling-value)']
                            if origin == 'command-unexport':
                                definition = 'unexport GIT_CEILING_DIRECTORIES\n'
                        else:
                            definition = ('CALLER_CEILING = ignored\n' if origin.endswith('recursive') else
                                          'CALLER_CEILING = $(file <../ceiling-value)\n')
                            definition += '' if origin in ('unexported', 'explicit-unexport') else 'export '
                            definition += 'GIT_CEILING_DIRECTORIES ' + (
                                ':=' if origin.endswith('simple') else '=') + ' $(CALLER_CEILING)\n'
                            definition += ('CALLER_CEILING = $(file <../ceiling-value)\n'
                                           if origin.endswith('recursive') else 'CALLER_CEILING = ignored\n')
                            if origin == 'explicit-unexport':
                                definition += 'unexport GIT_CEILING_DIRECTORIES\n'
                        supplemental.write_text(definition +
                            'include ../unchanged-include.mk\n'
                            'audit: ../unchanged-output\n\t@python3 ../ceiling-observe.py\n'
                            '../unchanged-include.mk ../unchanged-output:\n\t@echo unexpected-remake >> ../remakes\n'
                            'Makefile:\n\t@echo unexpected-remake >> ../remakes\n')
                        observations = []
                        for selected in (control, self.root / 'Makefile'):
                            result = subprocess.run(['make', '--no-print-directory', '-f', str(selected),
                                '-f', str(supplemental), 'audit', *arguments], cwd=self.root,
                                env=env, capture_output=True, text=True, timeout=30)
                            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                            observations.append(json.loads(result.stdout))
                        native, owned = observations
                        self.assertEqual(owned['ceiling'], native['ceiling'])
                        self.assertEqual(owned['restored_ceiling'], native['ceiling'])
                        self.assertEqual(owned['caller'], native['caller'])
                        managed = owned['managed']
                        expected = owned['tmp'] + (':' + native['ceiling'] if native['ceiling'] else '')
                        self.assertEqual(managed['ceiling'], expected)
                        self.assertEqual(managed['caller'], native['caller'])
                        self.assertEqual(owned['bounded']['status'], 0, owned)
                        self.assertEqual(managed['bounded']['status'], 128, managed)
                        self.assertEqual(managed['nested']['status'], 0, managed)
                        self.assertEqual(managed['nested']['root'], managed['fixture'])
                        self.assertFalse((self.parent / 'remakes').exists())

    def test_git_ceiling_reexec_and_nested_clients_keep_separate_native_boundaries(self):
        self.git('init', '-q', str(self.parent))
        boundaries = [self.parent / name for name in ('parent-boundary', 'child-boundary', 'sibling-boundary')]
        for boundary in boundaries:
            (boundary / 'empty').mkdir(parents=True)
        observer = self.parent / 'client-ceiling.py'
        observer.write_text("""import json,os,subprocess,sys,time,tempfile
from pathlib import Path
sys.path.insert(0,"scripts")
from specs_lib.isolation import isolated
def discover(path):
    return subprocess.run(['git','rev-parse','--show-toplevel'],cwd=path,capture_output=True,timeout=5).returncode
def snapshot():
    state={'run':os.environ['CODE_FACTORY_VERIFY_RUN'],'ceiling':os.environ['GIT_CEILING_DIRECTORIES'],
           'discovery':[discover(path) for path in sys.argv[2:]]}
    with isolated(Path.cwd()):
        state['managed_ceiling']=os.environ['GIT_CEILING_DIRECTORIES']
        state['bounded']=discover(tempfile.mkdtemp())
    return state
before=snapshot()
if sys.argv[1]=='parent':
    children=[]
    try:
        for index,path in enumerate(sys.argv[3:]):
            command=[sys.executable,'scripts/verification_run.py','--','make','--no-print-directory',
                     '-f','Makefile','-f','../child.mk','child','CLIENT_ID='+str(index),
                     'GIT_CEILING_DIRECTORIES='+str(Path(path).parent)]
            children.append(subprocess.Popen(command,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True))
        observations=[]
        for child in children:
            stdout,stderr=child.communicate(timeout=10)
            if child.returncode: raise RuntimeError(stdout+stderr)
            observations.append(json.loads(stdout))
        Path('../client-results.json').write_text(json.dumps({'before':before,'children':observations,'after':snapshot()}))
    finally:
        for child in children:
            if child.poll() is None: child.terminate()
            try: child.communicate(timeout=18)
            except subprocess.TimeoutExpired:
                child.kill(); child.communicate(timeout=2)
else:
    if sys.argv[1]=='child':
        Path('../client-ready-'+os.environ['CLIENT_ID']).touch()
        deadline=time.monotonic()+5
        while len(list(Path('..').glob('client-ready-*')))<2:
            if time.monotonic()>deadline: raise RuntimeError('concurrent client did not become ready')
            time.sleep(.01)
    print(json.dumps(before))
""")
        paths = ' '.join(str(boundary / 'empty') for boundary in boundaries)
        supplemental = self.parent / 'parent.mk'
        supplemental.write_text('include ../generated-ceiling.mk\n'
            '../generated-ceiling.mk:\n\t@touch $@\n'
            'verification-startup += $(file >>../ceiling-startups,$(shell echo $$PPID) $(CODE_FACTORY_VERIFY_RUN))\n'
            'parent:\n\t@python3 ../client-ceiling.py parent ' + paths + '\n'
            'after:\n\t@python3 ../client-ceiling.py after ' + paths + ' > ../client-after.json\n')
        (self.parent / 'child.mk').write_text('child:\n\t@python3 ../client-ceiling.py child ' + paths + '\n')
        result = self.make('-f', 'Makefile', '-f', str(supplemental), 'parent', 'after',
                           'GIT_CEILING_DIRECTORIES=' + str(boundaries[0]), parent=self.parent)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        state = json.loads((self.parent / 'client-results.json').read_text())
        self.assertEqual(state['before'], state['after'])
        self.assertEqual(state['before'], json.loads((self.parent / 'client-after.json').read_text()))
        self.assertTrue(all(child['run'] == state['before']['run'] for child in state['children']))
        self.assertEqual(state['before']['discovery'], [128, 0, 0])
        self.assertEqual([child['discovery'] for child in state['children']], [[0, 128, 0], [0, 0, 128]])
        self.assertTrue(all(row['bounded'] == 128
                            for row in [state['before'], *state['children']]))
        starts = [line.split() for line in (self.parent / 'ceiling-startups').read_text().splitlines()]
        self.assertGreaterEqual(len(starts), 2)
        self.assertEqual(len({row[0] for row in starts}), 1, starts)
        self.assertEqual(len({row[1] for row in starts}), 1, starts)
        self.assertFalse(Path(state['before']['run']).exists())

    def test_included_make_source_alias_keeps_native_shell_and_git_boundary(self):
        self.git('init', '-q', str(self.parent))
        boundary = self.parent / 'included-boundary'
        (boundary / 'empty').mkdir(parents=True)
        alias = self.parent / 'included Makefile.mk'
        alias.symlink_to(self.root / 'Makefile')
        observer = self.parent / 'included-ceiling.py'
        observer.write_text("""import json,os,subprocess,tempfile
from pathlib import Path
def discover(path):
    return subprocess.run(['git','rev-parse','--show-toplevel'],cwd=path,capture_output=True,timeout=5).returncode
print(json.dumps({'caller':discover(os.environ['CALLER_DISCOVERY']),
    'fixture':discover(tempfile.mkdtemp()),'tmp':os.environ['TMPDIR'],
    'ceiling':os.environ['GIT_CEILING_DIRECTORIES']}))
""")
        lead = self.parent / 'include.mk'
        for selection in (str(self.root / 'Makefile'), '../checkout/Makefile', str(alias)):
            with self.subTest(selection=selection):
                lead.write_text('include ' + selection.replace(' ', '\\ ') + '\n'
                    'SHELL := /bin/bash\n.SHELLFLAGS := -ec\n'
                    'export GIT_CEILING_DIRECTORIES = ' + str(boundary) + '\n'
                    'export CALLER_DISCOVERY = ' + str(boundary / 'empty') + '\n'
                    '.DEFAULT_GOAL := audit\naudit:\n'
                    '\t@[[ -n "$$BASH_VERSION" ]]\n\t@python3 scripts/verification_run.py -- python3 ../included-ceiling.py\n')
                result = self.make('-f', str(lead), parent=self.parent)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                state = json.loads(result.stdout)
                self.assertEqual(state['caller'], 128)
                self.assertEqual(state['fixture'], 128)
                self.assertEqual(state['ceiling'], state['tmp'] + ':' + str(boundary))

    def test_caller_extra_prerequisites_keep_native_order_and_managed_entry_bounds(self):
        self.git('init', '-q', str(self.parent))
        observer = self.parent / 'extra-observe.py'
        observer.write_text("""import json,os,subprocess,sys,tempfile
from pathlib import Path
sys.path.insert(0,'scripts')
from specs_lib.isolation import isolated
state={'event':sys.argv[1],'run':os.environ.get('CODE_FACTORY_VERIFY_RUN')}
with isolated(Path.cwd()):
    fixture=tempfile.mkdtemp()
    state['bounded']=subprocess.run(['git','rev-parse','--show-toplevel'],cwd=fixture,capture_output=True).returncode
    unbounded=dict(os.environ);unbounded.pop('GIT_CEILING_DIRECTORIES',None)
    state['control']=subprocess.run(['git','rev-parse','--show-toplevel'],cwd=fixture,env=unbounded,capture_output=True).returncode
with Path('../extra-events.jsonl').open('a') as output: output.write(json.dumps(state)+'\\n')
""")
        before = self.parent / 'before.mk'
        after = self.parent / 'after.mk'
        for location in ('before', 'after', 'target', 'command', 'command-empty'):
            for flags in ((), ('-j3',), ('-k', '-j3')):
                with self.subTest(location=location, flags=flags):
                    declaration = ('audit: .EXTRA_PREREQS := caller-prerequisite\n' if location == 'target'
                                   else '.EXTRA_PREREQS := caller-prerequisite\n')
                    before.write_text(declaration if location == 'before' else '')
                    after.write_text(('' if location == 'before' else declaration) +
                        'caller-prerequisite:\n\t@python3 ../extra-observe.py prerequisite\n'
                        'audit:\n\t@python3 ../extra-observe.py audit\n')
                    log = self.parent / 'extra-events.jsonl'
                    log.unlink(missing_ok=True)
                    result = self.make(*flags, '-f', str(before), '-f', str(self.root / 'Makefile'),
                                       '-f', str(after), 'audit',
                                       *(['.EXTRA_PREREQS=caller-prerequisite'] if location == 'command'
                                         else ['.EXTRA_PREREQS='] if location == 'command-empty' else []),
                                       parent=self.parent)
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    events = [json.loads(line) for line in log.read_text().splitlines()]
                    self.assertEqual([event['event'] for event in events],
                                     ['audit'] if location == 'command-empty' else ['prerequisite', 'audit'])
                    self.assertTrue(all(event['run'] for event in events), events)
                    self.assertEqual(len({event['run'] for event in events}), 1)
                    self.assertTrue(all((event['bounded'], event['control']) == (128, 0) for event in events))

    def test_standalone_dependency_install_keeps_recipe_working_directory(self):
        import shlex
        self.git('init', '-q', str(self.parent))
        web = self.root / 'web'
        web.mkdir()
        preinstall = """const fs=require('fs'), os=require('os'), path=require('path'), cp=require('child_process');
const fixture=fs.mkdtempSync(path.join(os.tmpdir(),'npm-git-'));
const args=['rev-parse','--show-toplevel'], options={cwd:fixture,timeout:5000};
const bounded=cp.spawnSync('git',args,options);
const environment={...process.env}; delete environment.GIT_CEILING_DIRECTORIES;
const control=cp.spawnSync('git',args,{...options,env:environment});
fs.writeFileSync('installed.json',JSON.stringify({cwd:process.cwd(),fixture:process.env.CODE_FACTORY_REAL_CODEX,
tmp:os.tmpdir(),ceiling:process.env.GIT_CEILING_DIRECTORIES,bounded:bounded.status,control:control.status}));"""
        package = {'name': 'owned-cwd-fixture', 'version': '1.0.0', 'scripts': {
            'preinstall': 'node -e ' + shlex.quote(preinstall)}}
        (web / 'package.json').write_text(json.dumps(package))
        (web / 'package-lock.json').write_text(json.dumps({
            'name': package['name'], 'version': package['version'], 'lockfileVersion': 3,
            'packages': {'': package}}))
        self.env.update(npm_config_audit='false', npm_config_fund='false',
                        npm_config_ignore_scripts='false')
        result = self.make('npm-deps', parent=self.parent)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        observation = json.loads((web / 'installed.json').read_text())
        self.assertEqual(observation['cwd'], str(web))
        self.assertEqual(observation['fixture'], '0')
        inherited = self.env.get('GIT_CEILING_DIRECTORIES', '')
        self.assertEqual(observation['ceiling'], observation['tmp'] + (':' + inherited if inherited else ''))
        self.assertEqual((observation['bounded'], observation['control']), (128, 0))
        self.assertFalse((self.root / 'installed.json').exists())

    def test_command_line_extra_prerequisites_cannot_prepare_a_contended_checkout(self):
        with isolated(self.root):
            result = self.make('fake-provider', '.EXTRA_PREREQS=', parent=self.parent)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('Cannot acquire verification ownership', result.stderr)
        self.assertFalse((self.root / 'bin').exists(), 'preparation ran before ownership')

    def test_checker_timeout_drains_guardian_and_helper_before_fixture_removal(self):
        unrelated = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)'])
        self.addCleanup(cleanup_process, unrelated)
        case = MakeOwnershipCheckerTests()
        case.setUp()
        try:
            worker = case.parent / 'slow-checker.py'
            worker.write_text('''import json,os,signal,time
from pathlib import Path
root=Path.cwd()
def stop(signum,frame):
    (root/'checker-cleanup-started').touch()
    time.sleep(.2)
    (root/'checker-cleanup-finished').touch()
    raise SystemExit(0)
signal.signal(signal.SIGTERM,stop)
owner=json.loads((Path(os.environ['TMPDIR']).parent/'owner.json').read_text())
(root/'checker-ready.json').write_text(json.dumps({'worker':os.getpid(),'guardian':owner['pid']}))
time.sleep(60)
''')
            with (case.root / 'Makefile').open('a') as makefile:
                makefile.write('\nchecker-timeout:\n\tpython3 scripts/verification_run.py -- python3 ../slow-checker.py\n')
            with self.assertRaises(subprocess.TimeoutExpired):
                case.make('checker-timeout', parent=case.parent, timeout=2)
            state = json.loads((case.root / 'checker-ready.json').read_text())
            self.assertTrue((case.root / 'checker-cleanup-started').exists())
            self.assertTrue((case.root / 'checker-cleanup-finished').exists())
            self.assertTrue(case.root.exists(), 'fixture removed before drain inspection')
            for pid in state.values():
                self.assertFalse(Path(f'/proc/{pid}').exists(), f'owned process {pid} survived')
            self.assertEqual(json.loads((case.root / 'probe-drained.json').read_text())['children'], [])
            self.assertEqual(list(case.parent.glob('.code-factory-verify-*')), [])
            self.assertIsNone(unrelated.poll(), 'checker supervisor touched unrelated child')
        finally:
            self.assertTrue(case.doCleanups(), 'checker fixture cleanup failed')
        self.assertFalse(case.root.exists())

    def checker_direct_timeout(self):
        # Deliberately bypass self.make(), as the native Git-ceiling comparison
        # and interactive stdin probes do. Whole-test supervision must own this
        # failure path before the parent is allowed to delete its fixture.
        worker = self.parent / 'direct-checker.py'
        worker.write_text('''import json,os,signal,time
from pathlib import Path
root=Path.cwd()
def stop(signum,frame):
    time.sleep(.2)
    (root/'direct-cleanup-finished').touch()
    raise SystemExit(0)
signal.signal(signal.SIGTERM,stop)
owner=json.loads((Path(os.environ['TMPDIR']).parent/'owner.json').read_text())
(root/'direct-ready.json').write_text(json.dumps({'worker':os.getpid(),'guardian':owner['pid']}))
time.sleep(60)
''')
        with (self.root / 'Makefile').open('a') as makefile:
            makefile.write('\ndirect-timeout:\n\tpython3 scripts/verification_run.py -- python3 ../direct-checker.py\n')
        subprocess.run(['make', '--no-print-directory', 'direct-timeout'], cwd=self.root,
                       env={**self.env, 'TMPDIR': str(self.parent)},
                       capture_output=True, text=True, timeout=2)

    def test_checker_case_failure_drains_direct_make_before_fixture_removal(self):
        unrelated = subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)'])
        self.addCleanup(cleanup_process, unrelated)
        case = MakeOwnershipCheckerTests()
        case.setUp()
        try:
            with self.assertRaisesRegex(AssertionError, 'TimeoutExpired'):
                case._callTestMethod(case.checker_direct_timeout)
            state = json.loads((case.root / 'direct-ready.json').read_text())
            self.assertTrue((case.root / 'direct-cleanup-finished').exists())
            self.assertTrue(case.root.exists(), 'fixture deleted before independent drain')
            self.assertTrue(case.probe_drained)
            self.assertEqual(json.loads((case.root / 'probe-drained.json').read_text())['children'], [])
            for pid in state.values():
                self.assertFalse(Path(f'/proc/{pid}').exists(), f'owned process {pid} survived')
            self.assertIsNone(unrelated.poll(), 'case supervisor touched unrelated child')
        finally:
            self.assertTrue(case.doCleanups(), 'direct checker fixture cleanup failed')
        self.assertFalse(case.root.exists())

    def test_malformed_temporary_parent_transport_fails_before_ownership(self):
        for encoded in ('terminal\\', '\\x'):
            with self.subTest(encoded=encoded):
                result = subprocess.run([sys.executable, 'scripts/verification_run.py',
                    '--acquire-make-owner', str(os.getpid()), encoded], cwd=self.root,
                    env=self.env, capture_output=True, text=True, timeout=5)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('Invalid Make temporary-parent encoding', result.stderr)
                self.assertFalse((self.root / '.git/verification.lock').exists())

    def test_opt_in_targets_preserve_provider_policy_without_services(self):
        self.git('init', '-q', str(self.parent))
        caller = self.parent / 'caller-boundary'
        caller.mkdir()
        tools = self.parent / 'recording-tools'
        tools.mkdir()
        recorder = tools / 'recorder'
        recorder.write_text('#!' + sys.executable + '\n' + """import json,os,subprocess,sys,tempfile
from pathlib import Path
command=Path(sys.argv[0]).name
if command=='python3' and sys.argv[1:2]!=['scripts/test_live_connection.py']:
    os.execv(sys.executable,[sys.executable,*sys.argv[1:]])
fixture=tempfile.mkdtemp()
bounded=subprocess.run(['git','rev-parse','--show-toplevel'],cwd=fixture,capture_output=True)
unbounded=dict(os.environ);unbounded.pop('GIT_CEILING_DIRECTORIES',None)
control=subprocess.run(['git','rev-parse','--show-toplevel'],cwd=fixture,env=unbounded,capture_output=True)
Path('../opt-in-observation.json').write_text(json.dumps({'command':command,'argv':sys.argv[1:],
    'real':os.environ['CODE_FACTORY_REAL_CODEX'],'cwd':os.getcwd(),'tmp':os.environ['TMPDIR'],
    'ceiling':os.environ['GIT_CEILING_DIRECTORIES'],'bounded':bounded.returncode,
    'control':control.returncode,'run':os.environ['CODE_FACTORY_VERIFY_RUN']}))
""")
        recorder.chmod(0o755)
        for command in ('go', 'node', 'python3'):
            (tools / command).symlink_to(recorder)
        self.env['PATH'] = str(tools) + ':' + self.env['PATH']
        for goal, command, arguments, real, managed in (
                ('test-real-provider', 'go', ['test', '-count=1', './internal/codex', './internal/app'], '1', True),
                ('connection-test', 'python3', ['scripts/test_live_connection.py', '--full'], '0', False),
                ('connection-sdk-test', 'node', ['scripts/test_mcp_sdk.mjs'], '0', False)):
            with self.subTest(goal=goal):
                result = self.make(goal, 'GIT_CEILING_DIRECTORIES=' + str(caller), parent=self.parent)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                state = json.loads((self.parent / 'opt-in-observation.json').read_text())
                self.assertEqual((state['command'], state['argv'], state['real']), (command, arguments, real))
                self.assertEqual(state['cwd'], str(self.root))
                self.assertEqual(state['ceiling'], state['tmp'] + ':' + str(caller) if managed else str(caller))
                self.assertEqual((state['bounded'], state['control']), (128 if managed else 0, 0))
                self.assertFalse(Path(state['run']).exists())
        environment = dict(self.env, TMPDIR=str(self.parent), CODE_FACTORY_REAL_CODEX='1',
                           GIT_CEILING_DIRECTORIES=str(caller))
        result = subprocess.run([sys.executable, 'scripts/verification_run.py', '--', 'go', 'version'],
                                cwd=self.root, env=environment, capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        state = json.loads((self.parent / 'opt-in-observation.json').read_text())
        self.assertEqual((state['command'], state['argv'], state['real']), ('go', ['version'], '0'))
        self.assertEqual(state['cwd'], str(self.root))
        self.assertEqual((state['bounded'], state['control']), (128, 0))

    def test_temporary_parent_origins_preserve_literal_values_and_real_checker(self):
        value = self.parent / 'caller-parent'
        observer = self.parent / 'parent-observe.py'
        observer.write_text("""import json,os
from pathlib import Path
Path('../parent-observation.json').write_text(json.dumps({
    'tmp':os.environ['TMPDIR'], 'origin':os.environ['CALLER_TMPDIR_ORIGIN'],
    'ceiling':str(Path(os.environ['GIT_CEILING_DIRECTORIES'].split(':')[0]).resolve())}))
""")
        supplemental = self.parent / 'origin.mk'
        names = ('with spaces', '$cash#parent', 'back\\slash', 'back\\#cash$parent',
                 ' leading', 'trailing ', '\tparent\t', '\nparent\n',
                 'terminal\r', 'terminal\r\n', 'terminal\n\r', 'Ω-中文', 'caller:state', "single'quote")
        for origin in ('environment', 'file', 'command line'):
            container = self.parent / origin
            container.mkdir()
            for name in names:
                with self.subTest(origin=origin, parent=name):
                    parent = container / name
                    parent.mkdir()
                    value.write_bytes(os.fsencode(parent) + b'\r\n')
                    supplemental.write_text(
                        ('' if origin == 'environment' else 'TMPDIR = $(file <../caller-parent)\n') +
                        'export CALLER_TMPDIR_ORIGIN := $(origin TMPDIR)\n'
                        'audit:\n\t@python3 scripts/verification_run.py -- python3 ../parent-observe.py\n')
                    arguments = ['TMPDIR=$(file <../caller-parent)'] if origin == 'command line' else []
                    result = self.make('-f', 'Makefile', '-f', str(supplemental),
                                       'audit', 'specs-check', 'FLOW_ARGS=--base HEAD', *arguments,
                                       parent=parent if origin == 'environment' else self.parent)
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    observation = json.loads((self.parent / 'parent-observation.json').read_text())
                    self.assertEqual(Path(observation['tmp']).parent.parent, parent)
                    self.assertEqual(observation['origin'], origin)
                    self.assertEqual(observation['ceiling'], observation['tmp'])
                    self.assertFalse(Path(observation['tmp']).exists())
                    self.assertIn('scripts/specs.py check --base HEAD', result.stdout)

    def test_supplemental_default_goal_preserves_real_checker_comparison(self):
        (self.root / 'app.py').write_text('def perform():\n    return 2\n')
        self.commit('Change requiring default-goal reconciliation')
        supplemental = self.parent / 'default.mk'
        supplemental.write_text('.DEFAULT_GOAL := specs-check\n'
                                'FLOW_ARGS = --base main --merge-base\n')
        result = self.make('-f', 'Makefile', '-f', str(supplemental))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('reconciliation required', result.stdout + result.stderr)
        overridden = self.make('-f', 'Makefile', '-f', str(supplemental),
                               'FLOW_ARGS=--base HEAD')
        self.assertEqual(overridden.returncode, 0, overridden.stdout + overridden.stderr)
        self.assertIn('scripts/specs.py check --base HEAD', overridden.stdout)

    def test_stdin_makefile_and_relative_directory_preserve_real_comparison(self):
        (self.root / 'app.py').write_text('def perform():\n    return 2\n')
        self.commit('Change requiring reconciliation against main')
        included = self.parent / 'stdin-include.mk'
        included.write_text('STDIN_INCLUDE_VISITS += visit\n')
        source = ('include ../stdin-include.mk\n'
                  'ifneq ($(words $(STDIN_INCLUDE_VISITS)),1)\n'
                  '$(error stdin include loaded twice)\nendif\n'
                  'FLOW_ARGS = --base main --merge-base\n')
        for selection in [('-f', 'Makefile', '-f', '-'), ('-f-', '-f', 'Makefile')]:
            with self.subTest(selection=selection):
                result = self.make('-C', 'checkout', *selection, 'specs-check',
                                   parent=self.parent, input=source, directory=self.parent)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('reconciliation required', result.stdout + result.stderr)
                self.assertNotIn('stdin include loaded twice', result.stderr)
                result = self.make('-C', 'checkout', *selection, 'specs-check',
                                   'FLOW_ARGS=--base HEAD', parent=self.parent,
                                   input=source, directory=self.parent)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(list(self.parent.glob('.code-factory-verify-*')), [])

    def test_stdin_survives_native_makefile_remake(self):
        (self.root / 'app.py').write_text('def perform():\n    return 2\n')
        self.commit('Change requiring reconciliation against main')
        source = ('include ../generated.mk\n'
                  '../generated.mk:\n\t@touch $@\n'
                  'verification-startup += $(file >>../native-owners,$(CODE_FACTORY_VERIFY_RUN))\n'
                  'FLOW_ARGS = --base main --merge-base\n')
        result = self.make('-f', 'Makefile', '-f', '-', 'specs-check',
                           parent=self.parent, input=source)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('reconciliation required', result.stdout + result.stderr)
        self.assertTrue((self.parent / 'generated.mk').exists())
        owners = (self.parent / 'native-owners').read_text().splitlines()
        self.assertGreaterEqual(len(owners), 2)
        self.assertEqual(len(set(owners)), 1, owners)

    def test_partial_make_owner_readiness_fails_before_work_and_cleans_state(self):
        helper = self.root / 'scripts/specs_lib/make_owner.py'
        source = helper.read_text().replace('message(process.stdout)',
                                           'message(process.stdout, timeout=.2)')
        source = source.replace("os.write(sys.stdout.fileno(), b'owner:' + encoded + b'\\n')",
                                "os.write(sys.stdout.fileno(), b'owner:')\n                time.sleep(60)")
        helper.write_text(source)
        result = self.make('specs-check', parent=self.parent)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('did not become ready', result.stderr)
        self.assertNotIn('command specifications --', result.stdout)
        self.assertEqual(list(self.parent.glob('.code-factory-verify-*')), [])
        with isolated(self.root):
            pass

    def test_stdin_alias_reaches_existing_timed_worker_with_real_comparison(self):
        (self.root / 'app.py').write_text('def perform():\n    return 2\n')
        self.commit('Change requiring aliased worker reconciliation')
        source = ('.DEFAULT_GOAL := selected-check\n.PHONY: selected-check\n'
                  'selected-check: ci\nFLOW_BASE = main\n')
        result = self.make('-f', 'Makefile', '-f', '-', parent=self.parent, input=source)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('reconciliation required', result.stdout + result.stderr)
        self.assertNotIn('Cannot preserve Make stdin', result.stderr)

    def test_standalone_stdin_uses_native_source_without_capture(self):
        decoy = self.parent / 'GmABCDEF'
        lead = self.parent / 'lead.mk'
        lead.write_text('$(shell touch ' + str(decoy) + '; chmod 600 ' + str(decoy) + ')\n'
                        'include ' + str(decoy) + '\n')
        result = self.make('-f', str(lead), '-f', 'Makefile', '-f', '-',
                           'specs-check', parent=self.parent, input='FLOW_ARGS = --base HEAD\n')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('command specifications --', result.stdout)
        self.assertEqual(list(self.parent.glob('.code-factory-make-stdin-*')), [])
        self.assertEqual(list(self.parent.glob('.code-factory-verify-*')), [])
        self.assertTrue(decoy.exists())

    def test_large_stdin_has_no_new_size_cap_and_retained_source_is_private(self):
        source = '# ' + 'x' * (256 * 1024) + '\n'
        self.assertEqual(len(source.encode()), 262147)
        result = self.make('-f', 'Makefile', '-f', '-', 'ci', 'FLOW_BASE=main',
                           parent=self.parent, input=source)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn('x' * 100, result.stdout + result.stderr)
        self.assertEqual(list(self.parent.glob('.code-factory-verify-*')), [])

    def retained_stdin_lifetime(self, temporary_parent):
        observed = self.parent / 'capture-path'
        source = ('verification-capture-stdin += $(if $(filter ci,$(verification-goals)),$(file >../capture-path,$(CODE_FACTORY_MAKE_STDIN)))\n'
                  'verify-steps: hold\n'
                  'hold:\n\t@sleep 30\n')
        child = subprocess.Popen(['make', '--no-print-directory', '-f', 'Makefile', '-f', '-', 'ci', 'FLOW_BASE=main'],
                                 cwd=self.root, env={**self.env, 'TMPDIR': str(temporary_parent)},
                                 stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                 stderr=subprocess.PIPE, text=True, start_new_session=True)
        self.addCleanup(cleanup_process, child)
        child.stdin.write(source)
        child.stdin.close()
        child.stdin = None
        deadline = time.monotonic() + 5
        while not observed.exists() or not observed.read_text().strip():
            self.assertIsNone(child.poll())
            self.assertLess(time.monotonic(), deadline, 'stdin capture did not become visible')
            time.sleep(.01)
        token = observed.read_text().strip()
        try:
            captured = Path(os.fsdecode(base64.b64decode(token, validate=True)))
        except ValueError:
            # The retained prior-source controls use the original raw-path protocol.
            captured = Path(token)
        self.assertFalse(captured.is_relative_to(self.root))
        if not temporary_parent.is_relative_to(self.root):
            self.assertEqual(captured.parent.parent, temporary_parent)
        self.assertEqual(captured.stat().st_mode & 0o777, 0o600)
        self.assertEqual(captured.parent.stat().st_mode & 0o777, 0o700)
        self.assertEqual(captured.read_text(), source)
        child.send_signal(signal.SIGTERM)
        cleanup_process(child)
        deadline = time.monotonic() + 5
        while captured.parent.exists():
            self.assertLess(time.monotonic(), deadline, 'owner exit did not remove retained stdin')
            time.sleep(.01)
        self.assertTrue(observed.exists())

    def test_stdin_capture_is_private_and_owner_exit_removes_it(self):
        self.retained_stdin_lifetime(self.parent)

    def test_checkout_contained_stdin_parent_uses_private_external_fallback(self):
        self.retained_stdin_lifetime(self.root)

    def test_cancellation_during_stdin_copy_removes_pre_guardian_state(self):
        ready = self.parent / 'copy-ready.json'
        helper = self.root / 'scripts/verification_run.py'
        barrier = ("output.write(source.read(1))\n"
                   "                output.flush()\n"
                   "                import time\n"
                   "                marker = Path(" + repr(str(ready)) + ")\n"
                   "                partial = marker.with_suffix('.partial')\n"
                   "                partial.write_text(json.dumps({'directory':str(retained),'pid':os.getpid(),'guardian':monitor is not None}))\n"
                   "                partial.rename(marker)\n"
                   "                deadline = time.monotonic() + 10\n"
                   "                while time.monotonic() < deadline:\n"
                   "                    time.sleep(.01)\n"
                   "                raise RuntimeError('copy barrier was not cancelled')")
        helper.write_text(helper.read_text().replace('shutil.copyfileobj(source, output)', barrier))
        child = subprocess.Popen(['make', '--no-print-directory', '-f', 'Makefile', '-f', '-', 'ci', 'FLOW_BASE=main'],
                                 cwd=self.root, env={**self.env, 'TMPDIR': str(self.parent)},
                                 stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                 stderr=subprocess.PIPE, text=True, start_new_session=True)
        self.addCleanup(cleanup_process, child)
        child.stdin.write('FLOW_ARGS = --base HEAD\n')
        child.stdin.close()
        child.stdin = None
        deadline = time.monotonic() + 5
        while not ready.exists():
            self.assertIsNone(child.poll())
            self.assertLess(time.monotonic(), deadline, 'copy barrier did not become ready')
            time.sleep(.01)
        state = json.loads(ready.read_text())
        self.assertFalse(state['guardian'])
        retained = Path(state['directory'])
        self.assertFalse(retained.is_relative_to(self.root))
        self.assertTrue(retained.name.startswith('.code-factory-make-stdin-'))
        # The parent removes this fixture only after whole-test supervision has
        # drained the Make/capture descendants, including assertion failures.
        self.assertTrue((retained / 'source.mk').is_file())
        os.killpg(child.pid, signal.SIGTERM)
        cleanup_process(child)
        self.assertNotEqual(child.returncode, 0)
        deadline = time.monotonic() + 5
        while retained.exists():
            self.assertLess(time.monotonic(), deadline, 'pre-guardian source survived cancellation')
            time.sleep(.01)
        self.assertTrue(ready.exists())

    def test_literal_external_temporary_parent_preserves_stdin_path(self):
        for name in ('external $cash#parent', 'external \\#cash$parent', 'external \\\\#cash$parent',
                     ' leading', 'trailing ', '\tleading-tab', 'trailing-tab\t',
                     '\nleading-newline', 'trailing-newline\n', 'line\nparent', 'Ω-中文'):
            with self.subTest(parent=name):
                parent = self.parent / name
                parent.mkdir()
                unrelated = parent / 'kept.txt'
                unrelated.write_text('caller state')
                result = self.make('-f', 'Makefile', '-f', '-', 'ci', 'FLOW_BASE=main',
                                   parent=parent, input='FLOW_ARGS = --base main --merge-base\n')
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(unrelated.read_text(), 'caller state')
                deadline = time.monotonic() + 5
                while list(parent.glob('.code-factory-make-stdin-*')):
                    self.assertLess(time.monotonic(), deadline, 'literal-parent capture was not removed')
                    time.sleep(.01)

    def test_stdin_dry_run_exposes_no_source_and_creates_no_journal(self):
        secret = 'PRIVATE_STDIN_SENTINEL'
        result = self.make('-n', '-f', 'Makefile', '-f', '-', 'ci', 'FLOW_BASE=main',
                           parent=self.parent, input='PRIVATE_SETTING = ' + secret + '\n')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn(secret, result.stdout + result.stderr)
        self.assertFalse((self.root / 'verification-results').exists())
        self.assertEqual(list(self.parent.glob('.code-factory-verify-*')), [])

    def test_inside_checkout_parent_runs_real_gate_and_preserves_untracked_inputs(self):
        for parent in (self.root, self.root / 'scratch'):
            parent.mkdir(exist_ok=True)
            with self.subTest(parent=parent):
                result = self.make('ci', 'FLOW_BASE=main', parent=parent)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertEqual(list(parent.glob('.code-factory-verify-*')), [])
                unrelated = parent / 'untracked_source.py'
                unrelated.write_text('unrelated source must remain visible\n')
                result = self.make('ci', 'FLOW_BASE=main', parent=parent)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('Unmapped file', result.stderr)
                self.assertIn('untracked_source.py', result.stderr)
                self.assertEqual(unrelated.read_text(), 'unrelated source must remain visible\n')
                self.assertEqual(list(parent.glob('.code-factory-verify-*')), [])
                unrelated.unlink()

    def test_external_temporary_parent_runs_real_gate(self):
        result = self.make('ci', 'FLOW_BASE=main', parent=self.parent)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(list(self.parent.glob('.code-factory-verify-*')), [])


class MakeOwnerFieldTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.run = Path(self.temporary.name) / "private $ #'\nrun\r\n"
        self.run.mkdir()
        descriptor = os.open(self.run, os.O_RDONLY | os.O_DIRECTORY)
        self.addCleanup(os.close, descriptor)
        self.state = {'pid': os.getpid(), 'directory_fd': descriptor,
                      'root': "checkout $ #'\nroot\r\n", 'token': 'fixture-token'}
        (self.run / 'owner.json').write_text(json.dumps(self.state))
        self.encoded = base64.b64encode(os.fsencode(self.run)).decode('ascii')

    def test_complete_batch_preserves_raw_values_private_modes_and_legacy_fields(self):
        from specs_lib import make_owner
        with patch.dict(os.environ, {'GIT_CEILING_DIRECTORIES': '/inherited'}):
            directory = Path(make_owner.fields(self.encoded))
            values = {'CODE_FACTORY_VERIFY_RUN': str(self.run),
                      'CODE_FACTORY_VERIFY_ROOT': self.state['root'],
                      'CODE_FACTORY_VERIFY_TOKEN': 'fixture-token',
                      'CODE_FACTORY_VERIFY_TMPDIR': str(self.run / 'tmp'),
                      'TMPDIR': str(self.run / 'tmp'),
                      'GIT_CEILING_DIRECTORIES': str(self.run / 'tmp') + ':/inherited'}
            self.assertEqual(set(path.name for path in directory.iterdir()), set(values))
            self.assertEqual(directory.stat().st_mode & 0o777, 0o700)
            self.assertNotIn('\n', str(directory))
            for key, value in values.items():
                path = directory / key
                self.assertEqual(path.read_bytes(), os.fsencode(value) + b'\r\n')
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
                self.assertEqual(Path(make_owner.field(self.encoded, key)).read_bytes(), path.read_bytes())

    def test_failed_batch_removes_partial_fields_before_returning_an_alias(self):
        from specs_lib import make_owner
        original = Path.open
        def fail(path, *args, **kwargs):
            if path.name == 'TMPDIR' and args == ('xb',):
                raise OSError('controlled field write failure')
            return original(path, *args, **kwargs)
        with patch.object(Path, 'open', fail):
            with self.assertRaisesRegex(OSError, 'controlled field write failure'):
                make_owner.fields(self.encoded)
        self.assertEqual(list((self.run / 'make-fields').iterdir()), [])


if __name__ == "__main__":
    if len(sys.argv) == 5 and sys.argv[1] == '--checker-worker':
        case = MakeOwnershipCheckerTests(sys.argv[4])
        case._checker_worker = True
        case.root, case.parent = Path(sys.argv[2]), Path(sys.argv[3])
        case.env = dict(os.environ)
        case.probe_drained = True
        result = unittest.TextTestRunner().run(case)
        sys.exit(0 if result.wasSuccessful() else 1)
    if len(sys.argv) >= 6 and sys.argv[1] in ('--supervise-probe', '--supervise-checker'):
        sys.exit(supervise_probe(Path(sys.argv[2]), float(sys.argv[3]), sys.argv[4:],
                                 drain_timeout=35 if sys.argv[1] == '--supervise-checker' else 0))
    unittest.main()
