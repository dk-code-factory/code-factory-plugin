#!/usr/bin/env python3
"""Isolated regression fixtures for the maintained specification system."""
import copy
import contextlib
import io
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import specs
import user_flows as uf
from test_user_flows import fixture as flow_fixture

ROOT = pathlib.Path(__file__).resolve().parents[1]


def put(files, path, value):
    files[path] = (json.dumps(value, indent=2) + '\n').encode()


def fixture():
    files = flow_fixture()
    c = uf.load(files, uf.CATALOG)
    c['exclusions'] += [{'glob': '.codex/**', 'reason': 'Maintenance instructions'}, {'glob': '.agents/**', 'reason': 'Maintenance instructions'}]
    c['suites']['tooling'] = c['suites']['unit']
    f = c['flows'][0]
    f.update(terms=['record'], ui=['W-EXAMPLE'], graph={'start': 'start', 'nodes': [{'id': 'start', 'label': 'Read record', 'scenario': 'S1'}, {'id': 'done', 'label': 'Observed result', 'terminal': True}], 'edges': [{'from': 'start', 'to': 'done'}]})
    put(files, uf.CATALOG, c)
    terms = [{'id': t, 'name': t.capitalize(), 'aliases': [], 'category': 'domain', 'definition': 'A named part of the available work.'} for t in ['record', 'page', 'pane', 'control', 'action', 'edge']]
    put(files, specs.TERMS, {'version': 1, 'terms': terms})
    def entity(eid, kind, term, **extras):
        return dict(id=eid, kind=kind, terms=[term], contains=[], elements=[], states=[], implementation=f['implementation'], assertions=['ASSERT'], gaps=[], suites=['unit'], **extras)
    entities = [entity('PAGE-EXAMPLE', 'page', 'page'), entity('P-EXAMPLE', 'pane', 'pane'), entity('W-EXAMPLE', 'widget', 'control'), entity('W-ACTION', 'widget', 'action'), entity('C-EXAMPLE', 'connection', 'edge', connection_type='edge', **{'from': ['W-ACTION'], 'to': ['W-EXAMPLE']})]
    entities[0]['contains'] = ['P-EXAMPLE']
    ui = {'version': 1, 'entities': entities, 'instances': [{'id': 'example', 'type': 'W-EXAMPLE', 'pane': 'P-EXAMPLE', 'repeat': 'one'}, {'id': 'action', 'type': 'W-ACTION', 'pane': 'P-EXAMPLE', 'repeat': 'one'}], 'assertions': [{'id': 'ASSERT', 'path': 'scripts/test_app.py', 'anchor': 'def test_perform():', 'assertion': 'An outcome is asserted.'}], 'source_map': [], 'coverage_groups': [], 'documents': [{'path': 'docs/ui/README.md', 'blocks': [{'name': 'instances', 'kind': 'instances', 'headers': ['Instance', 'Type', 'Pane', 'Context']}]}]}
    put(files, specs.UI, ui)
    for p, name in [('docs/glossary/README.md', 'terms'), ('docs/ui/README.md', 'instances')]:
        files[p] = ('# Inventory\n\n' + specs.marker(name)[0] + '\n' + specs.marker(name)[1] + '\n').encode()
    files['scripts/specs.py'] = b'# Operational tool fixture\n'
    files['scripts/user_flows.py'] = b'# Legacy tool fixture\n'
    files['scripts/test_specs.py'] = b'def test_fixture():\n    assert True\n'
    c['flows'][0]['implementation'].append({'path': 'scripts/user_flows.py', 'purpose': 'Legacy tool fixture'})
    c['flows'][0]['tests'].append({'path': 'scripts/test_specs.py', 'anchor': 'def test_fixture():', 'scenarios': ['S1'], 'purpose': 'Tool fixture'})
    c['flows'][0]['implementation'].append({'path': 'scripts/specs.py', 'purpose': 'Operational tool fixture'})
    put(files, uf.CATALOG, c)
    files['docs/specs/README.md'] = b'# Specification guide\n'
    files['docs/specs/reconciliations/.keep'] = b''
    # Exercise the actual standalone definitions and skill ownership references.
    for directory in ['.codex/agents', '.agents/skills']:
        for path in (ROOT / directory).rglob('*'):
            if path.is_file():
                files[str(path.relative_to(ROOT))] = path.read_bytes()
    render(files)
    return files


