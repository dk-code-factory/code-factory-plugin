#!/usr/bin/env python3
"""Qualify correctness-file concurrency; never runs the launch-performance suite."""
import argparse
from collections import Counter
import json
import math
import os
from pathlib import Path
import platform
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]


def specifications(report):
    def walk(suite):
        yield from suite.get("specs", [])
        for child in suite.get("suites", []):
            yield from walk(child)
    return list(walk(report))


def inventory(report):
    return Counter(spec["title"] for spec in specifications(report))


def validate_run(report, expected):
    if report.get("errors") or inventory(report) != expected:
        raise ValueError("Discovery errors or changed correctness test inventory")
    for spec in specifications(report):
        tests = spec["tests"]
        if len(tests) != 1:
            raise ValueError("Expected exactly one project execution per scenario")
        test = tests[0]
        results = test["results"]
        if (test["expectedStatus"] != "passed" or test["status"] != "expected"
                or len(results) != 1 or results[0]["status"] != "passed"
                or results[0]["retry"] != 0):
            raise ValueError("Every scenario must pass once without skips or retries")


def discover(command, output, **kwargs):
    stdout_path, stderr_path = output / "discovery.json", output / "discovery.stderr.log"
    with stdout_path.open("w") as stdout, stderr_path.open("w") as stderr:
        result = subprocess.run(command, stdout=stdout, stderr=stderr, **kwargs)
    diagnostics = f"stdout: {stdout_path}; stderr: {stderr_path}"
    if result.returncode:
        raise RuntimeError(f"Correctness discovery exited {result.returncode}; {diagnostics}")
    try:
        return json.loads(stdout_path.read_text())
    except ValueError as error:
        raise ValueError(f"Invalid discovery JSON; {diagnostics}: {error}") from error


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--baseline-inventory", type=Path)
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    env = dict(os.environ, CODE_FACTORY_REAL_CODEX="0")
    command = ["npx", "playwright", "test", "--retries=0", "--reporter=json"]
    report = discover(command + ["--list"], output, cwd=ROOT / "web", env=env)
    expected = inventory(report)
    if not expected or report.get("errors"):
        raise ValueError("Empty or invalid correctness inventory")
    if args.baseline_inventory:
        baseline = json.loads(args.baseline_inventory.read_text())
        if baseline.get("errors") or inventory(baseline) != expected:
            raise ValueError("Correctness inventory differs from the baseline")
    (output / "inventory.json").write_text(json.dumps(sorted(expected.elements()), indent=2) + "\n")
    summary = {
        "version": 1,
        "source": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
        "tracked_changes": bool(subprocess.check_output(["git", "diff", "HEAD", "--name-only"], cwd=ROOT)),
        "platform": platform.system(),
        "node": subprocess.check_output(["node", "--version"], text=True).strip(),
        "playwright": subprocess.check_output(["npx", "playwright", "--version"], cwd=ROOT / "web", text=True).strip(),
        "conditions": "prepared dependencies/build; fresh browser contexts and testhost per run; OS/toolchain caches retained",
        "rss_boundary": "GNU time maximum individual-process RSS in KiB, not aggregate process-tree memory",
        "scenarios": sum(expected.values()),
        "runs": [],
    }
    def save():
        (output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    save()
    # Reversed second pair reduces a consistent cache/order advantage.
    for index, workers in enumerate([1, 2, 2, 1], 1):
        run_dir = output / f"run-{index}-workers-{workers}"
        run_dir.mkdir()
        entry = {"workers": workers, "outcome": "started"}
        summary["runs"].append(entry)
        save()
        metrics = run_dir / "time.txt"
        with (run_dir / "report.json").open("w") as stdout, (run_dir / "stderr.log").open("w") as stderr:
            result = subprocess.run(
                ["/usr/bin/time", "-f", "%e %M", "-o", str(metrics),
                 *command, f"--workers={workers}", "--output", str(run_dir / "failures")],
                cwd=ROOT / "web", env=env, stdout=stdout, stderr=stderr)
        entry["exit_code"] = result.returncode
        entry["outcome"] = "failed"
        save()
        if result.returncode:
            raise RuntimeError(f"Correctness run {index} failed; retained {run_dir}")
        seconds, rss = metrics.read_text().split()
        seconds, rss = float(seconds), int(rss)
        if not math.isfinite(seconds) or seconds < 0 or rss < 0:
            raise ValueError(f"Invalid correctness metrics; retained {run_dir}")
        entry.update(seconds=seconds, max_process_rss_kib=rss)
        validate_run(json.loads((run_dir / "report.json").read_text()), expected)
        entry["outcome"] = "passed"
        save()
        print(json.dumps(entry), flush=True)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
