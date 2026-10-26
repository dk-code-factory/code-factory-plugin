#!/usr/bin/env python3
"""Exercise event comparisons, bootstrap boundaries and artifact promotion."""
import json
import io
import tarfile
import zipfile
import os
import re
from pathlib import Path
import subprocess
import shutil
import tempfile
import unittest
from unittest.mock import patch

import ci
import release


class HostedCITests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.mock_root = patch.object(ci, "ROOT", self.root)
        self.mock_root.start()
        self.addCleanup(self.mock_root.stop)
        self.git("init", "-b", "main")
        self.git("config", "user.name", "Fixture")
        self.git("config", "user.email", "fixture@example.invalid")
        self.first = self.change("one")
        self.git("update-ref", "refs/remotes/origin/main", self.first)

    def git(self, *argv):
        return subprocess.check_output(["git", *argv], cwd=self.root, text=True, stderr=subprocess.DEVNULL).strip()

    def change(self, value):
        (self.root / "source").write_text(value)
        self.git("add", ".")
        self.git("commit", "-m", "Update fixture")
        return self.git("rev-parse", "HEAD")

    def test_only_pinned_root_import_can_bootstrap(self):
        event = {"before": "0" * 40, "after": self.first}
        result = ci.comparison(event, "push", self.first, "refs/heads/main")
        self.assertTrue(result["bootstrap"])
        for pin, ref in (("", "refs/heads/main"), ("1" * 40, "refs/heads/main"), (self.first, "refs/heads/other")):
            with self.subTest(pin=pin, ref=ref), self.assertRaises(ValueError):
                ci.comparison(event, "push", pin, ref)
        with self.assertRaises(ValueError):
            ci.comparison({}, "pull_request_target", self.first, "refs/heads/main")

    def test_push_and_pr_use_exact_events_and_manual_uses_parent(self):
        second = self.change("two")
        third = self.change("three")
        event = {"before": self.first, "after": third}
        self.assertEqual(ci.comparison(event, "push", third, "refs/heads/main")["base"], self.first)
        pr = {"pull_request": {"base": {"sha": second}, "head": {"sha": third}}}
        self.assertEqual(ci.comparison(pr, "pull_request", "", "refs/pull/1/merge")["base"], second)
        self.assertEqual(ci.comparison({}, "workflow_dispatch", third, "refs/heads/main")["base"], second)
        self.assertFalse(ci.comparison({}, "workflow_dispatch", third, "refs/heads/main")["bootstrap"])
        with self.assertRaises(ValueError):
            ci.comparison({"before": second, "after": self.first}, "push", "", "refs/heads/main")

    def test_new_branches_use_merge_base_and_missing_refs_fail(self):
        second = self.change("two")
        result = ci.comparison({"before": "0" * 40, "after": second}, "push", "", "refs/heads/feature")
        self.assertEqual(result["base"], self.first)
        with self.assertRaises((ValueError, subprocess.CalledProcessError)):
            ci.comparison({"before": "f" * 40, "after": second}, "push", "", "refs/heads/main")
        with self.assertRaises(ValueError):
            ci.commit("main; echo unsafe")

    def test_every_verification_command_forces_fixture_mode(self):
        calls = []
        second = self.change("two")
        with patch.dict(os.environ, {"CODE_FACTORY_REAL_CODEX": "1"}):
            ci.run_verification({"base": self.first, "head": second, "bootstrap": False}, lambda argv, **kwargs: calls.append((argv, kwargs)))
        self.assertEqual(calls[0][1]["env"]["CODE_FACTORY_REAL_CODEX"], "0")
        self.assertIn("--base " + self.first + " --head " + second, calls[0][0][-1])
        calls.clear()
        ci.run_verification({"bootstrap": True}, lambda argv, **kwargs: calls.append(argv))
        self.assertIn(["make", "build", "check", "test"], calls)
        self.assertIn(["python3", "scripts/test_installer.py"], calls)
        self.assertIn(["python3", "scripts/smoke.py"], calls)

    def assets(self, head=None):
        directory = self.root / "dist"
        directory.mkdir()
        with tarfile.open(directory / "code-factory-test-linux-amd64.tar.gz", "w:gz") as archive:
            body = json.dumps({"version": "test-daemon"}).encode()
            member = tarfile.TarInfo("code-factory-test/release.json")
            member.size = len(body)
            archive.addfile(member, io.BytesIO(body))
        with zipfile.ZipFile(directory / "code-factory-plugin-test.zip", "w") as archive:
            for name in ("plugin.json", ".codex-plugin/plugin.json"):
                archive.writestr(name, json.dumps({"version": "test-plugin"}))
        ci.artifact_manifest(directory, head or self.first)
        return directory

    def test_component_versions_come_from_promoted_packages(self):
        directory = self.assets()
        data = json.loads((directory / "CI-MANIFEST.json").read_text())
        self.assertEqual(data["components"]["plugin"]["version"], "test-plugin")
        self.assertEqual(data["components"]["code-factory"]["version"], "test-daemon")
        data["components"]["plugin"]["version"] = "invented"
        (directory / "CI-MANIFEST.json").write_text(json.dumps(data))
        with self.assertRaisesRegex(ValueError, "provenance"):
            ci.verify_artifacts(directory, self.first)

    def test_fixed_issue_requires_ancestor_commit_and_explicit_issue_reference(self):
        registry = self.root / "release/fixed-issues.json"
        registry.parent.mkdir()
        components = {"code-factory": {"version": "test"}}
        fix = {"issue": "dk-code-factory/code-factory-plugin#17", "component": "code-factory", "commit": self.first}
        registry.write_text(json.dumps({"version": 1, "fixes": [fix]}))
        with self.assertRaisesRegex(ValueError, "explicitly reference"):
            ci.fixed_issues(self.first, components)
        self.git("commit", "--allow-empty", "-m", "Fixes dk-code-factory/code-factory-plugin#17")
        fixed = self.git("rev-parse", "HEAD")
        fix["commit"] = fixed
        registry.write_text(json.dumps({"version": 1, "fixes": [fix]}))
        self.assertEqual(ci.fixed_issues(fixed, components), [fix])
        with self.assertRaises(subprocess.CalledProcessError):
            ci.fixed_issues(self.first, components)
        fix["component"] = "unpackaged-dependency"
        registry.write_text(json.dumps({"version": 1, "fixes": [fix]}))
        with self.assertRaises(ValueError):
            ci.fixed_issues(fixed, components)

    def test_promoted_assets_require_exact_commit_and_unchanged_bytes(self):
        directory = self.assets()
        self.assertIn("SHA256SUMS", ci.verify_artifacts(directory, self.first))
        with self.assertRaises(ValueError):
            ci.verify_artifacts(directory, "a" * 40)
        path = directory / "code-factory-plugin-test.zip"
        path.write_bytes(b"tampered")
        with self.assertRaises(ValueError):
            ci.verify_artifacts(directory, self.first)

    def test_artifact_paths_extra_files_and_symlinks_fail_closed(self):
        directory = self.assets()
        manifest = directory / "CI-MANIFEST.json"
        original = manifest.read_text()
        data = json.loads(original)
        data["files"][0]["name"] = "../outside"
        manifest.write_text(json.dumps(data))
        with self.assertRaises(ValueError):
            ci.verify_artifacts(directory, self.first)
        manifest.write_text(original)
        (directory / "unexpected").touch()
        with self.assertRaises(ValueError):
            ci.verify_artifacts(directory, self.first)
        (directory / "unexpected").unlink()
        path = directory / "code-factory-plugin-test.zip"
        path.unlink()
        path.symlink_to(self.root / "source")
        with self.assertRaises(ValueError):
            ci.verify_artifacts(directory, self.first)

    def test_delivery_checkout_verifies_historical_fixes_without_publishing(self):
        self.git("commit", "--allow-empty", "-m", "Fixes dk-code-factory/code-factory-plugin#17")
        fix = self.git("rev-parse", "HEAD")
        registry = self.root / "release/fixed-issues.json"
        registry.parent.mkdir()
        registry.write_text(json.dumps({"version": 1, "fixes": [{
            "issue": "dk-code-factory/code-factory-plugin#17", "component": "code-factory", "commit": fix,
        }]}))
        head = self.change("release")
        directory = self.assets(head)
        workflow = (Path(__file__).resolve().parents[1] / ".github/workflows/release.yml").read_text()
        checkout = workflow.split("\n  deliver:\n", 1)[1].split("\n      - name:", 1)[0]
        depth = re.search(r"^\s+fetch-depth: (\d+)\s*$", checkout, re.MULTILINE)
        depth = int(depth.group(1)) if depth else 1
        publisher = self.root / "publisher"
        # file:// makes Git honor the workflow's depth for this local fixture.
        subprocess.run(["git", "clone", *(["--depth", str(depth)] if depth else []),
                        self.root.as_uri(), str(publisher)], check=True,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        calls = []
        summary = self.root / "summary.md"

        def download(*args):
            calls.append(args)
            self.assertEqual(args[:2], ("run", "download"))
            target = Path(args[args.index("--dir") + 1])
            for path in directory.iterdir():
                shutil.copyfile(path, target / path.name)
            return ""

        with patch.object(ci, "ROOT", publisher), patch.object(release, "gh", side_effect=download), patch.dict(os.environ, {
            "VERIFIED_COMMIT": head, "VERIFIED_RUN_ID": "42",
            "RELEASE_TAG": "", "DRY_RUN": "true", "GITHUB_REPOSITORY": "fixture/repo",
            "GITHUB_STEP_SUMMARY": str(summary),
        }):
            release.deliver()
        self.assertEqual(len(calls), 1)
        self.assertIn("nothing published", summary.read_text())

    def test_release_resolver_rejects_wrong_event_branch_and_tags(self):
        for event, ref, tag, dry in (
            ("pull_request", "refs/heads/main", "", "true"),
            ("workflow_dispatch", "refs/heads/feature", "", "true"),
            ("push", "refs/heads/main", "", "false"),
            ("push", "refs/tags/--unsafe", "", "false"),
            ("workflow_dispatch", "refs/heads/main", "", "false"),
        ):
            with self.subTest(event=event, ref=ref), patch.dict(os.environ, {
                "GITHUB_EVENT_NAME": event, "GITHUB_REF": ref,
                "RELEASE_TAG": tag, "DRY_RUN": dry,
            }), patch.object(release, "gh") as api:
                with self.assertRaises(ValueError):
                    release.resolve()
                api.assert_not_called()

    def test_release_accepts_only_successful_main_push_from_the_ci_workflow(self):
        good = {"head_sha": self.first, "head_branch": "main", "event": "push", "conclusion": "success", "path": ".github/workflows/ci.yml"}
        self.assertEqual(release.successful_run([good], self.first), good)
        for key, value in (("head_sha", "a" * 40), ("head_branch", "feature"), ("event", "pull_request"), ("conclusion", "failure"), ("path", ".github/workflows/other.yml")):
            with self.subTest(key=key):
                self.assertIsNone(release.successful_run([{**good, key: value}], self.first))


if __name__ == "__main__":
    unittest.main()
