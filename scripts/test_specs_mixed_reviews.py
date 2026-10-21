#!/usr/bin/env python3
"""Initial UI migration and new behavior require explicit scoped decisions."""
import copy
import unittest

import specs
import user_flows as uf
from test_specs import edit, fixture, put


class MixedReviewTests(unittest.TestCase):
    def setUp(self):
        self.after = fixture()
        self.before = copy.deepcopy(self.after)
        del self.before[specs.UI]
        self.after['modal_helper.py'] = b'def modal_helper():\n    return True\n'
        self.after['scripts/test_modal_helper.py'] = b'def test_modal_helper():\n    assert modal_helper()\n'
        def add_helper(ui):
            ui['entities'].append({
                'id': 'W-MODAL-HELPER', 'kind': 'widget', 'terms': ['control'],
                'contains': [], 'elements': [], 'states': [],
                'implementation': [{'path': 'modal_helper.py', 'anchor': 'def modal_helper():', 'purpose': 'New helper behavior'}],
                'assertions': ['MODAL-HELPER'], 'gaps': [], 'suites': ['unit']})
            ui['assertions'].append({'id': 'MODAL-HELPER', 'path': 'scripts/test_modal_helper.py',
                                     'anchor': 'def test_modal_helper():', 'assertion': 'New helper behavior is asserted.'})
            ui['instances'].append({'id': 'modal-helper', 'type': 'W-MODAL-HELPER', 'pane': 'P-EXAMPLE', 'repeat': 'one helper'})
        edit(self.after, specs.UI, add_helper)

    def item(self, base=None):
        return specs.analyze(self.before, self.after, specs.BASELINE if base is None else base)[1]['ui']

    def decision(self, kind):
        return {'kind': kind, **dict.fromkeys(('description', 'implementation', 'tests'),
                'Inspected this exact contract, its baseline source, implementation outcome and meaningful owned assertions.')}

    def mixed(self, item):
        return {**self.decision('mixed'), 'contracts': {
            key: self.decision('behavior' if key == 'ui:W-MODAL-HELPER' else 'adoption')
            for key in item['contracts']}}

    def record(self, item, review):
        record = {'version': 1, 'flow': 'ui', 'base': specs.BASELINE, 'fingerprint': item['fingerprint'],
                  'environment': uf.FIXTURE_ENV, 'dependencies': item['dependencies'], 'review': review,
                  'verification': [{'command': command, 'exit_code': 0}
                                   for suite in item['suites'].values() for command in suite['commands']]}
        put(self.after, specs.RECORDS + 'ui.json', record)

    def test_initial_ui_can_mix_baseline_adoption_with_owned_new_behavior(self):
        item = self.item()
        self.assertFalse(item['adopted'])
        self.assertFalse(item['contracts']['ui:W-MODAL-HELPER']['adopted'])
        self.assertTrue(all(contract['adopted'] for key, contract in item['contracts'].items()
                            if key != 'ui:W-MODAL-HELPER'))
        review = self.mixed(item)
        self.assertEqual(specs.constraints('ui', item, review), [])
        self.record(item, review)
        self.assertEqual(specs.receipt_errors(self.after, 'ui', item, specs.BASELINE), [])
        stored = uf.load(self.after, specs.RECORDS + 'ui.json')['review']
        self.assertEqual(stored, review)

    def test_mixed_behavior_cannot_borrow_unrelated_or_historical_orphan_tests(self):
        self.before['scripts/test_modal_helper.py'] = self.after['scripts/test_modal_helper.py']
        self.after['app.py'] += b'# Unrelated source change.\n'
        self.after['scripts/test_app.py'] += b'# Unrelated test change.\n'
        item = self.item()
        self.assertEqual(item['contracts']['ui:W-MODAL-HELPER']['tests'], [])
        self.assertTrue(any('ui:W-MODAL-HELPER' in error and 'related tests' in error
                            for error in specs.constraints('ui', item, self.mixed(item))))
        # Historical ownerless inventory stays a verification input, never a
        # substitute for this new contract's unchanged owned assertion file.
        self.before[specs.UI] = fixture()[specs.UI]
        self.before['scripts/test_orphan.py'] = b'def test_orphan():\n    assert True\n'
        edit(self.before, specs.UI, lambda ui: ui['assertions'].append({
            'id': 'ORPHAN', 'path': 'scripts/test_orphan.py', 'anchor': 'def test_orphan():',
            'assertion': 'Historical ownerless assertion inventory.'}))
        item = self.item()
        review = {**self.decision('mixed'), 'contracts': {
            key: self.decision('behavior' if key == 'ui:W-MODAL-HELPER' else 'structure')
            for key in item['contracts']}}
        self.assertEqual(item['contracts']['ui:W-MODAL-HELPER']['tests'], [])
        self.assertTrue(any('ui:W-MODAL-HELPER' in error and 'related tests' in error
                            for error in specs.constraints('ui', item, review)))

    def test_mixed_behavior_requires_its_own_implementation_change(self):
        self.before['modal_helper.py'] = self.after['modal_helper.py']
        self.after['app.py'] += b'# Unrelated implementation cannot cover the helper.\n'
        self.after['scripts/test_app.py'] += b'# Unrelated assertion cannot cover the helper.\n'
        item = self.item()
        self.assertEqual(item['contracts']['ui:W-MODAL-HELPER']['implementation'], [])
        self.assertTrue(item['contracts']['ui:W-MODAL-HELPER']['tests'])
        self.assertTrue(any('ui:W-MODAL-HELPER' in error and 'related implementation' in error
                            for error in specs.constraints('ui', item, self.mixed(item))))

    def test_adoption_is_pinned_per_contract_to_baseline_paths_and_anchors(self):
        item = self.item()
        review = self.mixed(item)
        review['contracts']['ui:W-MODAL-HELPER'] = self.decision('adoption')
        self.assertTrue(any('ui:W-MODAL-HELPER' in error and 'established source baseline' in error
                            for error in specs.constraints('ui', item, review)))
        self.record(item, review)
        self.assertTrue(any('ui:W-MODAL-HELPER' in error and 'established source baseline' in error
                            for error in specs.receipt_errors(self.after, 'ui', item, specs.BASELINE)))
        wrong_base = self.item('wrong-base')
        self.assertFalse(any(contract['adopted'] for contract in wrong_base['contracts'].values()))
        self.assertTrue(specs.constraints('ui', wrong_base, self.mixed(wrong_base)))
        self.before['app.py'] = self.before['app.py'].replace(b'def perform():', b'def historical_perform():')
        missing_anchor = self.item()
        self.assertFalse(missing_anchor['contracts']['ui:W-EXAMPLE']['adopted'])
        self.assertNotEqual(item['fingerprint'], missing_anchor['fingerprint'])

    def test_mixed_decisions_require_exact_contract_coverage_and_nested_shapes(self):
        item = self.item()
        review = self.mixed(item)
        key = next(iter(item['contracts']))
        mutations = (
            lambda value: value['contracts'].pop(key),
            lambda value: value['contracts'].update({'ui:UNKNOWN': self.decision('structure')}),
            lambda value: value.update(contracts=[]),
            lambda value: value['contracts'].update({key: []}),
            lambda value: value['contracts'][key].update(kind='mixed'),
            lambda value: value['contracts'][key].update(kind=[]),
            lambda value: value['contracts'][key].update(description=True),
            lambda value: value['contracts'][key].update(implementation='too short'),
            lambda value: value['contracts'][key].pop('tests'),
            lambda value: value['contracts'][key].update(unknown='extra field'),
            lambda value: value.update(unknown='extra field'),
            lambda value: value.update(description=[]),
            lambda value: value.pop('contracts'),
        )
        for mutate in mutations:
            with self.subTest(mutate=mutate):
                invalid = copy.deepcopy(review)
                mutate(invalid)
                self.assertTrue(specs.constraints('ui', item, invalid))
                self.record(item, invalid)
                self.assertTrue(specs.receipt_errors(self.after, 'ui', item, specs.BASELINE))

    def test_mixed_is_ui_only_and_plain_kinds_cannot_hide_scoped_decisions(self):
        item = self.item()
        for subject in ('glossary', 'flow-example'):
            with self.subTest(subject=subject):
                self.assertTrue(specs.constraints(subject, item, self.mixed(item)))
        for kind in specs.KINDS:
            with self.subTest(kind=kind):
                review = self.mixed(item)
                review['kind'] = kind
                self.assertTrue(any('contracts' in error for error in specs.constraints('ui', item, review)))

    def test_plain_adoption_reviews_and_retirement_cochanges_remain_compatible(self):
        baseline = fixture()
        before = copy.deepcopy(baseline)
        del before[specs.UI]
        item = specs.analyze(before, baseline, specs.BASELINE)[1]['ui']
        self.assertTrue(item['adopted'])
        self.assertEqual(specs.constraints('ui', item, self.decision('adoption')), [])
        self.assertTrue(specs.constraints('ui', item, self.decision('structure')))
        after = copy.deepcopy(baseline)
        edit(after, specs.UI, lambda ui: (ui.update(entities=[entity for entity in ui['entities']
             if entity['id'] not in ('W-ACTION', 'C-EXAMPLE')]), ui.update(instances=[instance
             for instance in ui['instances'] if instance['type'] != 'W-ACTION'])))
        item = specs.analyze(baseline, after, 'base')[1]['ui']
        review = {**self.decision('mixed'), 'contracts': {
            key: self.decision('structure') for key in item['contracts']}}
        self.assertTrue(specs.constraints('ui', item, review))
        after['app.py'] += b'# Retired related implementation.\n'
        after['scripts/test_app.py'] += b'# Retired related assertion.\n'
        item = specs.analyze(baseline, after, 'base')[1]['ui']
        review['contracts'] = {key: self.decision('structure') for key in item['contracts']}
        self.assertEqual(specs.constraints('ui', item, review), [])


if __name__ == '__main__':
    unittest.main()
