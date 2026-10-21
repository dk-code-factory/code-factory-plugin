#!/usr/bin/env python3
"""Behavioral tests of change detection, reconciliation and Git comparisons."""
import contextlib
import copy
import io
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import user_flows as flows


def fixture():
    flow = {"id": "example", "title": "Example", "spec": flows.DIRECTORY + "example.md",
            "scenarios": ["S1"], "implementation": [{"path": "app.py", "anchor": "def perform():", "purpose": "Perform work"}],
            "tests": [{"path": "scripts/test_app.py", "anchor": "def test_perform():", "scenarios": ["S1"], "purpose": "Check outcome"}],
            "suites": ["unit"], "gaps": []}
    catalog = {"version": 1, "exclusions": [{"glob": "docs/**", "reason": "Supporting prose"}],
               "suites": {"unit": {"commands": [[sys.executable, "scripts/test_app.py"]]}}, "flows": [flow]}
    files = {"app.py": b"def perform():\n    return 1\n",
             "scripts/test_app.py": b"def test_perform():\n    assert True\ntest_perform()\n"}
    files[flow["spec"]] = ("# Example\n\n" + "\n\n".join("## " + s + "\n\n" + ("**S1** Work completes." if s == "Scenarios" else "Work completes.") for s in flows.SECTIONS) + "\n\n" + flows.references(flow)).encode()
    files[flows.CATALOG] = json.dumps(catalog).encode()
    return files


def edit_catalog(files, edit):
    catalog = flows.load(files, flows.CATALOG)
    edit(catalog)
    files[flows.CATALOG] = json.dumps(catalog).encode()
    for flow in catalog["flows"]:
        files[flow["spec"]] = files[flow["spec"]].split(flows.MARKER.encode())[0] + flows.references(flow).encode()


