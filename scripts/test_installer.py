#!/usr/bin/env python3
"""Exercise packaged binaries with an isolated HOME and a process-backed service fixture."""
import hashlib
import ctypes
import json
import os
import pathlib
import shutil
import signal
import socket
import sqlite3
import subprocess
import sys
import tarfile
import tempfile
import time
import unittest
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]
INTERRUPTED_UPGRADE_PROBE = sys.argv[1:]==['--interrupted-upgrade-probe']
SYSTEMCTL = '''#!/usr/bin/python3
import json, os, pathlib, signal, subprocess, sys, time
home=pathlib.Path(os.environ['HOME'])
data=home/'data with spaces'
args=sys.argv[1:]
args=[a for a in args if a!='--user']
operation=args[0]
if operation=='is-active':
    sys.exit(1)
for name in args[1:]:
    if not name.endswith('.service'): continue
    pidfile=home/(name+'.pid')
    if operation in ('stop','disable','restart') and pidfile.exists():
        try:
            pid=int(pidfile.read_text()); os.kill(pid,signal.SIGTERM)
            for _ in range(200):
                try:
                    state=pathlib.Path('/proc')/str(pid)/'stat'
                    if not state.exists() or state.read_text().split()[2]=='Z': break
                except FileNotFoundError: break
                time.sleep(.01)
        except ProcessLookupError: pass
        pidfile.unlink(missing_ok=True)
    if name=='code-factory.service' and operation in ('restart','start','enable') and not pidfile.exists():
        if operation=='enable' and '--now' not in args: continue
        runtime=json.loads((data/'runtime.json').read_text())
        if os.environ.get('CF_INSTALL_FAIL')=='1': sys.exit(1)
        gate=os.environ.get('CF_INSTALL_RESTART_GATE')
        if operation=='restart' and gate:
            marker=pathlib.Path(gate)
            temporary=marker.with_name(marker.name+'.tmp')
            temporary.write_text(json.dumps({'pid':os.getpid()}))
            temporary.replace(marker)
            while True: time.sleep(1)
        with (home/'daemon.log').open('ab') as log:
            p=subprocess.Popen([str(home/'.local/bin/code-factory'),'serve','--data-dir',str(data),'--listen',runtime['listen']],stdout=log,stderr=log,start_new_session=True)
        pidfile.write_text(str(p.pid))
'''
class InstallerTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.built=tempfile.TemporaryDirectory(prefix='factory-second-release-')
        cls.second=pathlib.Path(cls.built.name)/'code-factory'
        subprocess.run(['go','build','-trimpath','-ldflags','-X codefactory/internal/buildinfo.Version=test-upgrade','-o',str(cls.second),'./cmd/code-factory'],cwd=ROOT,check=True)
        cls.archive = max((ROOT/'dist').glob('*.tar.gz'), key=lambda p:p.stat().st_mtime)
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(prefix='factory install ')
        self.home=pathlib.Path(self.temp.name)
        self.data=self.home/'data with spaces'
        self.deps=self.home/'deps with spaces'
        self.deps.mkdir()
        (self.deps/'systemctl').write_text(SYSTEMCTL)
        for name in ('git','gh','tunnel-client','codex'):
            (self.deps/name).write_text('#!/bin/sh\n'+('echo "codex-cli 0.161.0"\n' if name=='codex' else 'exit 0\n'))
        for p in self.deps.iterdir():p.chmod(0o700)
        with tarfile.open(self.archive) as tar:tar.extractall(self.home, filter='data')
        self.release=next(self.home.glob('code-factory-*'))
        self.binary=self.release/'code-factory'
        with socket.socket() as sock:sock.bind(('127.0.0.1',0));self.port=sock.getsockname()[1]
        self.env={**os.environ,'HOME':str(self.home),'XDG_CONFIG_HOME':str(self.home/'config with spaces'),'PATH':str(self.deps)+':/usr/bin:/bin'}
        self.env.pop('XDG_DATA_HOME',None)
    def tearDown(self):
        pidfile=self.home/'code-factory.service.pid'
        fixture_pid=int(pidfile.read_text()) if INTERRUPTED_UPGRADE_PROBE and pidfile.exists() else None
        subprocess.run([str(self.deps/'systemctl'),'--user','stop','code-factory.service'],env=self.env,capture_output=True)
        if fixture_pid is not None:
            deadline=time.monotonic()+5
            while True:
                try:finished,_=os.waitpid(fixture_pid,os.WNOHANG)
                except ChildProcessError:break
                if finished:break
                if time.monotonic()>=deadline:self.fail('fixture daemon survived teardown')
                time.sleep(.01)
        self.temp.cleanup()
    def run_cli(self,command,*args,ok=True,env=None):
        result=subprocess.run([str(self.binary),command,'--data-dir',str(self.data),'--listen',f'127.0.0.1:{self.port}',*args],env=env or self.env,capture_output=True,text=True,timeout=45)
        if ok:self.assertEqual(result.returncode,0,result.stderr)
        else:self.assertNotEqual(result.returncode,0,result.stdout)
        return result
    def install(self):return self.run_cli('install','--from',str(self.release))
    def mutate_release(self, version="test-broken"):

        new=self.home/'new release';shutil.copytree(self.release,new)
        manifest=json.loads((new/'release.json').read_text());manifest['version']=version
        if version=='test-upgrade':shutil.copy2(self.second,new/'code-factory')
        (new/'release.json').write_text(json.dumps(manifest))
        # Same executable with a different version cannot satisfy the health check;
        # This fixture deliberately exercises a failed upgrade's automatic recovery.
        sums=''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+str(p.relative_to(new))+'\n' for p in sorted(new.rglob('*')) if p.is_file() and p.name!='SHA256SUMS')
        (new/'SHA256SUMS').write_text(sums)
        return new
    def test_install_rerun_paths_backup_failed_upgrade_rollback_uninstall(self):
        self.install();token=(self.data/'owner.token').read_bytes()
        self.install();self.assertEqual(token,(self.data/'owner.token').read_bytes())
        installed=self.home/'.local/lib/code-factory'
        (installed/'plugin/.app.json').write_text('{"apps":{"code-factory":{"id":"asdk_app_fixture","required":true}}}')
        new=self.mutate_release()
        self.run_cli('upgrade','--from',str(new),ok=False)
        self.assertEqual((installed/'current').resolve().name,self.release.name.removeprefix('code-factory-'))
        self.assertEqual(token,(self.data/'owner.token').read_bytes())
        self.assertTrue(list((installed/'backups').glob('*.db')))
        self.run_cli('rollback') # uses the pending operation's pre-migration snapshot
        shutil.rmtree(new)
        new=self.mutate_release('test-upgrade')
        self.run_cli('upgrade','--from',str(new))
        self.assertEqual((installed/'current').resolve().name,'test-upgrade')
        self.assertIn('apps',json.loads((installed/'plugin/plugin.json').read_text())['extensions']['com.openai'])
        self.run_cli('rollback')
        self.assertEqual((installed/'current').resolve().name,self.release.name.removeprefix('code-factory-'))
        self.assertTrue((installed/'plugin/.app.json').exists())
        self.run_cli('uninstall');self.assertTrue((self.data/'state.db').exists());self.assertTrue((self.data/'vault.key').exists())
        self.assertFalse((self.home/'.local/bin/code-factory').exists())
        self.install();self.assertTrue((installed/'plugin/.app.json').exists())
    def test_missing_dependency_and_checksum_failure_do_not_install(self):
        (self.deps/'codex').unlink();self.run_cli('install',ok=False)
        self.assertFalse((self.home/'.local/bin/code-factory').exists())
        (self.release/'plugin/plugin.json').write_text('{}')
        self.run_cli('install',ok=False)
    def test_port_conflict(self):
        with socket.socket() as sock:
            sock.bind(('127.0.0.1',self.port));sock.listen()
            result=self.run_cli('install',ok=False)
            self.assertIn('occupied',result.stderr)
    def test_partial_service_failure_can_be_rerun(self):
        self.run_cli('install',env={**self.env,'CF_INSTALL_FAIL':'1'},ok=False)
        self.install()
    def test_interrupted_upgrade_pending_state_restores_database_and_archives_new_state(self):
        if not INTERRUPTED_UPGRADE_PROBE:
            probe=run_supervised_probe([sys.executable,__file__,'--interrupted-upgrade-probe'])
            self.assertEqual(probe.returncode,0,probe.stdout+probe.stderr)
            return
        self.install()
        owned_children={int((self.home/'code-factory.service.pid').read_text())}
        token=(self.data/'owner.token').read_bytes()
        installed=self.home/'.local/lib/code-factory'
        committed=(installed/'installation.json').read_bytes()
        old=json.loads(committed)
        self.assertFalse(old.get('previous'))
        with sqlite3.connect(self.data/'state.db') as db:
            db.execute('CREATE TABLE installer_interrupt_sentinel(value TEXT NOT NULL)')
            db.execute('INSERT INTO installer_interrupt_sentinel VALUES (?)',('before-upgrade',))
        def sentinel(path):
            with sqlite3.connect(path.resolve().as_uri()+'?mode=ro',uri=True) as db:
                return db.execute('SELECT value FROM installer_interrupt_sentinel').fetchone()[0]
        marker=self.home/'upgrade-restart-ready'
        new=self.mutate_release('test-upgrade')
        upgrade=subprocess.Popen([str(self.binary),'upgrade','--data-dir',str(self.data),
                                  '--listen',f'127.0.0.1:{self.port}','--from',str(new)],
                                 env={**self.env,'CF_INSTALL_RESTART_GATE':str(marker)},
                                 stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
        group_finished=False
        try:
            deadline=time.monotonic()+10
            while not marker.exists():
                exited=os.waitid(os.P_PID,upgrade.pid,os.WEXITED|os.WNOHANG|os.WNOWAIT)
                if exited is not None:
                    self.fail(f'upgrade exited before fixture restart gate (status {exited.si_status})')
                if time.monotonic()>=deadline:
                    self.fail('upgrade did not reach fixture restart gate within 10 seconds')
                time.sleep(.01)
            gate_pid=json.loads(marker.read_text())['pid']
            self.assertIs(type(gate_pid),int)
            self.assertEqual(os.getpgid(gate_pid),upgrade.pid)
            owned_children.add(gate_pid)
            self.assertIsNone(os.waitid(os.P_PID,upgrade.pid,os.WEXITED|os.WNOHANG|os.WNOWAIT))
            pending_path=installed/'pending.json'
            pending_bytes=pending_path.read_bytes()
            pending=json.loads(pending_bytes)
            self.assertEqual(pending['current'],'test-upgrade')
            self.assertEqual(pending['previous'],old['current'])
            backup=pathlib.Path(pending['backup'])
            self.assertTrue(backup.is_file())
            self.assertEqual(sentinel(backup),'before-upgrade')
            self.assertEqual((installed/'current').resolve().name,'test-upgrade')
            self.assertEqual((installed/'installation.json').read_bytes(),committed)
            self.assertFalse((self.home/'code-factory.service.pid').exists())

            os.killpg(upgrade.pid,signal.SIGKILL)
            upgrade.communicate(timeout=5)
            group_finished=True
            self.assertEqual(upgrade.returncode,-signal.SIGKILL)
            # A hard kill skips automatic recovery: fresh rollback must select
            # pending.json, since the committed installation has no previous.
            self.assertEqual(pending_path.read_bytes(),pending_bytes)
            self.assertEqual((installed/'current').resolve().name,'test-upgrade')
            self.assertEqual((installed/'installation.json').read_bytes(),committed)
            self.assertFalse((self.home/'code-factory.service.pid').exists())
            with sqlite3.connect(self.data/'state.db') as db:
                db.execute('UPDATE installer_interrupt_sentinel SET value=?',('after-interruption',))
            self.assertEqual(sentinel(self.data/'state.db'),'after-interruption')
            self.assertEqual(list((installed/'backups').glob('pre-rollback-*.db')),[])

            self.run_cli('rollback')
            self.assertEqual((installed/'current').resolve().name,old['current'])
            self.assertFalse(pending_path.exists())
            restored=json.loads((installed/'installation.json').read_text())
            self.assertEqual(restored['current'],old['current'])
            self.assertFalse(restored.get('previous'))
            self.assertEqual(sentinel(self.data/'state.db'),'before-upgrade')
            archives=list((installed/'backups').glob('pre-rollback-*.db'))
            self.assertEqual(len(archives),1)
            self.assertEqual(sentinel(archives[0]),'after-interruption')
            self.assertTrue(token==(self.data/'owner.token').read_bytes(),'fixture owner token changed')
            request=urllib.request.Request(f'http://127.0.0.1:{self.port}/healthz',
                                           headers={'Authorization':'Bearer '+token.decode().strip()})
            with urllib.request.urlopen(request,timeout=2) as response:
                self.assertEqual(response.status,200)
                self.assertEqual(json.load(response)['version'],old['current'])
        finally:
            if not group_finished:
                try:os.killpg(upgrade.pid,signal.SIGKILL)
                except ProcessLookupError:pass
                upgrade.communicate(timeout=5)
            # The private probe adopts only its fixture children. Reap explicit
            # PIDs, including a gate published just as a bounded wait failed.
            if marker.exists():owned_children.add(json.loads(marker.read_text())['pid'])
            pidfile=self.home/'code-factory.service.pid'
            if pidfile.exists():owned_children.add(int(pidfile.read_text()))
            subprocess.run([str(self.deps/'systemctl'),'--user','stop','code-factory.service'],
                           env=self.env,capture_output=True,timeout=5,check=True)
            for pid in owned_children:
                deadline=time.monotonic()+5
                while True:
                    try:finished,_=os.waitpid(pid,os.WNOHANG)
                    except ChildProcessError:break
                    if finished:break
                    if time.monotonic()>=deadline:
                        self.fail('owned installer fixture child survived cleanup')
                    time.sleep(.01)
def run_supervised_probe(command,timeout=45,grace=.2):
    """Let a private subreaper enforce the deadline across detached sessions."""
    config=json.dumps({'command':command,'timeout':timeout,'grace':grace})
    received=None
    previous={signum:signal.getsignal(signum) for signum in (signal.SIGINT,signal.SIGTERM)}
    def cancel(signum,_frame):
        nonlocal received
        if received is None or signum==signal.SIGTERM:received=signum
    for signum,handler in previous.items():
        if handler!=signal.SIG_IGN:signal.signal(signum,cancel)
    supervisor=None
    try:
        supervisor=subprocess.Popen([sys.executable,__file__,'--supervise-probe',config],
                                    stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
        forwarded=False
        while True:
            if received is not None and not forwarded:
                if supervisor.returncode is None:os.kill(supervisor.pid,signal.SIGTERM)
                forwarded=True
            try:
                stdout,stderr=supervisor.communicate(timeout=.1)
                break
            except subprocess.TimeoutExpired:pass
    except BaseException:
        if supervisor is not None and supervisor.returncode is None:
            os.kill(supervisor.pid,signal.SIGTERM)
            supervisor.communicate()
        raise
    finally:
        for signum,handler in previous.items():signal.signal(signum,handler)
        if received is not None:
            # Restore Unix termination after cleanup; unittest must not turn
            # TERM into an ordinary test error and continue the remaining suite.
            signal.raise_signal(received)
            if received==signal.SIGTERM:os._exit(143)
            raise KeyboardInterrupt
    return subprocess.CompletedProcess(command,supervisor.returncode,stdout,stderr)


def supervise_probe(config):
    if ctypes.CDLL(None,use_errno=True).prctl(36,1,0,0,0):
        raise OSError(ctypes.get_errno(),'test supervisor subreaper setup failed')
    cancelled=None
    def cancel(signum,_frame):
        nonlocal cancelled
        cancelled=signum
    signal.signal(signal.SIGTERM,cancel)
    signal.signal(signal.SIGINT,cancel)
    child=subprocess.Popen(config['command'],start_new_session=True)
    outcome=None
    deadline=time.monotonic()+config['timeout']
    try:
        while True:
            if cancelled is not None:
                outcome=128+cancelled
                break
            if os.waitid(os.P_PID,child.pid,os.WEXITED|os.WNOHANG|os.WNOWAIT) is not None:break
            if time.monotonic()>=deadline:
                outcome=124
                break
            time.sleep(.01)
    finally:
        # Keep the leader unreaped through the final group signal. Descendants
        # which started separate sessions become this subreaper's direct children.
        os.killpg(child.pid,signal.SIGTERM)
        until=time.monotonic()+config['grace']
        while time.monotonic()<until:time.sleep(.01)
        os.killpg(child.pid,signal.SIGKILL)
        children=pathlib.Path(f'/proc/self/task/{os.getpid()}/children')
        until=time.monotonic()+5
        while True:
            owned=[int(pid) for pid in children.read_text().split()]
            if not owned:break
            for pid in owned:
                # These are our unreaped children, so their IDs cannot be reused.
                os.kill(pid,signal.SIGKILL)
            for pid in owned:
                finished,status=os.waitpid(pid,os.WNOHANG)
                if finished==child.pid:child.returncode=os.waitstatus_to_exitcode(status)
            if time.monotonic()>=until:raise RuntimeError('owned probe children survived supervisor cleanup')
            time.sleep(.01)
    return outcome if outcome is not None else child.returncode


def inert_probe(config):
    signal.signal(signal.SIGTERM,signal.SIG_IGN)
    marker=pathlib.Path(config['marker'])
    descendant=subprocess.Popen([sys.executable,__file__,'--inert-probe-leaf',str(marker)+'.ready'],
                                start_new_session=True)
    deadline=time.monotonic()+3
    while not pathlib.Path(str(marker)+'.ready').exists():
        if time.monotonic()>=deadline:raise RuntimeError('inert descendant did not start')
        time.sleep(.01)
    temporary=marker.with_name(marker.name+'.tmp')
    temporary.write_text(json.dumps({'leader':os.getpid(),'descendant':descendant.pid}))
    temporary.replace(marker)
    if config['mode']=='success':return 0
    if config['mode'] in ('cancel','terminate'):
        os.kill(config['interrupt_pid'],signal.SIGTERM if config['mode']=='terminate' else signal.SIGINT)
    time.sleep(30)
    return 0


class ProbeSupervisorTests(unittest.TestCase):
    def test_timeout_cancellation_and_success_reap_detached_owned_children(self):
        for mode,expected in (('timeout',124),('cancel',130),('terminate',-signal.SIGTERM),('success',0)):
            with self.subTest(mode=mode),tempfile.TemporaryDirectory(prefix='installer-supervisor-') as directory:
                marker=pathlib.Path(directory)/'owned.json'
                config=json.dumps({'mode':mode,'marker':str(marker)})
                client=subprocess.Popen([sys.executable,__file__,'--inert-probe-client',config],
                                        stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
                handles=[]
                try:
                    deadline=time.monotonic()+5
                    while not marker.exists():
                        if time.monotonic()>=deadline:self.fail('inert probe did not publish owned processes')
                        if os.waitid(os.P_PID,client.pid,os.WEXITED|os.WNOHANG|os.WNOWAIT) is not None:
                            self.fail('inert client exited before publishing its owned processes')
                        time.sleep(.01)
                    owned=json.loads(marker.read_text())
                    for pid in owned.values():
                        try:handles.append(os.pidfd_open(pid))
                        except ProcessLookupError:pass
                    stdout,stderr=client.communicate(timeout=10)
                    self.assertEqual(client.returncode,expected,stdout+stderr)
                    for pid in owned.values():
                        self.assertFalse(pathlib.Path(f'/proc/{pid}').exists(),f'owned inert process {pid} survived or remained a zombie')
                finally:
                    # pidfds refer to these exact fixture instances, even if a
                    # failure happens after another process reuses a numeric ID.
                    for handle in handles:
                        try:signal.pidfd_send_signal(handle,signal.SIGKILL)
                        except ProcessLookupError:pass
                        os.close(handle)
                    if client.returncode is None:
                        os.kill(client.pid,signal.SIGTERM)
                        client.communicate(timeout=10)


if __name__=='__main__':
    if len(sys.argv)>1 and sys.argv[1]=='--supervise-probe':
        raise SystemExit(supervise_probe(json.loads(sys.argv[2])))
    elif len(sys.argv)>1 and sys.argv[1]=='--inert-probe-client':
        config=json.loads(sys.argv[2]);config['interrupt_pid']=os.getpid()
        try:
            result=run_supervised_probe([sys.executable,__file__,'--inert-probe',json.dumps(config)],timeout=.5,grace=.1)
            raise SystemExit(result.returncode)
        except KeyboardInterrupt:raise SystemExit(130)
    elif len(sys.argv)>1 and sys.argv[1]=='--inert-probe':
        raise SystemExit(inert_probe(json.loads(sys.argv[2])))
    elif len(sys.argv)>1 and sys.argv[1]=='--inert-probe-leaf':
        signal.signal(signal.SIGTERM,signal.SIG_IGN)
        pathlib.Path(sys.argv[2]).write_text('ready')
        time.sleep(30)
    elif INTERRUPTED_UPGRADE_PROBE:
        # Keep process-wide subreaper state out of the parent test runner.
        if ctypes.CDLL(None,use_errno=True).prctl(36,1,0,0,0):
            raise OSError(ctypes.get_errno(),'test subreaper setup failed')
        def cancel_probe(signum,_frame):raise SystemExit(128+signum)
        signal.signal(signal.SIGTERM,cancel_probe)
        signal.signal(signal.SIGINT,cancel_probe)
        unittest.main(argv=[sys.argv[0],'InstallerTests.test_interrupted_upgrade_pending_state_restores_database_and_archives_new_state'])
    else:unittest.main()
