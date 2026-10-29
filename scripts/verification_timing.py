#!/usr/bin/env python3
"""Bounded, allowlisted phase evidence for local and hosted verification."""
import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import signal
import subprocess
import sys
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
PHASES = {
    'npm-install': 'npm ci',
    'frontend': 'npm run build',
    'embed': 'python3 scripts/package_plugin.py --embed',
    'fake-provider': 'go build -o bin/fake-codex ./internal/testutil/fakecodex',
    'build': 'go build -trimpath -o bin/code-factory ./cmd/code-factory',
    'specifications-validate': 'python3 scripts/specs.py validate',
    'static': 'go vet ./...',
    'frontend-check': 'npm run check',
    'correctness': 'go test -race -count=1 ./...',
    'frontend-tests': 'npm test',
    'performance': 'npm run test:performance',
    'packaging': 'python3 scripts/package.py',
    'installer': 'python3 scripts/test_installer.py',
    'smoke': 'python3 scripts/smoke.py',
    'python-install': 'python3 -m pip install -r scripts/requirements-dev.txt',
    'browser-install': 'npx playwright install --with-deps chromium',
    'specifications': 'python3 scripts/specs.py check',
    'tooling-packaging': 'python3 scripts/test_packaging.py',
    'tooling-live-connection-test': 'python3 scripts/test_live_connection_test.py',
    'tooling-connection-probe': 'python3 scripts/test_connection_probe.py',
    'tooling-ci': 'python3 scripts/test_ci.py',
    'tooling-user-flows': 'python3 scripts/test_user_flows.py',
    'browser-lab': 'node --test scripts/test_browser_lab.mjs',
    'browser-control': 'node --test scripts/test_browser_control.mjs',
    'specification-tests': "python3 -m unittest discover -s scripts -p 'test_specs*.py'",
    'setup': 'Hosted toolchain setup',
    'manifest': 'CI package provenance',
    'daemon': 'go build ./cmd/code-factory',
}
# Planned identities describe expected work; Make owns every executable dependency.
PLAN = [
    'specifications',
    'npm-install',
    'frontend',
    'embed',
    'build',
    'specifications-validate',
    'static',
    'frontend-check',
    'fake-provider',
    'correctness',
    'frontend-tests',
    'tooling-packaging',
    'tooling-live-connection-test',
    'tooling-connection-probe',
    'browser-lab',
    'browser-control',
    'tooling-ci',
    'tooling-user-flows',
    'specification-tests',
    'performance',
    'packaging',
    'installer',
    'smoke',
]

def git(*args):
    return subprocess.check_output(["git", *args], cwd=ROOT, stderr=subprocess.DEVNULL).strip()


def identity():
    """Hash inputs without publishing filenames, content, or local paths."""
    try:
        head = git("rev-parse", "HEAD").decode()
        digest = hashlib.sha256()
        paths = git("ls-files", "-z", "--cached", "--others", "--exclude-standard").split(b"\0")
        for name in sorted(set(paths) - {b""}):
            path = ROOT / os.fsdecode(name)
            digest.update(name + b"\0")
            if path.is_symlink():
                digest.update(b"link\0" + os.fsencode(os.readlink(path)))
            elif path.is_file():
                digest.update(str(path.stat().st_mode & 0o111).encode() + b"\0" + path.read_bytes())
            else:
                digest.update(b"absent")
            digest.update(b"\0")
        return {"head": head, "inputs_sha256": digest.hexdigest(),
                "dirty": bool(git("status", "--porcelain", "--untracked-files=normal"))}
    except (OSError, subprocess.CalledProcessError):
        return {"head": None, "inputs_sha256": None, "dirty": None}


