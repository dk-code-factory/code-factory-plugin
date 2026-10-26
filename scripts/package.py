#!/usr/bin/env python3
"""Build a self-contained Linux x86-64 release. Only the builder needs Go/Node."""
import hashlib
import json
import os
import pathlib
import shutil
import subprocess
import tarfile
import tempfile
import package_plugin

ROOT = pathlib.Path(__file__).resolve().parents[1]
OUT = ROOT / 'dist'
OUT.mkdir(exist_ok=True)
plugin_archive = package_plugin.build(ROOT / 'plugin', OUT)
embedded = ROOT / 'internal/pluginbundle/dist/code-factory.zip'
embedded.parent.mkdir(parents=True, exist_ok=True)
shutil.copyfile(plugin_archive, embedded)
def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()
# Content-address the source snapshot, including the already-built embedded UI.
h = hashlib.sha256()
for folder in ('cmd', 'internal', 'plugin'):
    for path in sorted((ROOT / folder).rglob('*')):
        if path.is_file() and path.name != '.app.json':
            h.update(str(path.relative_to(ROOT)).encode())
            h.update(path.read_bytes())
for name in ('go.mod', 'go.sum', 'scripts/package.py'):
    h.update((ROOT / name).read_bytes())
version = '0.1.0-' + h.hexdigest()[:12]
with tempfile.TemporaryDirectory(dir=OUT) as temporary:
    stage = pathlib.Path(temporary) / ('code-factory-' + version)
    stage.mkdir()
    subprocess.run(['go', 'build', '-trimpath', '-ldflags', '-X codefactory/internal/buildinfo.Version='+version,
                    '-o', str(stage / 'code-factory'), './cmd/code-factory'], cwd=ROOT,
                   env={**os.environ, 'CGO_ENABLED': '0', 'GOOS': 'linux', 'GOARCH': 'amd64'}, check=True)
    shutil.copytree(ROOT / 'plugin', stage / 'plugin', ignore=shutil.ignore_patterns('.app.json', '__pycache__'))
    shutil.copytree(ROOT / 'docs', stage / 'docs')
    manifest = {'version': version, 'os': 'linux', 'arch': 'amd64', 'dependencies':
                {'git': 'existing installation', 'gh': 'existing authenticated installation',
                 'codex': 'dkropachev/codex stable >=0.161.0; managed upgrades require compatibility probe', 'tailscale': 'authenticated Tailscale installation for Funnel and Serve', 'tunnel-client': 'optional alternate OpenAI tunnel transport', 'systemctl': 'user session'}}
    (stage / 'release.json').write_text(json.dumps(manifest, indent=2)+'\n')
    files = sorted(p for p in stage.rglob('*') if p.is_file())
    (stage / 'SHA256SUMS').write_text(''.join(digest(p)+'  '+str(p.relative_to(stage))+'\n' for p in files))
    archive = OUT / (stage.name + '-linux-amd64.tar.gz')
    with tarfile.open(archive, 'w:gz') as tar:
        tar.add(stage, arcname=stage.name)
    (OUT / (archive.name+'.sha256')).write_text(digest(archive)+'  '+archive.name+'\n')
    shutil.copy2(stage / 'code-factory', ROOT / 'bin/code-factory')
print(archive)
print(package_plugin.build(ROOT / "plugin", OUT))
