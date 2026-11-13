#!/usr/bin/env python3
"""Execute one fixture verification command with checkout and process ownership."""
from pathlib import Path
import base64
import json
import os
import re
import stat
import select
import signal
import shutil
import subprocess
import tempfile
import sys

from specs_lib.isolation import isolated, managed, enclosing_managed_group
from specs_lib.processes import run_verification


def makefile_names(pid):
    """Replay only explicit Makefiles; their includes are read by Make itself."""
    arguments = Path(f"/proc/{int(pid)}/cmdline").read_bytes().split(b"\0")[1:-1]
    arguments = [os.fsdecode(argument) for argument in arguments]
    files = []
    consumes = {"--directory", "--include-dir", "--old-file", "--assume-old",
                "--new-file", "--assume-new", "--what-if", "--eval",
                "--jobserver-auth", "--jobserver-fds"}
    index = 0
    while index < len(arguments):
        argument = arguments[index]
        index += 1
        if argument == "--":
            break
        if argument.startswith("--"):
            option, equal, value = argument.partition("=")
            if option == "--temp-stdin":
                if not equal:
                    index += 1
                files.append("-")
            elif any(name.startswith(option) for name in ("--file", "--makefile")):
                if not equal:
                    value = arguments[index]
                    index += 1
                files.append(value)
            elif any(name.startswith(option) for name in consumes) and not equal:
                index += 1
        elif argument.startswith("-"):
            flags = argument[1:]
            for position, flag in enumerate(flags):
                if flag in "fCIoWE":
                    value = flags[position + 1:]
                    if not value:
                        value = arguments[index]
                        index += 1
                    if flag == "f":
                        files.append(value)
                    break
                if flag in "jlO":
                    # These optional arguments consume the rest of a short group.
                    break
    return files


def capture_make_stdin(pid, read_files):
    if "-" not in makefile_names(pid):
        return ""
    previous = os.environ.get('CODE_FACTORY_MAKE_STDIN')
    if previous and previous != 'capture-error':
        captured = Path(os.fsdecode(base64.b64decode(previous, validate=True)))
        from specs_lib.isolation import process_identity
        if json.loads((captured.parent / 'owner.json').read_text()) == {
                'pid': pid, 'identity': process_identity(pid)}:
            return previous
    # GNU Make main.c creates TMPDIR/GmXXXXXX before reading any Makefile,
    # then unlinks it before ordinary recipes. Capture during Makefile remaking.
    # Never guess among multiple possible sources or read an unrelated older file.
    from specs_lib.isolation import process_identity
    identity = process_identity(pid)
    environment = dict(item.split(b"=", 1) for item in
                       Path(f"/proc/{pid}/environ").read_bytes().split(b"\0") if b"=" in item)
    temporary_parent = os.fsdecode(environment.get(b"TMPDIR") or b"/tmp")
    directory = Path(temporary_parent)
    if not directory.is_absolute():
        directory = Path(os.readlink(f"/proc/{pid}/cwd")) / directory
    boot = next(int(line.split()[1]) for line in Path('/proc/stat').read_text().splitlines()
                if line.startswith('btime '))
    started = boot + int(identity) / os.sysconf('SC_CLK_TCK')
    candidates = []
    prefix = temporary_parent.rstrip('/') + '/'
    listed_names = {match.group(1) for match in re.finditer(
        r'(?:^| )' + re.escape(prefix) + r'(Gm[^/ ]{6})(?= |$)', read_files)}
    for name in listed_names:
        path = directory / name
        info = path.lstat()
        if (stat.S_ISREG(info.st_mode) and stat.S_IMODE(info.st_mode) == 0o600
                and info.st_uid == os.getuid() and info.st_ctime >= started):
            candidates.append((path, info.st_dev, info.st_ino))
    if len(candidates) != 1:
        raise ValueError("Cannot identify one live Make stdin source safely")
    root = Path(__file__).resolve().parents[1]
    parents = [directory.resolve(), Path('/var/tmp'), Path('/tmp')]
    retained = None
    monitor = None
    interrupted = None
    preparing = True
    handlers = {}
    def check_interruption():
        if interrupted is not None:
            raise SystemExit(128 + interrupted)
    def interrupt(signum, frame):
        nonlocal interrupted
        interrupted = signum
        # Allocation/cleanup must first bind or remove the owned directory.
        if not preparing:
            check_interruption()
    path, device, inode = candidates[0]
    try:
        for signum in (signal.SIGINT, signal.SIGTERM):
            handlers[signum] = signal.signal(signum, interrupt)
        for parent in dict.fromkeys(parents):
            if not parent.is_dir() or parent.resolve().is_relative_to(root):
                continue
            try:
                retained = Path(tempfile.mkdtemp(prefix='.code-factory-make-stdin-', dir=parent))
            except OSError:
                continue
            break
        if retained is None:
            raise RuntimeError('No private external Make stdin storage is available')
        preparing = False
        check_interruption()
        with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as source:
            opened = os.fstat(source.fileno())
            if (opened.st_dev, opened.st_ino) != (device, inode):
                raise ValueError('Make stdin source changed during capture')
            destination = retained / 'source.mk'
            with destination.open('xb') as output:
                os.fchmod(output.fileno(), 0o600)
                shutil.copyfileobj(source, output)
        if process_identity(pid) != identity:
            raise ValueError('Make stdin owner changed during capture')
        (retained / 'owner.json').write_text(json.dumps({'pid': pid, 'identity': identity}))
        monitor = subprocess.Popen([sys.executable, str(Path(__file__).resolve()),
                                    '--retain-make-stdin', str(pid), str(retained)],
                                   stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                   stderr=subprocess.DEVNULL, start_new_session=True)
        if not select.select([monitor.stdout], [], [], 5)[0] or os.read(monitor.stdout.fileno(), 6) != b'ready\n':
            raise RuntimeError('Make stdin cleanup owner did not become ready')
        monitor.stdout.close()
        return base64.b64encode(os.fsencode(destination)).decode('ascii')
    except BaseException:
        preparing = True
        if monitor is not None:
            monitor.kill()
            monitor.wait(timeout=2)
            monitor.stdout.close()
        if retained is not None:
            try:
                shutil.rmtree(retained)
            except FileNotFoundError:
                pass
        raise
    finally:
        preparing = True
        for signum, handler in handlers.items():
            signal.signal(signum, handler)
        check_interruption()


