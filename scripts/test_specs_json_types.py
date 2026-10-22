#!/usr/bin/env python3
"""Raw JSON and Git regressions for unambiguous registry membership."""
import contextlib
import copy
import io
import json
import pathlib
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import specs
import user_flows as uf
from test_specs import fixture, put, render, write_tree
from test_user_flows import fixture as legacy_fixture


def mutate(files, path, trail, value):
    data = uf.load(files, path)
    target = data
    for key in trail[:-1]:
        target = target[key]
    target[trail[-1]] = value
    put(files, path, data)


class JSONTypeTests(unittest.TestCase):
    def setUp(self):
        self.files = fixture()

    def run_cli(self, root, *args):
        output = io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(io.StringIO()):
            status = specs.main([*args, '--root', str(root), '--json'])
        return status, json.loads(output.getvalue())

    def git_root(self, root, files):
        write_tree(root, files)
        for args in [('init', '-q'), ('config', 'user.name', 'Fixture'),
                     ('config', 'user.email', 'fixture@example.invalid'),
                     ('add', '.'), ('commit', '-qm', 'baseline')]:
            subprocess.run(['git', '-C', str(root), *args], check=True, capture_output=True)

    def test_raw_duplicate_members_reject_zero_impact_changes(self):
        # Last-member-wins decoding used to erase this apparent state change.
        after = dict(self.files)
        after[specs.UI] = after[specs.UI].replace(
            b'"states": []', b'"states": ["action"], "states": []', 1)
        self.assertNotEqual(self.files[specs.UI], after[specs.UI])
        for operation in [lambda: specs.validate(after),
                          lambda: specs.analyze(self.files, after, 'base'),
                          lambda: specs.analyze(after, self.files, 'base')]:
            with self.assertRaisesRegex(uf.FlowError, 'Duplicate JSON member: states'):
                operation()
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            self.git_root(root, self.files)
            self.assertEqual(self.run_cli(root, 'check', '--base', 'HEAD'),
                             (0, {'base': uf.revision(root, 'HEAD'), 'terms': [], 'entities': [],
                                  'flows': [], 'affected': {}, 'errors': []}))
            (root / specs.UI).write_bytes(after[specs.UI])
            for command in ['validate', 'impact', 'check', 'render']:
                with self.subTest(command=command):
                    before = uf.snapshot(root)
                    status, result = self.run_cli(root, command, '--base', 'HEAD')
                    self.assertEqual(status, 1)
                    self.assertIn('Duplicate JSON member: states', '\n'.join(result['errors']))
                    self.assertEqual(uf.snapshot(root), before)

    def test_duplicate_members_at_each_layer_and_escaped_names(self):
        for path, old, new in [
            (specs.TERMS, b'"aliases": []', b'"aliases": ["hidden"], "aliases": []'),
            (specs.UI, b'"states": []', b'"\\u0073tates": ["action"], "states": []'),
            (uf.CATALOG, b'"flows": [', b'"flows": [], "flows": [')]:
            with self.subTest(path=path):
                files = dict(self.files)
                files[path] = files[path].replace(old, new, 1)
                self.assertNotEqual(files[path], self.files[path])
                with self.assertRaisesRegex(uf.FlowError, 'Duplicate JSON member'):
                    specs.validate(files)
        # Member names can repeat in separate objects; current valid arrays remain valid.
        specs.validate(self.files)
        self.assertEqual(specs.analyze(self.files, self.files, 'base')[1], {})
        uf.validate(legacy_fixture())
        for path in ['docs/glossary/support.json', 'docs/ui/support.json']:
            files = dict(self.files)
            files[path] = b'{"meaning": "hidden", "meaning": "accepted"}'
            with self.subTest(path=path), self.assertRaisesRegex(uf.FlowError, 'Duplicate JSON member: meaning'):
                specs.validate(files)

    def test_false_membership_and_string_gaps_reject_before_render_or_analysis(self):
        cases = [
            (specs.UI, ['entities', 0, 'contains'], {'P-EXAMPLE': False}),
            (specs.UI, ['entities', 2, 'assertions'], {'ASSERT': False}),
            (specs.UI, ['entities', 2, 'gaps'], 'unverified'),
            (uf.CATALOG, ['flows', 0, 'scenarios'], {'S1': False}),
            (uf.CATALOG, ['flows', 0, 'tests', 0, 'scenarios'], {'S1': False})]
        for path, trail, value in cases:
            with self.subTest(field=trail):
                files = copy.deepcopy(self.files)
                mutate(files, path, trail, value)
                if trail[-1] == 'gaps':
                    mutate(files, specs.UI, ['entities', 2, 'assertions'], [])
                for operation in [lambda: specs.validate(files, rendered=False),
                                  lambda: specs.analyze(self.files, files, 'base'),
                                  lambda: specs.analyze(files, self.files, 'base')]:
                    with self.assertRaisesRegex(uf.FlowError, trail[-1] + ' must be an array'):
                        operation()
        for trail in [['flows', 0, 'scenarios'], ['flows', 0, 'tests', 0, 'scenarios']]:
            files = legacy_fixture()
            mutate(files, uf.CATALOG, trail, {'S1': False})
            with self.assertRaisesRegex(uf.FlowError, 'scenarios must be an array'):
                uf.validate(files, rendered=False)

    def test_collection_shapes_across_registry_recipes_and_suites(self):
        cases = [
            (specs.TERMS, ['terms']),
            (specs.TERMS, ['terms', 0, 'aliases']),
            *[(specs.UI, [field]) for field in
              ['entities', 'instances', 'assertions', 'source_map', 'coverage_groups', 'documents']],
            *[(specs.UI, ['entities', 2, field]) for field in
              ['terms', 'contains', 'states', 'assertions', 'suites', 'elements', 'implementation']],
            (specs.UI, ['documents', 0, 'blocks']),
            (specs.UI, ['documents', 0, 'blocks', 0, 'headers']),
            (uf.CATALOG, ['exclusions']),
            (uf.CATALOG, ['flows']),
            *[(uf.CATALOG, ['flows', 0, field]) for field in
              ['terms', 'ui', 'scenarios', 'suites', 'implementation', 'tests', 'gaps']],
            (uf.CATALOG, ['suites', 'unit', 'commands']),
            (uf.CATALOG, ['suites', 'unit', 'commands', 0]),
            (uf.CATALOG, ['suites', 'unit', 'inputs'])]
        for path, trail in cases:
            # Objects and strings both used to act as iterable collections.
            for value in [{}, 'unverified', None, 1, False]:
                with self.subTest(path=path, field=trail, value=value):
                    files = copy.deepcopy(self.files)
                    mutate(files, path, trail, value)
                    with self.assertRaisesRegex(uf.FlowError, 'must be an array|require array entries'):
                        specs.validate(files, rendered=False)

    def test_optional_inventory_arrays_and_entry_types(self):
        ui = uf.load(self.files, specs.UI)
        ui['source_map'] = [{'path': 'app.py', 'anchor': 'def perform():',
                             'anchors': ['def perform():'], 'location_labels': ['perform'],
                             'entities': 'Example', 'detail': 'Perform work.'}]
        ui['coverage_groups'] = [{'name': 'Example', 'entities': ['W-EXAMPLE'],
                                  'assertions': ['ASSERT'], 'gap': 'Host acceptance is unverified.'}]
        ui['documents'][0]['blocks'].append({'name': 'widgets', 'kind': 'entities',
                                           'ids': [e['id'] for e in ui['entities']],
                                           'fields': ['kind'], 'headers': ['Entity', 'Kind']})
        self.files['docs/ui/README.md'] += b'\n<!-- specs:widgets -->\n<!-- /specs:widgets -->\n'
        put(self.files, specs.UI, ui)
        render(self.files)
        specs.validate(self.files)
        for trail in [['source_map', 0, 'anchors'], ['source_map', 0, 'location_labels'],
                      ['coverage_groups', 0, 'entities'], ['coverage_groups', 0, 'assertions'],
                      ['documents', 0, 'blocks', 1, 'ids'], ['documents', 0, 'blocks', 1, 'fields']]:
            files = copy.deepcopy(self.files)
            mutate(files, specs.UI, trail, {'ignored': False})
            with self.subTest(field=trail), self.assertRaisesRegex(uf.FlowError, 'must be an array'):
                specs.validate(files, rendered=False)
        for path, trail, value in [(specs.UI, ['entities', 0, 'states'], [{}]),
                                   (specs.UI, ['entities', 0, 'implementation'], [False]),
                                   (uf.CATALOG, ['flows', 0, 'tests', 0, 'scenarios'], [False])]:
            files = copy.deepcopy(self.files)
            mutate(files, path, trail, value)
            with self.subTest(field=trail), self.assertRaisesRegex(uf.FlowError, 'require .* entries'):
                specs.validate(files, rendered=False)

    def test_invalid_collection_cli_render_preserves_every_file(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            self.git_root(root, self.files)
            files = dict(self.files)
            mutate(files, specs.UI, ['entities', 0, 'contains'], {'P-EXAMPLE': False})
            write_tree(root, files)
            # A stale earlier output must not be repaired before the later registry error.
            (root / 'docs/glossary/README.md').write_bytes(b'# Stale\n<!-- specs:terms -->\n<!-- /specs:terms -->\n')
            before = uf.snapshot(root)
            for command in ['render', 'check']:
                status, result = self.run_cli(root, command, '--base', 'HEAD')
                self.assertEqual(status, 1)
                self.assertIn('contains must be an array', '\n'.join(result['errors']))
                self.assertEqual(uf.snapshot(root), before)

    def test_duplicate_receipts_and_reviews_cannot_override_rejected_evidence(self):
        self.files['app.py'] += b'# implementation refactor\n'
        affected = specs.analyze(fixture(), self.files, 'base')[1]
        notes = {'kind': 'refactor', 'description': 'The described observable behavior still matches the refactored implementation.',
                 'implementation': 'Inspected the mapped implementation change and confirmed the same observable result.',
                 'tests': 'Read the mapped assertion and confirmed it still checks the preserved behavior.'}
        item = affected['ui']
        record = {'version': 1, 'flow': 'ui', 'base': 'base', 'fingerprint': item['fingerprint'],
                  'dependencies': item['dependencies'], 'environment': uf.FIXTURE_ENV, 'review': notes,
                  'verification': [{'command': command, 'exit_code': 0}
                                   for suite in item['suites'].values() for command in suite['commands']]}
        path = specs.RECORDS + 'ui.json'
        put(self.files, path, record)
        self.assertEqual(specs.receipt_errors(self.files, 'ui', item, 'base'), [])
        for raw in [b'{"version": 2, ' + json.dumps(record).encode()[1:],
                    json.dumps(record).encode().replace(b'"exit_code": 0', b'"exit_code": 7, "exit_code": 0', 1)]:
            self.files[path] = raw
            self.assertTrue(specs.receipt_errors(self.files, 'ui', item, 'base'))
            alias = {uf.RECORDS + 'ui.json': raw}
            self.assertTrue(uf.receipt_errors(alias, 'ui', {**item, 'description_changed': False}, 'base'))
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            prior = root / path
            prior.parent.mkdir(parents=True)
            prior.write_bytes(b'prior receipt must survive invalid review')
            review = root / 'review.json'
            for raw in [b'{"ui": {}, "ui": ' + json.dumps(notes).encode() + b'}',
                        json.dumps({'ui': notes}).encode().replace(b'"kind": "refactor"', b'"kind": "behavior", "kind": "refactor"')]:
                review.write_bytes(raw)
                with patch.object(uf, 'run_verification') as verifier:
                    with self.assertRaisesRegex(uf.FlowError, 'Review file must be JSON'):
                        uf.reconcile(root, self.files, affected, 'base', review,
                                     records=specs.RECORDS, constraints=specs.constraints)
                    verifier.assert_not_called()
                self.assertEqual(prior.read_bytes(), b'prior receipt must survive invalid review')


if __name__ == '__main__':
    unittest.main()