def comparison(arguments):
    """Export only resolved SHAs and selection mode, never caller argument text."""
    parser = argparse.ArgumentParser(add_help=False, exit_on_error=False)
    parser.add_argument("--base", default="HEAD")
    parser.add_argument("--head")
    parser.add_argument("--merge-base", action="store_true")
    parser.add_argument("--staged", action="store_true")
    try:
        args, unknown = parser.parse_known_args(shlex.split(arguments))
        if unknown:
            return {"status": "unavailable"}
        base = git("rev-parse", "--verify", args.base + "^{commit}").decode()
        head = git("rev-parse", "--verify", (args.head or "HEAD") + "^{commit}").decode()
        if args.merge_base:
            base = git("merge-base", base, head).decode()
        return {"status": "resolved", "base": base, "head": head,
                "selection": "index" if args.staged else "commit" if args.head else "worktree",
                "merge_base": args.merge_base}
    except (argparse.ArgumentError, ValueError, OSError, subprocess.CalledProcessError):
        return {"status": "unavailable"}


def toolchains():
    versions = {}
    for name, argv in (("python", [sys.executable, "--version"]), ("go", ["go", "version"]),
                       ("node", ["node", "--version"]), ("npm", ["npm", "--version"]),
                       ("make", ["make", "--version"])):
        try:
            output = subprocess.check_output(argv, stderr=subprocess.DEVNULL, timeout=10, text=True)
            match = re.search(r"\b(?:go|v)?(\d+\.\d+(?:\.\d+)?)\b", output)
            versions[name] = match.group(1) if match else None
        except (OSError, subprocess.SubprocessError):
            versions[name] = None
    return versions


def hosted_comparison():
    try:
        event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
        if os.environ.get("GITHUB_EVENT_NAME") == "workflow_dispatch":
            # Reuse the verifier's main/root policy; a reviewed root has no parent.
            from ci import comparison as ci_comparison
            plan = ci_comparison(event, "workflow_dispatch", os.environ.get("CI_BOOTSTRAP_SHA", ""),
                                 os.environ.get("GITHUB_REF", ""))
            return {"status": "resolved", **plan, "selection": "commit"}
        if "pull_request" in event:
            base, head = event["pull_request"]["base"]["sha"], event["pull_request"]["head"]["sha"]
        else:
            base, head = event.get("before"), event.get("after")
        if all(isinstance(s, str) and re.fullmatch("[0-9a-f]{40}", s) for s in (base, head)):
            return {"status": "event-pinned", "base": base, "head": head, "selection": "commit"}
    except (KeyError, OSError, ValueError, subprocess.CalledProcessError):
        pass
    return {"status": "unavailable"}


def observe(callback, *args, **kwargs):
    """Instrumentation must never replace the command's exit or signal status."""
    try:
        return callback(*args, **kwargs)
    except Exception:
        # Do not expose exception messages: paths and arbitrary argv may occur.
        print("Verification timing unavailable; command outcomes remain authoritative.", file=sys.stderr)
        return None


def row(phase, number, parent=None):
    return {"id": str(number), "phase": phase, "command": PHASES[phase], "parent": parent,
            "outcome": "unstarted", "start_seconds": None, "end_seconds": None,
            "seconds": None, "exit_code": None}