class ContractTests(unittest.TestCase):
    def setUp(self):
        self.before = fixture()
        self.after = copy.deepcopy(self.before)

    def impact(self):
        return flows.analyze(self.before, self.after, "base")[1]

    def receipt(self, fid, item):
        return {"version": 1, "flow": fid, "base": "base", "fingerprint": item["fingerprint"],
                "environment": flows.FIXTURE_ENV,
                "review": dict.fromkeys(("description", "implementation", "tests"), "Reviewed against the changed behavior."),
                "verification": [{"command": c, "exit_code": 0} for s in item["suites"].values() for c in s["commands"]]}

    def test_code_and_test_changes_require_reconciliation(self):
        self.assertEqual(self.impact(), {})
        for path in ("app.py", "scripts/test_app.py"):
            with self.subTest(path=path):
                self.after = copy.deepcopy(self.before)
                self.after[path] += b"\n# change\n"
                item = self.impact()["example"]
                self.assertIn("reconciliation required", " ".join(flows.receipt_errors(self.after, "example", item, "base")))
                self.after[flows.RECORDS + "example.json"] = json.dumps(self.receipt("example", item)).encode()
                self.assertEqual(flows.receipt_errors(self.after, "example", item, "base"), [])

    def test_description_changes_require_both_code_and_tests(self):
        spec = flows.DIRECTORY + "example.md"
        self.after[spec] = self.after[spec].replace(b"Work completes.", b"Work completes twice.")
        self.assertIn("implementation and tests", flows.contract_errors("example", self.impact()["example"])[0])
        self.after["app.py"] += b"# implementation update\n"
        self.assertIn("tests", flows.contract_errors("example", self.impact()["example"])[0])
        self.after["scripts/test_app.py"] += b"# test update\n"
        self.assertEqual(flows.contract_errors("example", self.impact()["example"]), [])

    def test_new_flow_cannot_claim_adoption_after_catalog_exists(self):
        def add(catalog):
            flow = copy.deepcopy(catalog["flows"][0])
            flow.update(id="new", spec=flows.DIRECTORY + "new.md")
            catalog["flows"].append(flow)
            catalog["adoption"] = {"base": "base", "flows": ["new"]}
        self.after[flows.DIRECTORY + "new.md"] = self.after[flows.DIRECTORY + "example.md"]
        edit_catalog(self.after, add)
        item = self.impact()["new"]
        self.assertFalse(item["adopted"])
        self.assertTrue(flows.contract_errors("new", item))

    def test_initial_adoption_is_pinned_and_references_existing_code(self):
        del self.before[flows.CATALOG]
        del self.before[flows.DIRECTORY + "example.md"]
        edit_catalog(self.after, lambda c: c.update(adoption={"base": "base", "flows": ["example"]}))
        self.assertTrue(self.impact()["example"]["adopted"])
        edit_catalog(self.after, lambda c: c["adoption"].update(base="wrong"))
        self.assertFalse(self.impact()["example"]["adopted"])

    def test_stale_base_input_and_suite_receipts_fail(self):
        self.after["app.py"] += b"# change\n"
        item = self.impact()["example"]
        record = self.receipt("example", item)
        for key, value in (("base", "wrong"), ("fingerprint", "wrong"), ("verification", []), ("review", {})):
            with self.subTest(key=key):
                changed = {**record, key: value}
                self.after[flows.RECORDS + "example.json"] = json.dumps(changed).encode()
                self.assertTrue(flows.receipt_errors(self.after, "example", item, "base"))
        self.after[flows.RECORDS + "example.json"] = json.dumps(record).encode()
        self.after["scripts/test_app.py"] += b"# newer test\n"
        self.assertTrue(flows.receipt_errors(self.after, "example", self.impact()["example"], "base"))

    def test_deleted_and_renamed_paths_cannot_escape_mapping(self):
        del self.after["app.py"]
        with self.assertRaises(flows.FlowError):
            self.impact()
        self.after["renamed.py"] = self.before["app.py"]
        edit_catalog(self.after, lambda c: c["flows"][0]["implementation"][0].update(path="renamed.py"))
        self.assertEqual(self.impact()["example"]["implementation"], ["app.py", "renamed.py"])

    def test_unmapped_files_and_broken_anchors_fail(self):
        self.after["unmapped.py"] = b"pass\n"
        with self.assertRaisesRegex(flows.FlowError, "Unmapped file"):
            self.impact()
        del self.after["unmapped.py"]
        self.after["scripts/test_app.py"] = b"# removed test\n"
        with self.assertRaisesRegex(flows.FlowError, "anchor"):
            self.impact()

    def test_missing_scenario_coverage_and_unknown_scenarios_fail(self):
        for scenarios in ([], ["S2"]):
            with self.subTest(scenarios=scenarios):
                self.after = copy.deepcopy(self.before)
                edit_catalog(self.after, lambda c: c["flows"][0]["tests"][0].update(scenarios=scenarios))
                with self.assertRaises(flows.FlowError):
                    self.impact()

    def test_removed_flow_needs_implementation_and_test_changes(self):
        # Keep a second flow so removing the first is a valid catalog.
        second = copy.deepcopy(flows.load(self.before, flows.CATALOG)["flows"][0])
        second.update(id="other", spec=flows.DIRECTORY + "other.md")
        for files in (self.before, self.after):
            files[second["spec"]] = files[flows.DIRECTORY + "example.md"]
            edit_catalog(files, lambda c: c["flows"].append(second))
        del self.after[flows.DIRECTORY + "example.md"]
        edit_catalog(self.after, lambda c: c.update(flows=[second]))
        self.assertTrue(flows.contract_errors("example", self.impact()["example"]))

    def test_policy_and_reference_changes_require_new_review(self):
        edit_catalog(self.after, lambda c: c["exclusions"].append({"glob": "app.py", "reason": "Reviewed exception"}))
        self.assertIn("example", self.impact())
        self.after["app.py"] += b"# exclusion must not hide this change\n"
        edit_catalog(self.after, lambda c: c["flows"][0].update(implementation=[{"path": "other.py", "purpose": "Replacement"}]))
        self.after["other.py"] = b"pass\n"
        self.assertIn("app.py", self.impact()["example"]["implementation"])

    def test_unrelated_prose_does_not_invalidate_reconciliation(self):
        self.after["app.py"] += b"# change\n"
        fingerprint = self.impact()["example"]["fingerprint"]
        self.after["docs/notes.md"] = b"Unrelated operator notes.\n"
        self.assertEqual(self.impact()["example"]["fingerprint"], fingerprint)

    def test_new_suite_command_invalidates_old_verification(self):
        self.after["app.py"] += b"# change\n"
        item = self.impact()["example"]
        self.after[flows.RECORDS + "example.json"] = json.dumps(self.receipt("example", item)).encode()
        edit_catalog(self.after, lambda c: c["suites"]["unit"]["commands"].append([sys.executable, "--version"]))
        self.assertTrue(flows.receipt_errors(self.after, "example", self.impact()["example"], "base"))


class GitWorkflowTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = pathlib.Path(self.temporary.name)
        self.write(fixture())
        flows.git(self.root, "init", "-b", "main")
        self.commit()
        self.base = flows.revision(self.root, "HEAD")

    def write(self, files):
        for name, value in files.items():
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(value)

    def commit(self):
        flows.git(self.root, "add", "--all")
        flows.git(self.root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Fixture")

    def command(self, *args):
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            return flows.main([*args, "--root", str(self.root)])

    def review(self):
        path = pathlib.Path(self.temporary.name).parent / (self.root.name + "-review.json")
        path.write_text(json.dumps({"example": dict.fromkeys(("description", "implementation", "tests"), "Existing behavior remains; reviewed test assertions.")}))
        self.addCleanup(path.unlink, missing_ok=True)
        return path

    def test_reconcile_executes_tests_and_head_index_worktree_are_distinct(self):
        (self.root / "app.py").write_text("def perform():\n    return 2\n")
        self.assertEqual(self.command("check"), 1)
        self.assertEqual(self.command("check", "--head", "HEAD"), 0)
        self.assertEqual(self.command("check", "--staged"), 0)
        self.assertEqual(self.command("reconcile", "--review", str(self.review())), 0)
        self.assertEqual(self.command("check"), 0)
        self.commit()
        self.assertEqual(self.command("check", "--base", self.base, "--head", "HEAD"), 0)
        flows.git(self.root, "checkout", "-b", "feature")
        (self.root / "app.py").write_text("def perform():\n    return 3\n")
        self.commit()
        self.assertEqual(self.command("check", "--base", "main", "--merge-base", "--head", "HEAD"), 1)

    def test_failure_and_input_mutation_write_no_receipt(self):
        for body in ("raise SystemExit(1)\n", "from pathlib import Path\nPath('app.py').write_text('def perform(): return 99')\n"):
            with self.subTest(body=body):
                (self.root / "scripts/test_app.py").write_text("def test_perform(): pass\n" + body)
                self.assertEqual(self.command("reconcile", "--review", str(self.review())), 1)
                self.assertFalse((self.root / flows.RECORDS / "example.json").exists())

    def test_failed_rerun_invalidates_previous_success(self):
        (self.root / "scripts/test_app.py").write_text(
            "def test_perform(): pass\nimport os\nraise SystemExit(int(os.environ.get('FLOW_TEST_FAIL', '0')))\n")
        with patch.dict("os.environ", {"FLOW_TEST_FAIL": "0"}):
            self.assertEqual(self.command("reconcile", "--review", str(self.review())), 0)
        self.assertEqual(self.command("check"), 0)
        with patch.dict("os.environ", {"FLOW_TEST_FAIL": "1"}):
            self.assertEqual(self.command("reconcile", "--review", str(self.review())), 1)
        self.assertEqual(self.command("check"), 1)

    def test_bad_refs_and_unsafe_combinations_fail_closed(self):
        self.assertEqual(self.command("check", "--base", "missing"), 1)
        self.assertEqual(self.command("reconcile", "--staged"), 1)
        self.assertEqual(self.command("check", "--staged", "--head", "HEAD"), 1)

    def test_head_check_ignores_broken_unstaged_catalog(self):
        (self.root / flows.CATALOG).write_text("invalid JSON")
        self.assertEqual(self.command("check", "--head", "HEAD"), 0)
        self.assertEqual(self.command("check", "--staged"), 0)
        self.assertEqual(self.command("check"), 1)

    def test_verification_forces_fixture_mode(self):
        (self.root / "scripts/test_app.py").write_text(
            "def test_perform(): pass\nimport os\nassert os.environ['CODE_FACTORY_REAL_CODEX'] == '0'\n")
        with patch.dict("os.environ", {"CODE_FACTORY_REAL_CODEX": "1"}):
            self.assertEqual(self.command("reconcile", "--review", str(self.review())), 0)

    def test_committed_snapshot_ignores_export_attributes(self):
        (self.root / ".gitattributes").write_text("app.py export-ignore\n")
        flows.git(self.root, "add", ".gitattributes")
        self.commit()
        self.assertEqual(flows.snapshot(self.root, "HEAD")["app.py"], fixture()["app.py"])

    def test_executable_mode_changes_are_implementation_changes(self):
        (self.root / "app.py").chmod(0o755)
        _, affected = flows.analyze(flows.snapshot(self.root, self.base), flows.snapshot(self.root), self.base)
        self.assertEqual(affected["example"]["implementation"], ["app.py"])


if __name__ == "__main__":
    unittest.main()
