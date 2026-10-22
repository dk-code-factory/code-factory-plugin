#!/usr/bin/env python3
"""Regression assertions for behavior classification and related cochanges."""
import copy
import unittest

import specs
import user_flows as uf
from test_specs import edit, fixture, put


class PolicyTests(unittest.TestCase):
    def setUp(self):
        self.before = fixture()
        self.after = copy.deepcopy(self.before)

    def review(self, kind='behavior'):
        return {'kind': kind, **dict.fromkeys(
            ['description', 'implementation', 'tests'],
            'Reviewed the particular transition, its implementation and the assertion that preserves its observed result.')}

    def impact(self):
        return specs.analyze(self.before, self.after, 'base')[1]

    def add_isolated(self, files):
        files['isolated.py'] = b'def isolated():\n    return 1\n'
        files['scripts/test_isolated.py'] = b'def test_isolated():\n    assert isolated() == 1\n'
        def add(ui):
            ui['entities'].append({
                'id': 'W-ISOLATED', 'kind': 'widget', 'terms': ['control'],
                'contains': [], 'elements': [], 'states': [],
                'implementation': [{'path': 'isolated.py', 'anchor': 'def isolated():', 'purpose': 'Independent control implementation'}],
                'assertions': ['ISOLATED'], 'gaps': [], 'suites': ['unit']})
            ui['assertions'].append({'id': 'ISOLATED', 'path': 'scripts/test_isolated.py',
                                     'anchor': 'def test_isolated():', 'assertion': 'Independent outcome is asserted.'})
            ui['instances'].append({'id': 'isolated', 'type': 'W-ISOLATED', 'pane': 'P-EXAMPLE', 'repeat': 'one'})
        edit(files, specs.UI, add)

    def test_behavior_without_description_edit_requires_test_cochange(self):
        self.after['app.py'] = self.after['app.py'].replace(b'return 1', b'return 2')
        for subject, item in self.impact().items():
            with self.subTest(subject=subject):
                self.assertFalse(item['description_changed'])
                self.assertEqual(item['tests'], [])
                self.assertTrue(any('related tests' in error for error in specs.constraints(subject, item, self.review())))
                for kind in ('refactor', 'terminology', 'structure'):
                    self.assertEqual(specs.constraints(subject, item, self.review(kind)), [])

    def test_behavior_receipt_cannot_bypass_missing_test_cochange(self):
        self.after['app.py'] += b'# Changed source without a test change\n'
        for subject, item in self.impact().items():
            record = {'version': 1, 'flow': subject, 'base': 'base',
                      'fingerprint': item['fingerprint'], 'environment': uf.FIXTURE_ENV,
                      'review': self.review(), 'dependencies': item['dependencies'],
                      'verification': [{'command': command, 'exit_code': 0}
                                       for suite in item['suites'].values() for command in suite['commands']]}
            put(self.after, specs.RECORDS + subject + '.json', record)
            self.assertTrue(any('related tests' in error for error in specs.receipt_errors(self.after, subject, item, 'base')))

    def test_new_ui_contract_cannot_borrow_unrelated_cochanges(self):
        # The new contract maps files already present at the source baseline.
        self.before['isolated.py'] = b'def isolated():\n    return 1\n'
        self.before['scripts/test_isolated.py'] = b'def test_isolated():\n    assert isolated() == 1\n'
        self.after = copy.deepcopy(self.before)
        self.add_isolated(self.after)
        self.after['app.py'] += b'# Independent source change\n'
        self.after['scripts/test_app.py'] += b'# Independent assertion change\n'
        item = self.impact()['ui']
        self.assertTrue(item['implementation'])
        self.assertTrue(item['tests'])
        errors = specs.constraints('ui', item, self.review('structure'))
        self.assertTrue(any('ui:W-ISOLATED' in error and 'implementation and tests' in error for error in errors))

    def test_direct_ui_behavior_cannot_borrow_unrelated_cochanges(self):
        self.add_isolated(self.before)
        self.after = copy.deepcopy(self.before)
        edit(self.after, specs.UI, lambda ui: ui['entities'][-1].update(states=['action']))
        self.after['app.py'] += b'# Independent source change\n'
        self.after['scripts/test_app.py'] += b'# Independent assertion change\n'
        item = self.impact()['ui']
        self.assertFalse(item['lifecycle_changed'])
        errors = specs.constraints('ui', item, self.review())
        self.assertTrue(any('ui:W-ISOLATED' in error and 'implementation and tests' in error for error in errors))
        self.assertEqual(specs.constraints('ui', item, self.review('structure')), [])

    def test_retired_ui_contract_checks_old_related_mappings(self):
        self.add_isolated(self.before)
        edit(self.before, uf.CATALOG, lambda catalog: catalog['exclusions'].extend([
            {'glob': 'isolated.py', 'reason': 'Retained independent source fixture'},
            {'glob': 'scripts/test_isolated.py', 'reason': 'Retained independent test fixture'}]))
        self.after = copy.deepcopy(self.before)
        def retire(ui):
            ui['entities'] = [e for e in ui['entities'] if e['id'] != 'W-ISOLATED']
            ui['instances'] = [i for i in ui['instances'] if i['type'] != 'W-ISOLATED']
            ui['assertions'] = [a for a in ui['assertions'] if a['id'] != 'ISOLATED']
        edit(self.after, specs.UI, retire)
        self.after['app.py'] += b'# Independent source change\n'
        self.after['scripts/test_app.py'] += b'# Independent assertion change\n'
        item = self.impact()['ui']
        errors = specs.constraints('ui', item, self.review('structure'))
        self.assertTrue(any('ui:W-ISOLATED' in error and 'implementation and tests' in error for error in errors))
        del self.after['isolated.py']
        del self.after['scripts/test_isolated.py']
        item = self.impact()['ui']
        self.assertEqual(item['contracts']['ui:W-ISOLATED']['implementation'], ['isolated.py'])
        self.assertEqual(item['contracts']['ui:W-ISOLATED']['tests'], ['scripts/test_isolated.py'])
        self.assertEqual(specs.constraints('ui', item, self.review('structure')), [])

    def test_indirect_unchanged_ui_contract_needs_no_independent_cochanges(self):
        self.add_isolated(self.before)
        def map_parent(ui):
            parent = next(e for e in ui['entities'] if e['id'] == 'PAGE-EXAMPLE')
            independent = next(e for e in ui['entities'] if e['id'] == 'W-ISOLATED')
            parent['implementation'] = copy.deepcopy(independent['implementation'])
            parent['assertions'] = ['ISOLATED']
        edit(self.before, specs.UI, map_parent)
        self.after = copy.deepcopy(self.before)
        self.after['app.py'] += b'# Related source change\n'
        self.after['scripts/test_app.py'] += b'# Related assertion change\n'
        item = self.impact()['ui']
        self.assertIn('ui:P-EXAMPLE', item['subjects'])
        self.assertIn('ui:PAGE-EXAMPLE', item['subjects'])
        self.assertNotIn('ui:PAGE-EXAMPLE', item['contracts'])
        self.assertEqual(specs.constraints('ui', item, self.review()), [])

    def test_flow_counts_consumed_ui_implementation_and_assertion_changes(self):
        self.before['ui_helper.py'] = b'def assist():\n    return 1\n'
        self.before['scripts/test_ui_helper.py'] = b'def test_assist():\n    assert assist() == 1\n'
        def map_helper(ui):
            widget = next(e for e in ui['entities'] if e['id'] == 'W-EXAMPLE')
            widget['implementation'] = [{'path': 'ui_helper.py', 'anchor': 'def assist():', 'purpose': 'Consumed helper implementation'}]
            widget['assertions'] = ['HELPER']
            ui['assertions'].append({'id': 'HELPER', 'path': 'scripts/test_ui_helper.py',
                                     'anchor': 'def test_assist():', 'assertion': 'Consumed helper outcome is asserted.'})
        edit(self.before, specs.UI, map_helper)
        self.after = copy.deepcopy(self.before)
        self.after['ui_helper.py'] = self.after['ui_helper.py'].replace(b'return 1', b'return 2')
        self.after['scripts/test_ui_helper.py'] = self.after['scripts/test_ui_helper.py'].replace(b'== 1', b'== 2')
        edit(self.after, uf.CATALOG, lambda catalog: catalog['flows'][0]['graph']['nodes'][0].update(label='Read updated helper outcome'))
        item = self.impact()['flow-example']
        self.assertTrue(item['description_changed'])
        self.assertEqual(item['implementation'], ['ui_helper.py'])
        self.assertEqual(item['tests'], ['scripts/test_ui_helper.py'])
        self.assertEqual(specs.constraints('flow-example', item, self.review()), [])


if __name__ == '__main__':
    unittest.main()
