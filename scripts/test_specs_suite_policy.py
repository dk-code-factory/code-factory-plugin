#!/usr/bin/env python3
"""Suite definitions select their real consumers and invalidate prior evidence."""
import copy
import unittest

import specs
import user_flows as uf
from test_specs import edit, fixture, put, render


class SuitePolicyTests(unittest.TestCase):
    def setUp(self):
        self.before = fixture()
        self.after = copy.deepcopy(self.before)

    def impact(self, files=None):
        return specs.analyze(self.before, self.after if files is None else files, 'base')

    def receipt(self, subject, item):
        review = {'kind': 'structure', **dict.fromkeys(
            ['description', 'implementation', 'tests'],
            'Inspected this subject and its verification requirements; the described outcome remains accurate.')}
        return {'version': 1, 'flow': subject, 'base': 'base',
                'fingerprint': item['fingerprint'], 'environment': uf.FIXTURE_ENV,
                'review': review, 'dependencies': item['dependencies'],
                'verification': [{'command': command, 'exit_code': 0}
                                 for suite in item['suites'].values() for command in suite['commands']]}

    def test_unit_executable_change_selects_ui_without_glossary(self):
        edit(self.after, uf.CATALOG, lambda c: c['suites']['unit']['commands'][0].__setitem__(0, 'python'))
        report, affected = self.impact()
        self.assertEqual(report['terms'], [])
        self.assertEqual({x['id'] for x in report['entities']},
                         {e['id'] for e in uf.load(self.before, specs.UI)['entities']})
        self.assertEqual(set(affected), {'ui', 'flow-example'})
        self.assertTrue(all('verification suite changed: unit' in x['reasons'] for x in report['entities']))

    def test_tooling_executable_change_selects_every_subject(self):
        edit(self.after, uf.CATALOG, lambda c: c['suites']['tooling']['commands'][0].__setitem__(0, 'python'))
        report, affected = self.impact()
        self.assertEqual({x['id'] for x in report['terms']},
                         {t['id'] for t in uf.load(self.before, specs.TERMS)['terms']})
        self.assertEqual(set(affected), {'glossary', 'ui', 'flow-example'})
        self.assertTrue(all('verification suite changed: tooling' in x['reasons'] for x in report['terms']))

    def test_command_addition_deletion_and_replacement_stale_ui_receipts(self):
        # Keep the ordinary source change fixed while changing only argv. Its
        # existing success must not survive changed verification requirements.
        self.after['app.py'] += b'# Existing reviewed implementation change\n'
        item = self.impact()[1]['ui']
        put(self.after, specs.RECORDS + 'ui.json', self.receipt('ui', item))
        self.assertEqual(specs.receipt_errors(self.after, 'ui', item, 'base'), [])
        initial = copy.deepcopy(self.after)
        command = uf.load(initial, uf.CATALOG)['suites']['unit']['commands'][0]
        for commands in ([['python', command[1]]], [command, command + ['--extra']]):
            with self.subTest(commands=commands):
                self.after = copy.deepcopy(initial)
                edit(self.after, uf.CATALOG, lambda c: c['suites']['unit'].update(commands=commands))
                changed = self.impact()[1]['ui']
                self.assertNotEqual(item['fingerprint'], changed['fingerprint'])
                self.assertNotEqual(item['dependencies'], changed['dependencies'])
                self.assertTrue(any('stale' in e for e in specs.receipt_errors(self.after, 'ui', changed, 'base')))
        self.before = copy.deepcopy(initial)
        edit(self.before, uf.CATALOG, lambda c: c['suites']['unit']['commands'].append(command + ['--extra']))
        self.after = copy.deepcopy(self.before)
        edit(self.after, uf.CATALOG, lambda c: c['suites']['unit']['commands'].pop())
        self.assertIn('ui', self.impact()[1])

    def test_input_addition_and_removal_stale_glossary_receipts(self):
        edit(self.after, specs.TERMS, lambda t: t['terms'][0].update(definition='A revised name for this available work item.'))
        item = self.impact()[1]['glossary']
        put(self.after, specs.RECORDS + 'glossary.json', self.receipt('glossary', item))
        self.assertEqual(specs.receipt_errors(self.after, 'glossary', item, 'base'), [])
        initial = copy.deepcopy(self.after)
        edit(self.after, uf.CATALOG, lambda c: c['suites']['tooling'].update(inputs=['app.py']))
        changed = self.impact()[1]['glossary']
        self.assertNotEqual(item['dependencies'], changed['dependencies'])
        self.assertTrue(any('stale' in e for e in specs.receipt_errors(self.after, 'glossary', changed, 'base')))
        # Removing an input from an already verified requirement also invalidates
        # evidence, even when that input's content and argv are unchanged.
        put(self.after, specs.RECORDS + 'glossary.json', self.receipt('glossary', changed))
        self.after[uf.CATALOG] = initial[uf.CATALOG]
        reverted = self.impact()[1]['glossary']
        self.assertTrue(any('stale' in e for e in specs.receipt_errors(self.after, 'glossary', reverted, 'base')))
        self.before = copy.deepcopy(initial)
        edit(self.before, uf.CATALOG, lambda c: c['suites']['tooling'].update(inputs=['app.py']))
        self.after = copy.deepcopy(self.before)
        edit(self.after, uf.CATALOG, lambda c: c['suites']['tooling'].pop('inputs'))
        self.assertEqual(set(self.impact()[1]), {'glossary', 'ui', 'flow-example'})

    def test_replaced_suite_retains_history_but_executes_current_requirements(self):
        def replace(catalog):
            catalog['suites']['replacement'] = catalog['suites'].pop('unit')
            catalog['flows'][0]['suites'] = ['replacement']
        edit(self.after, uf.CATALOG, replace)
        edit(self.after, specs.UI, lambda u: [e.update(suites=['replacement']) for e in u['entities']])
        report, affected = self.impact()
        self.assertEqual(report['terms'], [])
        self.assertEqual(set(affected), {'ui', 'flow-example'})
        self.assertEqual(set(affected['ui']['suites']), {'tooling', 'replacement'})
        self.assertTrue(all('verification suite changed: unit' in x['reasons'] for x in report['entities']))

    def test_unused_suite_change_leaves_consumers_and_existing_evidence_alone(self):
        edit(self.before, uf.CATALOG, lambda c: c['suites'].update(unused=copy.deepcopy(c['suites']['unit'])))
        self.after = copy.deepcopy(self.before)
        edit(self.after, uf.CATALOG, lambda c: c['suites']['unused']['commands'][0].__setitem__(0, 'python'))
        report, affected = self.impact()
        self.assertEqual(report, {'terms': [], 'entities': [], 'flows': []})
        self.assertEqual(affected, {})
        baseline = copy.deepcopy(self.before)
        edit(baseline, specs.TERMS, lambda t: t['terms'][0].update(definition='A revised name for this available work item.'))
        item = self.impact(baseline)[1]['glossary']
        edit(self.after, specs.TERMS, lambda t: t['terms'][0].update(definition='A revised name for this available work item.'))
        put(self.after, specs.RECORDS + 'glossary.json', self.receipt('glossary', item))
        self.assertEqual(specs.receipt_errors(self.after, 'glossary', self.impact()[1]['glossary'], 'base'), [])

    def test_flow_suite_change_does_not_select_unrelated_flow_or_foundations(self):
        catalog = uf.load(self.before, uf.CATALOG)
        catalog['suites']['independent'] = copy.deepcopy(catalog['suites']['unit'])
        flow = copy.deepcopy(catalog['flows'][0])
        flow.update(id='isolated', spec='docs/user-flows/isolated.md', suites=['independent'], ui=[])
        catalog['flows'].append(flow)
        self.before[flow['spec']] = self.before['docs/user-flows/example.md']
        put(self.before, uf.CATALOG, catalog)
        render(self.before)
        self.after = copy.deepcopy(self.before)
        edit(self.after, uf.CATALOG, lambda c: c['suites']['independent']['commands'][0].__setitem__(0, 'python'))
        report, affected = self.impact()
        self.assertEqual(report['terms'], [])
        self.assertEqual(report['entities'], [])
        self.assertEqual(set(affected), {'flow-isolated'})

    def test_ui_suite_change_selects_only_its_actual_consumers(self):
        edit(self.before, uf.CATALOG, lambda c: c['suites'].update(independent=copy.deepcopy(c['suites']['unit'])))
        edit(self.before, specs.UI, lambda u: u['entities'][0].update(suites=['independent']))
        self.after = copy.deepcopy(self.before)
        edit(self.after, uf.CATALOG, lambda c: c['suites']['independent']['commands'][0].__setitem__(0, 'python'))
        report, affected = self.impact()
        self.assertEqual(report['terms'], [])
        self.assertEqual([x['id'] for x in report['entities']], ['PAGE-EXAMPLE'])
        self.assertEqual(report['flows'], [])
        self.assertEqual(set(affected), {'ui'})


if __name__ == '__main__':
    unittest.main()
