#!/usr/bin/env python3
"""Promote exact successful main-branch CI artifacts; never install services."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

import ci

TAG = re.compile(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9]+(?:[.-][A-Za-z0-9]+)*)?")


def gh(*args):
    return subprocess.check_output(["gh", *args], text=True)


def successful_run(runs, head):
    return next((run for run in runs if run.get("head_sha") == head
                 and run.get("head_branch") == "main"
                 and run.get("event") == "push"
                 and run.get("conclusion") == "success"
                 and run.get("path") == ".github/workflows/ci.yml"), None)


def resolve():
    event = os.environ["GITHUB_EVENT_NAME"]
    ref = os.environ["GITHUB_REF"]
    dry = os.environ.get("DRY_RUN", "false") == "true"
    if event == "push":
        if not ref.startswith("refs/tags/"):
            raise ValueError("Release pushes must be version tags")
        tag, dry = ref.removeprefix("refs/tags/"), False
    elif event == "workflow_dispatch" and ref == "refs/heads/main":
        tag = os.environ.get("RELEASE_TAG", "")
    else:
        raise ValueError("Manual release verification must run from main")
    if tag and not TAG.fullmatch(tag) or not dry and not tag:
        raise ValueError("Use an existing vMAJOR.MINOR.PATCH version tag")
    head = ci.commit(ci.git("rev-parse", "refs/tags/" + tag + "^{commit}") if tag else ci.git("rev-parse", "HEAD"))
    subprocess.run(["git", "merge-base", "--is-ancestor", head, "origin/main"], cwd=ci.ROOT, check=True)
    repo = os.environ["GITHUB_REPOSITORY"]
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repo):
        raise ValueError("Invalid repository identity")
    runs = json.loads(gh("api", f"repos/{repo}/actions/workflows/ci.yml/runs?head_sha={head}&event=push&per_page=100"))["workflow_runs"]
    run = successful_run(runs, head)
    if run is None:
        raise ValueError("This main-branch commit needs successful push CI before release; rerun after CI finishes")
    artifacts = json.loads(gh("api", f"repos/{repo}/actions/runs/{run['id']}/artifacts?per_page=100"))["artifacts"]
    matching = [a for a in artifacts if a["name"] == "linux-amd64-packages" and not a["expired"]]
    if len(matching) != 1:
        raise ValueError("Expected one unexpired verified package artifact; rerun its CI if expired")
    outputs = {"commit": head, "run_id": str(run["id"]), "tag": tag, "dry_run": str(dry).lower()}
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as f:
        for key, value in outputs.items():
            f.write(key + "=" + value + "\n")
    print(json.dumps(outputs))


def deliver():
    head = ci.commit(os.environ["VERIFIED_COMMIT"])
    if ci.git("rev-parse", "HEAD") != head:
        raise ValueError("Publisher checkout differs from the verified source")
    run_id = os.environ["VERIFIED_RUN_ID"]
    tag = os.environ.get("RELEASE_TAG", "")
    dry = os.environ.get("DRY_RUN", "false") == "true"
    if not run_id.isdigit() or not dry and not TAG.fullmatch(tag):
        raise ValueError("Invalid verified run or release tag")
    repo = os.environ["GITHUB_REPOSITORY"]
    with tempfile.TemporaryDirectory(prefix="code-factory-release-") as temporary:
        directory = Path(temporary)
        gh("run", "download", run_id, "--repo", repo, "--name", "linux-amd64-packages", "--dir", str(directory))
        names = ci.verify_artifacts(directory, head)
        print(json.dumps({"commit": head, "run_id": run_id, "verified_files": names, "dry_run": dry}), flush=True)
        if dry:
            with Path(os.environ["GITHUB_STEP_SUMMARY"]).open("a") as f:
                f.write("Release delivery dry-run passed for `" + head + "`. Checksums and exact CI provenance verified; nothing published.\n")
            return
        # Fail rather than silently replace assets on a previously published tag.
        existing = subprocess.run(["gh", "release", "view", tag, "--repo", repo], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if existing.returncode == 0:
            raise ValueError("A release already exists for this tag; existing assets are never overwritten")
        notes = directory / "release-notes.md"
        notes.write_text("Verified source: `" + head + "`.\n\nCI: https://github.com/" + repo + "/actions/runs/" + run_id + "\n\nLinux x86-64 and portable plugin packages are promoted unchanged from that CI run. Verify SHA256SUMS before installation. Deployment is manual; real-host acceptance is installation-specific.\n")
        argv = ["gh", "release", "create", tag, "--repo", repo, "--verify-tag", "--target", head,
                "--title", "Code Factory " + tag, "--notes-file", str(notes)]
        if tag.startswith("v0.") or "-" in tag:
            argv.append("--prerelease")
        subprocess.run(argv + [str(directory / name) for name in names], check=True)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("command", choices=["resolve", "deliver"])
    args = p.parse_args()
    try:
        {"resolve": resolve, "deliver": deliver}[args.command]()
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        p.exit(1, "Release stopped: " + str(error) + "\n")


if __name__ == "__main__":
    main()
