"""Supervise one Node fixture test command in its original Linux process group."""
import ctypes
import json
import os
import select
import signal
import subprocess
import sys
import time


def main():
    # Only this short-lived test helper adopts orphaned fixture descendants.
    if ctypes.CDLL(None, use_errno=True).prctl(36, 1, 0, 0, 0) != 0:
        raise OSError(ctypes.get_errno(), "Cannot become a fixture test subreaper")
    stopped = False

    def interrupt(signum, frame):
        nonlocal stopped
        stopped = True

    for signum in (signal.SIGINT, signal.SIGTERM):
        signal.signal(signum, interrupt)
    pending = b""

    def controls():
        nonlocal pending
        if not select.select([sys.stdin], [], [], .02)[0]:
            return []
        data = os.read(sys.stdin.fileno(), 4096)
        if not data:
            if pending:
                raise ValueError("Incomplete fixture test control command")
            return [None]
        pending += data
        if len(pending) > 4096:
            raise ValueError("Fixture test control command exceeds 4096 bytes")
        *lines, pending = pending.split(b"\n")
        return [line.decode("ascii") for line in lines]

    observe_exit = sys.argv[1:2] == ["--observe-exit"]
    argv = sys.argv[2:] if observe_exit else sys.argv[1:]
    process = subprocess.Popen(argv, start_new_session=True)
    try:
        with os.fdopen(3, "w") as events:
            events.write(json.dumps({"pid": process.pid}) + "\n")
            events.flush()
            soft_deadline, deadline = time.monotonic() + 30, time.monotonic() + 40
            while not stopped:
                # Retain the exited leader: its PID reserves the original group ID.
                status = os.waitid(os.P_PID, process.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT)
                if status is not None:
                    if observe_exit:
                        exited = {"code": status.si_status, "signal": None} if status.si_code == os.CLD_EXITED else {
                            "code": None, "signal": signal.Signals(status.si_status).name,
                        }
                        events.write(json.dumps({"exited": exited}) + "\n")
                        events.flush()
                        # The observer checks runner-owned cleanup before our safety
                        # cleanup can remove evidence. Only explicit acknowledgment
                        # succeeds; EOF, interruption and expiry still clean up.
                        observed_deadline = time.monotonic() + 5
                        acknowledged = False
                        while not stopped:
                            if time.monotonic() >= observed_deadline:
                                raise TimeoutError("Fixture cleanup observation exceeded 5 seconds")
                            for command in controls():
                                if command is None:
                                    raise RuntimeError("Fixture cleanup observation ended without acknowledgment")
                                if command.strip() == "CLEANUP":
                                    acknowledged = True
                                    break
                                if command.strip() != "SIGTERM":
                                    raise ValueError("Unknown fixture test control command")
                            if acknowledged:
                                break
                        if not acknowledged:
                            raise RuntimeError("Fixture cleanup observation interrupted before acknowledgment")
                    break
                now = time.monotonic()
                if now >= deadline:
                    raise TimeoutError("Fixture test command exceeded 40 seconds")
                if now >= soft_deadline:
                    os.kill(process.pid, signal.SIGTERM)
                    soft_deadline = float("inf")
                for command in controls():
                    if command is None:
                        stopped = True
                    elif command.strip() == "SIGTERM":
                        os.kill(process.pid, signal.SIGTERM)
                    else:
                        raise ValueError("Unknown fixture test control command")
    finally:
        try:
            # No Popen.poll/wait has reaped this leader, including on early exit.
            try:
                os.killpg(process.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            time.sleep(.1)
        finally:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait(timeout=5)
            deadline = time.monotonic() + 5
            while True:
                try:
                    reaped, _ = os.waitpid(-1, os.WNOHANG)
                except ChildProcessError:
                    break
                if not reaped:
                    if time.monotonic() >= deadline:
                        raise TimeoutError("Fixture descendants exceeded cleanup deadline")
                    time.sleep(.02)
    return process.returncode


if __name__ == "__main__":
    result = main()
    sys.exit(result if result >= 0 else 128 - result)
