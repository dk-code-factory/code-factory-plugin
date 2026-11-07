"""Per-checkout ownership and private temporary state for fixture verification."""
from contextlib import contextmanager, ExitStack
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


def enclosing_managed_group():
    """For an admitted helper, prove this owner's active pinned session."""
    try:
        owner, identity = os.environ.get('CODE_FACTORY_VERIFY_GROUP_OWNER', '').split(':')
        owner = int(owner)
        if process_identity(owner) != identity:
            return False
        state = json.loads((Path(os.environ['CODE_FACTORY_VERIFY_RUN']) / 'owner.json').read_text())
        if process_identity(state['pid']) != state['identity']:
            return False
        # The runner publishes this grant only within its acknowledged lease.
        # It remains valid through drain after the original Make PID has died.
        folder = Path(os.environ['CODE_FACTORY_VERIFY_RUN']) / 'groups' / f'{owner}-{identity}'
        granted = False
        for path in folder.glob('grant-*.json'):
            try:
                if json.loads(path.read_text()) == state:
                    granted = True
                    break
            except (OSError, ValueError):
                continue
        if not granted:
            return False
        group = os.getpgrp()
        leader = Path(f'/proc/{group}/stat').read_text().rsplit(')', 1)[1].split()
        # The leader may have exited, but the runner keeps its direct child
        # unreaped until its last group signal, reserving this numeric group ID.
        return (int(leader[1]) == owner and int(leader[2]) == group
                and int(leader[3]) == group and process_identity(owner) == identity)
    except (OSError, ValueError, KeyError, TypeError, IndexError):
        return False


@contextmanager
def managed_group_grant():
    """Bind one runner's group capability to its current checkout lease."""
    pid = os.getpid()
    identity = process_identity(pid)
    grant = None
    try:
        run = os.environ.get('CODE_FACTORY_VERIFY_RUN')
        if run:
            state = json.loads((Path(run) / 'owner.json').read_text())
            folder = Path(run) / 'groups' / f'{pid}-{identity}'
            folder.mkdir(mode=0o700, parents=True, exist_ok=True)
            with tempfile.NamedTemporaryFile(mode='w', prefix='grant-', suffix='.json',
                                             dir=folder, delete=False) as output:
                grant = Path(output.name)
                json.dump(state, output)
        yield str(pid) + ':' + identity
    finally:
        if grant is not None:
            grant.unlink(missing_ok=True)


def checkout_lock_path(root):
    git_dir = root / ".git"
    if git_dir.is_file():
        marker = git_dir.read_text().strip()
        if not marker.startswith("gitdir: "):
            raise RuntimeError("Invalid checkout Git marker")
        git_dir = (root / marker.removeprefix("gitdir: ")).resolve()
    return git_dir / "verification.lock" if git_dir.is_dir() else root / ".verification.lock"


def git_ceiling(run, state):
    temporary = run / 'tmp'
    boundary = str(temporary)
    if ':' in boundary:
        # Git splits ceilings before resolving symlinks. This live descriptor
        # alias has no delimiter, then resolves to the exact caller-selected root.
        boundary = f"/proc/{state['pid']}/fd/{state['directory_fd']}/tmp"
    inherited = os.environ.get('GIT_CEILING_DIRECTORIES', '')
    # Nested managed entries retain the already-first private boundary verbatim.
    if inherited == boundary or inherited.startswith(boundary + ':'):
        return inherited
    return boundary + (':' + inherited if inherited else '')


@contextmanager
def isolated(root, *, make_owner=None):
    root = Path(root).resolve()
    # Keep the inode: unlinking a flock file allows two independent owners.
    lock_path = checkout_lock_path(root)
    with lock_path.open("a+") as lock:
        inherited = os.environ.get("CODE_FACTORY_VERIFY_RUN")
        if os.environ.get("CODE_FACTORY_VERIFY_ROOT") and not inherited:
            raise RuntimeError("Stale or malformed verification run; start a fresh invocation")
        if inherited:
            try:
                state = json.loads((Path(inherited) / "owner.json").read_text())
                valid = (isinstance(state, dict) and isinstance(state.get("root"), str)
                         and Path(state["root"]).is_absolute()
                         and state["root"] == os.environ.get("CODE_FACTORY_VERIFY_ROOT")
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
                    # Native Make exports its caller's choice. Compose only after
                    # authenticating this borrowed owner, and restore on exit.
                    previous_ceiling = os.environ.get('GIT_CEILING_DIRECTORIES')
                    os.environ['GIT_CEILING_DIRECTORIES'] = git_ceiling(Path(inherited), state)
                    try:
                        yield
                    finally:
                        if previous_ceiling is None:
                            os.environ.pop('GIT_CEILING_DIRECTORIES', None)
                        else:
                            os.environ['GIT_CEILING_DIRECTORIES'] = previous_ceiling
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
            with tempfile.TemporaryDirectory(prefix=".code-factory-verify-", dir=parent) as directory, ExitStack() as handles:
                run = Path(directory)
                directory_fd = os.open(run, os.O_RDONLY | os.O_DIRECTORY)
                handles.callback(os.close, directory_fd)
                os.set_inheritable(directory_fd, False)
                temporary = run / "tmp"
                temporary.mkdir(mode=0o700)
                token = secrets.token_hex(32)
                state = {
                    "root": str(root), "pid": os.getpid(),
                    "identity": process_identity(os.getpid()), "token": token,
                    "directory_fd": directory_fd,
                }
                if make_owner is not None:
                    state.update(make_owner)
                    state['make_run'] = str(run)
                (run / "owner.json").write_text(json.dumps(state))
                lock.seek(0)
                lock.truncate()
                json.dump(state, lock)
                lock.flush()
                os.environ.update(CODE_FACTORY_VERIFY_RUN=str(run),
                                  CODE_FACTORY_VERIFY_ROOT=str(root),
                                  CODE_FACTORY_VERIFY_TOKEN=token,
                                  CODE_FACTORY_VERIFY_TMPDIR=str(temporary), TMPDIR=str(temporary))
                os.environ["GIT_CEILING_DIRECTORIES"] = git_ceiling(run, state)
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
        with managed(root):
            return function(root, *args, **kwargs)
    return call


@contextmanager
def managed(root):
    """Keep a native Make owner alive until this helper finishes, including exec."""
    with isolated(root):
        state = json.loads((Path(os.environ['CODE_FACTORY_VERIFY_RUN']) / 'owner.json').read_text())
        connection = None
        handlers = {}
        def interrupt(signum, frame):
            if signum == signal.SIGINT:
                raise KeyboardInterrupt
            raise SystemExit(128 + signum)
        try:
            if 'make_pid' in state:
                import socket
                connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                connection.settimeout(5)
                connection.connect('\0code-factory-verify-' + state['token'])
                connection.sendall(process_identity(os.getpid()).encode() + b'\n')
                if connection.recv(1) != b'1':
                    raise RuntimeError('Make verification owner rejected managed helper')
                # Authenticated verification_run may exec npm/Node. Its lease must
                # remain live until that same process exits, not just until exec.
                connection.set_inheritable(True)
            for signum in (signal.SIGINT, signal.SIGTERM):
                handlers[signum] = signal.signal(signum, interrupt)
            yield
        finally:
            for signum, handler in handlers.items():
                signal.signal(signum, handler)
            if connection is not None:
                connection.close()