def render(files):
    for path, text in specs.generated(files).items():
        files[path] = text.encode()


def edit(files, path, mutate, *, rerender=True):
    value = uf.load(files, path)
    mutate(value)
    put(files, path, value)
    if rerender:
        render(files)


def write_tree(root, files):
    for path, data in files.items():
        p = root / path
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(data)


class RegistryTests(unittest.TestCase):
    def setUp(self):
        self.before = fixture()
        self.after = copy.deepcopy(self.before)

    def impact(self):
        return specs.analyze(self.before, self.after, 'base')

    def test_dependency_direction(self):
        for prefix in ['[inline](../user-flows/example.md)', '[ref][x]\n[x]: ../user-flows/example.md', '<a href="../user-flows/example.md">text</a>', '../user-flows/example.md', 'docs/user-flows/example.md', 'example.md', '<a href="..%2Fuser-flows%2Fexample.md">x</a>']:
            with self.subTest(prefix=prefix):
                for path in ['docs/ui/README.md', 'docs/glossary/README.md']:
                    files = copy.deepcopy(self.before)
                    files[path] += ('\n' + prefix).encode()
                    with self.assertRaisesRegex(uf.FlowError, 'forbidden dependency'):
                        specs.validate(files)
        for reference in ['[ui](../ui/README.md)', '[u]: ../ui/entities.json', '<a href="../ui/widgets.md">x</a>', 'docs/ui/entities.json', 'W-EXAMPLE']:
            files = copy.deepcopy(self.before)
            files['docs/glossary/README.md'] += reference.encode()
            with self.assertRaisesRegex(uf.FlowError, 'forbidden dependency'):
                specs.validate(files)
        for heading in ['## Used by', '## Usage', '## Consumers', '## Backlinks']:
            files = copy.deepcopy(self.before)
            files['docs/glossary/README.md'] += ('\n' + heading + '\nConsumer list').encode()
            with self.assertRaisesRegex(uf.FlowError, 'forbidden glossary'):
                specs.validate(files)
        specs.validate(self.before)

    def test_missing_ids_names_definitions_and_scopes(self):
        mutations = [
            (specs.TERMS, lambda x: x['terms'].append(x['terms'][0])),
            (specs.TERMS, lambda x: x['terms'][1].update(name='Record')),
            (specs.TERMS, lambda x: x['terms'][1].update(aliases=['Record'])),
            (specs.TERMS, lambda x: x['terms'][0].update(definition='')),
            (specs.TERMS, lambda x: x['terms'][0].update(definition='Run the scripts/check.py command.')),
            (specs.TERMS, lambda x: x['terms'][0].update(scope='undefined')),
            (specs.UI, lambda x: x['entities'][0].update(terms=['unknown'])),
            (specs.UI, lambda x: x['entities'].append(x['entities'][0])),
            (uf.CATALOG, lambda x: x['flows'][0].update(ui=['W-MISSING'])),
            (uf.CATALOG, lambda x: x['flows'][0].update(terms=['missing']))]
        for path, mutation in mutations:
            with self.subTest(path=path):
                files = copy.deepcopy(self.before)
                edit(files, path, mutation)
                with self.assertRaises(uf.FlowError):
                    specs.validate(files)
        edit(self.after, specs.TERMS, lambda x: x['terms'][1].update(name='Record', scope='record'))
        specs.validate(self.after)

    def test_containment_cycles_instances_and_connections(self):
        mutations = [lambda x: x['entities'][1]['contains'].append('PAGE-EXAMPLE'), lambda x: x['entities'][4].update(to=['W-NOPE']), lambda x: x['entities'][4].update(to=['W-EXAMPLE.missing']), lambda x: x['instances'][0].update(pane='W-EXAMPLE'), lambda x: x.update(instances=[])]
        for mutation in mutations:
            files = copy.deepcopy(self.before)
            edit(files, specs.UI, mutation)
            with self.assertRaises(uf.FlowError):
                specs.validate(files)

    def test_assertion_and_source_anchors(self):
        for path, mutation in [(specs.UI, lambda x: x['assertions'][0].update(anchor='missing')), (specs.UI, lambda x: x['entities'][0]['implementation'][0].update(anchor='missing'))]:
            files = copy.deepcopy(self.before)
            edit(files, path, mutation)
            with self.assertRaisesRegex(uf.FlowError, 'anchor'):
                specs.validate(files)

    def test_render_is_deterministic_and_preserves_prose(self):
        self.after['docs/ui/README.md'] += b'\nIndependently authored prose.\n'
        self.after['docs/user-flows/example.md'] += b'\nAuthored after generated references.\n'
        render(self.after)
        once = copy.deepcopy(self.after)
        render(self.after)
        self.assertEqual(once, self.after)
        self.assertIn(b'Independently authored prose.', self.after['docs/ui/README.md'])
        self.assertIn(b'Authored after generated references.', self.after['docs/user-flows/example.md'])
        for path in ['docs/ui/README.md', 'docs/glossary/README.md', 'docs/user-flows/example.md']:
            stale = dict(self.after)
            stale[path] = stale[path].replace(b'<!--', b'<!-- stale ', 1)
            with self.assertRaises(uf.FlowError):
                specs.validate(stale)

    def test_graph_validation_and_mermaid_escaping(self):
        graph = uf.load(self.before, uf.CATALOG)['flows'][0]['graph']
        for mutation in [lambda g: g['nodes'].append(g['nodes'][0]), lambda g: g['edges'][0].update(to='absent'), lambda g: g.update(edges=[]), lambda g: g['nodes'][1].update(terminal=False), lambda g: g['edges'].append({'from': 'done', 'to': 'start'})]:
            g = copy.deepcopy(graph); mutation(g)
            self.assertTrue(specs.graph_errors(g))
        graph['nodes'][0]['label'] = 'quote " ] --> evil [ <script>\n & | %% { }'
        text = specs.flow_diagram(graph)
        self.assertIn('#34;', text)
        self.assertNotIn('<script>', text)
        self.assertNotIn('%%', text)
        self.assertEqual(text.count('-->'), 1)

    def test_reverse_impact_and_flow_only_direction(self):
        edit(self.after, specs.TERMS, lambda x: x['terms'][3].update(definition='A revised meaning for the visible control.'))
        report, affected = self.impact()
        self.assertIn('W-EXAMPLE', [x['id'] for x in report['entities']])
        self.assertIn('W-ACTION', [x['id'] for x in report['entities']])
        self.assertIn('PAGE-EXAMPLE', [x['id'] for x in report['entities']])
        self.assertIn('flow-example', affected)
        self.after = copy.deepcopy(self.before)
        path = 'docs/user-flows/example.md'
        self.after[path] = self.after[path].replace(b'Work completes.', b'Work completes twice.')
        report, affected = self.impact()
        self.assertEqual(report['terms'], [])
        self.assertEqual(report['entities'], [])
        self.assertEqual(set(affected), {'flow-example'})

    def test_removed_mapping_and_renamed_deleted_entity(self):
        self.after['new.py'] = self.after.pop('app.py')
        edit(self.after, uf.CATALOG, lambda x: x['flows'][0]['implementation'][0].update(path='new.py'))
        edit(self.after, specs.UI, lambda x: [r.update(path='new.py') for e in x['entities'] for r in e['implementation']])
        report, affected = self.impact()
        self.assertIn('app.py', affected['ui']['implementation'])
        self.assertIn('new.py', affected['ui']['implementation'])
        self.after = copy.deepcopy(self.before)
        edit(self.after, specs.UI, lambda x: (x['entities'][2].update(id='W-RENAMED'), x['instances'][0].update(type='W-RENAMED'), x['entities'][4].update(to=['W-RENAMED'])))
        edit(self.after, uf.CATALOG, lambda x: x['flows'][0].update(ui=['W-RENAMED']))
        report, affected = self.impact()
        self.assertIn('W-EXAMPLE', [x['id'] for x in report['entities']])
        self.assertIn('W-RENAMED', [x['id'] for x in report['entities']])
        self.assertTrue(affected['ui']['lifecycle_changed'])

    def test_records_require_review_and_dependency_freshness(self):
        self.after['app.py'] += b'# reviewed refactor\n'
        report, affected = self.impact()
        for subject, item in affected.items():
            self.assertTrue(specs.receipt_errors(self.after, subject, item, 'base'))
            review = {'kind': 'refactor', **dict.fromkeys(['description', 'implementation', 'tests'], 'The existing outcome is preserved; inspected the specific assertion and implementation.')}
            record = {'version': 1, 'flow': subject, 'base': 'base', 'fingerprint': item['fingerprint'], 'environment': uf.FIXTURE_ENV, 'review': review, 'dependencies': item['dependencies'], 'verification': [{'command': c, 'exit_code': 0} for s in item['suites'].values() for c in s['commands']]}
            put(self.after, specs.RECORDS + subject + '.json', record)
            self.assertEqual(specs.receipt_errors(self.after, subject, item, 'base'), [])
            self.assertTrue(specs.receipt_errors(self.after, subject, item, 'wrong-base'))
        edit(self.after, specs.TERMS, lambda x: x['terms'][3].update(definition='A revised meaning of the named control.'))
        self.assertTrue(specs.receipt_errors(self.after, 'flow-example', self.impact()[1]['flow-example'], 'base'))

    def test_behavior_cochanges_and_classified_corrections(self):
        item = {'description_changed': True, 'adopted': False, 'implementation': [], 'tests': []}
        notes = dict.fromkeys(['description', 'implementation', 'tests'], 'Reviewed the relevant transition and assertion; outcome is accurately described here.')
        self.assertTrue(specs.constraints('flow-example', item, {**notes, 'kind': 'behavior'}))
        self.assertEqual(specs.constraints('flow-example', item, {**notes, 'kind': 'structure'}), [])
        item['lifecycle_changed'] = True
        self.assertTrue(specs.constraints('flow-example', item, {**notes, 'kind': 'structure'}))
        self.assertTrue(specs.constraints('ui', item, {**notes, 'kind': 'adoption'}))
        self.assertTrue(specs.constraints('ui', item, {**notes, 'kind': 'refactor', 'tests': 'reviewed'}))

    def test_agent_toml_skills_and_ownership(self):
        self.assertEqual(specs.validate_agents(self.before), [])
        for path, payload in [('.codex/agents/glossary-maintainer.toml', b'model = "override"\n'), ('.codex/agents/glossary-maintainer.toml', b'bad {{{'), ('.agents/skills/maintain-glossary/SKILL.md', b'# Missing metadata')]:
            files = copy.deepcopy(self.before);files[path] = payload
            self.assertTrue(specs.validate_agents(files))
        files = copy.deepcopy(self.before)
        files['.codex/agents/glossary-maintainer.toml'] = files['.codex/agents/glossary-maintainer.toml'].replace(b'`docs/glossary/terms.json`', b'`missing/ownership.json`')
        self.assertTrue(specs.validate_agents(files))


    def test_ui_only_mappings_and_deleted_source_are_reviewed(self):
        self.before['ui_helper.py'] = b'def assist():\n    return 1\n'
        edit(self.before, specs.UI, lambda x: x['entities'][3]['implementation'].append({'path': 'ui_helper.py', 'anchor': 'def assist():', 'purpose': 'UI-only helper'}))
        specs.validate(self.before)
        self.after = copy.deepcopy(self.before)
        self.after['ui_helper.py'] += b'# preserved behavior\n'
        report, affected = self.impact()
        self.assertIn('ui_helper.py', affected['ui']['implementation'])
        self.assertIn('flow-example', affected)
        del self.after['ui_helper.py']
        edit(self.after, specs.UI, lambda x: x['entities'][3]['implementation'].pop())
        self.assertIn('ui_helper.py', self.impact()[1]['ui']['implementation'])

    def test_unconsumed_term_does_not_stale_unrelated_flow(self):
        orphan = {'id': 'unconsumed', 'name': 'Unconsumed', 'aliases': [], 'category': 'domain', 'definition': 'A separate named concept without a consumer.'}
        edit(self.before, specs.TERMS, lambda x: x['terms'].append(orphan))
        self.after = copy.deepcopy(self.before)
        self.after['app.py'] += b'# preserved behavior\n'
        fingerprint = self.impact()[1]['flow-example']['fingerprint']
        edit(self.after, specs.TERMS, lambda x: x['terms'][-1].update(definition='A corrected independent meaning without consumers.'))
        self.assertEqual(self.impact()[1]['flow-example']['fingerprint'], fingerprint)

    def test_graph_only_contract_changes_need_review_but_not_foundation_edits(self):
        edit(self.after, uf.CATALOG, lambda x: x['flows'][0]['graph']['nodes'][0].update(label='Inspect two records'))
        self.assertEqual(uf.prose(self.before, uf.load(self.before, uf.CATALOG)['flows'][0]), uf.prose(self.after, uf.load(self.after, uf.CATALOG)['flows'][0]))
        report, affected = self.impact()
        self.assertEqual(report['entities'], [])
        item = affected['flow-example']
        self.assertTrue(item['description_changed'])
        notes = dict.fromkeys(['description', 'implementation', 'tests'], 'Reviewed this changed transition against its relevant implementation and concrete assertions.')
        self.assertTrue(specs.constraints('flow-example', item, {**notes, 'kind': 'behavior'}))

    def test_deletion_of_entity_and_mapping_retains_old_impact(self):
        edit(self.after, specs.UI, lambda x: (x.update(entities=[e for e in x['entities'] if e['id'] not in {'C-EXAMPLE', 'W-ACTION'}]), x.update(instances=[i for i in x['instances'] if i['type'] != 'W-ACTION'])))
        report, affected = self.impact()
        self.assertIn('W-ACTION', [e['id'] for e in report['entities']])
        self.assertIn('C-EXAMPLE', [e['id'] for e in report['entities']])
        self.assertIn('flow-example', affected)
        self.assertTrue(affected['ui']['lifecycle_changed'])

    def test_stale_generated_content_and_undefined_render_references(self):
        for path, old, new in [('docs/ui/README.md', b'W-EXAMPLE', b'W-STALE'), ('docs/glossary/README.md', b'A named part', b'A stale part'), ('docs/user-flows/example.md', b'Read record', b'Read stale record')]:
            with self.subTest(path=path):
                files = copy.deepcopy(self.before)
                self.assertIn(old, files[path])
                files[path] = files[path].replace(old, new, 1)
                with self.assertRaisesRegex(uf.FlowError, 'stale'):
                    specs.validate(files)
        files = copy.deepcopy(self.before)
        edit(files, specs.UI, lambda x: x['documents'][0]['blocks'].append({'name': 'missing', 'kind': 'entities', 'ids': ['W-MISSING'], 'fields': [], 'headers': ['ID']}), rerender=False)
        with self.assertRaisesRegex(uf.FlowError, 'Undefined entity'):
            specs.validate(files)


    def test_ui_coverage_metadata_invalidates_existing_fingerprints(self):
        self.after['app.py'] += b'# preserved behavior\n'
        initial = self.impact()[1]
        edit(self.after, specs.UI, lambda x: x['coverage_groups'].append({'name': 'Control coverage', 'entities': ['W-EXAMPLE'], 'assertions': ['ASSERT'], 'gap': 'Keyboard and screen-reader coverage remain unverified.'}))
        changed = self.impact()[1]
        self.assertNotEqual(initial['ui']['fingerprint'], changed['ui']['fingerprint'])
        self.assertNotEqual(initial['flow-example']['fingerprint'], changed['flow-example']['fingerprint'])


