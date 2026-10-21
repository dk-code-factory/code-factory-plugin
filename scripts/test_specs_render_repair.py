#!/usr/bin/env python3
"""Render repairs obsolete owned content while keeping authored checks atomic."""
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

import specs
import user_flows as uf
from test_specs import ROOT, fixture, put, write_tree


class RenderRepairTests(unittest.TestCase):
    scripts = ('specs.py', 'user_flows.py')

    def cli(self, root, script):
        return subprocess.run([sys.executable, str(ROOT / 'scripts' / script), 'render', '--root', str(root), '--json'], capture_output=True, text=True)

    def tree(self, root):
        return {str(path.relative_to(root)): path.read_bytes() for path in root.rglob('*')
                if path.is_file() and '.git' not in path.relative_to(root).parts}

    def exercise(self, files, *, error=None):
        for script in self.scripts:
            with self.subTest(script=script), tempfile.TemporaryDirectory() as directory:
                root = pathlib.Path(directory)
                write_tree(root, files)
                subprocess.run(['git', 'init', '-q', str(root)], check=True)
                before = self.tree(root)
                result = self.cli(root, script)
                report = json.loads(result.stdout)
                if error:
                    self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertIn(error, '\n'.join(report['errors']))
                    self.assertEqual(self.tree(root), before)
                    continue
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                after = self.tree(root)
                specs.validate(after)
                self.assertEqual(after, {**files, **{path: text.encode() for path, text in specs.generated(files).items()}})
                for path in ('docs/glossary/README.md', 'docs/ui/README.md'):
                    self.assertEqual(specs.authored(after, path), specs.authored(files, path))
                second = self.cli(root, script)
                self.assertEqual(second.returncode, 0, second.stdout + second.stderr)
                self.assertEqual(self.tree(root), after)

    def stale_body(self, files, path, name, body):
        files[path] = specs.replace_block(files[path].decode(), name, body).encode()

    def test_render_repairs_forbidden_dependencies_only_in_stale_owned_bodies(self):
        for path, name, stale in (
            ('docs/ui/README.md', 'instances', '[old generated reference](../user-flows/example.md)'),
            ('docs/glossary/README.md', 'terms', '[old generated UI](../ui/README.md)\nW-EXAMPLE\n## Consumers'),
            ('docs/glossary/README.md', 'terms', '[old generated behavior](../user-flows/example.md)'),
            ('docs/ui/README.md', 'instances', '<!-- specs:obsolete -->\n[old generated reference](../user-flows/example.md)\n<!-- /specs:obsolete -->'),
        ):
            with self.subTest(path=path, stale=stale):
                files = fixture()
                files[path] = b'Authored prefix with [ordinary brackets].\n' + files[path] + b'\nAuthored suffix.\n'
                self.stale_body(files, path, name, stale)
                with self.assertRaisesRegex(uf.FlowError, 'forbidden'):
                    specs.validate(files)
                specs.validate(files, rendered=False)
                self.exercise(files)

    def test_stale_owned_reference_labels_do_not_turn_current_brackets_into_links(self):
        files = fixture()
        terms = uf.load(files, specs.TERMS)
        terms['terms'][0]['definition'] = 'A record with optional [context].'
        put(files, specs.TERMS, terms)
        self.stale_body(files, 'docs/glossary/README.md', 'terms', '[context]: https://example.com\nObsolete generated prose.')
        with self.assertRaisesRegex(uf.FlowError, 'definition contains a link'):
            specs.validate(files)
        specs.validate(files, rendered=False)
        self.exercise(files)

    def test_authored_and_unregistered_marker_dependencies_fail_without_writes(self):
        for path, suffix in (
            ('docs/ui/README.md', '[authored](../user-flows/example.md)'),
            ('docs/ui/README.md', '<!-- specs:unknown -->\n[authored](../user-flows/example.md)\n<!-- /specs:unknown -->'),
            ('docs/ui/README.md', '<!-- specs:terms -->\nflow:example\n<!-- /specs:terms -->'),
            ('docs/glossary/README.md', '<!-- specs:instances -->\nW-EXAMPLE\n<!-- /specs:instances -->'),
            ('docs/ui/extra.md', '<!-- specs:instances -->\nflow:example\n<!-- /specs:instances -->'),
        ):
            with self.subTest(path=path, suffix=suffix):
                files = fixture()
                self.stale_body(files, 'docs/ui/README.md', 'instances', 'Stale inventory to repair.')
                files[path] = files.get(path, b'') + ('\n' + suffix + '\n').encode()
                self.exercise(files, error='forbidden dependency')

    def test_authored_reference_labels_and_current_definition_links_fail_without_writes(self):
        for definition, label in (
            ('A record with [context].', '\n[context]: https://example.com\n'),
            ('A record with [context].', '\n<!-- specs:unknown -->\n[context]: https://example.com\n<!-- /specs:unknown -->\n'),
            ('A record with [context](https://example.com).', ''),
            ('A record with [context][outside].', ''),
        ):
            with self.subTest(definition=definition, label=label):
                files = fixture()
                terms = uf.load(files, specs.TERMS)
                terms['terms'][0]['definition'] = definition
                put(files, specs.TERMS, terms)
                files['docs/glossary/README.md'] += label.encode()
                self.exercise(files, error='definition contains a link')

    def test_current_registry_dependencies_fail_without_writes(self):
        for registry in (specs.TERMS, specs.UI):
            with self.subTest(registry=registry):
                files = fixture()
                value = uf.load(files, registry)
                if registry == specs.TERMS:
                    value['terms'][0]['name'] = 'Record ../user-flows/example.md'
                else:
                    value['entities'][0]['purpose'] = '[behavior](../user-flows/example.md)'
                put(files, registry, value)
                self.exercise(files, error='forbidden dependency')

    def test_malformed_owned_markers_fail_without_writes(self):
        start, end = (part.encode() for part in specs.marker('instances'))
        for mutation in (
            lambda data: data.replace(end, b''),
            lambda data: data + start,
            lambda data: data.replace(start, b'PLACEHOLDER').replace(end, start).replace(b'PLACEHOLDER', end),
        ):
            with self.subTest(mutation=mutation):
                files = fixture()
                files['docs/ui/README.md'] = mutation(files['docs/ui/README.md'])
                self.exercise(files, error='generated markers')

    def test_current_generated_reference_context_is_validated_before_any_write(self):
        files = fixture()
        terms = uf.load(files, specs.TERMS)
        terms['terms'][0]['definition'] = 'A record with optional [context].'
        # Current generated headings can introduce labels absent from authored
        # prose. Preflight may pass, but the full candidate must reject this link.
        terms['terms'][1]['name'] = 'Page\n[context]: https://example.com'
        put(files, specs.TERMS, terms)
        specs.validate(files, rendered=False)
        candidate = {**files, **{path: text.encode() for path, text in specs.generated(files).items()}}
        with self.assertRaisesRegex(uf.FlowError, 'definition contains a link'):
            specs.validate(candidate)
        self.exercise(files, error='definition contains a link')


if __name__ == '__main__':
    unittest.main()
