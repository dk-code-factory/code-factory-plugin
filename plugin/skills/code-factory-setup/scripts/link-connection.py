#!/usr/bin/env python3
"""Link an observed registered MCP app to the private package; no cloud changes."""
import argparse
import json
import os
import pathlib
import re
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--app-id', required=True)
parser.add_argument('--plugin-dir', type=pathlib.Path, default=pathlib.Path(__file__).resolve().parents[3])
args = parser.parse_args()
app_id = args.app_id.removeprefix('plugin_')
if not re.fullmatch(r'(asdk_app_|connector_|templated_apps_)[A-Za-z0-9_-]+', app_id):
    parser.error('Provide the observed registered app ID or its plugin_ URL ID')
root = args.plugin_dir.resolve()
manifest_path = root / 'plugin.json'
manifest = json.loads(manifest_path.read_text())
if manifest.get('name') != 'code-factory':
    parser.error('Target must be the Code Factory package')
app_path = root / '.app.json'
apps = json.loads(app_path.read_text()) if app_path.exists() else {'apps': {}}
apps.setdefault('apps', {})['code-factory'] = {'id': app_id, 'required': True}
manifest.setdefault('extensions', {}).setdefault('com.openai', {})['apps'] = './.app.json'

def atomic_json(path, value):
    handle, filename = tempfile.mkstemp(prefix='.link-', dir=path.parent)
    try:
        with os.fdopen(handle, 'w') as target:
            json.dump(value, target, indent=2)
            target.write('\n')
        os.replace(filename, path)
    finally:
        if os.path.exists(filename):
            os.unlink(filename)

# Write the referenced mapping first; the manifest never points at a missing file.
atomic_json(app_path, apps)
atomic_json(manifest_path, manifest)
print('Linked registered app. Refresh the private package and verify a Work tool call.')
