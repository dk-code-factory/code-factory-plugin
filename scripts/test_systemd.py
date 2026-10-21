#!/usr/bin/env python3
"""Opt-in rootless Podman check: real user systemd, no Go/Node, provider fixtures."""
import json
import os
import pathlib
import subprocess
import tempfile
import time

ROOT=pathlib.Path(__file__).resolve().parents[1]
# An independent store prevents test lifecycle locks from affecting existing
# personal containers. Retain the store path on failure for explicit recovery.
store=pathlib.Path(os.environ.get('CODE_FACTORY_SYSTEMD_STORE') or tempfile.mkdtemp(prefix='code-factory-container-'))
print('Isolated Podman store:',store,flush=True)
PODMAN=['podman','--root',str(store/'storage'),'--runroot',str(store/'run'),'--storage-driver','vfs','--events-backend','file']
subprocess.run([*PODMAN,'build','-t','localhost/code-factory-systemd-test','-f',str(ROOT/'scripts/Containerfile.systemd'),str(ROOT)],check=True,timeout=300)
archive=max((ROOT/'dist').glob('*.tar.gz'),key=lambda p:p.stat().st_mtime)
name='code-factory-systemd-'+str(int(time.time()))
subprocess.run([*PODMAN,'run','-d','--name',name,'--systemd=always','localhost/code-factory-systemd-test'],check=True,timeout=300)
def run(*args,**kwargs):return subprocess.run([*PODMAN,'exec',name,*args],check=True,timeout=60,**kwargs)
try:
    subprocess.run([*PODMAN,'cp',str(archive),name+':/opt/release.tar.gz'],check=True,timeout=300)
    run('bash','-ec', '''tar -xzf /opt/release.tar.gz -C /home/pilot
mkdir -p /home/pilot/.local/bin
for tool in gh tunnel-client; do printf '#!/bin/sh\nexit 0\n' > /home/pilot/.local/bin/$tool; done
printf '#!/bin/sh\necho "codex-cli 0.161.0"\n' > /home/pilot/.local/bin/codex
chmod 700 /home/pilot/.local/bin/*
chown -R pilot:pilot /home/pilot
systemctl start user@1001.service
''')
    user=['runuser','-u','pilot','--','env','HOME=/home/pilot','XDG_RUNTIME_DIR=/run/user/1001','DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1001/bus','PATH=/home/pilot/.local/bin:/usr/bin:/bin']
    run(*user,'bash','-ec', '''test ! -e /usr/bin/go
test ! -e /usr/bin/node
/home/pilot/code-factory-*/code-factory install
code-factory install --from /home/pilot/code-factory-*
systemctl --user is-active code-factory.service
systemctl --user restart code-factory.service
python3 - <<'PYHEALTH'
import json,pathlib,sqlite3,time,urllib.request
root=pathlib.Path('/home/pilot/.local/share/code-factory')
token=(root/'owner.token').read_text()
for attempt in range(100):
    try:
        request=urllib.request.Request('http://127.0.0.1:8787/healthz',headers={'Authorization':'Bearer '+token})
        with urllib.request.urlopen(request,timeout=1) as response: health=json.load(response)
        assert health['status']=='verified'
        break
    except OSError:
        if attempt==99: raise
        time.sleep(.1)
with sqlite3.connect(root/'state.db') as db: assert db.execute("select count(*) from entities where kind='profile'").fetchone()[0]==3
print('PASS: authenticated restart health and retained SQLite profiles')
PYHEALTH
code-factory doctor
code-factory uninstall
test -e /home/pilot/.local/share/code-factory/state.db
''')
    print('PASS: isolated Linux user systemd install/rerun/restart/uninstall without Go or Node; provider executables are fixtures')
finally:
    subprocess.run([*PODMAN,'rm','-f',name],check=True,timeout=300)
