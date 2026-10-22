#!/usr/bin/env python3
"""Require integer schema versions and integer successful verification evidence."""
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


class ReceiptScalarTests(unittest.TestCase):
    def fresh_receipts(self, unified):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        directory = pathlib.Path(temporary.name)
        root = directory / "repository"
        root.mkdir()
        write_tree(root, fixture() if unified else legacy_fixture())
        for args in (("init", "-q"), ("config", "user.name", "Receipt fixture"),
                     ("config", "user.email", "fixture@example.invalid"), ("add", "."),
                     ("commit", "-qm", "Receipt baseline")):
            uf.git(root, *args)
        base = uf.revision(root, "HEAD")
        (root / "app.py").write_bytes((root / "app.py").read_bytes() + b"# preserved fixture behavior\n")
        analyzer = specs.analyze if unified else uf.analyze
        constraints = specs.constraints if unified else uf.contract_errors
        records = specs.RECORDS if unified else uf.RECORDS
        checker = specs.receipt_errors if unified else uf.receipt_errors
        affected = analyzer(uf.snapshot(root, base), uf.snapshot(root), base)[1]
        note = "Inspected the preserved fixture behavior and the exact implementation and assertion."
        review = {subject: {**dict.fromkeys(("description", "implementation", "tests"), note),
                            **({"kind": "refactor"} if unified else {})}
                  for subject in affected}
        review_path = directory / "review.json"
        review_path.write_text(json.dumps(review))
        with (directory / "verification.log").open("w") as log:
            uf.reconcile(root, uf.snapshot(root), affected, base, review_path, records=records,
                         analyzer=analyzer, constraints=constraints, log_stream=log)
        files = uf.snapshot(root)
        paths = [records + subject + ".json" for subject in affected]
        for subject, path in zip(affected, paths):
            record = uf.load(files, path)
            self.assertIs(type(record["version"]), int)
            self.assertTrue(record["verification"])
            self.assertTrue(all(type(result["exit_code"]) is int and result["exit_code"] == 0
                                for result in record["verification"]))
            self.assertEqual(checker(files, subject, affected[subject], base), [])
        return root, base, affected, paths, checker

    def command(self, root, base, unified, status, command="check"):
        entrypoint = pathlib.Path(__file__).with_name("specs.py" if unified else "user_flows.py")
        result = subprocess.run(
            [sys.executable, str(entrypoint), command, "--root", str(root), "--base", base, "--json"],
            capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, status, result.stdout + result.stderr)
        self.assertNotIn("Traceback", result.stderr)
        return json.loads(result.stdout)

    def test_fresh_receipt_helpers_reject_coerced_versions_and_success_statuses(self):
        for unified in (False, True):
            root, base, affected, paths, checker = self.fresh_receipts(unified)
            original = uf.snapshot(root)
            subject = next(iter(affected))
            for field, values in (("version", (True, 1.0, False, 0, "1", None)),
                                  ("exit_code", (False, 0.0, True, 1, "0", None))):
                for value in values:
                    with self.subTest(unified=unified, field=field, value=value):
                        files = copy.deepcopy(original)
                        record = uf.load(files, paths[0])
                        if field == "version":
                            record[field] = value
                        else:
                            record["verification"][0][field] = value
                        put(files, paths[0], record)
                        before = dict(files)
                        self.assertTrue(checker(files, subject, affected[subject], base))
                        self.assertEqual(files, before)
            self.assertEqual(uf.snapshot(root), original)

    def test_cli_rejects_boolean_and_float_receipts_without_rewriting_evidence(self):
        for unified in (False, True):
            root, base, _, paths, _ = self.fresh_receipts(unified)
            original = {path: (root / path).read_bytes() for path in paths}
            self.assertEqual(self.command(root, base, unified, 0)["errors"], [])
            for field, value in (("version", True), ("version", 1.0),
                                 ("exit_code", False), ("exit_code", 0.0)):
                with self.subTest(unified=unified, field=field, value=value):
                    for path in paths:
                        record = json.loads(original[path])
                        if field == "version":
                            record[field] = value
                        else:
                            for result in record["verification"]:
                                result[field] = value
                        (root / path).write_text(json.dumps(record))
                    evidence = {path: (root / path).read_bytes() for path in paths}
                    report = self.command(root, base, unified, 1)
                    message = "reconciliation stale" if field == "version" else "successful required verification missing"
                    self.assertIn(message, "\n".join(report["errors"]))
                    self.assertEqual({path: (root / path).read_bytes() for path in paths}, evidence)
            for path in paths:
                (root / path).write_bytes(original[path])
            self.assertEqual(self.command(root, base, unified, 0)["errors"], [])
            self.assertEqual({path: (root / path).read_bytes() for path in paths}, original)

    def test_catalog_version_requires_integer_one_in_legacy_and_unified_checks(self):
        for unified in (False, True):
            root, base, _, paths, _ = self.fresh_receipts(unified)
            original = (root / uf.CATALOG).read_bytes()
            evidence = {path: (root / path).read_bytes() for path in paths}
            for value in (True, 1.0, False, 0, "1", None):
                with self.subTest(unified=unified, value=value):
                    catalog = json.loads(original)
                    catalog["version"] = value
                    (root / uf.CATALOG).write_text(json.dumps(catalog))
                    files = uf.snapshot(root)
                    with self.assertRaisesRegex(uf.FlowError, "Unsupported catalog version"):
                        uf.validate(files, specs.ui_paths(uf.load(files, specs.UI)) if unified else ())
                    report = self.command(root, base, unified, 1, command="validate")
                    self.assertIn("Unsupported catalog version", "\n".join(report["errors"]))
                    self.assertEqual({path: (root / path).read_bytes() for path in paths}, evidence)
            (root / uf.CATALOG).write_bytes(original)
            self.assertEqual(self.command(root, base, unified, 0)["errors"], [])


if __name__ == "__main__":
    unittest.main()
