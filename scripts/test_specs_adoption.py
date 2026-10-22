#!/usr/bin/env python3
"""Initial adoption eligibility must not bypass behavioral review policy."""
import copy
import unittest

import specs
import user_flows as uf
from test_specs import edit, fixture, put


class AdoptionTests(unittest.TestCase):
    def setUp(self):
        self.before = fixture()
        self.after = copy.deepcopy(self.before)
        del self.before[specs.UI]

    def review(self, kind):
        return {'kind': kind, **dict.fromkeys(
            ['description', 'implementation', 'tests'],
            'Reviewed the initial UI contracts against their baseline implementation and concrete assertions.')}

    def impact(self):
        return specs.analyze(self.before, self.after, specs.BASELINE)[1]['ui']

    def receipt_errors(self, item, kind):
        record = {'version': 1, 'flow': 'ui', 'base': specs.BASELINE,
                  'fingerprint': item['fingerprint'], 'environment': uf.FIXTURE_ENV,
                  'review': self.review(kind), 'dependencies': item['dependencies'],
                  'verification': [{'command': command, 'exit_code': 0}
                                   for suite in item['suites'].values() for command in suite['commands']]}
        put(self.after, specs.RECORDS + 'ui.json', record)
        return specs.receipt_errors(self.after, 'ui', item, specs.BASELINE)

    def test_eligible_adoption_behavior_review_and_receipt_require_test_cochange(self):
        self.after['app.py'] = self.after['app.py'].replace(b'return 1', b'return 2')
        item = self.impact()
        self.assertTrue(item['adopted'])
        self.assertIn('app.py', item['implementation'])
        self.assertEqual(item['tests'], [])
        errors = specs.constraints('ui', item, self.review('behavior'))
        self.assertTrue(any('related tests' in error for error in errors))
        # Valid fingerprints and successful verification cannot waive policy.
        self.assertEqual(self.receipt_errors(item, 'behavior'), errors)
        self.after['scripts/test_app.py'] = self.after['scripts/test_app.py'].replace(
            b'assert True', b'assert 1 + 1 == 2')
        item = self.impact()
        self.assertEqual(specs.constraints('ui', item, self.review('behavior')), [])
        self.assertEqual(self.receipt_errors(item, 'behavior'), [])

    def test_only_adoption_classification_waives_initial_contract_cochanges(self):
        item = self.impact()
        self.assertTrue(item['adopted'])
        self.assertTrue(item['lifecycle_changed'])
        self.assertEqual(item['implementation'], [])
        self.assertEqual(item['tests'], [])
        self.assertEqual(specs.constraints('ui', item, self.review('adoption')), [])
        self.assertEqual(self.receipt_errors(item, 'adoption'), [])
        for kind in ('behavior', 'refactor', 'terminology', 'structure'):
            with self.subTest(kind=kind):
                errors = specs.constraints('ui', item, self.review(kind))
                self.assertTrue(any('implementation and tests' in error for error in errors))
                self.assertEqual(self.receipt_errors(item, kind), errors)

    def test_eligible_ui_adoption_behavior_checks_each_direct_contract(self):
        for files in (self.before, self.after):
            files['isolated.py'] = b'def isolated():\n    return 1\n'
            files['scripts/test_isolated.py'] = b'def test_isolated():\n    assert isolated() == 1\n'
        def add(ui):
            ui['entities'].append({
                'id': 'W-ISOLATED', 'kind': 'widget', 'terms': ['control'],
                'contains': [], 'elements': [], 'states': [],
                'implementation': [{'path': 'isolated.py', 'anchor': 'def isolated():', 'purpose': 'Independent control implementation'}],
                'assertions': ['ISOLATED'], 'gaps': [], 'suites': ['unit']})
            ui['assertions'].append({'id': 'ISOLATED', 'path': 'scripts/test_isolated.py',
                                     'anchor': 'def test_isolated():', 'assertion': 'Independent control outcome is asserted.'})
            ui['instances'].append({'id': 'isolated', 'type': 'W-ISOLATED', 'pane': 'P-EXAMPLE', 'repeat': 'one'})
        edit(self.after, specs.UI, add)
        self.after['app.py'] += b'# Independent implementation change\n'
        self.after['scripts/test_app.py'] += b'# Independent assertion change\n'
        item = self.impact()
        self.assertTrue(item['adopted'])
        self.assertTrue(item['implementation'])
        self.assertTrue(item['tests'])
        errors = specs.constraints('ui', item, self.review('behavior'))
        self.assertTrue(any('ui:W-ISOLATED' in error and 'implementation and tests' in error for error in errors))
        self.assertEqual(self.receipt_errors(item, 'behavior'), errors)

    def test_adoption_review_still_requires_baseline_eligibility(self):
        item = specs.analyze(self.before, self.after, 'wrong-base')[1]['ui']
        self.assertFalse(item['adopted'])
        errors = specs.constraints('ui', item, self.review('adoption'))
        self.assertTrue(any('established source baseline' in error for error in errors))
        self.assertTrue(any('implementation and tests' in error for error in errors))


if __name__ == '__main__':
    unittest.main()
