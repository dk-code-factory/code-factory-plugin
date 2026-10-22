#!/usr/bin/env python3
"""Registry ownership, canonical names and displayed-instance invariants."""
import copy
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

import specs
import user_flows as uf
from test_specs import ROOT, edit, fixture, put, render, write_tree


class RegistryInvariantTests(unittest.TestCase):
    def setUp(self):
        self.before = fixture()
        self.before['docs/ui/README.md'] += b'\n<!-- specs:assertions -->\n<!-- /specs:assertions -->\n'
        edit(self.before, specs.UI, lambda ui: ui['documents'][0]['blocks'].append({
            'name': 'assertions', 'kind': 'assertions', 'headers': ['Assertion', 'Source', 'Meaning']}))

    def hidden_assertion(self, *, owned=False, before=None):
        files = copy.deepcopy(self.before if before is None else before)
        files['scripts/test_hidden.py'] = b'def test_hidden():\n    assert False\n'
        ui = uf.load(files, specs.UI)
        ui['assertions'].append({'id': 'HIDDEN', 'path': 'scripts/test_hidden.py',
                                 'anchor': 'def test_hidden():', 'assertion': 'A newly declared test outcome.'})
        if owned:
            ui['entities'][2]['assertions'].append('HIDDEN')
        put(files, specs.UI, ui)
        render(files)
        return files

    def test_orphan_assertion_cannot_cover_a_new_test_even_with_an_inventory_row(self):
        files = self.hidden_assertion()
        self.assertIn(b'`HIDDEN`', files['docs/ui/README.md'])
        self.assertIn(b'scripts/test_hidden.py', files['docs/ui/README.md'])
        with self.assertRaisesRegex(uf.FlowError, 'assertions need entity owners.*HIDDEN'):
            specs.validate(files)
        with self.assertRaisesRegex(uf.FlowError, 'assertions need entity owners.*HIDDEN'):
            specs.analyze(self.before, files, 'base')

    def test_actual_validate_and_impact_cli_reject_orphan_coverage(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            write_tree(root, self.before)
            uf.git(root, 'init', '-q')
            uf.git(root, 'add', '.')
            uf.git(root, '-c', 'user.name=Test', '-c', 'user.email=test@example.invalid', 'commit', '-qm', 'Fixture')
            write_tree(root, self.hidden_assertion())
            for command in ('validate', 'impact'):
                with self.subTest(command=command):
                    result = subprocess.run([sys.executable, str(ROOT / 'scripts/specs.py'), command,
                                             '--root', str(root), '--base', 'HEAD', '--json'],
                                            capture_output=True, text=True, timeout=10)
                    self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
                    self.assertNotIn('Traceback', result.stderr)
                    self.assertRegex('\n'.join(json.loads(result.stdout)['errors']),
                                     'assertions need entity owners.*HIDDEN')

    def test_entity_owned_assertion_restores_impact_and_receipt_freshness(self):
        owned = self.hidden_assertion(owned=True)
        specs.validate(owned)
        report, affected = specs.analyze(self.before, owned, 'base')
        self.assertEqual(set(affected), {'ui', 'flow-example'})
        self.assertIn('W-EXAMPLE', [entry['id'] for entry in report['entities']])
        self.assertEqual(report['terms'], [])
        self.assertIn('scripts/test_hidden.py', affected['ui']['tests'])
        item = affected['ui']
        record = {'version': 1, 'flow': 'ui', 'base': 'base', 'fingerprint': item['fingerprint'],
                  'environment': uf.FIXTURE_ENV, 'dependencies': item['dependencies'],
                  'review': {'kind': 'refactor', **dict.fromkeys(('description', 'implementation', 'tests'),
                             'Reviewed the preserved behavior, owned assertion and exact verification inputs.')},
                  'verification': [{'command': command, 'exit_code': 0}
                                   for suite in item['suites'].values() for command in suite['commands']]}
        put(owned, specs.RECORDS + 'ui.json', record)
        self.assertEqual(specs.receipt_errors(owned, 'ui', item, 'base'), [])
        modified = copy.deepcopy(owned)
        modified['scripts/test_hidden.py'] += b'# Changed the owned assertion input.\n'
        refreshed = specs.analyze(self.before, modified, 'base')[1]['ui']
        self.assertNotEqual(item['fingerprint'], refreshed['fingerprint'])
        self.assertTrue(specs.receipt_errors(modified, 'ui', refreshed, 'base'))
        unrelated = copy.deepcopy(owned)
        unrelated['docs/unrelated.md'] = b'Unrelated supporting prose.\n'
        self.assertEqual(specs.analyze(owned, unrelated, 'base')[1], {})

    def historical_cleanup(self):
        before = copy.deepcopy(self.before)
        path = 'scripts/test_legacy_orphan.py'
        before[path] = b'def test_legacy_orphan():\n    assert False\n'
        edit(before, specs.UI, lambda ui: ui['assertions'].append({
            'id': 'ORPHAN-LEGACY', 'path': path, 'anchor': 'def test_legacy_orphan():',
            'assertion': 'Historical inventory entry without an entity owner.'}))
        after = copy.deepcopy(before)
        del after[path]
        edit(after, specs.UI, lambda ui: ui.update(assertions=[
            assertion for assertion in ui['assertions'] if assertion['id'] != 'ORPHAN-LEGACY']))
        return before, after, path

    def test_historical_orphan_cleanup_requires_actual_cli_reconciliation(self):
        before, after, path = self.historical_cleanup()
        specs.validate(after)
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            write_tree(root, before)
            uf.git(root, 'init', '-q')
            uf.git(root, 'add', '.')
            uf.git(root, '-c', 'user.name=Test', '-c', 'user.email=test@example.invalid', 'commit', '-qm', 'Historical orphan inventory')
            base = uf.revision(root, 'HEAD')
            write_tree(root, after)
            (root / path).unlink()
            result = subprocess.run([sys.executable, str(ROOT / 'scripts/specs.py'), 'check',
                                     '--root', str(root), '--base', base, '--json'],
                                    capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
            self.assertNotIn('Traceback', result.stderr)
            report = json.loads(result.stdout)
            self.assertEqual(set(report['affected']), {'ui', 'flow-example'})
            self.assertTrue(all('reconciliation required' in error for error in report['errors']))
            uf.git(root, 'add', '.')
            uf.git(root, '-c', 'user.name=Test', '-c', 'user.email=test@example.invalid', 'commit', '-qm', 'Remove historical orphan inventory')
            for comparison, status in (('HEAD', 0), (base, 1)):
                with self.subTest(comparison=comparison):
                    repeated = subprocess.run([sys.executable, str(ROOT / 'scripts/specs.py'), 'check',
                                               '--root', str(root), '--base', comparison, '--json'],
                                              capture_output=True, text=True, timeout=10)
                    self.assertEqual(repeated.returncode, status, repeated.stdout + repeated.stderr)
                    repeated_report = json.loads(repeated.stdout)
                    self.assertEqual(repeated_report['affected'], {} if status == 0 else report['affected'])

    def test_historical_orphan_inputs_retain_freshness_without_behavior_cochanges(self):
        before, after, path = self.historical_cleanup()
        report, affected = specs.analyze(before, after, 'base')
        self.assertEqual(set(affected), {'ui', 'flow-example'})
        self.assertEqual(report['terms'], [])
        notes = {'kind': 'structure', **dict.fromkeys(('description', 'implementation', 'tests'),
                 'Removed historical ownerless inventory; inspected preserved entities and their actual owned assertions.')}
        for subject, item in affected.items():
            self.assertFalse(item['lifecycle_changed'])
            self.assertEqual(specs.constraints(subject, item, notes), [])
            self.assertNotIn(path, item['tests'], 'ownerless tests are not entity behavior cochanges')
        item = affected['ui']
        record = {'version': 1, 'flow': 'ui', 'base': 'base', 'fingerprint': item['fingerprint'],
                  'environment': uf.FIXTURE_ENV, 'dependencies': item['dependencies'], 'review': notes,
                  'verification': [{'command': command, 'exit_code': 0}
                                   for suite in item['suites'].values() for command in suite['commands']]}
        put(after, specs.RECORDS + 'ui.json', record)
        repeated = specs.analyze(before, after, 'base')[1]['ui']
        self.assertEqual(item['fingerprint'], repeated['fingerprint'])
        self.assertEqual(specs.receipt_errors(after, 'ui', repeated, 'base'), [])
        changed_history = copy.deepcopy(before)
        changed_history[path] += b'# Historical input content changed.\n'
        changed = specs.analyze(changed_history, after, 'base')[1]['ui']
        self.assertNotEqual(item['fingerprint'], changed['fingerprint'])
        self.assertTrue(specs.receipt_errors(after, 'ui', changed, 'base'))
        self.assertTrue(specs.receipt_errors(after, 'ui', repeated, 'different-base'))
        unrelated_history = copy.deepcopy(before)
        unrelated_history['docs/unrelated.md'] = b'Unrelated historical supporting prose.\n'
        self.assertEqual(item['fingerprint'], specs.analyze(unrelated_history, after, 'base')[1]['ui']['fingerprint'])
        self.assertEqual(specs.analyze(after, after, 'base')[1], {})
        retained = copy.deepcopy(after)
        retained[path] = before[path]
        edit(retained, uf.CATALOG, lambda catalog: catalog['exclusions'].append({
            'glob': path, 'reason': 'Retained historical fixture, outside current owned verification.'}))
        first = specs.analyze(before, retained, 'base')[1]['ui']
        retained[path] += b'# Retained old mapping changed after inventory cleanup.\n'
        self.assertNotEqual(first['fingerprint'], specs.analyze(before, retained, 'base')[1]['ui']['fingerprint'])

    def test_owned_assertion_changes_leave_independent_entities_unaffected(self):
        before = copy.deepcopy(self.before)
        ui = uf.load(before, specs.UI)
        for index, eid in ((0, 'PAGE-ISLAND'), (1, 'P-ISLAND'), (2, 'W-ISLAND')):
            entity = copy.deepcopy(ui['entities'][index])
            entity.update(id=eid, contains=['P-ISLAND'] if index == 0 else [])
            ui['entities'].append(entity)
        ui['instances'].append({'id': 'island', 'type': 'W-ISLAND', 'pane': 'P-ISLAND', 'repeat': 'one independent widget'})
        put(before, specs.UI, ui)
        render(before)
        after = self.hidden_assertion(owned=True, before=before)
        report, affected = specs.analyze(before, after, 'base')
        self.assertEqual(set(affected), {'ui', 'flow-example'})
        self.assertTrue({'PAGE-ISLAND', 'P-ISLAND', 'W-ISLAND'}.isdisjoint(
            entry['id'] for entry in report['entities']))

    def test_historical_ownerless_inventory_without_entities_requires_flow_review(self):
        before, after, path = self.historical_cleanup()
        for files in (before, after):
            ui = uf.load(files, specs.UI)
            ui.update(entities=[], instances=[], assertions=[
                assertion for assertion in ui['assertions'] if assertion['id'] == 'ORPHAN-LEGACY'])
            put(files, specs.UI, ui)
            edit(files, uf.CATALOG, lambda catalog: catalog['flows'][0].update(ui=[]))
        specs.validate(after)
        report, affected = specs.analyze(before, after, 'base')
        self.assertEqual(set(affected), {'flow-example'})
        self.assertEqual(report['entities'], [])
        self.assertNotIn(path, affected['flow-example']['tests'])
        changed = copy.deepcopy(before)
        changed[path] += b'# Historical content is still a freshness input.\n'
        self.assertNotEqual(affected['flow-example']['fingerprint'],
                            specs.analyze(changed, after, 'base')[1]['flow-example']['fingerprint'])

    def test_nfc_equivalent_same_scope_names_and_aliases_collide(self):
        for role in ('name', 'alias', 'own-alias'):
            with self.subTest(role=role):
                files = copy.deepcopy(self.before)
                terms = uf.load(files, specs.TERMS)
                terms['terms'][0]['name'] = 'Café'
                if role == 'name':
                    terms['terms'][1]['name'] = 'Cafe\u0301'
                elif role == 'alias':
                    terms['terms'][1]['aliases'] = ['CAFE\u0301']
                else:
                    terms['terms'][0]['aliases'] = ['Cafe\u0301']
                put(files, specs.TERMS, terms)
                with self.assertRaisesRegex(uf.FlowError, 'duplicate/ambiguous scoped name'):
                    specs.validate(files, rendered=False)

    def test_equivalent_names_in_separate_scopes_preserve_display_spelling(self):
        files = copy.deepcopy(self.before)
        terms = uf.load(files, specs.TERMS)
        terms['terms'][0].update(name='Café', scope='control')
        terms['terms'][1].update(name='Cafe\u0301', aliases=['Coffee'], scope='action')
        put(files, specs.TERMS, terms)
        render(files)
        specs.validate(files)
        self.assertEqual(uf.load(files, specs.TERMS), terms)
        self.assertIn('## Café', files['docs/glossary/README.md'].decode())
        self.assertIn('## Cafe\u0301', files['docs/glossary/README.md'].decode())

    def test_instance_fields_require_meaningful_text_and_supported_shape(self):
        for field in ('id', 'type', 'pane', 'repeat'):
            for value in ('', '  ', True, 1, {}, [], None):
                with self.subTest(field=field, value=value):
                    files = copy.deepcopy(self.before)
                    edit(files, specs.UI, lambda ui: ui['instances'][0].update({field: value}), rerender=False)
                    with self.assertRaisesRegex(uf.FlowError, 'instances require nonempty text'):
                        specs.validate(files, rendered=False)
        for mutation in (lambda instance: instance.pop('repeat'),
                         lambda instance: instance.update(unknown='context')):
            with self.subTest(mutation=mutation):
                files = copy.deepcopy(self.before)
                edit(files, specs.UI, lambda ui: mutation(ui['instances'][0]), rerender=False)
                with self.assertRaisesRegex(uf.FlowError, 'instances require nonempty text'):
                    specs.validate(files, rendered=False)

    def test_stable_instance_id_with_pane_and_widget_separator_remains_valid(self):
        files = copy.deepcopy(self.before)
        edit(files, specs.UI, lambda ui: ui['instances'][0].update(
            id='P-IDENTITY/W-IDENTITY', repeat='one per displayed pane'))
        specs.validate(files)
        self.assertEqual(uf.load(files, specs.UI)['instances'][0]['id'], 'P-IDENTITY/W-IDENTITY')

    def test_foundation_versions_require_exact_integer_one(self):
        for path in (specs.TERMS, specs.UI):
            for version in (True, False, 1.0, 0, '1', None):
                with self.subTest(path=path, version=version):
                    files = copy.deepcopy(self.before)
                    edit(files, path, lambda value: value.update(version=version), rerender=False)
                    with self.assertRaisesRegex(uf.FlowError, 'Unsupported registry version'):
                        specs.validate(files, rendered=False)
        specs.validate(self.before)


if __name__ == '__main__':
    unittest.main()
