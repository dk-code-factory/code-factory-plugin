#!/usr/bin/env python3
"""Exercise packaged binaries with an isolated HOME and a process-backed service fixture."""
import hashlib
import json
import os
import pathlib
import shutil
import signal
import socket
import subprocess
import tarfile
import tempfile
import time
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
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
        subprocess.run([str(self.deps/'systemctl'),'--user','stop','code-factory.service'],env=self.env,capture_output=True)
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
if __name__=='__main__':unittest.main()
