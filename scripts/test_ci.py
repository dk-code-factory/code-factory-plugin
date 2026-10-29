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
import sys
import tempfile
import unittest
import yaml
from contextlib import redirect_stderr
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

    def test_workflow_routes_features_through_pull_requests(self):
        workflow = (Path(__file__).resolve().parents[1] / ".github/workflows/ci.yml").read_text()
        triggers = workflow.split("\npermissions:", 1)[0]
        self.assertIn("  push:\n    branches: [main]\n", triggers)
        self.assertIn("  pull_request:\n    branches: [main, sync/local-development]\n", triggers)
        self.assertIn("  workflow_dispatch:\n", triggers)

    def test_browser_benchmark_retains_samples_and_stops_on_failure(self):
        workflow = yaml.safe_load((Path(__file__).resolve().parents[1] /
            ".github/workflows/browser-benchmark.yml").read_text())
        job = workflow["jobs"]["compare-workers"]
        script = next(step["run"] for step in job["steps"]
            if step.get("name") == "Compare serial and bounded parallel correctness")
        tools = self.root / "tools"
        tools.mkdir()
        npx = tools / "npx"
        npx.write_text("#!" + sys.executable + "\n" + '''import json, os, sys
from pathlib import Path
if "test" in sys.argv:
    with Path("calls.jsonl").open("a") as output:
        output.write(json.dumps(sys.argv[1:]) + "\\n")
    print('{"fixture": true}')
    sys.exit(int(os.environ.get("SAMPLE_EXIT", "0")))
print("fixture-version")
''')
        npx.chmod(0o755)
        for code in (0, 19):
            with self.subTest(exit_code=code):
                sample = self.root / str(code)
                sample.mkdir()
                result = subprocess.run(["bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", script],
                    cwd=sample, env={**os.environ, "PATH": str(tools) + os.pathsep + os.environ["PATH"],
                        "SAMPLE_EXIT": str(code)}, capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, code, result.stderr)
                calls = [json.loads(line) for line in (sample / "calls.jsonl").read_text().splitlines()]
                workers = [1, 2, 1, 2] if code == 0 else [1]
                self.assertEqual(calls, [["playwright", "test", "--workers=" + str(w),
                    "--retries=0", "--reporter=json"] for w in workers])
                outputs = list((sample / "benchmark-results").glob("*.json"))
                self.assertEqual(len(outputs), len(workers))
                for output in outputs:
                    self.assertEqual(json.loads(output.read_text()), {"fixture": True})
                    self.assertIn("max_process_rss_kib=", output.with_suffix(".txt").read_text())

    def test_feature_push_and_dispatch_reject_before_verification(self):
        event = self.root / "event.json"
        event.write_text(json.dumps({"before": self.first, "after": self.first}))
        for name in ("push", "workflow_dispatch"):
            for ref in ("refs/heads/feature", "refs/tags/v1.0.0"):
                errors = io.StringIO()
                with self.subTest(event=name, ref=ref), patch.dict(os.environ, {
                    "GITHUB_EVENT_NAME": name, "GITHUB_REF": ref,
                }), patch.object(sys, "argv", ["ci.py", "verify", "--event", str(event)]), \
                        patch.object(ci, "run_verification") as verify, \
                        patch.object(ci, "artifact_manifest") as manifest, redirect_stderr(errors):
                    with self.assertRaises(SystemExit) as stopped:
                        ci.main()
                    self.assertEqual(stopped.exception.code, 1)
                    self.assertIn("only on main; open a pull request", errors.getvalue())
                    verify.assert_not_called()
                    manifest.assert_not_called()

    def test_missing_comparison_commits_fail(self):
        second = self.change("two")
        with self.assertRaises((ValueError, subprocess.CalledProcessError)):
            ci.comparison({"before": "f" * 40, "after": second}, "push", "", "refs/heads/main")
        with self.assertRaises(ValueError):
            ci.commit("main; echo unsafe")

    def test_missing_event_base_fetches_exact_orphaned_commit(self):
        before = self.change("orphaned previous head")
        remote_temp = tempfile.TemporaryDirectory()
        self.addCleanup(remote_temp.cleanup)
        remote = Path(remote_temp.name) / "origin.git"
        self.git("clone", "--bare", ".", str(remote))
        self.git("reset", "--hard", self.first)
        after = self.change("rewritten current head")
        self.git("push", "--force", str(remote), "HEAD:refs/heads/main")
        for name in ("push", "pull_request"):
            with self.subTest(event=name):
                checkout = Path(remote_temp.name) / name
                self.git("clone", "--no-local", str(remote), str(checkout))
                with patch.object(ci, "ROOT", checkout):
                    with self.assertRaises(subprocess.CalledProcessError):
                        ci.commit(before)
                    # Neither an unavailable object, invalid input nor a wrong
                    # checkout may fall back to a parent or another comparison.
                    for base in ("f" * 40, "main; echo unsafe"):
                        event = {"before": base, "after": after} if name == "push" else {
                            "pull_request": {"base": {"sha": base}, "head": {"sha": after}}}
                        with self.assertRaises((ValueError, subprocess.CalledProcessError)):
                            ci.comparison(event, name, "", "refs/heads/main")
                    wrong = {"before": before, "after": self.first} if name == "push" else {
                        "pull_request": {"base": {"sha": before}, "head": {"sha": self.first}}}
                    with self.assertRaises(ValueError):
                        ci.comparison(wrong, name, "", "refs/heads/main")
                    with self.assertRaises(subprocess.CalledProcessError):
                        ci.commit(before)
                    event = {"before": before, "after": after} if name == "push" else {
                        "pull_request": {"base": {"sha": before}, "head": {"sha": after}}}
                    plan = ci.comparison(event, name, "", "refs/heads/main")
                    self.assertEqual(plan, {"base": before, "head": after, "bootstrap": False})
                    self.assertEqual(ci.git("show", before + ":source"), "orphaned previous head")
                    self.assertEqual(ci.git("rev-parse", "HEAD"), after)
                    commands = []
                    ci.run_verification(plan, lambda argv, **kwargs: commands.append(argv))
                    self.assertEqual(commands, [["make", "hosted-verify",
                        "FLOW_ARGS=--base " + before + " --head " + after]])

    def test_every_verification_command_forces_fixture_mode(self):
        calls = []
        second = self.change("two")
        with patch.dict(os.environ, {"CODE_FACTORY_REAL_CODEX": "1"}):
            ci.run_verification({"base": self.first, "head": second, "bootstrap": False}, lambda argv, **kwargs: calls.append((argv, kwargs)))
        self.assertEqual(calls[0][1]["env"]["CODE_FACTORY_REAL_CODEX"], "0")
        self.assertIn("--base " + self.first + " --head " + second, calls[0][0][-1])
        calls.clear()
        ci.run_verification({"bootstrap": True}, lambda argv, **kwargs: calls.append(argv))
        self.assertEqual(calls, [["make", "hosted-verify-bootstrap"]])

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


