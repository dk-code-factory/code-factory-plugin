#!/usr/bin/env python3
"""Keep comparison dispatch accountable to both intended Git snapshots."""
import copy
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


class BaselineDispatchTests(unittest.TestCase):
    def repository(self, files):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = pathlib.Path(temporary.name)
        write_tree(root, files)
        uf.git(root, "init", "-q")
        uf.git(root, "config", "user.name", "Specification fixture")
        uf.git(root, "config", "user.email", "fixture@example.invalid")
        self.commit(root, "Fixture baseline")
        return root

    def commit(self, root, message):
        uf.git(root, "add", ".")
        uf.git(root, "commit", "--allow-empty", "-qm", message)
        return uf.revision(root, "HEAD")

    def replace(self, root, files):
        for path in uf.snapshot(root):
            if path not in files:
                (root / path).unlink()
        write_tree(root, files)

    def command(self, root, *arguments, status=0):
        result = subprocess.run(
            [sys.executable, str(ENTRYPOINT), *arguments, "--root=" + str(root), "--json"],
            capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, status, result.stdout + result.stderr)
        self.assertNotIn("Traceback", result.stderr)
        # The historical legacy reconcile command logs verification on stdout.
        output = "\n".join(line for line in result.stdout.splitlines() if not line.startswith("Verify: "))
        return json.loads(output)

    def downgrade(self, original):
        files = copy.deepcopy(original)
        for path in list(files):
            if path.startswith(("docs/glossary/", "docs/ui/", "docs/specs/")):
                del files[path]
        catalog = uf.load(files, uf.CATALOG)
        del catalog["suites"]["tooling"]
        for flow in catalog["flows"]:
            for field in ("terms", "ui", "graph"):
                del flow[field]
            files[flow["spec"]] = uf.render_flow(files[flow["spec"]].decode(), flow).encode()
        put(files, uf.CATALOG, catalog)
        # This is otherwise a valid legacy tree: failure must come from the
        # selected comparison's unified policy, rather than broken fixtures.
        self.assertFalse(uf.unified_registry(files))
        uf.validate(files)
        return files

    def assert_foundation_failure(self, root, *selection):
        for command in ("impact", "check"):
            with self.subTest(command=command, selection=selection):
                report = self.command(root, command, *selection, status=1)
                self.assertIn("Missing or invalid JSON: " + specs.TERMS, "\n".join(report["errors"]))

    def test_complete_worktree_downgrade_cannot_reconcile_away_foundations_or_tooling(self):
        original = fixture()
        root = self.repository(original)
        base = uf.revision(root, "HEAD")
        self.replace(root, self.downgrade(original))
        self.assert_foundation_failure(root, "--base", base)
        with tempfile.TemporaryDirectory() as directory:
            review = pathlib.Path(directory) / "review.json"
            review.write_text(json.dumps({"example": dict.fromkeys(
                ("description", "implementation", "tests"),
                "Inspected unchanged behavior and the related implementation and assertion.",
            )}))
            report = self.command(root, "reconcile", "--base", base, "--review", str(review), status=1)
            self.assertIn("Missing or invalid JSON: " + specs.TERMS, "\n".join(report["errors"]))
        self.assertFalse((root / uf.RECORDS / "example.json").exists())
        self.assertEqual(list((root / specs.RECORDS).glob("*.json")), [])
        # Restoring foundational files still cannot silently drop mandatory tooling.
        write_tree(root, {p: data for p, data in original.items() if p.startswith(("docs/glossary/", "docs/ui/"))})
        report = self.command(root, "impact", "--base", base, status=1)
        self.assertIn("Required tooling verification suite is missing", "\n".join(report["errors"]))

    def test_committed_and_staged_downgrades_use_baseline_despite_unified_worktree(self):
        original = fixture()
        root = self.repository(original)
        base = uf.revision(root, "HEAD")
        self.replace(root, self.downgrade(original))
        head = self.commit(root, "Remove every unified marker")
        self.replace(root, original)
        self.assert_foundation_failure(root, "--base", base, "--head", head)
        self.assert_foundation_failure(root, "--base", base, "--staged")
        # Historical validation reads only the explicitly selected snapshot.
        for selection in (("--head", head), ("--staged",)):
            self.assertEqual(self.command(root, "validate", *selection), {"valid": True, "flows": 1})

    def test_unified_to_unified_comparisons_ignore_removed_worktree_registries(self):
        original = fixture()
        root = self.repository(original)
        base = uf.revision(root, "HEAD")
        for command in ("impact", "check"):
            report = self.command(root, command, "--base", base)
            self.assertEqual(report["affected"], {})
            self.assertEqual(report["terms"], [])
            self.assertEqual(report["entities"], [])
        for path in (specs.TERMS, specs.UI):
            (root / path).unlink()
        for selection in (("--head", base), ("--staged",)):
            for command in ("impact", "check"):
                with self.subTest(selection=selection, command=command):
                    report = self.command(root, command, "--base", base, *selection)
                    self.assertEqual(report["base"], base)
                    self.assertEqual(report["affected"], {})
                    self.assertEqual(report["terms"], [])
                    self.assertEqual(report["entities"], [])

    def test_legacy_to_legacy_comparisons_and_reconciliation_remain_supported(self):
        root = self.repository(legacy_fixture())
        base = uf.revision(root, "HEAD")
        (root / "app.py").write_bytes((root / "app.py").read_bytes() + b"# refactored implementation\n")
        report = self.command(root, "impact", "--base", base)
        self.assertEqual(set(report["affected"]), {"example"})
        self.command(root, "check", "--base", base, status=1)
        with tempfile.TemporaryDirectory() as directory:
            review = pathlib.Path(directory) / "review.json"
            review.write_text(json.dumps({"example": dict.fromkeys(
                ("description", "implementation", "tests"), "Reviewed the preserved implementation and assertion.",
            )}))
            self.assertEqual(self.command(root, "reconcile", "--base", base, "--review", str(review)), {"reconciled": ["example"]})
        self.command(root, "check", "--base", base)
        head = self.commit(root, "Legacy refactor and reconciliation")
        # Physical unified files are outside both committed/index selections.
        write_tree(root, {specs.TERMS: b"invalid untracked glossary", specs.UI: b"{}"})
        for selection in (("--head", head), ("--staged",)):
            report = self.command(root, "check", "--base", base, *selection)
            self.assertEqual(set(report["affected"]), {"example"})

    def test_exact_unified_base_and_legacy_merge_base_select_different_policies(self):
        root = self.repository(legacy_fixture())
        ancestor = uf.revision(root, "HEAD")
        unified = fixture()
        self.replace(root, unified)
        unified_base = self.commit(root, "Unified branch tip")
        uf.git(root, "checkout", "-q", "-b", "legacy-head", ancestor)
        head = self.commit(root, "Independent legacy head")
        for selection in ((), ("--staged",), ("--head", head)):
            self.assert_foundation_failure(root, "--base", unified_base, *selection)
            for command in ("impact", "check"):
                report = self.command(root, command, "--base", unified_base, "--merge-base", *selection)
                self.assertEqual(report["base"], ancestor)
                self.assertEqual(report["affected"], {})
        # --head controls the merge ancestor even when physical HEAD is unified.
        uf.git(root, "checkout", "-q", unified_base)
        report = self.command(root, "check", "--base", unified_base, "--merge-base", "--head", head)
        self.assertEqual(report["base"], ancestor)
        self.assertEqual(report["affected"], {})

    def test_legacy_tips_cannot_hide_unified_resolved_merge_base(self):
        original = fixture()
        root = self.repository(original)
        ancestor = uf.revision(root, "HEAD")
        legacy = self.downgrade(original)
        self.replace(root, legacy)
        legacy_base = self.commit(root, "Legacy base branch")
        uf.git(root, "checkout", "-q", "-b", "other-legacy", ancestor)
        self.replace(root, legacy)
        head = self.commit(root, "Legacy selected head")
        for selection in ((), ("--staged",), ("--head", head)):
            self.assert_foundation_failure(root, "--base", legacy_base, "--merge-base", *selection)
            for command in ("impact", "check"):
                report = self.command(root, command, "--base", legacy_base, *selection)
                self.assertEqual(report["base"], legacy_base)
                self.assertEqual(report["affected"], {})

    def test_comparison_without_catalog_in_pre_adoption_base_still_dispatches(self):
        original = legacy_fixture()
        root = self.repository({"app.py": original["app.py"], "scripts/test_app.py": original["scripts/test_app.py"]})
        base = uf.revision(root, "HEAD")
        self.replace(root, original)
        report = self.command(root, "impact", "--base", base, status=1)
        self.assertEqual(set(report["affected"]), {"example"})
        self.assertIn("description changed", "\n".join(report["errors"]))

    def test_invalid_modes_and_unknown_refs_still_return_nonzero_json(self):
        root = self.repository(fixture())
        self.replace(root, self.downgrade(fixture()))
        for arguments in (
            ("impact", "--base", "unknown-reference"),
            ("check", "--base", "unknown-reference", "--staged"),
            ("impact", "--base", "unknown-reference", "--merge-base"),
            ("check", "--head", "unknown-reference"),
            ("check", "--staged", "--head", "HEAD"),
            ("reconcile", "--staged"), ("reconcile", "--head", "HEAD"),
        ):
            with self.subTest(arguments=arguments):
                report = self.command(root, *arguments, status=1)
                self.assertTrue(report["errors"])


if __name__ == "__main__":
    unittest.main()
