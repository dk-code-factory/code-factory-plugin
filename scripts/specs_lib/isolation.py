"""Per-checkout ownership and private temporary state for fixture verification."""
from contextlib import contextmanager
import fcntl
from functools import wraps
import json
import os
from pathlib import Path
import secrets
import signal
import tempfile


def process_identity(pid):
    # Linux start time disambiguates a stale PID after reuse.
    fields = Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
    if fields[0] in ("Z", "X"):
        raise ProcessLookupError(pid)
    return fields[19]


@contextmanager
def isolated(root):
    root = Path(root).resolve()
    # Keep the inode: unlinking a flock file allows two independent owners.
    git_dir = root / ".git"
    if git_dir.is_file():
        marker = git_dir.read_text().strip()
        if not marker.startswith("gitdir: "):
            raise RuntimeError("Invalid checkout Git marker")
        git_dir = (root / marker.removeprefix("gitdir: ")).resolve()
    lock_path = git_dir / "verification.lock" if git_dir.is_dir() else root / ".verification.lock"
    with lock_path.open("a+") as lock:
        inherited = os.environ.get("CODE_FACTORY_VERIFY_RUN")
        if os.environ.get("CODE_FACTORY_VERIFY_ROOT") and not inherited:
            raise RuntimeError("Stale or malformed verification run; start a fresh invocation")
        if inherited:
            try:
                state = json.loads((Path(inherited) / "owner.json").read_text())
                valid = (state["root"] == os.environ.get("CODE_FACTORY_VERIFY_ROOT")
                         and state["identity"] == process_identity(state["pid"])
                         and state["token"] == os.environ.get("CODE_FACTORY_VERIFY_TOKEN")
                         and (state["root"] != str(root)
                              or os.environ.get("TMPDIR") == str(Path(inherited) / "tmp")))
            except (OSError, ValueError, KeyError, TypeError):
                valid = False
            if not valid:
                raise RuntimeError("Stale or malformed verification run; start a fresh invocation")
            if state["root"] == str(root):
                try:
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                except BlockingIOError:
                    # A contended lock alone cannot identify its current owner.
                    # Bind admission to the state written under that same lock.
                    lock.seek(0)
                    try:
                        current = json.load(lock)
                    except (OSError, ValueError):
                        current = None
                    if current != state:
                        raise RuntimeError("Stale or malformed verification run; checkout owner changed")
                    yield
                    return
                raise RuntimeError("Stale verification ownership: checkout lock is no longer held")
            # A child verifying a different checkout needs its own ownership.
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise RuntimeError("Verification already owns this checkout; use a separate worktree") from error
        # TMPDIR is a caller-selected parent, never a shared workspace itself.
        parent = Path(os.environ.get("TMPDIR") or tempfile.gettempdir()).resolve()
        previous_tempdir = tempfile.tempdir
        previous = dict(os.environ)
        interrupted = None
        preparing = True
        handlers = {}
        def check_interruption():
            if interrupted == signal.SIGINT:
                raise KeyboardInterrupt
            if interrupted is not None:
                raise SystemExit(128 + interrupted)
        def interrupt(signum, frame):
            nonlocal interrupted
            interrupted = signum
            # Defer while acquiring/cleaning the temporary directory, including
            # the interval before TemporaryDirectory returns its owned handle.
            if not preparing:
                check_interruption()
        try:
            for signum in (signal.SIGINT, signal.SIGTERM):
                handlers[signum] = signal.signal(signum, interrupt)
            with tempfile.TemporaryDirectory(prefix="code-factory-verify-", dir=parent) as directory:
                run = Path(directory)
                temporary = run / "tmp"
                temporary.mkdir(mode=0o700)
                token = secrets.token_hex(32)
                state = {
                    "root": str(root), "pid": os.getpid(),
                    "identity": process_identity(os.getpid()), "token": token,
                }
                (run / "owner.json").write_text(json.dumps(state))
                lock.seek(0)
                lock.truncate()
                json.dump(state, lock)
                lock.flush()
                os.environ.update(CODE_FACTORY_VERIFY_RUN=str(run),
                                  CODE_FACTORY_VERIFY_ROOT=str(root),
                                  CODE_FACTORY_VERIFY_TOKEN=token,
                                  CODE_FACTORY_VERIFY_TMPDIR=str(temporary), TMPDIR=str(temporary))
                ceilings = os.environ.get("GIT_CEILING_DIRECTORIES", "")
                os.environ["GIT_CEILING_DIRECTORIES"] = str(temporary) + (":" + ceilings if ceilings else "")
                tempfile.tempdir = None
                check_interruption()
                preparing = False
                try:
                    yield
                finally:
                    preparing = True
        finally:
            os.environ.clear()
            os.environ.update(previous)
            tempfile.tempdir = previous_tempdir
            for signum, handler in handlers.items():
                signal.signal(signum, handler)
            check_interruption()


def owned(function):
    @wraps(function)
    def call(root, *args, **kwargs):
        with isolated(root):
            return function(root, *args, **kwargs)
    return call