class VerificationGraphTests(unittest.TestCase):
    """Run the real Make graph with recording tools, without nested heavy suites."""

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        source = Path(__file__).resolve().parents[1]
        for name in ('Makefile', 'scripts/verify.sh'):
            target = self.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source / name, target)
        # A file default must not replace a caller's command-line variable in
        # recursive verification; inheriting only an environment value loses it.
        with (self.root / 'Makefile').open('a') as makefile:
            makefile.write('\nVERIFICATION_COMMAND_MARKER := file default\n')
            makefile.write('export VERIFICATION_COMMAND_FLAVOR = $(flavor VERIFICATION_COMMAND_MARKER)\n')
        for name in ('web/package.json', 'web/package-lock.json', 'web/vite.config.ts',
                     'web/defer-ui.mjs', 'web/src/app.tsx', 'plugin/plugin.json',
                     'internal/testutil/fakecodex/main.go', 'go.mod'):
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text('original ' + name)
        self.tools = self.root / 'tools'
        self.tools.mkdir()
        # The recorder validates dependency outputs, so a missing graph edge fails
        # instead of simply producing an apparently plausible command count.
        recorder = self.tools / 'recorder'
        recorder.write_text('''#!/usr/bin/python3
import hashlib, json, os, pathlib, sys, time
root = pathlib.Path(os.environ['RECORD_ROOT'])
command = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
key = command + ' ' + ' '.join(args)
inputs = ['web/package.json', 'web/package-lock.json', 'web/vite.config.ts',
          'web/defer-ui.mjs', 'web/src/app.tsx', 'plugin/plugin.json',
          'internal/testutil/fakecodex/main.go', 'go.mod']
digest = hashlib.sha256(b''.join((root / p).read_bytes() for p in inputs)).hexdigest()
def record(event):
    with (root / 'commands.jsonl').open('a') as output:
        output.write(json.dumps(dict(event=event, key=key, fixture=os.environ.get('CODE_FACTORY_REAL_CODEX'), marker=os.environ.get('VERIFICATION_COMMAND_MARKER'), marker_flavor=os.environ.get('VERIFICATION_COMMAND_FLAVOR'), tmpdir=os.environ.get('TMPDIR'), tmpdir_exists=pathlib.Path(os.environ.get('TMPDIR', '/tmp')).is_dir(), digest=digest)) + '\\n')
def require(*paths):
    for path in paths:
        assert (root / path).read_text() == digest, 'stale or missing ' + path
def produce(path):
    output = root / path
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(digest)
record('start')
time.sleep(0.005)
if key == os.environ.get('FAIL_COMMAND'):
    sys.exit(23)
if key == 'npm ci':
    produce('web/node_modules/prepared')
elif key == 'npm run build':
    require('web/node_modules/prepared')
    produce('internal/web/dist/index.html')
elif key == 'python3 scripts/package_plugin.py --embed':
    produce('internal/pluginbundle/dist/code-factory.zip')
elif command == 'go' and args[0] == 'build':
    if args[-1] == './cmd/code-factory':
        require('internal/web/dist/index.html', 'internal/pluginbundle/dist/code-factory.zip')
    produce(args[args.index('-o') + 1])
elif key.startswith('npm ') or command == 'npx':
    require('web/node_modules/prepared')
    if key in ('npm test', 'npm run test:performance'):
        require('internal/web/dist/index.html', 'internal/pluginbundle/dist/code-factory.zip', 'bin/fake-codex')
elif key == 'go test -race -count=1 ./...':
    require('internal/web/dist/index.html', 'internal/pluginbundle/dist/code-factory.zip', 'bin/fake-codex')
elif key == 'python3 scripts/package.py':
    require('bin/code-factory', 'internal/web/dist/index.html', 'internal/pluginbundle/dist/code-factory.zip')
record('end')
''')
        recorder.chmod(0o755)
        for name in ('npm', 'npx', 'go', 'python3', 'node'):
            (self.tools / name).symlink_to(recorder.name)
        self.env = {**os.environ, 'PATH': str(self.tools) + ':' + os.environ['PATH'],
                    'RECORD_ROOT': str(self.root), 'CODE_FACTORY_REAL_CODEX': '1'}
        # Preserve explicitly tested inherited flags without inheriting a parent
        # Make jobserver whose descriptors subprocess deliberately does not pass.
        self.env.pop('MAKEFLAGS', None)
        self.env.pop('MFLAGS', None)
        self.env.pop('MAKELEVEL', None)
        self.env.pop('FLOW_BASE', None)
        self.env.pop('FLOW_ARGS', None)

    def run_make(self, *goals, flags=(), fail=None, inherited_flags=None, direct=False):
        log = self.root / 'commands.jsonl'
        log.unlink(missing_ok=True)
        env = {**self.env}
        if fail:
            env['FAIL_COMMAND'] = fail
        if inherited_flags:
            env['MAKEFLAGS'] = inherited_flags
        argv = ['sh', 'scripts/verify.sh'] if direct else ['make', '--no-print-directory']
        result = subprocess.run([*argv, *flags, *goals], cwd=self.root, env=env,
                                capture_output=True, text=True, timeout=30)
        events = [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []
        self.assertTrue(all(e['fixture'] == '0' for e in events), events)
        return result, events

    def successful(self, *goals, **kwargs):
        result, events = self.run_make(*goals, **kwargs)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return events

    def assert_shared_once(self, events):
        starts = [e['key'] for e in events if e['event'] == 'start']
        for command in ('npm ci', 'npm run build', 'go build -o bin/fake-codex ./internal/testutil/fakecodex',
                        'python3 scripts/package_plugin.py --embed'):
            self.assertEqual(starts.count(command), 1, starts)
        # No executable verification phases overlap, including inherited -j.
        active = None
        for event in events:
            if event['event'] == 'start':
                self.assertIsNone(active, (active, event))
                active = event['key']
            else:
                self.assertEqual(active, event['key'])
                active = None
        self.assertIsNone(active)
        self.assertLess(starts.index('npm test'), starts.index('npm run test:performance'))
        self.assertLess(starts.index('go test -race -count=1 ./...'), starts.index('npm run test:performance'))
        self.assertLess(starts.index('npm run test:performance'), starts.index('python3 scripts/package.py'))
        self.assertLess(starts.index('python3 scripts/package.py'), starts.index('python3 scripts/test_installer.py'))
        self.assertLess(starts.index('python3 scripts/test_installer.py'), starts.index('python3 scripts/smoke.py'))

    def test_local_hosted_and_direct_entrypoints_share_preparation_once(self):
        for goal in ('verify', 'ci', 'hosted-verify', 'hosted-verify-bootstrap', 'direct'):
            for workspace in ('clean', 'prepared'):
                with self.subTest(goal=goal, workspace=workspace):
                    if workspace == 'clean':
                        for name in ('web/node_modules', 'internal/web/dist', 'internal/pluginbundle/dist', 'bin'):
                            shutil.rmtree(self.root / name, ignore_errors=True)
                    goals = ['FLOW_BASE=main', 'CODE_FACTORY_REAL_CODEX=1']
                    events = self.successful(*([] if goal == 'direct' else [goal]), *goals, direct=goal == 'direct')
                    self.assert_shared_once(events)
                    starts = [e['key'] for e in events if e['event'] == 'start']
                    if goal.startswith('hosted'):
                        self.assertEqual(starts.count('python3 -m pip install -r scripts/requirements-dev.txt'), 1)
                        self.assertEqual(starts.count('npx playwright install --with-deps chromium'), 1)
                        self.assertLess(starts.index('npm ci'), starts.index('npx playwright install --with-deps chromium'))
                    if goal == 'hosted-verify-bootstrap':
                        self.assertFalse(any('specs.py check' in s for s in starts))
                        self.assertIn('python3 scripts/specs.py validate', starts)
                    else:
                        self.assertTrue(any('specs.py check' in s for s in starts))

    def test_standalone_commands_prepare_their_dependencies(self):
        for goal in ('ui', 'build', 'check', 'test', 'test-performance', 'package', 'plugin'):
            with self.subTest(goal=goal):
                for name in ('web/node_modules', 'internal/web/dist', 'internal/pluginbundle/dist', 'bin'):
                    shutil.rmtree(self.root / name, ignore_errors=True)
                events = self.successful(goal)
                starts = [e['key'] for e in events if e['event'] == 'start']
                self.assertEqual(starts.count('npm ci'), 0 if goal == 'plugin' else 1)
                if goal in ('test', 'test-performance'):
                    self.assertEqual(starts.count('go build -o bin/fake-codex ./internal/testutil/fakecodex'), 1)

    def test_independent_invocations_rebuild_changed_inputs_and_missing_outputs(self):
        self.assert_shared_once(self.successful('verify'))
        for path in ('web/package.json', 'web/package-lock.json', 'web/vite.config.ts', 'web/defer-ui.mjs',
                     'web/src/app.tsx', 'plugin/plugin.json', 'internal/testutil/fakecodex/main.go', 'go.mod'):
            with self.subTest(changed=path):
                (self.root / path).write_text('changed ' + path)
                self.assert_shared_once(self.successful('verify'))
        for path in ('web/node_modules', 'internal/web/dist', 'internal/pluginbundle/dist', 'bin'):
            with self.subTest(deleted=path):
                shutil.rmtree(self.root / path)
                self.assert_shared_once(self.successful('verify'))

    def test_parallel_flags_serialize_verification_phases(self):
        for goal in ('verify', 'ci', 'hosted-verify', 'hosted-verify-bootstrap'):
            for flags, inherited in ((('-j8',), None), ((), '-j8 CODE_FACTORY_REAL_CODEX=1')):
                with self.subTest(goal=goal, flags=flags, inherited=inherited):
                    self.assert_shared_once(self.successful(goal, 'FLOW_BASE=main', flags=flags, inherited_flags=inherited))

    def test_failures_stop_downstream_work_and_propagate_nonzero_status(self):
        events = self.successful('hosted-verify')
        commands = [e['key'] for e in events if e['event'] == 'start']
        for command in commands:
            with self.subTest(failure=command):
                result, failed = self.run_make('hosted-verify', flags=('-k', '-j8'), fail=command)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('Error 23', result.stderr)
                self.assertEqual([e['key'] for e in failed if e['event'] == 'start'], commands[:commands.index(command) + 1])
        result, events = self.run_make(fail='npm ci', direct=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(events[-1]['key'], 'npm ci')

    def test_keep_going_flags_stop_all_verification_entrypoints(self):
        comparison = '--base ' + 'a' * 40 + ' --head ' + 'b' * 40
        for goal in ('verify', 'verify-bootstrap', 'ci', 'hosted-verify',
                     'hosted-verify-bootstrap', 'direct'):
            goals = [*([] if goal == 'direct' else [goal]),
                     'FLOW_BASE=fixture-main', 'FLOW_ARGS=' + comparison]
            baseline = self.successful(*goals, direct=goal == 'direct')
            commands = [e['key'] for e in baseline if e['event'] == 'start']
            expected = commands[:commands.index('go vet ./...') + 1]
            for flags, inherited in ((('-k', '-j8'), None),
                                     (('--keep-going', '-j8'), None),
                                     ((), '-k -j8'),
                                     ((), '--keep-going -j8'),
                                     (('MAKEFLAGS=-k -j8',), None)):
                with self.subTest(goal=goal, flags=flags, inherited=inherited):
                    result, events = self.run_make(
                        *goals, direct=goal == 'direct', flags=flags,
                        inherited_flags=inherited, fail='go vet ./...')
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn('Error 23', result.stderr)
                    self.assertEqual([e['key'] for e in events if e['event'] == 'start'], expected)

    def test_keep_going_preserves_shared_preparation_and_comparison_arguments(self):
        comparison = '--base ' + 'a' * 40 + ' --head ' + 'b' * 40
        marker = "literal 'quoted' value with spaces = retained"
        for goal in ('verify', 'verify-bootstrap', 'ci', 'hosted-verify',
                     'hosted-verify-bootstrap', 'direct'):
            for flags, inherited in ((('-k', '-j8'), None), ((), '-k -j8'),
                                     (('MAKEFLAGS=-k -j8',), None)):
                with self.subTest(goal=goal, flags=flags, inherited=inherited):
                    variables = ['FLOW_BASE=fixture-main', 'FLOW_ARGS=' + comparison]
                    if inherited:
                        inherited += ' -- VERIFICATION_COMMAND_MARKER=' + marker.replace(' ', '\\ ')
                    else:
                        variables.append('VERIFICATION_COMMAND_MARKER=' + marker)
                    result, events = self.run_make(
                        *([] if goal == 'direct' else [goal]), *variables,
                        direct=goal == 'direct', flags=flags, inherited_flags=inherited)
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assert_shared_once(events)
                    self.assertTrue(all(e['marker'] == marker for e in events), events)
                    self.assertNotIn(marker, result.stdout)
                    commands = [e['key'] for e in events if e['event'] == 'start']
                    if goal.endswith('bootstrap'):
                        self.assertFalse(any('specs.py check' in command for command in commands))
                    else:
                        selected = '--base fixture-main --merge-base' if goal == 'ci' else comparison
                        self.assertIn('python3 scripts/specs.py check ' + selected, commands)

    def test_assigned_makeflags_preserve_variable_flavor_and_literal_dollars(self):
        temporary = self.root / 'ci$run'
        temporary.mkdir()
        marker = "literal 'quoted' $run ref$value $ preserved"
        for goal in ('verify', 'verify-bootstrap', 'ci', 'hosted-verify',
                     'hosted-verify-bootstrap', 'direct'):
            for assignment, flavor in ((':=', 'simple'), ('=', 'recursive')):
                with self.subTest(goal=goal, flavor=flavor):
                    events = self.successful(
                        *([] if goal == 'direct' else [goal]),
                        'FLOW_BASE=fixture-main', 'MAKEFLAGS=-k -j8',
                        'TMPDIR:=' + str(temporary).replace('$', '$$'),
                        'VERIFICATION_REFERENCE:=ref$$value',
                        'VERIFICATION_COMMAND_MARKER' + assignment +
                        "literal 'quoted' $$run $(VERIFICATION_REFERENCE) $$ preserved",
                        direct=goal == 'direct')
                    self.assert_shared_once(events)
                    self.assertTrue(all(e['marker'] == marker and e['marker_flavor'] == flavor
                                        and e['tmpdir'] == str(temporary) and e['tmpdir_exists']
                                        for e in events), events)

    def test_parallel_flags_preserve_native_variable_flavors(self):
        temporary = self.root / 'ci$run'
        temporary.mkdir()
        for goal in ('verify', 'verify-bootstrap', 'ci', 'hosted-verify',
                     'hosted-verify-bootstrap', 'direct'):
            for flags, inherited in ((('-k', '-j8'), None), ((), '-k -j8')):
                # GNU Make already re-expands simple-flavor literal dollars
                # during normal recursive forwarding; keep that behavior out
                # of the raw-MAKEFLAGS repair's compatibility assertion.
                for assignment, flavor, expression, expected in (
                    (':=', 'simple', "literal 'quoted' value with spaces = retained",
                     "literal 'quoted' value with spaces = retained"),
                    ('=', 'recursive', "literal 'quoted' $$run $(VERIFICATION_REFERENCE) $$ preserved",
                     "literal 'quoted' $run reference $ preserved"),
                ):
                    with self.subTest(goal=goal, flags=flags, inherited=inherited, flavor=flavor):
                        events = self.successful(
                            *([] if goal == 'direct' else [goal]),
                            'FLOW_BASE=fixture-main',
                            'TMPDIR=' + str(temporary).replace('$', '$$'),
                            'VERIFICATION_REFERENCE:=reference',
                            'VERIFICATION_COMMAND_MARKER' + assignment + expression,
                            flags=flags, inherited_flags=inherited, direct=goal == 'direct')
                        self.assert_shared_once(events)
                        self.assertTrue(all(e['marker'] == expected and e['marker_flavor'] == flavor
                                            and e['tmpdir'] == str(temporary) and e['tmpdir_exists']
                                            for e in events), events)

    def test_standalone_goals_retain_requested_keep_going(self):
        result, events = self.run_make('check', 'ui', flags=('-k', '-j1'), fail='go vet ./...')
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(any(e['key'] == 'npm run build' and e['event'] == 'end' for e in events))

    def test_verification_policy_preserves_dry_run_options(self):
        for goal in ('verify', 'verify-bootstrap', 'ci', 'hosted-verify',
                     'hosted-verify-bootstrap', 'direct'):
            for flags, inherited in ((('-n', '-k', '-j8'), None),
                                     (('-n',), '-k -j8'),
                                     (('-n', 'MAKEFLAGS=-k -j8'), None)):
                with self.subTest(goal=goal, flags=flags, inherited=inherited):
                    result, events = self.run_make(
                        *([] if goal == 'direct' else [goal]), 'FLOW_BASE=fixture-main',
                        direct=goal == 'direct', flags=flags, inherited_flags=inherited)
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertEqual(events, [], 'Dry-run must not execute verification tools')
                    self.assertIn('npm ci', result.stdout)
                    self.assertIn('go vet ./...', result.stdout)

    def test_ci_requires_comparison_before_preparation(self):
        result, events = self.run_make('ci')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(events, [])

    def test_hosted_dispatch_passes_exact_comparison_through_real_graph(self):
        with patch.object(ci, 'ROOT', self.root), patch.dict(os.environ, self.env):
            ci.run_verification({'base': 'a' * 40, 'head': 'b' * 40, 'bootstrap': False},
                                runner=lambda *args, **kwargs: subprocess.run(*args, timeout=30, **kwargs))
        events = [json.loads(line) for line in (self.root / 'commands.jsonl').read_text().splitlines()]
        self.assert_shared_once(events)
        self.assertIn('python3 scripts/specs.py check --base ' + 'a' * 40 + ' --head ' + 'b' * 40,
                      [e['key'] for e in events])

    def test_workflow_does_not_prepare_npm_outside_graph(self):
        workflow = (Path(__file__).resolve().parents[1] / '.github/workflows/ci.yml').read_text()
        self.assertNotIn('npm ci', workflow)
        self.assertNotIn('playwright install', workflow)
        self.assertIn('python3 scripts/ci.py verify', workflow)


if __name__ == "__main__":
    unittest.main()