def retain_make_stdin(pid, directory):
    from specs_lib.isolation import process_identity
    owner = json.loads((directory / 'owner.json').read_text())
    descriptor = os.pidfd_open(pid)
    try:
        if owner != {'pid': pid, 'identity': process_identity(pid)}:
            raise ValueError('Make stdin owner changed before retention')
        os.write(sys.stdout.fileno(), b'ready\n')
        # The pidfd becomes readable even if the exited owner remains unreaped.
        # No workload waits on us: this process retains source only while Make lives.
        select.select([descriptor], [], [])
    finally:
        os.close(descriptor)
        shutil.rmtree(directory)


def main():
    command = sys.argv[1:]
    if command[:1] == ['--retain-make-owner']:
        from specs_lib.make_owner import retain
        retain(Path(__file__).resolve().parents[1], int(command[1]), command[2])
        return 0
    if command[:1] == ['--acquire-make-owner']:
        from specs_lib.make_owner import acquire
        if len(command) > 2:
            encoded = command[2]
            parent = []
            index = 0
            while index < len(encoded):
                value = encoded[index]
                if value == '\\':
                    index += 1
                    if index == len(encoded) or encoded[index] not in ('\\', 'n'):
                        raise ValueError('Invalid Make temporary-parent encoding')
                    value = '\n' if encoded[index] == 'n' else '\\'
                parent.append(value)
                index += 1
            os.environ['TMPDIR'] = ''.join(parent)
        print(acquire(Path(__file__).resolve().parents[1], int(command[1])))
        return 0
    if command[:1] == ['--make-owner-fields']:
        from specs_lib.make_owner import fields
        print(fields(command[1]))
        return 0
    if command[:1] == ['--make-owner-field']:
        from specs_lib.make_owner import field
        print(field(command[1], command[2]))
        return 0
    if command[:1] == ["--retain-make-stdin"]:
        retain_make_stdin(int(command[1]), Path(command[2]))
        return 0
    if command[:1] == ["--capture-make-stdin"]:
        try:
            print(capture_make_stdin(int(command[1]), command[2]))
        except (OSError, ValueError, RuntimeError):
            print("capture-error")
        return 0
    files = []
    if command[:1] == ["--make-parent"]:
        files = makefile_names(command[1])
        command = command[2:]
    if command == ["--validate-owner"]:
        root = Path(__file__).resolve().parents[1]
        if (os.environ.get("CODE_FACTORY_VERIFY_ROOT") != str(root)
                or not os.environ.get("CODE_FACTORY_VERIFY_RUN")):
            return 1
        with isolated(root):
            print("owned")
        return 0
    if command[:1] == ["--"]:
        command = command[1:]
    if not command:
        raise ValueError("A verification command is required")
    root = Path(__file__).resolve().parents[1]
    inherited = os.environ.get("CODE_FACTORY_VERIFY_ROOT") == str(root)
    with managed(root):
        if "-" in files:
            captured = os.environ.pop("CODE_FACTORY_MAKE_STDIN", "capture-error")
            if captured == "capture-error":
                raise ValueError("Cannot preserve Make stdin safely; use a named Makefile")
            captured = Path(os.fsdecode(base64.b64decode(captured, validate=True)))
            metadata = json.loads((captured.parent / 'owner.json').read_text())
            from specs_lib.isolation import process_identity
            if metadata != {'pid': int(sys.argv[2]), 'identity': process_identity(int(sys.argv[2]))}:
                raise ValueError('Stale Make stdin capture')
            source = Path(os.environ["TMPDIR"]) / 'stdin.mk'
            shutil.copyfile(captured, source)
            files = [str(source) if name == "-" else name for name in files]
        command += [part for name in files for part in ("-f", name)]
        if inherited and enclosing_managed_group():
            # A live managed runner owns this group. A native Make guardian owns
            # the checkout lifetime, but does not own Make's caller process group.
            os.chdir(root)
            os.execvpe(command[0], command, {**os.environ, "CODE_FACTORY_REAL_CODEX": "0"})
        # A Make jobserver may use pipe descriptors. Preserve only those explicitly
        # declared by Make, never arbitrary owner or credential descriptors.
        from verification_timing import jobserver_fds
        descriptors = jobserver_fds(command)
        # Nested timing and browser runners finalize evidence and reap their
        # own groups after TERM. Keep this outer deadline beyond their grace.
        return run_verification(command, cwd=root, timeout=7200, pass_fds=descriptors,
                                termination_grace=15).returncode


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, ValueError) as error:
        sys.exit(str(error))
