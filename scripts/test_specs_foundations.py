#!/usr/bin/env python3
"""Foundation boundaries and authored-prose review regression fixtures."""
import copy
import unittest

import specs
import user_flows as uf
from test_specs import edit, fixture, render


class FoundationTests(unittest.TestCase):
    def setUp(self):
        self.before = fixture()

    def test_unowned_markers_require_review_and_fresh_fingerprints(self):
        path = "docs/ui/README.md"
        after = copy.deepcopy(self.before)
        after[path] += b"\n<!-- specs:unowned -->Changed authored instructions.<!-- /specs:unowned -->\n"
        specs.validate(after)
        _, affected = specs.analyze(self.before, after, "base")
        self.assertIn("ui", affected)
        self.assertIn("flow-example", affected)
        self.assertIn(b"Changed authored instructions.", specs.authored(after, path))
        first = affected["ui"]["fingerprint"]
        after[path] = after[path].replace(b"Changed authored instructions.", b"Different authored instructions.")
        self.assertNotEqual(first, specs.analyze(self.before, after, "base")[1]["ui"]["fingerprint"])

    def test_generated_exclusion_requires_ownership_at_the_document_path(self):
        path = "docs/ui/README.md"
        after = copy.deepcopy(self.before)
        after[path] = after[path].replace(b"<!-- specs:instances -->", b"<!-- specs:instances -->\nGenerated table changed.")
        self.assertEqual(specs.authored(self.before, path), specs.authored(after, path))
        # A block owned by the glossary or by another UI document stays authored.
        for name in ("terms", "other-table"):
            with self.subTest(name=name):
                after = copy.deepcopy(self.before)
                after[path] += ("\n" + specs.marker(name)[0] + "\nAuthored instructions.\n" + specs.marker(name)[1]).encode()
                specs.validate(after)
                self.assertIn(b"Authored instructions.", specs.authored(after, path))
                self.assertIn("ui", specs.analyze(self.before, after, "base")[1])
        # The same spelling becomes generated only at its registry-owned path.
        after = copy.deepcopy(self.before)
        after["docs/ui/extra.md"] = b"<!-- specs:instances -->Authored.<!-- /specs:instances -->"
        self.assertIn(b"Authored.", specs.authored(after, "docs/ui/extra.md"))

    def test_normalized_backlinks_are_rejected_in_every_link_form(self):
        references = [
            "[UI](.././ui/README.md)",
            "[UI][outside]\n[outside]: ../unused/../ui/README.md",
            '<a href="../%2e/ui/README.md">UI</a>',
            "<a href=.././ui/README.md>UI</a>",
            '<a href="..&#47;.&#47;ui&#47;README.md">UI</a>',
            "docs/glossary/../ui/README.md",
            "/docs/./ui/README.md",
            "https://example.com/docs/glossary/../ui/README.md",
            "../%252e/ui/README.md",
        ]
        for reference in references:
            with self.subTest(reference=reference):
                after = copy.deepcopy(self.before)
                after["docs/glossary/README.md"] += ("\n" + reference).encode()
                with self.assertRaisesRegex(uf.FlowError, "forbidden dependency"):
                    specs.validate(after)
        for reference in ("[Flow](.././user-flows/example.md)", '<a href="../unused/../user-flows/example.md">Flow</a>'):
            for path in ("docs/glossary/README.md", "docs/ui/README.md"):
                with self.subTest(path=path, reference=reference):
                    after = copy.deepcopy(self.before)
                    after[path] += ("\n" + reference).encode()
                    with self.assertRaisesRegex(uf.FlowError, "forbidden dependency"):
                        specs.validate(after)

    def test_short_ui_ids_use_the_accepted_entity_grammar(self):
        for entity_id in ("W-X", "P-X", "PAGE-X", "D-X", "S-X", "C-X", "W-X9-A"):
            with self.subTest(entity_id=entity_id):
                after = copy.deepcopy(self.before)
                after["docs/glossary/README.md"] += ("\n" + entity_id).encode()
                with self.assertRaisesRegex(uf.FlowError, "forbidden dependency"):
                    specs.validate(after)
        # The shortest widget ID really is accepted by the registry validator.
        after = copy.deepcopy(self.before)
        edit(after, specs.UI, lambda ui: (
            ui["entities"][2].update(id="W-X"),
            ui["instances"][0].update(type="W-X"),
            ui["entities"][4].update(to=["W-X"])))
        edit(after, uf.CATALOG, lambda catalog: catalog["flows"][0].update(ui=["W-X"]))
        specs.validate(after)

    def test_explicit_flow_references_survive_renames_and_deletion(self):
        for flow_id in ("example", "renamed", "never-defined"):
            for path in ("docs/glossary/README.md", "docs/ui/README.md"):
                with self.subTest(flow_id=flow_id, path=path):
                    after = copy.deepcopy(self.before)
                    after[path] += ("\nflow:" + flow_id).encode()
                    with self.assertRaisesRegex(uf.FlowError, "forbidden dependency"):
                        specs.validate(after)
                    self.assertTrue(specs.dependency_errors(after, {"flows": []}))
        after = self.renamed_flow()
        after["docs/ui/README.md"] += b"\nflow:example"
        with self.assertRaisesRegex(uf.FlowError, "forbidden dependency"):
            specs.validate(after)

    def renamed_flow(self):
        after = copy.deepcopy(self.before)
        after["docs/user-flows/renamed.md"] = after.pop("docs/user-flows/example.md")
        edit(after, uf.CATALOG, lambda catalog: catalog["flows"][0].update(id="renamed", spec="docs/user-flows/renamed.md"))
        return after

    def test_comparisons_retain_old_flow_reference_mappings(self):
        for reference in ("example.md", "flow-example", "#example"):
            with self.subTest(reference=reference):
                after = self.renamed_flow()
                after["docs/ui/README.md"] += ("\n" + reference).encode()
                specs.validate(after)
                with self.assertRaisesRegex(uf.FlowError, "forbidden dependency"):
                    specs.analyze(self.before, after, "base")

    def test_scopes_are_nonempty_known_term_ids(self):
        for scope in ("", " ", None, False, 0, [], {}, "unknown", " record", "record "):
            with self.subTest(scope=scope):
                after = copy.deepcopy(self.before)
                edit(after, specs.TERMS, lambda terms: terms["terms"][1].update(name="Record", scope=scope))
                with self.assertRaisesRegex(uf.FlowError, "scope"):
                    specs.validate(after)
        after = copy.deepcopy(self.before)
        edit(after, specs.TERMS, lambda terms: terms["terms"][1].update(name="Record", scope="record"))
        specs.validate(after)
        edit(after, specs.TERMS, lambda terms: terms["terms"][2].update(aliases=["Record"], scope="record"))
        with self.assertRaisesRegex(uf.FlowError, "duplicate/ambiguous scoped name"):
            specs.validate(after)

    def test_definitions_reject_links_resolved_by_the_glossary(self):
        definitions = [
            "See [the definition][outside].",
            "See [outside][].",
            "See [outside].",
            "See [OUTSIDE].",
            "See [outside\nlabel].",
            "See [the\ndefinition][outside].",
            "See [the definition](https://example.com).",
            'See <a href="https://example.com">the definition</a>.',
            "See &lt;a href=&quot;https://example.com&quot;&gt;the definition&lt;/a&gt;.",
            "See https://example.com.",
            "See www.example.com.",
            "See <mailto:outside@example.com>.",
        ]
        for definition in definitions:
            with self.subTest(definition=definition):
                after = copy.deepcopy(self.before)
                after["docs/glossary/README.md"] += b"\n[outside]: https://example.com\n[outside label]: https://example.com\n"
                edit(after, specs.TERMS, lambda terms: terms["terms"][0].update(definition=definition))
                with self.assertRaisesRegex(uf.FlowError, "definition contains a link"):
                    specs.validate(after)

    def test_definitions_allow_ordinary_punctuation(self):
        after = copy.deepcopy(self.before)
        edit(after, specs.TERMS, lambda terms: terms["terms"][0].update(
            definition="A record (one item): optional [context], quoted 'details', and a value < 10 or > 1."))
        specs.validate(after)
        render(after)
        self.assertEqual(after, {**after, **{p: t.encode() for p, t in specs.generated(after).items()}})


if __name__ == "__main__":
    unittest.main()
