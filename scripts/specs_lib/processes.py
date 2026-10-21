"""Processes for local specification tooling."""
import json
import os
import signal
import subprocess
import time


def run_verification(command, *, cwd, log_stream=None, timeout=600):
    """Run one verifier in an owned session, including descendant cleanup.

    Observe completion without reaping the leader until the last group signal.
    Retaining its PID prevents an unrelated process group from reusing that ID
    between command completion and cleanup. This runner targets Linux.
    """
    import user_flows as uf
    interrupted = None
    def interrupt(signum, frame):
        nonlocal interrupted
        interrupted = signum
    def check_interruption():
        if interrupted == signal.SIGINT:
            raise KeyboardInterrupt
        if interrupted is not None:
            raise SystemExit(128 + interrupted)
    handlers = {}
    try:
        # Record cancellation during process creation as well as waiting. Raising
        # directly inside Popen could interrupt it before we own its returned PID.
        for signum in (signal.SIGINT, signal.SIGTERM):
            handlers[signum] = signal.signal(signum, interrupt)
        process = subprocess.Popen(command, cwd=cwd, env={**os.environ, **uf.FIXTURE_ENV},
                                   stdout=log_stream, stderr=log_stream, start_new_session=True)
        try:
            deadline = time.monotonic() + timeout
            while True:
                check_interruption()
                if os.waitid(os.P_PID, process.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT) is not None:
                    break
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise subprocess.TimeoutExpired(command, timeout)
                time.sleep(min(0.02, remaining))
        finally:
            # The leader remains our unreaped child, so only this invocation can
            # own this group ID. TERM permits cooperative parents to reap their
            # children; KILL stops group members that ignore termination. This
            # owns the session's original group, not children that detach from it.
            try:
                os.killpg(process.pid, signal.SIGTERM)
                time.sleep(0.1)
            finally:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                finally:
                    process.wait()
        return subprocess.CompletedProcess(command, process.returncode)
    finally:
        for signum, handler in handlers.items():
            signal.signal(signum, handler)
        # A cancellation received during cleanup must also supersede a pending
        # timeout or other failure, after the owned group is reaped and previous
        # handlers are restored. An ordinary exception skips code after cleanup.
        check_interruption()


def reconcile(root, files, affected, base, review_path, *, records, analyzer, constraints, log_stream=None):
    import user_flows as uf
    try:
        reviews = uf.decode_json(review_path.read_text())
    except (OSError, ValueError) as error:
        raise uf.FlowError("Review file must be JSON keyed by affected flow ID") from error
    if not isinstance(reviews, dict) or not reviews or set(reviews) - set(affected):
        raise uf.FlowError("Review file must name one or more affected flows; use impact to list them")
    errors = []
    for fid, notes in reviews.items():
        errors.extend(constraints(fid, affected[fid], notes) if constraints is not uf.contract_errors else constraints(fid, affected[fid]))
        if not isinstance(notes, dict) or any(not isinstance(notes.get(role), str) or not notes[role].strip() for role in ("description", "implementation", "tests")):
            errors.append(fid + ": supply description, implementation and tests review notes")
    if errors:
        raise uf.FlowError("\n".join(errors))
    # Validate all notes/constraints before executing anything. Commands are argv
    # arrays from the inspected catalog, never shell snippets from a review file.
    # A failed rerun must not leave an older success usable for the same inputs.
    for fid in reviews:
        (root / records / (fid + ".json")).unlink(missing_ok=True)
    completed = {}
    for fid in reviews:
        for suite in affected[fid]["suites"].values():
            for command in suite["commands"]:
                key = tuple(command)
                if key in completed:
                    continue
                print("Verify: " + json.dumps(command), flush=True, file=log_stream)
                started = time.monotonic()
                try:
                    result = uf.run_verification(command, cwd=root, log_stream=log_stream)
                except (OSError, subprocess.TimeoutExpired) as error:
                    raise uf.FlowError("Verification could not complete; no receipts written") from error
                if result.returncode:
                    raise uf.FlowError("Verification failed; no receipts written")
                completed[key] = {"command": command, "exit_code": 0, "seconds": round(time.monotonic() - started, 3)}
    # Recompute input digests after testing; a test/build must not accidentally
    # attest to inputs that it changed while running.
    _, refreshed = analyzer(uf.snapshot(root, base), uf.snapshot(root), base)
    if any(fid not in refreshed or refreshed[fid]["fingerprint"] != affected[fid]["fingerprint"] for fid in reviews):
        raise uf.FlowError("Flow inputs changed during verification; review and reconcile again")
    target = root / records
    target.mkdir(parents=True, exist_ok=True)
    for fid, notes in reviews.items():
        item = affected[fid]
        record = {"version": 1, "flow": fid, "base": base, "fingerprint": item["fingerprint"],
                  "environment": uf.FIXTURE_ENV, "review": notes,
                  "verification": [completed[tuple(command)] for suite in item["suites"].values() for command in suite["commands"]]}
        if "dependencies" in item:
            record["dependencies"] = item["dependencies"]
        path = target / (fid + ".json")
        temporary = path.with_suffix(".tmp")
        temporary.write_text(json.dumps(record, indent=2) + "\n")
        temporary.replace(path)
    return {"reconciled": sorted(reviews)}
