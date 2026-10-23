#!/usr/bin/env python3
import json
import pathlib
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'plugin/skills/code-factory-setup/scripts/link-connection.py'

class PackagingTests(unittest.TestCase):
    def test_registered_mapping_and_manifest_preserve_existing_fields(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / 'plugin.json').write_text(json.dumps({'name': 'code-factory', 'extensions': {'com.openai': {'onboardingSkill': './skills/setup/SKILL.md'}}}))
            (root / '.app.json').write_text(json.dumps({'apps': {'other': {'id': 'connector_existing'}}}))
            command = ['python3', str(SCRIPT), '--plugin-dir', str(root), '--app-id', 'plugin_asdk_app_observed']
            subprocess.run(command, check=True, capture_output=True)
            subprocess.run(command, check=True, capture_output=True)
            apps = json.loads((root / '.app.json').read_text())['apps']
            self.assertEqual(apps['code-factory'], {'id': 'asdk_app_observed', 'required': True})
            self.assertIn('other', apps)
            self.assertIn('onboardingSkill', json.loads((root / 'plugin.json').read_text())['extensions']['com.openai'])
            invalid = subprocess.run(command[:-1]+['https://example.com'], capture_output=True)
            self.assertNotEqual(invalid.returncode, 0)


class CompletePluginTests(unittest.TestCase):
    def test_manifests_and_connection_validate_against_portable_schemas(self):
        import jsonschema
        for kind in ('plugin', 'mcp'):
            schema = json.loads((ROOT / 'scripts/schemas' / (kind + '.schema.json')).read_text())
            value = json.loads((ROOT / 'plugin' / (kind + '.json')).read_text())
            jsonschema.Draft202012Validator(schema).validate(value)
        manifest = json.loads((ROOT / 'plugin/plugin.json').read_text())
        interface = manifest['extensions']['com.openai']['interface']
        for field in ('composerIcon', 'logo'):
            self.assertTrue((ROOT / 'plugin' / interface[field]).is_file())
        for name in ('code-factory', 'code-factory-setup', 'code-factory-admin'):
            self.assertTrue((ROOT / 'plugin/skills' / name / 'SKILL.md').is_file())

    def test_zip_is_self_contained_deterministic_and_contains_no_host_state(self):
        import hashlib
        import shutil
        import zipfile
        from package_plugin import build
        with tempfile.TemporaryDirectory() as directory:
            temporary = pathlib.Path(directory)
            source = temporary / 'plugin'
            shutil.copytree(ROOT / 'plugin', source)
            manifest_path = source / 'plugin.json'
            manifest = json.loads(manifest_path.read_text())
            manifest['extensions']['com.openai']['apps'] = './.app.json'
            manifest_path.write_text(json.dumps(manifest))
            (source / '.app.json').write_text('{"apps":{"code-factory":{"id":"asdk_app_private"}}}')
            (source / 'owner.token').write_text('owner-credential-must-not-ship')
            (source / 'runtime.json').write_text('{"secret":"private-host-state"}')
            endpoint = 'https://example.ts.net:10000/mcp'
            archive = build(source, temporary / 'output', endpoint)
            original = archive.read_bytes()
            self.assertEqual(build(source, temporary / 'output', endpoint).read_bytes(), original)
            with zipfile.ZipFile(archive) as bundle:
                names = set(bundle.namelist())
                self.assertTrue({'plugin.json', 'mcp.json', '.mcp.json', '.codex-plugin/plugin.json', 'SHA256SUMS'}.issubset(names))
                self.assertFalse({'.app.json', 'owner.token', 'runtime.json'} & names)
                self.assertNotIn('apps', json.loads(bundle.read('plugin.json'))['extensions']['com.openai'])
                self.assertEqual(json.loads(bundle.read('mcp.json'))['mcpServers']['code-factory']['url'], endpoint)
                self.assertEqual(json.loads(bundle.read('.mcp.json'))['mcpServers']['code-factory']['url'], endpoint)
                self.assertFalse(json.loads(bundle.read('mcp.json'))['mcpServers']['code-factory'].get('headers'))
                for line in bundle.read('SHA256SUMS').decode().splitlines():
                    digest, filename = line.split('  ', 1)
                    self.assertEqual(hashlib.sha256(bundle.read(filename)).hexdigest(), digest)
            self.assertEqual(archive.with_suffix('.zip.sha256').read_text().split()[0], hashlib.sha256(original).hexdigest())
            with self.assertRaises(ValueError):
                build(source, temporary / 'output', 'https://user:secret@example.com/mcp')

class BrowserPackageTests(unittest.TestCase):
    def test_registered_browser_package_has_no_local_mcp_launcher(self):
        import zipfile
        from package_plugin import build
        with tempfile.TemporaryDirectory() as directory:
            archive = build(ROOT / 'plugin', directory, registered_app_id='plugin_asdk_app_fixture')
            self.assertIn('browser-plugin', archive.name)
            with zipfile.ZipFile(archive) as bundle:
                self.assertNotIn('mcp.json', bundle.namelist())
                self.assertNotIn('.mcp.json', bundle.namelist())
                manifest = json.loads(bundle.read('plugin.json'))
                compatibility = json.loads(bundle.read('.codex-plugin/plugin.json'))
                self.assertEqual(manifest['extensions']['com.openai']['apps'], './.app.json')
                self.assertEqual(compatibility['apps'], './.app.json')
                self.assertNotIn('mcpServers', compatibility)
                mapping = json.loads(bundle.read('.app.json'))
                self.assertEqual(mapping['apps']['code-factory'], {'id': 'asdk_app_fixture', 'required': True})
                self.assertIn('skills/code-factory/SKILL.md', bundle.namelist())
                self.assertIn('skills/code-factory-setup/SKILL.md', bundle.namelist())
                self.assertIn('skills/code-factory-admin/SKILL.md', bundle.namelist())

    def test_package_id_cannot_be_used_as_registered_server_id(self):
        from package_plugin import build
        with tempfile.TemporaryDirectory() as directory:
            for invalid in ('Plugin_e162fcc74c788191975d60b6f80137f2', '', 'https://chatgpt.com/plugins/example', 'asdk_app_'):
                with self.assertRaises(ValueError):
                    build(ROOT / 'plugin', directory, registered_app_id=invalid)


if __name__ == '__main__':
    unittest.main()
