#!/usr/bin/env python3
"""Build a private installable plugin ZIP, without the daemon or local credentials."""
import argparse
import hashlib
import json
import pathlib
import re
import shutil
import urllib.parse
import zipfile

ROOT = pathlib.Path(__file__).resolve().parents[1]


def build(source, output_dir, endpoint=None, registered_app_id=None):
    source = pathlib.Path(source)
    output_dir = pathlib.Path(output_dir)
    manifest = json.loads((source / 'plugin.json').read_text())
    # A portable package connects by URL. Registered Work app IDs belong only to
    # the persistent installed copy and must never create dangling ZIP references.
    manifest.get('extensions', {}).get('com.openai', {}).pop('apps', None)
    mcp = json.loads((source / 'mcp.json').read_text())
    legacy = json.loads((source / '.mcp.json').read_text())
    compat = json.loads((source / '.codex-plugin/plugin.json').read_text())
    compat.pop('apps', None)
    if endpoint is not None:
        url = urllib.parse.urlsplit(endpoint)
        if url.scheme != 'https' or not url.hostname or url.username or url.password or url.query or url.fragment:
            raise ValueError('The plugin endpoint must be an HTTPS URL without credentials, query, or fragment')
        mcp['mcpServers']['code-factory']['url'] = endpoint
        legacy['mcpServers']['code-factory']['url'] = endpoint
    if registered_app_id is not None:
        registered_app_id = registered_app_id.removeprefix('plugin_')
        if not re.fullmatch(r'(asdk_app_|connector_|templated_apps_)[A-Za-z0-9][A-Za-z0-9_-]*', registered_app_id):
            raise ValueError('Browser packages require an observed registered MCP app ID (asdk_app_...), not a Plugin_ package ID')
        manifest.setdefault('extensions', {}).setdefault('com.openai', {})['apps'] = './.app.json'
        compat.pop('mcpServers', None)
        compat['apps'] = './.app.json'
    files = {}
    for folder in ('assets', 'skills'):
        for path in sorted((source / folder).rglob('*')):
            if path.is_symlink():
                raise ValueError('Plugin packages cannot contain symlinks')
            if path.is_file() and '__pycache__' not in path.parts:
                files[str(path.relative_to(source))] = path.read_bytes()
    components = [('plugin.json', manifest), ('.codex-plugin/plugin.json', compat)]
    if registered_app_id is None:
        components += [('mcp.json', mcp), ('.mcp.json', legacy)]
    else:
        components.append(('.app.json', {'apps': {'code-factory': {'id': registered_app_id, 'required': True}}}))
    for name, value in components:
        files[name] = (json.dumps(value, indent=2) + '\n').encode()
    if registered_app_id is not None:
        files['README.md'] = b'Code Factory private browser package. Requires access to the referenced registered MCP server and OAuth consent. Install in a supported private/workspace plugin flow; public submission uses With MCP.\n'
    elif (source / 'README.md').exists():
        files['README.md'] = (source / 'README.md').read_bytes()
    sums = ''.join(hashlib.sha256(data).hexdigest() + '  ' + name + '\n' for name, data in sorted(files.items()))
    files['SHA256SUMS'] = sums.encode()
    output_dir.mkdir(parents=True, exist_ok=True)
    archive = output_dir / ('code-factory-' + ('browser-plugin-' if registered_app_id is not None else 'plugin-') + manifest['version'] + '.zip')
    with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_DEFLATED) as output:
        for name, data in sorted(files.items()):
            info = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = 0o100644 << 16
            output.writestr(info, data)
    checksum = hashlib.sha256(archive.read_bytes()).hexdigest()
    archive.with_suffix('.zip.sha256').write_text(checksum + '  ' + archive.name + '\n')
    return archive


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', type=pathlib.Path, default=ROOT / 'plugin')
    parser.add_argument('--output-dir', type=pathlib.Path, default=ROOT / 'dist')
    parser.add_argument('--endpoint')
    parser.add_argument('--registered-app-id', help='Build a private browser package linked to this observed remote MCP app')
    parser.add_argument('--embed', action='store_true', help='Refresh the plugin ZIP embedded in the gateway')
    args = parser.parse_args()
    if args.embed and args.registered_app_id:
        parser.error("Do not embed a private account-specific app mapping in the portable release")
    archive = build(args.source, args.output_dir, args.endpoint, args.registered_app_id)
    if args.embed:
        target = ROOT / 'internal/pluginbundle/dist/code-factory.zip'
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(archive, target)
    print(archive)
