#!/usr/bin/env python3
"""Verify the package using a real Codex plugin host in an isolated CODEX_HOME."""
import json
import os
import pathlib
import shutil
import subprocess
import tempfile

ROOT = pathlib.Path(__file__).resolve().parents[1]

def verify():
    results = []
    with tempfile.TemporaryDirectory(prefix='code-factory-plugin-host-') as temporary:
        root = pathlib.Path(temporary)
        market = root / 'marketplace'
        (market / '.agents/plugins').mkdir(parents=True)
        shutil.copytree(ROOT / 'plugin', market / 'plugin', ignore=shutil.ignore_patterns('.app.json', '__pycache__'))
        (market / '.agents/plugins/marketplace.json').write_text(json.dumps({'name': 'code-factory-host-test', 'plugins': [{'name': 'code-factory', 'source': {'source': 'local', 'path': './plugin'}, 'policy': {'installation': 'AVAILABLE', 'authentication': 'ON_INSTALL'}, 'category': 'Productivity'}]}))
        home = root / 'codex'
        home.mkdir()
        env = {**os.environ, 'CODEX_HOME': str(home)}
        commands = [
            ['codex', 'plugin', 'marketplace', 'add', str(market), '--json'],
            ['codex', 'plugin', 'list', '--available', '--json'],
            ['codex', 'plugin', 'add', 'code-factory@code-factory-host-test', '--json'],
        ]
        for command in commands:
            result = subprocess.run(command, env=env, cwd=root, stdin=subprocess.DEVNULL, text=True, capture_output=True, timeout=30, check=True)
            results.append(json.loads(result.stdout))
        installed = pathlib.Path(results[-1]['installedPath'])
        assert (installed / 'mcp.json').is_file()
        assert (installed / 'skills/code-factory/SKILL.md').is_file()
        assert (installed / 'skills/code-factory-setup/SKILL.md').is_file()
        assert results[-1]['version'] == json.loads((ROOT / 'plugin/plugin.json').read_text())['version']
        return {'result': 'passed', 'plugin': results[-1]['pluginId'], 'version': results[-1]['version'], 'checks': ['real marketplace discovery', 'real isolated plugin install', 'cached MCP configuration and both skills'], 'workAcceptance': 'not verified'}

if __name__ == '__main__':
    print(json.dumps(verify(), indent=2))
