#!/usr/bin/env python3
"""Exercise public report/gap commands and versioned registry integration."""
import copy
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

import specs
import user_flows as uf
from specs_lib import gaps
from test_specs import ROOT, fixture, put, render, write_tree


def structured_fixture():
    files = fixture()
    ui, catalog = uf.load(files, specs.UI), uf.load(files, uf.CATALOG)
    ui['version'] = catalog['version'] = 2
    def convert(value, identifier, owner):
        return gaps.normalize(value, gap_id=identifier, kind='unverified-behavior',
                              severity='P2', owner=owner,
                              next_verification='Exercise the described result and assert its recovery state.')
    for index, entity in enumerate(ui['entities']):
        entity['gaps'] = [convert(value, f'gap-ui-{index}-{number}', 'ui-specification-maintainer')
                          for number, value in enumerate(entity['gaps'])]
    for index, group in enumerate(ui['coverage_groups']):
        group['gap'] = convert(group['gap'], f'gap-coverage-{index}', 'ui-specification-maintainer')
    for index, flow in enumerate(catalog['flows']):
        flow['gaps'] = [convert(value, f'gap-flow-{index}-{number}', 'user-flow-maintainer')
                        for number, value in enumerate(flow['gaps'])]
    # Ensure the integration checks both UI and flow metadata, not empty arrays.
    ui['entities'][2]['gaps'].append(convert('A widget recovery is unverified.', 'gap-ui-recovery', 'ui-specification-maintainer'))
    catalog['flows'][0]['gaps'].append(convert({'scenario': 'S1', 'reason': 'A flow recovery is unverified.'}, 'gap-flow-recovery', 'user-flow-maintainer'))
    put(files, specs.UI, ui); put(files, uf.CATALOG, catalog)
    render(files)
    return files


class CLIIntegrationTests(unittest.TestCase):
    def repository(self, files):
        temporary = tempfile.TemporaryDirectory(); self.addCleanup(temporary.cleanup)
        root = pathlib.Path(temporary.name); write_tree(root, files)
        for args in [('init', '-q'), ('config', 'user.name', 'CLI fixture'),
                     ('config', 'user.email', 'cli@example.invalid'), ('add', '.'), ('commit', '-qm', 'Baseline')]:
            uf.git(root, *args)
        return root, uf.revision(root, 'HEAD')

    def command(self, root, *args, status=0):
        result = subprocess.run([sys.executable, str(ROOT / 'scripts/specs.py'), *args, '--root', str(root)],
                                capture_output=True, text=True, timeout=15)
        self.assertEqual(result.returncode, status, result.stdout + result.stderr)
        return result.stdout

    def test_version_two_renders_metadata_and_reports_typed_inventory_read_only(self):
        files = structured_fixture(); specs.validate(files)
        root, _ = self.repository(files); before = uf.snapshot(root)
        result = json.loads(self.command(root, 'gaps', '--json'))
        self.assertEqual(result['counts']['total'], result['counts']['typed'])
        self.assertEqual(result['counts']['legacy'], 0)
        self.assertEqual(result['counts']['with_next_verification'], result['counts']['total'])
        self.assertIn('P2', result['counts']['severity'])
        self.assertEqual({row['id'] for row in result['gaps']},
                         {record['id'] for entity in uf.load(files, specs.UI)['entities'] for record in entity['gaps']}
                         | {record['id'] for flow in uf.load(files, uf.CATALOG)['flows'] for record in flow['gaps']})
        rendered = files['docs/user-flows/example.md'].decode()
        self.assertIn('gap-flow-recovery', rendered)
        self.assertIn('Next verification:', rendered)
        self.assertEqual(uf.snapshot(root), before)

    def test_strict_gap_errors_and_global_duplicate_identity_fail_closed(self):
        before = structured_fixture()
        for mutation in [lambda ui: ui['entities'][2]['gaps'][0].pop('next_verification'),
                         lambda ui: ui['entities'][2]['gaps'][0].update(owner='user-flow-maintainer'),
                         lambda ui: ui['entities'][2]['gaps'][0].update(id='gap-flow-recovery')]:
            files = copy.deepcopy(before); ui = uf.load(files, specs.UI); mutation(ui); put(files, specs.UI, ui)
            with self.assertRaises(uf.FlowError): specs.validate(files, rendered=False)
        for field, value in [('scenario', []), ('reason', None)]:
            files = copy.deepcopy(before); catalog = uf.load(files, uf.CATALOG)
            catalog['flows'][0]['gaps'][0][field] = value; put(files, uf.CATALOG, catalog)
            with self.assertRaises(uf.FlowError): specs.validate(files, rendered=False)
        root, _ = self.repository(before)
        ui = uf.load(before, specs.UI); ui['entities'][2]['gaps'][0].pop('severity')
        (root / specs.UI).write_text(json.dumps(ui)); snapshot = uf.snapshot(root)
        self.assertTrue(json.loads(self.command(root, 'gaps', '--json', status=1))['errors'])
        self.assertEqual(uf.snapshot(root), snapshot)

    def test_version_two_gap_metadata_stales_consumers_without_rewriting_history(self):
        before, after = fixture(), structured_fixture()
        original = copy.deepcopy(before)
        first = specs.analyze(before, after, 'base')[1]
        ui = uf.load(after, specs.UI)
        identifier = ui['entities'][2]['gaps'][0]['id']
        ui['entities'][2]['gaps'][0]['severity'] = 'P3'
        put(after, specs.UI, ui); render(after)
        second = specs.analyze(before, after, 'base')[1]
        self.assertNotEqual(first['ui']['fingerprint'], second['ui']['fingerprint'])
        self.assertNotEqual(first['flow-example']['fingerprint'], second['flow-example']['fingerprint'])
        self.assertEqual(uf.load(after, specs.UI)['entities'][2]['gaps'][0]['id'], identifier)
        self.assertEqual(before, original)

    def test_legacy_gap_inventory_preserves_reasons_without_invented_metadata(self):
        files = fixture()
        ui = uf.load(files, specs.UI); ui['entities'][2]['gaps'] = ['A historical unverified result.']
        put(files, specs.UI, ui); render(files)
        root, _ = self.repository(files)
        result = json.loads(self.command(root, 'gaps', '--json'))
        self.assertEqual(result['counts']['typed'], 0)
        self.assertTrue(result['counts']['legacy'])
        self.assertEqual(result['counts']['severity'], {})
        self.assertTrue(all(set(row) <= {'subject', 'scenario', 'reason'} for row in result['legacy_gaps']))
        self.assertEqual(result['counts']['with_next_verification'], 0)

    def test_report_preserves_selected_snapshot_and_existing_impact_stdout(self):
        files = fixture(); root, base = self.repository(files)
        (root / 'app.py').write_bytes(files['app.py'] + b'# Changed implementation.\n')
        before = uf.snapshot(root)
        report = json.loads(self.command(root, 'report', '--base', base, '--json'))
        self.assertTrue(report['reviews']); self.assertTrue(report['references'])
        self.assertNotIn('fingerprint', report['reviews'][0])
        self.assertTrue(self.command(root, 'impact', '--base', base).startswith('Affected: '))
        selected = json.loads(self.command(root, 'report', '--base', base, '--head', base, '--json'))
        self.assertFalse(selected['reviews'])
        self.assertTrue(all(row['action'] not in ('render', 'reconcile') for row in selected['next_commands']))
        self.assertEqual(uf.snapshot(root), before)
