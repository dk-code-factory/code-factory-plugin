#!/usr/bin/env python3
"""Hosted verification with exact Git comparisons and checked release artifacts."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import zipfile

import verification_timing as timing

ROOT = Path(__file__).resolve().parents[1]
SHA = re.compile(r"[0-9a-f]{40}")
ASSET = re.compile(r"code-factory[-A-Za-z0-9_.]+\.(?:tar\.gz|zip|sha256)")


def git(*args):
    return subprocess.check_output(["git", *args], cwd=ROOT, text=True).strip()


def commit(value):
    if not isinstance(value, str) or not SHA.fullmatch(value):
        raise ValueError("Expected an exact 40-character commit SHA")
    if git("rev-parse", value + "^{commit}") != value:
        raise ValueError("Commit is unavailable or ambiguous")
    return value


def comparison_commit(value):
    """Recover an event-pinned base omitted by checkout after a branch rewrite."""
    try:
        return commit(value)
    except subprocess.CalledProcessError:
        git("fetch", "--no-tags", "origin", value)
        return commit(value)


def comparison(event, name, bootstrap_sha, ref):
    """Only an explicitly pinned reviewed root import can omit a Git diff."""
    if name in ("push", "workflow_dispatch") and ref != "refs/heads/main":
        raise ValueError("Push and manual verification run only on main; open a pull request for feature branches")
    head = commit(git("rev-parse", "HEAD"))
    if name == "pull_request":
        pr = event["pull_request"]
        if commit(pr["head"]["sha"]) != head:
            raise ValueError("Checkout does not match the pull-request head")
        return {"base": comparison_commit(pr["base"]["sha"]), "head": head, "bootstrap": False}
    if name == "push":
        if commit(event["after"]) != head:
            raise ValueError("Checkout does not match the pushed head")
        before = event.get("before", "")
        if before != "0" * 40:
            return {"base": comparison_commit(before), "head": head, "bootstrap": False}
    elif name != "workflow_dispatch":
        raise ValueError("Unsupported CI event")
    root = git("rev-list", "--count", head) == "1"
    if head == bootstrap_sha and root and ref == "refs/heads/main":
        return {"base": None, "head": head, "bootstrap": True}
    if root:
        raise ValueError("Unreviewed root import; exact comparison is required")
    if name == "workflow_dispatch":
        base = git("rev-parse", "HEAD^")
    else:
        base = git("merge-base", "origin/main", head)
    return {"base": commit(base), "head": head, "bootstrap": False}


def run_verification(plan, runner=subprocess.run):
    env = {**os.environ, "CODE_FACTORY_REAL_CODEX": "0"}
    journal = timing.selected()
    if journal:
        timing.observe(journal.refresh, plan={"status": "resolved", **plan, "selection": "commit"})
    if plan["bootstrap"]:
        # No fabricated ancestor or HEAD-to-HEAD comparison: this single reviewed
        # public root has no publishable predecessor. Every source check runs.
        print("Reviewed public root import: full validation/tests; no Git diff exists.", flush=True)
        if journal:
            timing.observe(journal.bootstrap)
        commands = [["make", "hosted-verify-bootstrap"]]
    else:
        commands = [["make", "hosted-verify", "FLOW_ARGS=--base " + plan["base"] + " --head " + plan["head"]]]
    for argv in commands:
        runner(argv, cwd=ROOT, env=env, check=True)


def package_components(directory, records, head):
    """Derive component identities from the exact promoted package bytes."""
    daemon_versions, plugin_versions = set(), set()
    for record in records:
        path = directory / record["name"]
        if path.name.endswith(".tar.gz"):
            with tarfile.open(path, "r:gz") as archive:
                manifests = [m for m in archive.getmembers() if m.name.count("/") == 1 and m.name.endswith("/release.json")]
                if len(manifests) != 1 or not manifests[0].isfile():
                    raise ValueError("Release archive needs one ordinary release manifest")
                data = json.load(archive.extractfile(manifests[0]))
                daemon_versions.add(data["version"])
        elif path.name.endswith(".zip"):
            with zipfile.ZipFile(path) as archive:
                data = json.loads(archive.read("plugin.json"))
                compatibility = json.loads(archive.read(".codex-plugin/plugin.json"))
                if compatibility["version"] != data["version"]:
                    raise ValueError("Portable and Codex plugin versions disagree")
                plugin_versions.add(data["version"])
    if len(daemon_versions) != 1 or len(plugin_versions) != 1:
        raise ValueError("Conflicting or missing packaged component versions")
    return {"code-factory": {"repository": "dk-code-factory/code-factory-plugin", "version": daemon_versions.pop(), "commit": head},
            "plugin": {"repository": "dk-code-factory/code-factory-plugin", "version": plugin_versions.pop(), "commit": head}}


def fixed_issues(head, components):
    path = ROOT / "release/fixed-issues.json"
    data = json.loads(path.read_text()) if path.exists() else {"version": 1, "fixes": []}
    if type(data.get("version")) is not int or data["version"] != 1 or not isinstance(data.get("fixes"), list):
        raise ValueError("Invalid fixed-issue registry")
    seen = set()
    for fix in data["fixes"]:
        if set(fix) != {"issue", "component", "commit"} or fix["component"] not in components:
            raise ValueError("Fixed issue must identify a packaged component")
        if not re.fullmatch(r"dk-code-factory/code-factory-plugin#[1-9][0-9]*", fix["issue"]):
            raise ValueError("Fixed issue must belong to its component repository")
        sha = commit(fix["commit"])
        subprocess.run(["git", "merge-base", "--is-ancestor", sha, head], cwd=ROOT, check=True)
        message = git("show", "-s", "--format=%B", sha)
        number = fix["issue"].split("#")[1]
        reference = r"(?:" + re.escape(fix["issue"]) + r"\b|#" + number + r"\b|https://github\.com/dk-code-factory/code-factory-plugin/issues/" + number + r"\b)"
        if not re.search(r"(?i)\b(?:fix(?:e[sd])?|close[sd]?|resolve[sd]?)\s*:?\s*" + reference, message):
            raise ValueError("Fix commit does not explicitly reference this fixed issue")
        key = (fix["issue"], fix["component"], sha)
        if key in seen:
            raise ValueError("Duplicate fixed-issue reference")
        seen.add(key)
    return data["fixes"]


def artifact_manifest(directory, head):
    directory = Path(directory)
    paths = sorted(p for p in directory.iterdir() if ASSET.fullmatch(p.name))
    if not paths or not any(p.name.endswith(".tar.gz") for p in paths) or not any(p.name.endswith(".zip") for p in paths):
        raise ValueError("Linux release and portable plugin are required")
    records = []
    for path in paths:
        if path.is_symlink() or not path.is_file():
            raise ValueError("Release assets must be ordinary files")
        records.append({"name": path.name, "sha256": hashlib.sha256(path.read_bytes()).hexdigest(), "size": path.stat().st_size})
    manifest = directory / "CI-MANIFEST.json"
    components = package_components(directory, records, head)
    manifest.write_text(json.dumps({"version": 1, "commit": head, "files": records, "components": components, "fixes": fixed_issues(head, components)}, indent=2) + "\n")
    checks = [*records, {"name": manifest.name, "sha256": hashlib.sha256(manifest.read_bytes()).hexdigest()}]
    (directory / "SHA256SUMS").write_text("".join(r["sha256"] + "  " + r["name"] + "\n" for r in checks))
    verify_artifacts(directory, head)


def verify_artifacts(directory, head):
    directory = Path(directory)
    manifest_path = directory / "CI-MANIFEST.json"
    if manifest_path.is_symlink():
        raise ValueError("Manifest must not be a symlink")
    data = json.loads(manifest_path.read_text())
    if type(data.get("version")) is not int or data["version"] != 1 or data.get("commit") != head or not isinstance(data.get("files"), list):
        raise ValueError("Artifact provenance does not match the verified commit")
    names = []
    for record in data["files"]:
        name = record["name"]
        if not ASSET.fullmatch(name) or name in names:
            raise ValueError("Unsafe or duplicate release filename")
        path = directory / name
        if path.is_symlink() or not path.is_file() or path.stat().st_size != record["size"] or hashlib.sha256(path.read_bytes()).hexdigest() != record["sha256"]:
            raise ValueError("Release artifact content changed")
        names.append(name)
    if not any(n.endswith(".tar.gz") for n in names) or not any(n.endswith(".zip") for n in names):
        raise ValueError("Incomplete release artifact set")
    components = package_components(directory, data["files"], head)
    if data.get("components") != components or data.get("fixes") != fixed_issues(head, components):
        raise ValueError("Component or fixed-issue provenance differs from checked source/packages")
    if set(p.name for p in directory.iterdir()) != set(names) | {"CI-MANIFEST.json", "SHA256SUMS"}:
        raise ValueError("Unexpected artifact files")
    expected = "".join(r["sha256"] + "  " + r["name"] + "\n" for r in data["files"])
    expected += hashlib.sha256(manifest_path.read_bytes()).hexdigest() + "  CI-MANIFEST.json\n"
    if (directory / "SHA256SUMS").is_symlink() or (directory / "SHA256SUMS").read_text() != expected:
        raise ValueError("Checksum list does not match the artifact manifest")
    return names + ["CI-MANIFEST.json", "SHA256SUMS"]


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("command", choices=["verify", "manifest", "verify-artifacts"])
    p.add_argument("--event", type=Path)
    p.add_argument("--head")
    p.add_argument("--directory", type=Path, default=ROOT / "dist")
    args = p.parse_args()
    try:
        if args.command == "verify":
            event = json.loads((args.event or Path(os.environ["GITHUB_EVENT_PATH"])).read_text())
            plan = comparison(event, os.environ["GITHUB_EVENT_NAME"], os.environ.get("CI_BOOTSTRAP_SHA", ""), os.environ["GITHUB_REF"])
            print(json.dumps(plan), flush=True)
            run_verification(plan)
            journal = timing.selected()
            identifier = timing.observe(journal.start, "manifest") if journal else None
            code = 1
            try:
                artifact_manifest(args.directory, plan["head"])
                code = 0
            finally:
                if identifier is not None:
                    timing.observe(journal.stop, identifier, code)
        elif args.command == "manifest":
            artifact_manifest(args.directory, commit(args.head or git("rev-parse", "HEAD")))
        else:
            print(json.dumps(verify_artifacts(args.directory, commit(args.head))))
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        p.exit(1, "CI failed: " + str(error) + "\n")


if __name__ == "__main__":
    main()
