#!/usr/bin/env python3
"""Bounded, fixed-seed combinations of independently checked registry invariants."""
import copy
import pathlib
import random
import sys
import tempfile
import unittest

import specs
import user_flows as uf
from test_specs import edit, fixture, put, render, write_tree


SEED = 743291
CASES = 6


def renamed_case(number, salt):
    before = fixture()
    # Keep the mandatory tooling runner independent of the product assertion
    # being edited, so a product test change does not affect every glossary term.
    edit(before, uf.CATALOG, lambda catalog: catalog['suites']['tooling'].update(
        commands=[[sys.executable, 'scripts/test_specs.py']]))
    after = copy.deepcopy(before)
    new_id = f'W-RENAMED-{salt}'
    source = f'implementation_{salt}.py'
    after[source] = after.pop('app.py') + f'# Case {number}: preserved implementation.\n'.encode()
    after['scripts/test_app.py'] += f'# Case {number}: independently inspected assertion.\n'.encode()

    def change_ui(ui):
        ui['entities'] = [e for e in ui['entities'] if e['id'] not in {'W-ACTION', 'C-EXAMPLE'}]
        for entity in ui['entities']:
            if entity['id'] == 'W-EXAMPLE':
                entity['id'] = new_id
            for reference in entity['implementation']:
                if reference['path'] == 'app.py':
                    reference['path'] = source
        ui['instances'] = [i for i in ui['instances'] if i['type'] != 'W-ACTION']
        for instance in ui['instances']:
            if instance['type'] == 'W-EXAMPLE':
                instance['type'] = new_id
    edit(after, specs.UI, change_ui)

    def change_flow(catalog):
        catalog['flows'][0]['ui'] = [new_id]
        catalog['flows'][0]['implementation'][0]['path'] = source
    edit(after, uf.CATALOG, change_flow)
    spelling = 'Café' if number % 2 else 'Cafe\u0301'
    edit(after, specs.TERMS, lambda terms: terms['terms'][0].update(name=f'{spelling} {salt}'))
    prose = f'\nAuthored case {number}: {spelling}; ordinary [context] survives.\n'
    after['docs/ui/README.md'] += (prose + r'[Vocabulary](../glossary/README\.md)' + '\n').encode()
    render(after)
    return before, after, new_id, source, prose.encode()


