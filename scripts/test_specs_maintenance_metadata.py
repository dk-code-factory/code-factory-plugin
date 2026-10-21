#!/usr/bin/env python3
"""Required maintenance definitions validate without owning unrelated agents."""
import copy
import re
import unittest

import specs
import user_flows as uf
from test_specs import fixture


class MaintenanceMetadataTests(unittest.TestCase):
    skill = '.agents/skills/maintain-glossary/SKILL.md'

    def setUp(self):
        self.before = fixture()

    def with_description(self, description):
        files = copy.deepcopy(self.before)
        text = files[self.skill].decode()
        files[self.skill] = re.sub(r'(?m)^description: [^\n]*$',
                                  lambda match: 'description: ' + description, text).encode()
        return files

    def test_unrelated_agent_can_declare_its_own_configuration_and_paths(self):
        files = copy.deepcopy(self.before)
        files['.codex/agents/security-reviewer.toml'] = b'''name = "security-reviewer"
description = "Review security-sensitive changes."
developer_instructions = "Inspect `unrelated/private-module/` using the security review policy."
model = "a-different-model"
sandbox_mode = "read-only"
'''
        self.assertEqual(specs.validate_agents(files), [])
        specs.validate(files)

    def test_required_agents_still_require_inheritance_and_declared_ownership(self):
        path = '.codex/agents/glossary-maintainer.toml'
        for mutation in (
            lambda files: files.pop(path),
            lambda files: files.update({path: files[path] + b'\nmodel = "override"\n'}),
            lambda files: files.update({path: files[path].replace(b'name = "glossary-maintainer"', b'name = "other-maintainer"')}),
            lambda files: files.update({path: files[path].replace(b'`docs/glossary/terms.json`', b'`missing/ownership.json`')}),
        ):
            with self.subTest(mutation=mutation):
                files = copy.deepcopy(self.before)
                mutation(files)
                self.assertTrue(specs.validate_agents(files))

    def test_required_skill_rejects_nonstring_ambiguous_and_malformed_scalars(self):
        for description in (
            '', '[]', '{}', '[one, two]', '{label: thing}', 'null', '~',
            'true', 'False', 'yes', 'ON', 'Y', 'n', '<<', '=', '0', '-17', '1.5', '1e3', '0x10',
            '.inf', '2026-10-20', '!!str content', '&named content', '*named',
            '|', '>', "'unterminated", '"unterminated', '"invalid \\q"',
            'A mapping: value', 'A \x01 value', '"\\uD800"',
        ):
            with self.subTest(description=description):
                files = self.with_description(description)
                with self.assertRaisesRegex(uf.FlowError, 'invalid skill metadata'):
                    specs.validate(files)

    def test_empty_quoted_descriptions_are_rejected(self):
        for description in ('""', "''", '"  "', "'  '", '"\\n"'):
            with self.subTest(description=description):
                self.assertTrue(specs.validate_agents(self.with_description(description)))

    def test_description_policy_applies_to_decoded_content_and_scalar_boundaries(self):
        for description in (
            'x' * 1025, '"' + 'x' * 1025 + '"', "'" + 'x' * 1025 + "'",
            'Use <input> terms.', '"Use \\u003cinput\\u003e terms."',
            "'[TODO: describe the skill]'", 'Maintain the glossary.\t# comment',
            'Maintain\tthe glossary.', '\t"Maintain the glossary."',
            "'Maintain the glossary.'\t# comment",
        ):
            with self.subTest(description=description):
                with self.assertRaisesRegex(uf.FlowError, 'invalid skill metadata'):
                    specs.validate(self.with_description(description))
        for description in (
            'x' * 1024, '"' + 'x' * 1024 + '"', "'" + 'x' * 1024 + "'",
            '"' + '\\u0061' * 1024 + '"',
            'Maintain the glossary. # comment\tallowed after comment marker',
            "'Maintain\tthe glossary.'", '"Maintain\\tthe glossary."',
            '"' + ' ' * 3 + 'x' * 1024 + ' ' * 3 + '"',
        ):
            with self.subTest(description=description):
                self.assertEqual(specs.validate_agents(self.with_description(description)), [])

    def test_plain_and_quoted_string_descriptions_remain_valid(self):
        for description in (
            'Maintain terms, [context], punctuation & Unicode café — 安全.',
            'Read https://example.com/docs#context when appropriate.',
            '123 records describe available work.',
            'Use a name-value pair; retain a:b and #context spellings.',
            'Describe the available work. # An optional metadata comment',
            "'Maintain the owner''s glossary: names and meanings.'",
            '"Maintain terms: preserve \\"quoted\\" names and Unicode \\u00e9."',
            '"[] is a string here" # A trailing comment',
            "'null' # Quoted scalars remain strings",
        ):
            with self.subTest(description=description):
                files = self.with_description(description)
                self.assertEqual(specs.validate_agents(files), [])
                specs.validate(files)

    def test_metadata_validation_retains_sections_and_known_command_checks(self):
        for old, new in ((b'## Handoff', b'## Missing section'),
                         (b'scripts/specs.py', b'scripts/missing.py')):
            with self.subTest(old=old):
                files = copy.deepcopy(self.before)
                files[self.skill] = files[self.skill].replace(old, new)
                self.assertTrue(specs.validate_agents(files))


if __name__ == '__main__':
    unittest.main()
