#!/usr/bin/env python3
"""Render preflight, lossless tables and Mermaid identifier regressions."""
import copy
import json
import pathlib
import re
import subprocess
import sys
import tempfile
import unittest

import specs
import user_flows as uf
from test_specs import ROOT, fixture, put, write_tree
from test_user_flows import fixture as legacy_fixture


class RenderingTests(unittest.TestCase):
    def cli(self, root, script='specs.py'):
        return subprocess.run([sys.executable, str(ROOT / 'scripts' / script), 'render', '--root', str(root), '--json'], capture_output=True, text=True)

    def tree(self, root):
        return {str(p.relative_to(root)): p.read_bytes() for p in root.rglob('*') if p.is_file() and '.git' not in p.relative_to(root).parts}

    def run_invalid(self, files, script='specs.py'):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            write_tree(root, files)
            subprocess.run(['git', 'init', '-q', str(root)], check=True)
            before = self.tree(root)
            result = self.cli(root, script)
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertTrue(json.loads(result.stdout)['errors'])
            self.assertEqual(self.tree(root), before)

    def test_render_preflight_rejects_unowned_ui_path_without_any_writes(self):
        files = fixture()
        ui = uf.load(files, specs.UI)
        ui['documents'][0]['path'] = 'app.py'
        put(files, specs.UI, ui)
        files['app.py'] = specs.marker('instances')[0].encode() + b'\n' + files['app.py'] + specs.marker('instances')[1].encode() + b'\n'
        files['docs/glossary/README.md'] = files['docs/glossary/README.md'].replace(b'A named part', b'Stale generated part')
        self.run_invalid(files)
        self.run_invalid(files, 'user_flows.py')

    def test_invalid_later_flow_and_incomplete_legacy_metadata_leave_all_files_unchanged(self):
        for unified in (False, True):
            files = fixture() if unified else legacy_fixture()
            catalog = uf.load(files, uf.CATALOG)
            # A valid first output must not be written before a bad later recipe.
            catalog['flows'][0]['implementation'][0]['purpose'] = 'Updated reference purpose'
            bad = copy.deepcopy(catalog['flows'][0])
            bad.update(id='other', spec='app.py')
            catalog['flows'].append(bad)
            files['app.py'] += ('\n' + uf.MARKER + '\nAuthored source body\n' + uf.END + '\n').encode()
            put(files, uf.CATALOG, catalog)
            self.run_invalid(files, 'specs.py' if unified else 'user_flows.py')
            if not unified:
                catalog['flows'][1]['spec'] = uf.DIRECTORY + 'other.md'
                files[catalog['flows'][1]['spec']] = files[uf.DIRECTORY + 'example.md']
                del catalog['flows'][1]['implementation'][0]['purpose']
                put(files, uf.CATALOG, catalog)
                self.run_invalid(files, 'user_flows.py')

    def test_json_registry_cannot_be_a_render_target_even_with_matching_markers(self):
        files = fixture()
        ui = uf.load(files, specs.UI)
        ui['documents'][0]['path'] = specs.UI
        ui['authored_note'] = specs.marker('instances')[0] + 'Authored registry value' + specs.marker('instances')[1]
        put(files, specs.UI, ui)
        self.run_invalid(files)

    def test_overlapping_owned_blocks_cannot_remove_authored_text(self):
        files = fixture()
        ui = uf.load(files, specs.UI)
        ui['documents'][0]['blocks'].append({'name': 'nested', 'kind': 'terminology', 'headers': ['Entity', 'Kind', 'Name']})
        put(files, specs.UI, ui)
        files['docs/ui/README.md'] = ('Authored prefix\n' + specs.marker('instances')[0] + '\n' + specs.marker('nested')[0] + '\nAuthored middle\n' + specs.marker('nested')[1] + '\n' + specs.marker('instances')[1] + '\nAuthored suffix').encode()
        self.run_invalid(files)

    def test_stale_owned_blocks_render_idempotently_and_preserve_authored_content(self):
        for unified in (False, True):
            with self.subTest(unified=unified), tempfile.TemporaryDirectory() as directory:
                files = fixture() if unified else legacy_fixture()
                catalog = uf.load(files, uf.CATALOG)
                catalog['flows'][0]['implementation'][0]['purpose'] = 'A revised generated reference'
                put(files, uf.CATALOG, catalog)
                flow_path = catalog['flows'][0]['spec']
                suffix = b'\nIndependently authored trailing prose.\n'
                files[flow_path] += suffix
                if unified:
                    terms = uf.load(files, specs.TERMS)
                    terms['terms'][0]['name'] = 'Visible record'
                    put(files, specs.TERMS, terms)
                    files['docs/ui/README.md'] += suffix
                    files['docs/glossary/README.md'] += suffix
                    specs.validate(files, rendered=False)
                else:
                    uf.validate(files, rendered=False)
                with self.assertRaisesRegex(uf.FlowError, 'stale'):
                    (specs.validate if unified else uf.validate)(files)
                source = files['app.py']
                authored = uf.prose(files, catalog['flows'][0])
                root = pathlib.Path(directory)
                write_tree(root, files)
                subprocess.run(['git', 'init', '-q', str(root)], check=True)
                script = 'specs.py' if unified else 'user_flows.py'
                first = self.cli(root, script)
                self.assertEqual(first.returncode, 0, first.stdout + first.stderr)
                once = self.tree(root)
                second = self.cli(root, script)
                self.assertEqual(second.returncode, 0, second.stdout + second.stderr)
                self.assertEqual(once, self.tree(root))
                self.assertEqual(once['app.py'], source)
                self.assertEqual(uf.prose(once, catalog['flows'][0]), authored)
                for path in ('docs/ui/README.md', 'docs/glossary/README.md', flow_path) if unified else (flow_path,):
                    self.assertTrue(once[path].endswith(suffix))
                (specs.validate if unified else uf.validate)(once)

    def test_mermaid_uses_safe_aliases_for_reserved_registry_ids_and_preserves_edges(self):
        graph = {'start': 'end', 'nodes': [
            {'id': 'end', 'label': 'Begin " ] --> <script>\n'},
            {'id': 'graph', 'label': 'Decide'},
            {'id': 'N0', 'label': 'Done', 'terminal': True}],
            'edges': [{'from': 'end', 'to': 'graph', 'label': 'yes | " ]'},
                      {'from': 'graph', 'to': 'N0'}, {'from': 'graph', 'to': 'end', 'label': 'retry'}]}
        original = copy.deepcopy(graph)
        rendered = specs.flow_diagram(graph)
        self.assertEqual(graph, original)
        self.assertEqual(rendered, specs.flow_diagram(graph))
        self.assertEqual(re.findall(r'^    (N\d+)\[', rendered, re.M), ['N0', 'N1'])
        self.assertIn('    N2(["Done"])', rendered)
        self.assertIn('    N0 -->|"yes #124; #34; #93;"| N1', rendered)
        self.assertIn('    N1 --> N2', rendered)
        self.assertIn('    N1 -->|"retry"| N0', rendered)
        self.assertNotRegex(rendered, r'^    (end|graph)\b')
        self.assertNotIn('<script>', rendered)
        self.assertEqual(rendered.count('-->'), len(graph['edges']))

    def table_fixture(self, kind, fields=None):
        files = fixture()
        ui = uf.load(files, specs.UI)
        widths = {'terminology': 3, 'sources': 3, 'assertions': 3, 'coverage': 3, 'instances': 4}
        block = {'name': 'inventory', 'kind': kind}
        if kind == 'entities':
            block.update(ids=['W-EXAMPLE'], fields=fields)
            width = len(fields) + 1
        else:
            width = widths[kind]
        block['headers'] = ['Column ' + str(i) for i in range(width)]
        ui['documents'][0]['blocks'] = [block]
        put(files, specs.UI, ui)
        files['docs/ui/README.md'] = ('# Authored title\n' + specs.marker('inventory')[0] + '\n' + specs.marker('inventory')[1] + '\nAuthored ending\n').encode()
        return files, ui, block, width

    def test_every_table_recipe_rejects_wrong_header_width_and_invalid_header_values(self):
        for kind in ('entities', 'terminology', 'sources', 'assertions', 'coverage', 'instances'):
            for headers in ([], ['One'], ['x'] * 8, 'Columns', [None] * (3 if kind != 'instances' else 4), [' '] * (3 if kind != 'instances' else 4)):
                with self.subTest(kind=kind, headers=headers):
                    files, ui, block, _ = self.table_fixture(kind, ['kind', 'terms'] if kind == 'entities' else None)
                    block['headers'] = headers
                    put(files, specs.UI, ui)
                    with self.assertRaisesRegex(uf.FlowError, 'headers'):
                        specs.validate(files, rendered=False)
                    self.run_invalid(files)

    def test_varied_entity_tables_and_inventory_tables_render_all_columns(self):
        for kind, fields in [('entities', []), ('entities', ['kind']), ('entities', ['kind', 'terms']), ('entities', ['kind', 'terms', 'gaps'])] + [(kind, None) for kind in ('terminology', 'sources', 'assertions', 'coverage', 'instances')]:
            with self.subTest(kind=kind, fields=fields):
                files, _, _, width = self.table_fixture(kind, fields)
                for path, text in specs.generated(files).items():
                    files[path] = text.encode()
                specs.validate(files)
                rows = [line for line in files['docs/ui/README.md'].decode().splitlines() if line.startswith('|')]
                self.assertTrue(rows)
                self.assertTrue(all(line.count('|') == width + 1 for line in rows))

    def test_table_defensive_width_check_and_header_escaping(self):
        for headers, rows in [(['One'], [[1, 2]]), (['One', 'Two'], [[1]]), ([], []), ([None], [[1]])]:
            with self.assertRaises(uf.FlowError):
                specs.table(headers, rows)
        self.assertIn('| A&#124;B<br>C |', specs.table(['A|B\nC'], [['kept']]))
        self.assertIn('| A<br>B |', specs.table(['A\rB'], [['kept']]))

    def test_production_table_recipe_shapes_accept_varied_headers_and_fields(self):
        # Exercise the maintained widget, pane, page, dialog and inventory shapes.
        production = json.loads((ROOT / specs.UI).read_bytes())
        for doc in production['documents']:
            for block in doc['blocks']:
                if block['kind'] in ('containment', 'connections'):
                    continue
                with self.subTest(path=doc['path'], block=block['name']):
                    fields = block.get('fields')
                    files, ui, recipe, width = self.table_fixture(block['kind'], fields)
                    if fields is not None:
                        for entity in ui['entities']:
                            for field in fields:
                                entity[field] = 'Maintained description'
                    recipe['headers'] = block['headers']
                    put(files, specs.UI, ui)
                    result = specs.generated(files)['docs/ui/README.md']
                    rows = [line for line in result.splitlines() if line.startswith('|')]
                    self.assertTrue(all(line.count('|') == width + 1 for line in rows))


if __name__ == '__main__':
    unittest.main()