class GitTests(unittest.TestCase):
    def run_cli(self, root, *args):
        out = io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(io.StringIO()):
            status = specs.main([*args, '--root', str(root), '--json'])
        return status, json.loads(out.getvalue())

    def test_git_targets_exact_and_merge_base_and_failed_rerun(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            def git(*args):
                return subprocess.check_output(['git', '-C', str(root), *args], stderr=subprocess.DEVNULL).decode().strip()
            git('init', '-q');git('config', 'user.name', 'Fixture');git('config', 'user.email', 'fixture@example.invalid')
            write_tree(root, fixture());git('add', '.');git('commit', '-qm', 'baseline');base = git('rev-parse', 'HEAD')
            (root/'app.py').write_text('def perform():\n    return 1\n# staged refactor\n');git('add', 'app.py')
            status, result = self.run_cli(root, 'impact', '--base', base, '--head', base)
            self.assertEqual(status, 0);self.assertEqual(result['affected'], {})
            (root/'app.py').write_text('def perform():\n    return 1\n')
            self.assertTrue(self.run_cli(root, 'impact', '--base', base, '--staged')[1]['affected'])
            self.assertEqual(self.run_cli(root, 'impact', '--base', base)[1]['affected'], {})
            git('checkout', '-b', 'other');git('commit', '-qm', 'indexed edit');other = git('rev-parse', 'HEAD');git('restore', '--worktree', 'app.py')
            git('checkout', '-b', 'topic', base)
            (root/'app.py').write_text('def perform():\n    return 2\n');git('add', '.');git('commit', '-qm', 'topic edit');head=git('rev-parse','HEAD')
            exact = self.run_cli(root, 'impact', '--base', other, '--head', head)[1]
            merged = self.run_cli(root, 'impact', '--base', other, '--head', head, '--merge-base')[1]
            self.assertEqual(exact['base'], other);self.assertEqual(merged['base'], base)
            self.assertNotEqual(exact['affected']['flow-example']['fingerprint'], merged['affected']['flow-example']['fingerprint'])
            self.assertEqual(self.run_cli(root, 'impact', '--base', 'missing')[0], 1)
            self.assertEqual(self.run_cli(root, 'check', '--head', head, '--staged')[0], 1)
            report, affected = specs.analyze(uf.snapshot(root, base), uf.snapshot(root), base)
            reviews = {s: {'kind': 'refactor', **dict.fromkeys(['description', 'implementation', 'tests'], 'Reviewed the preserved outcome and exact implementation/assertion references in this fixture.')} for s in affected}
            review_path = root.parent / (root.name + '-review.json')
            try:
                review_path.write_text(json.dumps(reviews))
                # Execute successful actual verification, then ensure a failed
                # rerun removes every selected success without changing inputs.
                with contextlib.redirect_stdout(io.StringIO()):
                    uf.reconcile(root, uf.snapshot(root), affected, base, review_path, records=specs.RECORDS, analyzer=specs.analyze, constraints=specs.constraints)
                self.assertEqual(self.run_cli(root, 'check', '--base', base)[0], 0)
                real_run = uf.run_verification
                def fail_verification(command, *args, **kwargs):
                    if command == [sys.executable, 'scripts/test_app.py']:
                        return subprocess.CompletedProcess(command, 1)
                    return real_run(command, *args, **kwargs)
                with patch('user_flows.run_verification', side_effect=fail_verification), contextlib.redirect_stdout(io.StringIO()):
                    with self.assertRaisesRegex(uf.FlowError, 'Verification failed'):
                        uf.reconcile(root, uf.snapshot(root), affected, base, review_path, records=specs.RECORDS, analyzer=specs.analyze, constraints=specs.constraints)
                self.assertEqual(self.run_cli(root, 'check', '--base', base)[0], 1)
            finally:
                review_path.unlink(missing_ok=True)


if __name__ == '__main__':
    unittest.main()