class GeneratedCaseTests(unittest.TestCase):
    def cases(self):
        rng = random.Random(SEED)
        for number in range(CASES):
            yield number, rng.randrange(100000, 999999)

    def test_combined_renames_retirements_unicode_rendering_impact_and_staleness(self):
        for number, salt in self.cases():
            with self.subTest(case=number):
                before, after, new_id, source, prose = renamed_case(number, salt)
                specs.validate(after)
                once = copy.deepcopy(after)
                render(after)
                self.assertEqual(after, once)
                self.assertIn(prose, after['docs/ui/README.md'])
                report, affected = specs.analyze(before, after, 'generated-base')
                # These IDs follow the explicit rename/deletion plan, independent
                # of the analyzer's own dependency or fingerprint calculation.
                impacted = {row['id'] for row in report['entities']}
                self.assertTrue({'W-EXAMPLE', 'W-ACTION', 'C-EXAMPLE', new_id} <= impacted)
                self.assertEqual({row['id'] for row in report['terms']}, {'record'})
                self.assertEqual(set(affected), {'glossary', 'ui', 'flow-example'})
                self.assertTrue({'app.py', source} <= set(affected['ui']['implementation']))
                self.assertIn('scripts/test_app.py', affected['ui']['tests'])
                item = affected['ui']
                notes = {'kind': 'structure', **dict.fromkeys(('description', 'implementation', 'tests'),
                         'Reviewed the renamed implementation, retired contracts and unchanged concrete assertion behavior.')}
                record = {'version': 1, 'flow': 'ui', 'base': 'generated-base',
                          'fingerprint': item['fingerprint'], 'dependencies': item['dependencies'],
                          'environment': uf.FIXTURE_ENV, 'review': notes,
                          'verification': [{'command': command, 'exit_code': 0}
                                           for suite in item['suites'].values() for command in suite['commands']]}
                put(after, specs.RECORDS + 'ui.json', record)
                self.assertEqual(specs.receipt_errors(after, 'ui', item, 'generated-base'), [])
                after[source] += b'# A subsequent source change invalidates the recorded review.\n'
                changed = specs.analyze(before, after, 'generated-base')[1]['ui']
                self.assertTrue(specs.receipt_errors(after, 'ui', changed, 'generated-base'))

    def test_combined_renames_do_not_hide_escaped_foundation_links(self):
        for number, salt in self.cases():
            before, after, _, _, _ = renamed_case(number, salt)
            destination = (r'../user\-flows/example.md' if number % 2 else
                           '..%2Fuser%2Dflows%2Fexample.md')
            after['docs/ui/README.md'] += ('\n[Forbidden](' + destination + ')\n').encode()
            with self.subTest(case=number), self.assertRaisesRegex(uf.FlowError, 'forbidden dependency'):
                specs.analyze(before, after, 'generated-base')

    def test_seeded_unicode_collisions_are_normalized_without_rewriting_valid_display(self):
        for number, salt in self.cases():
            _, after, _, _, _ = renamed_case(number, salt)
            original = uf.load(after, specs.TERMS)['terms'][0]['name']
            opposite = ('Cafe\u0301' if number % 2 else 'Café') + f' {salt}'
            edit(after, specs.TERMS, lambda terms: terms['terms'][1].update(aliases=[opposite]))
            with self.subTest(case=number), self.assertRaisesRegex(uf.FlowError, 'duplicate/ambiguous scoped name'):
                specs.validate(after)
            self.assertEqual(uf.load(after, specs.TERMS)['terms'][0]['name'], original)

    def test_fixed_seed_git_selections_keep_combined_index_and_worktree_edits_separate(self):
        for number, salt in list(self.cases())[:3]:
            with self.subTest(case=number), tempfile.TemporaryDirectory() as directory:
                before, staged_files, new_id, source, _ = renamed_case(number, salt)
                root = pathlib.Path(directory)
                write_tree(root, before)
                for args in [('init', '-q'), ('config', 'user.name', 'Generated fixture'),
                             ('config', 'user.email', 'fixture@example.invalid'),
                             ('add', '.'), ('commit', '-qm', 'Generated baseline')]:
                    uf.git(root, *args)
                base = uf.revision(root, 'HEAD')
                (root / 'app.py').unlink()
                write_tree(root, staged_files)
                uf.git(root, 'add', '.')
                index_tree = uf.git(root, 'write-tree').decode().strip()
                (root / source).write_bytes((root / source).read_bytes() + b'# Unstaged implementation edit.\n')
                (root / 'docs/ui/README.md').write_bytes((root / 'docs/ui/README.md').read_bytes() +
                    b'\n[Forbidden](../user\\-flows/example.md)\n')
                index = uf.snapshot(root, index_tree)
                committed = uf.snapshot(root, base)
                self.assertNotIn('app.py', index)
                self.assertIn(source, index)
                self.assertIn('app.py', committed)
                self.assertNotIn(source, committed)
                report, affected = specs.analyze(committed, index, base)
                self.assertIn(new_id, {row['id'] for row in report['entities']})
                self.assertIn('ui', affected)
                self.assertEqual(specs.analyze(committed, committed, base)[1], {})
                with self.assertRaisesRegex(uf.FlowError, 'forbidden dependency'):
                    specs.analyze(committed, uf.snapshot(root), base)
                uf.git(root, 'commit', '-qm', 'Commit the valid selected index')
                head = uf.snapshot(root, uf.revision(root, 'HEAD'))
                self.assertEqual(head[source], staged_files[source])
                self.assertNotEqual(uf.snapshot(root)[source], head[source])
                self.assertEqual(specs.analyze(committed, head, base)[1]['ui']['fingerprint'],
                                 affected['ui']['fingerprint'])


if __name__ == '__main__':
    unittest.main()
