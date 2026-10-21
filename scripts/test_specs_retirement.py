#!/usr/bin/env python3
"""Current verification survives retired suites, runners and contract IDs."""
import copy
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

import specs
import user_flows as uf
from test_specs import ROOT, edit, fixture, put, render, write_tree


class RetirementTests(unittest.TestCase):
    def setUp(self):
        self.before = fixture()
        edit(self.before, uf.CATALOG, lambda c: c['suites']['tooling'].update(commands=[[
            sys.executable, '-c',
            "import os; assert os.environ['CODE_FACTORY_REAL_CODEX'] == '0'; print('MANDATORY_TOOLING')"]]))
        edit(self.before, uf.CATALOG, lambda c: c['exclusions'].append({
            'glob': 'scripts/test_app.py', 'reason': 'Allow retaining a historical runner as an inert artifact'}))
        self.before['scripts/test_app.py'] += b"print('HISTORICAL_RUNNER')\n"
        self.after = copy.deepcopy(self.before)

    def rename_verification(self, *, flow=False):
        self.after['scripts/test_new.py'] = (
            b"import runpy\ndef test_perform():\n"
            b"    assert runpy.run_path('app.py')['perform']() == 1\n"
            b"test_perform()\nprint('CURRENT_RUNNER')\n")
        del self.after['scripts/test_app.py']
        def rename(catalog):
            catalog['suites']['replacement'] = catalog['suites'].pop('unit')
            catalog['suites']['replacement']['commands'] = [[sys.executable, 'scripts/test_new.py']]
            for contract in catalog['flows']:
                contract['suites'] = ['replacement']
                for assertion in contract['tests']:
                    if assertion['path'] == 'scripts/test_app.py':
                        assertion['path'] = 'scripts/test_new.py'
                if flow:
                    contract.update(id='renamed', spec='docs/user-flows/renamed.md')
        if flow:
            self.after['docs/user-flows/renamed.md'] = self.after.pop('docs/user-flows/example.md')
            self.after['app.py'] += b'# Implementation retained under the renamed contract.\n'
        edit(self.after, uf.CATALOG, rename)
        def update_ui(ui):
            for entity in ui['entities']:
                entity['suites'] = ['replacement']
            ui['assertions'][0]['path'] = 'scripts/test_new.py'
        edit(self.after, specs.UI, update_ui)

    def add_retired_flow(self):
        self.before['isolated.py'] = b'def isolated():\n    return 1\n'
        self.before['scripts/test_isolated.py'] = b'def test_isolated():\n    assert True\n'
        catalog = uf.load(self.before, uf.CATALOG)
        retired = copy.deepcopy(catalog['flows'][0])
        retired.update(id='retired', spec='docs/user-flows/retired.md', ui=[], suites=['retiring'],
                       implementation=[{'path': 'isolated.py', 'purpose': 'Retired implementation'}],
                       tests=[{'path': 'scripts/test_isolated.py', 'anchor': 'def test_isolated():',
                               'scenarios': ['S1'], 'purpose': 'Retired assertion'}])
        catalog['flows'].append(retired)
        catalog['suites']['retiring'] = {'commands': [[sys.executable, 'scripts/test_isolated.py']]}
        self.before[retired['spec']] = self.before['docs/user-flows/example.md']
        put(self.before, uf.CATALOG, catalog)
        render(self.before)
        self.after = copy.deepcopy(self.before)
        for path in ('isolated.py', 'scripts/test_isolated.py', retired['spec']):
            del self.after[path]
        edit(self.after, uf.CATALOG, lambda c: c.update(flows=[c['flows'][0]]))

    def test_retired_contract_uses_current_definition_of_surviving_suite(self):
        self.add_retired_flow()
        edit(self.after, uf.CATALOG, lambda c: c['suites']['retiring'].update(
            commands=[[sys.executable, 'scripts/test_app.py']]))
        affected = specs.analyze(self.before, self.after, 'base')[1]
        self.assertEqual(affected['flow-retired']['suites']['retiring']['commands'],
                         [[sys.executable, 'scripts/test_app.py']])
        self.assertIn('scripts/test_isolated.py', affected['flow-retired']['tests'])

    def test_retired_contract_cannot_borrow_unrelated_suite_or_shared_terms(self):
        self.add_retired_flow()
        edit(self.after, uf.CATALOG, lambda c: c['suites'].pop('retiring'))
        specs.validate(self.after)
        with self.assertRaisesRegex(uf.FlowError, 'flow:retired: retired verification.*current replacement verification'):
            specs.analyze(self.before, self.after, 'base')

    def test_history_definitions_and_deleted_inputs_still_invalidate_evidence(self):
        self.rename_verification()
        item = specs.analyze(self.before, self.after, 'base')[1]['ui']
        self.assertEqual(set(item['suites']), {'tooling', 'replacement'})
        self.assertIn('scripts/test_app.py', item['tests'])
        for change in ('definition', 'deleted-runner'):
            before = copy.deepcopy(self.before)
            after = copy.deepcopy(self.after)
            if change == 'definition':
                edit(before, uf.CATALOG, lambda c: c['suites']['unit']['commands'][0].append('--historical'))
            else:
                # Reintroducing a deleted old input must stale evidence even
                # though current commands no longer execute that path.
                after['scripts/test_app.py'] = before['scripts/test_app.py']
            changed = specs.analyze(before, after, 'base')[1]['ui']
            with self.subTest(change=change):
                self.assertNotEqual(item['fingerprint'], changed['fingerprint'])
                self.assertNotEqual(item['dependencies'], changed['dependencies'])

    def cli(self, root, base, command, *arguments):
        result = subprocess.run([sys.executable, str(ROOT / 'scripts/specs.py'), command,
                                 '--root', str(root), '--base', base, '--json', *arguments],
                                env={**os.environ, 'CODE_FACTORY_REAL_CODEX': '1'},
                                capture_output=True, text=True)
        return result, json.loads(result.stdout)

    def test_real_reconcile_check_renamed_suite_runner_and_flow_preserve_freshness(self):
        for rename_flow in (False, True):
            with self.subTest(rename_flow=rename_flow), tempfile.TemporaryDirectory() as directory:
                root = pathlib.Path(directory) / 'repository'
                root.mkdir()
                write_tree(root, self.before)
                uf.git(root, 'init', '-q')
                uf.git(root, 'config', 'user.name', 'Isolated fixture')
                uf.git(root, 'config', 'user.email', 'fixture@example.invalid')
                uf.git(root, 'add', '.')
                uf.git(root, 'commit', '-qm', 'Isolated baseline')
                base = uf.revision(root, 'HEAD')
                self.after = copy.deepcopy(self.before)
                self.after['app.py'] += b'# Reviewed source refactor.\n'
                write_tree(root, self.after)
                review = pathlib.Path(directory) / 'review.json'
                notes = {'kind': 'refactor', **dict.fromkeys(
                    ('description', 'implementation', 'tests'),
                    'Reviewed the retained outcome, mapped implementation, and exact current fixture assertion.')}
                def reconcile():
                    _, impacted = self.cli(root, base, 'impact')
                    review.write_text(json.dumps(dict.fromkeys(impacted['affected'], notes)))
                    return self.cli(root, base, 'reconcile', '--review', str(review))
                initial, _ = reconcile()
                self.assertEqual(initial.returncode, 0, initial.stderr)
                self.assertIn('HISTORICAL_RUNNER', initial.stderr)
                self.assertEqual(self.cli(root, base, 'check')[0].returncode, 0)
                self.rename_verification(flow=rename_flow)
                for removed in uf.snapshot(root).keys() - self.after.keys():
                    if not removed.startswith(specs.RECORDS):
                        (root / removed).unlink()
                write_tree(root, self.after)
                stale, detail = self.cli(root, base, 'check')
                self.assertEqual(stale.returncode, 1)
                self.assertTrue(any('stale' in error for error in detail['errors']))
                verified, _ = reconcile()
                self.assertEqual(verified.returncode, 0, verified.stderr)
                self.assertIn('CURRENT_RUNNER', verified.stderr)
                self.assertIn('MANDATORY_TOOLING', verified.stderr)
                self.assertNotIn('HISTORICAL_RUNNER', verified.stderr)
                self.assertNotIn('scripts/test_app.py', verified.stderr)
                current, detail = self.cli(root, base, 'check')
                self.assertEqual(current.returncode, 0, detail)
                record = json.loads((root / specs.RECORDS / 'ui.json').read_text())
                commands = [entry['command'] for entry in record['verification']]
                self.assertIn([sys.executable, 'scripts/test_new.py'], commands)
                self.assertNotIn([sys.executable, 'scripts/test_app.py'], commands)
                # Fresh current evidence must still fail after the new runner
                # changes, despite an unchanged command and retired old runner.
                (root / 'scripts/test_new.py').write_bytes(self.after['scripts/test_new.py'] + b'# Fresh input.\n')
                self.assertEqual(self.cli(root, base, 'check')[0].returncode, 1)


if __name__ == '__main__':
    unittest.main()