class Journal:
    def __init__(self, path, clock=time.monotonic):
        self.path = Path(path)
        self.clock = clock

    @classmethod
    def create(cls, *, hosted=False, prerequisites=False, bootstrap=False, clock=time.monotonic):
        directory = ROOT / "verification-results"
        directory.mkdir(exist_ok=True)
        journal = cls(directory / (uuid.uuid4().hex + ".json"), clock)
        data = {"version": 1, "run_id": journal.path.stem, "origin": "hosted" if hosted else "local",
                "clock_origin": clock(), "wall_seconds": None, "outcome": "running",
                "aggregation": "Intervals may overlap or nest; do not sum phase durations as wall time.",
                "source": identity(), "comparison": hosted_comparison() if hosted else comparison(os.environ.get("FLOW_ARGS", "")),
                "cache_condition": os.environ.get("CODE_FACTORY_TIMING_CACHE", "unspecified")
                    if os.environ.get("CODE_FACTORY_TIMING_CACHE") in ("cold", "prepared") else "unspecified",
                "toolchains": {}, "phases": [row(p, i) for i, p in enumerate(
                    (["setup"] if hosted else []) + (["python-install", "browser-install"] if hosted or prerequisites else []) + [p for p in PLAN if p != "specifications" or not bootstrap] + (["manifest"] if hosted else []))]}
        journal.path.write_text(json.dumps(data, indent=2) + "\n")
        return journal

    @contextmanager
    def update(self):
        # A separate lock survives atomic replacement of the JSON. Nested or
        # concurrently running commands cannot lose one another's intervals.
        with self.path.with_suffix(".lock").open("a") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            data = json.loads(self.path.read_text())
            yield data
            temporary = self.path.with_suffix(".tmp")
            temporary.write_text(json.dumps(data, indent=2) + "\n")
            temporary.replace(self.path)

    def start(self, phase, parent=None):
        if phase not in PHASES:
            raise ValueError("Unknown timing phase")
        with self.update() as data:
            if len(data["phases"]) >= 256 or data["outcome"] != "running":
                raise ValueError("Timing run is closed or phase limit reached")
            if parent is not None and parent not in {p["id"] for p in data["phases"]}:
                raise ValueError("Unknown timing parent")
            item = next((p for p in data["phases"] if p["phase"] == phase and
                         p["outcome"] == "unstarted" and parent is None), None)
            if item is None:
                item = row(phase, max((int(p["id"]) for p in data["phases"]), default=-1) + 1, parent)
                data["phases"].append(item)
            item.update(outcome="running", start_seconds=self.clock() - data["clock_origin"])
            return item["id"]

    def stop(self, identifier, code, *, interrupted=False):
        with self.update() as data:
            item = next(p for p in data["phases"] if p["id"] == identifier)
            if item["outcome"] != "running":
                raise ValueError("Timing phase is not running")
            end = self.clock() - data["clock_origin"]
            item.update(end_seconds=end, seconds=end - item["start_seconds"], exit_code=code,
                        outcome="interrupted" if interrupted else "success" if code == 0 else "failure")

    def finish(self, outcome):
        with self.update() as data:
            end = self.clock() - data["clock_origin"]
            for item in data["phases"]:
                if item["outcome"] == "running":
                    item.update(outcome="interrupted", end_seconds=end,
                                seconds=end - item["start_seconds"])
            outcomes = {p["outcome"] for p in data["phases"]}
            if outcome == "success" and outcomes - {"success"}:
                outcome = "incomplete"
            data.update(outcome=outcome, wall_seconds=end)

    def refresh(self, plan=None):
        with self.update() as data:
            data["toolchains"] = toolchains()
            data["comparison"] = plan if plan is not None else comparison(os.environ.get("FLOW_ARGS", ""))

    def bootstrap(self):
        with self.update() as data:
            data["phases"] = [p for p in data["phases"] if p["phase"] != "specifications"]


def selected():
    path = os.environ.get("CODE_FACTORY_TIMING_FILE")
    return Journal(path) if path else None


def execute(phase, argv, *, journal=None, owned=False, runner=None):
    journal = journal or selected()
    identifier = observe(journal.start, phase, os.environ.get("CODE_FACTORY_TIMING_PARENT")) if journal else None
    prior = os.environ.get("CODE_FACTORY_TIMING_PARENT")
    if identifier is not None:
        os.environ["CODE_FACTORY_TIMING_PARENT"] = identifier
    code, interrupted = 1, False
    try:
        if runner is None and owned:
            from specs_lib.processes import run_verification
            runner = run_verification
        result = (runner(argv, cwd=ROOT, timeout=float("inf")) if owned else
                  subprocess.run(argv, check=False))
        code = result.returncode
        interrupted = code < 0
        return 128 - code if code < 0 else code
    except (KeyboardInterrupt, SystemExit) as error:
        interrupted = True
        code = 130 if isinstance(error, KeyboardInterrupt) else int(error.code)
        raise
    finally:
        if prior is None:
            os.environ.pop("CODE_FACTORY_TIMING_PARENT", None)
        else:
            os.environ["CODE_FACTORY_TIMING_PARENT"] = prior
        if identifier is not None:
            observe(journal.stop, identifier, code, interrupted=interrupted)


