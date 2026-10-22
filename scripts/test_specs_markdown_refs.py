#!/usr/bin/env python3
"""Markdown spellings cannot bypass foundation reference boundaries."""
import copy
import unittest

import specs
import user_flows as uf
from test_specs import edit, fixture


class MarkdownReferenceTests(unittest.TestCase):
    def setUp(self):
        self.before = fixture()

    def test_escaped_flow_destinations_fail_in_both_foundation_documents(self):
        references = (
            r'[Flow](../user\-flows/README.md)',
            r'[Flow](\.\./user\-flows/README\.md)',
            r'[Flow](..\/user\-flows/README.md)',
            '[Flow][outside]\n' + r'[outside]: ../user\-flows/README.md',
            '> [Flow][outside]\n> ' + r'[outside]: ../unused/../user\-flows/README.md',
        )
        for path in ('docs/glossary/README.md', 'docs/ui/README.md'):
            for reference in references:
                with self.subTest(path=path, reference=reference):
                    files = copy.deepcopy(self.before)
                    files[path] += ('\n' + reference + '\n').encode()
                    with self.assertRaisesRegex(uf.FlowError, 'forbidden dependency'):
                        specs.validate(files, rendered=False)

    def test_json_string_escapes_do_not_hide_markdown_destinations(self):
        for registry in (specs.TERMS, specs.UI):
            with self.subTest(registry=registry):
                files = copy.deepcopy(self.before)
                if registry == specs.TERMS:
                    edit(files, registry, lambda value: value['terms'][0].update(
                        name=r'Record [Flow](../user\-flows/README.md)'))
                else:
                    edit(files, registry, lambda value: value['entities'][0].update(
                        purpose=r'[Flow](../user\-flows/README.md)'))
                with self.assertRaisesRegex(uf.FlowError, 'forbidden dependency'):
                    specs.validate(files, rendered=False)

    def test_shortcut_links_resolve_reference_definitions_inside_containers(self):
        for prefix in ('> ', '- ', '+ ', '* ', '1. ', '2) ', '> - ', '- > ', '> > ', '  - > '):
            with self.subTest(prefix=prefix):
                files = copy.deepcopy(self.before)
                files['docs/glossary/README.md'] += ('\n' + prefix + '[Outside Label]: https://example.com\n').encode()
                edit(files, specs.TERMS, lambda value: value['terms'][0].update(
                    definition='A record with [OUTSIDE   LABEL].'))
                with self.assertRaisesRegex(uf.FlowError, 'definition contains a link'):
                    specs.validate(files)

    def test_container_reference_destination_may_follow_on_the_next_line(self):
        files = copy.deepcopy(self.before)
        files['docs/glossary/README.md'] += b'\n> [outside]:\n> https://example.com\n'
        edit(files, specs.TERMS, lambda value: value['terms'][0].update(definition='A record with [outside].'))
        with self.assertRaisesRegex(uf.FlowError, 'definition contains a link'):
            specs.validate(files)

    def test_shortcut_labels_resolve_on_nested_list_continuation_lines(self):
        for container in (
            '10. List item\n\n    [outside]: https://example.com\n',
            '10. Outer item\n\n    - Nested item\n\n      [outside]: https://example.com\n',
            '> 10. List item\n>\n>     [outside]: https://example.com\n',
            '10. > Quoted item\n\n    > [outside]: https://example.com\n',
            '10. List item\n\n    [outside]:\n    https://example.com\n',
        ):
            with self.subTest(container=container):
                files = copy.deepcopy(self.before)
                files['docs/glossary/README.md'] += ('\n' + container).encode()
                edit(files, specs.TERMS, lambda value: value['terms'][0].update(
                    definition='A record with [outside].'))
                with self.assertRaisesRegex(uf.FlowError, 'definition contains a link'):
                    specs.validate(files)

    def test_indented_code_labels_do_not_resolve_shortcuts(self):
        for container in (
            '    [outside]: https://example.com\n',
            '10. List item\n\n        [outside]: https://example.com\n',
            '10. List item\n\nAn ordinary paragraph ends the list.\n\n    [outside]: https://example.com\n',
            '>     [outside]: https://example.com\n',
        ):
            with self.subTest(container=container):
                files = copy.deepcopy(self.before)
                files['docs/glossary/README.md'] += ('\n' + container).encode()
                edit(files, specs.TERMS, lambda value: value['terms'][0].update(
                    definition='A record with optional [outside].'))
                specs.validate(files)

    def test_ordinary_prose_and_permitted_destinations_remain_valid(self):
        files = copy.deepcopy(self.before)
        files['docs/glossary/README.md'] += b'\n> [outside]: https://example.com\n'
        edit(files, specs.TERMS, lambda value: value['terms'][0].update(
            definition=r'A record with [context], escaped \[outside], and a value < 10 or > 1.'))
        files['docs/ui/README.md'] += ('\n' + r'[Glossary](../glossary/README\.md)' + '\n').encode()
        specs.validate(files)


if __name__ == '__main__':
    unittest.main()
