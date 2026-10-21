#!/usr/bin/env python3
"""Exercise historical CLI dispatch against actual Git comparison snapshots."""
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

import specs
import user_flows as uf
from test_specs import fixture, put, write_tree
from test_user_flows import fixture as legacy_fixture


ENTRYPOINT = pathlib.Path(__file__).with_name("user_flows.py")


class DispatchTests(unittest.TestCase):
    def repository(self, files):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = pathlib.Path(temporary.name)
        write_tree(root, files)
        uf.git(root, "init", "-q")
        uf.git(root, "config", "user.name", "Specification fixture")
        uf.git(root, "config", "user.email", "fixture@example.invalid")
        uf.git(root, "add", ".")
        uf.git(root, "commit", "-qm", "Fixture baseline")
        return root

    def command(self, root, *arguments, status=0, root_equals=False):
        root_args = ["--root=" + str(root)] if root_equals else ["--root", str(root)]
        result = subprocess.run(
            [sys.executable, str(ENTRYPOINT), *arguments, *root_args, "--json"],
            capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, status, result.stdout + result.stderr)
        self.assertNotIn("Traceback", result.stderr)
        return json.loads(result.stdout)

    def invalid_glossary(self, files):
        terms = uf.load(files, specs.TERMS)
        terms["terms"][0]["aliases"].append("Pane")
        put(files, specs.TERMS, terms)

    def test_staged_invalid_glossary_cannot_downgrade_after_worktree_deletion(self):
        files = fixture()
        root = self.repository(files)
        self.invalid_glossary(files)
        write_tree(root, {specs.TERMS: files[specs.TERMS]})
        uf.git(root, "add", specs.TERMS)
        (root / specs.TERMS).unlink()
        report = self.command(root, "validate", "--staged", status=1)
        self.assertIn("duplicate/ambiguous scoped name Pane", "\n".join(report["errors"]))

    def test_staged_unified_validation_ignores_broken_worktree_foundations(self):
        root = self.repository(fixture())
        (root / specs.TERMS).unlink()
        (root / specs.UI).write_text("invalid worktree JSON")
        report = self.command(root, "validate", "--staged", root_equals=True)
        self.assertTrue(report["valid"])
        self.assertEqual(report["terms"], 6)
        self.assertEqual(report["entities"], 5)
        self.command(root, "validate", status=1)

    def test_committed_unified_validation_ignores_missing_worktree_glossary(self):
        root = self.repository(fixture())
        (root / specs.TERMS).unlink()
        report = self.command(root, "validate", "--head", "HEAD")
        self.assertTrue(report["valid"])
        self.assertEqual(report["terms"], 6)
        self.command(root, "validate", status=1)

    def test_committed_invalid_registry_ignores_valid_worktree_replacement(self):
        files = fixture()
        root = self.repository(files)
        valid_terms = files[specs.TERMS]
        self.invalid_glossary(files)
        write_tree(root, {specs.TERMS: files[specs.TERMS]})
        uf.git(root, "add", specs.TERMS)
        uf.git(root, "commit", "-qm", "Invalid committed glossary")
        (root / specs.TERMS).write_bytes(valid_terms)
        report = self.command(root, "validate", "--head", "HEAD", status=1)
        self.assertIn("duplicate/ambiguous scoped name Pane", "\n".join(report["errors"]))
        self.assertTrue(self.command(root, "validate")["valid"])

    def test_legacy_snapshots_ignore_untracked_worktree_registries(self):
        root = self.repository(legacy_fixture())
        write_tree(root, {specs.TERMS: b"invalid untracked glossary", specs.UI: b"{}"})
        for selection in [("--head", "HEAD"), ("--staged",)]:
            with self.subTest(selection=selection):
                report = self.command(root, "validate", *selection)
                self.assertEqual(report, {"valid": True, "flows": 1})
        report = self.command(root, "validate", status=1)
        self.assertIn(specs.TERMS, "\n".join(report["errors"]))

    def test_incomplete_worktree_registries_fail_closed(self):
        for missing in [(specs.TERMS,), (specs.UI,), (specs.TERMS, specs.UI)]:
            with self.subTest(missing=missing):
                root = self.repository(fixture())
                for path in missing:
                    (root / path).unlink()
                report = self.command(root, "validate", status=1)
                self.assertIn("Missing or invalid JSON: " + missing[0], "\n".join(report["errors"]))

    def test_structural_flow_fields_prevent_downgrade_without_foundation_files(self):
        files = fixture()
        for path in list(files):
            if path.startswith(("docs/glossary/", "docs/ui/", "docs/specs/")):
                del files[path]
        root = self.repository(files)
        for selection in [(), ("--staged",), ("--head", "HEAD")]:
            with self.subTest(selection=selection):
                report = self.command(root, "validate", *selection, status=1)
                self.assertIn("Missing or invalid JSON: " + specs.TERMS, "\n".join(report["errors"]))

    def test_legacy_fixture_without_unified_metadata_remains_supported(self):
        root = self.repository(legacy_fixture())
        self.assertEqual(self.command(root, "validate"), {"valid": True, "flows": 1})

    def test_invalid_comparison_modes_fail_for_both_dispatch_targets(self):
        modes = [
            ("validate", "--staged", "--head", "HEAD"),
            ("render", "--staged"), ("reconcile", "--staged"),
            ("render", "--head", "HEAD"), ("reconcile", "--head", "HEAD"),
        ]
        for factory in [legacy_fixture, fixture]:
            root = self.repository(factory())
            for arguments in modes:
                with self.subTest(factory=factory.__module__, arguments=arguments):
                    report = self.command(root, *arguments, status=1)
                    self.assertTrue(report["errors"])
                    self.assertRegex("\n".join(report["errors"]), "working tree|--staged")

    def test_missing_refs_fail_for_both_dispatch_targets(self):
        modes = [
            ("validate", "--head", "missing-ref"),
            ("impact", "--base", "missing-ref"),
            ("check", "--staged", "--base", "missing-ref"),
            ("impact", "--merge-base", "--base", "missing-ref"),
        ]
        for factory in [legacy_fixture, fixture]:
            root = self.repository(factory())
            for arguments in modes:
                with self.subTest(factory=factory.__module__, arguments=arguments):
                    report = self.command(root, *arguments, status=1)
                    self.assertIn("Git comparison failed", "\n".join(report["errors"]))

    def test_invalid_flags_and_missing_flag_values_remain_cli_errors(self):
        root = self.repository(fixture())
        for arguments in [("--unknown",), ("--head",), ("--root",)]:
            with self.subTest(arguments=arguments):
                result = subprocess.run(
                    [sys.executable, str(ENTRYPOINT), "validate", "--root", str(root), *arguments],
                    capture_output=True, text=True,
                )
                self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
                self.assertIn("error:", result.stderr)
                self.assertNotIn("Traceback", result.stderr)


if __name__ == "__main__":
    unittest.main()