def verify(argv=None):
    """Observe one Make worker graph; Make alone selects and orders its work."""
    argv = argv or ["make", "--no-keep-going", "verify-steps"]
    os.environ["CODE_FACTORY_REAL_CODEX"] = "0"
    journal = None
    code, interrupted = 1, False
    try:
        journal = selected() or observe(Journal.create,
            prerequisites=any(a.startswith("hosted-verify") for a in argv),
            bootstrap=any("bootstrap-steps" in a for a in argv))
        if journal:
            os.environ["CODE_FACTORY_TIMING_FILE"] = str(journal.path)
            print("Verification timing: " + journal.path.name, flush=True)
            if not os.environ.get("GITHUB_ACTIONS"):
                arguments = next((a.split("=", 1)[1] for a in argv if a.startswith("FLOW_ARGS=")),
                                 os.environ.get("FLOW_ARGS", ""))
                observe(journal.refresh, comparison(arguments))
        from specs_lib.processes import run_verification
        result = run_verification(argv, cwd=ROOT, timeout=float("inf"))
        code = result.returncode
        interrupted = code < 0
        return 128 - code if code < 0 else code
    except (KeyboardInterrupt, SystemExit):
        interrupted = True
        raise
    finally:
        if journal and not os.environ.get("GITHUB_ACTIONS"):
            observe(journal.finish, "interrupted" if interrupted else "success" if code == 0 else "failure")


def hosted_action(action, outcome=None):
    if action == "hosted-start":
        journal = Journal.create(hosted=True)
        identifier = journal.start("setup")
        with Path(os.environ["GITHUB_ENV"]).open("a") as output:
            output.write("CODE_FACTORY_TIMING_FILE=" + str(journal.path) + "\n")
            output.write("CODE_FACTORY_TIMING_SETUP=" + identifier + "\n")
    elif action == "setup-finish":
        journal = selected()
        if journal:
            journal.stop(os.environ["CODE_FACTORY_TIMING_SETUP"], 0 if outcome == "success" else 1,
                         interrupted=outcome == "cancelled")
            with journal.update() as data:
                data["toolchains"] = toolchains()
    elif selected():
        selected().finish("interrupted" if outcome == "cancelled" else outcome)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="action", required=True)
    sub.add_parser("verify").add_argument("argv", nargs=argparse.REMAINDER)
    sub.add_parser("hosted-start")
    end = sub.add_parser("hosted-finish")
    end.add_argument("--outcome", choices=["success", "failure", "cancelled"], required=True)
    setup = sub.add_parser("setup-finish")
    setup.add_argument("--outcome", choices=["success", "failure", "cancelled"], required=True)
    command = sub.add_parser("command")
    command.add_argument("phase", choices=PHASES)
    command.add_argument("argv", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.action in ("hosted-start", "setup-finish", "hosted-finish"):
        observe(hosted_action, args.action, getattr(args, "outcome", None))
    elif args.action == "verify":
        return verify(args.argv[1:] if args.argv[:1] == ["--"] else args.argv)
    else:
        argv = args.argv[1:] if args.argv[:1] == ["--"] else args.argv
        if not argv:
            parser.error("A command is required")
        return execute(args.phase, argv)
    return 0


if __name__ == "__main__":
    # Nested wrappers share the outer verifier's owned process group. Preserve
    # cancellation in their journal while the outer runner cleans up the group.
    signal.signal(signal.SIGTERM, lambda number, frame: sys.exit(128 + number))
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        sys.exit(130)
