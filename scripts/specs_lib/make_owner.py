"""Bind checkout ownership to the actual GNU Make process and drain managed helpers."""
import base64
import json
import os
from pathlib import Path
import select
import selectors
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import time

from specs_lib.isolation import checkout_lock_path, git_ceiling, isolated, process_identity


def message(stream, timeout=5):
    deadline = time.monotonic() + timeout
    data = b''
    while b'\n' not in data:
        remaining = deadline - time.monotonic()
        if remaining <= 0 or not select.select([stream], [], [], remaining)[0]:
            raise RuntimeError('Make verification owner did not become ready')
        part = os.read(stream.fileno(), 8192)
        if not part:
            raise RuntimeError('Make verification owner exited before readiness')
        data += part
        if len(data) > 8192:
            raise RuntimeError('Invalid Make verification owner response')
    return data.split(b'\n', 1)[0]


def acquire(root, pid):
    identity = process_identity(pid)
    if os.environ.get('CODE_FACTORY_VERIFY_ROOT') == str(root):
        with isolated(root):
            return base64.b64encode(os.fsencode(os.environ['CODE_FACTORY_VERIFY_RUN'])).decode()
    try:
        state = json.loads(checkout_lock_path(root).read_text())
    except (OSError, ValueError):
        state = {}
    if not isinstance(state, dict):
        state = {}
    if state.get('make_pid') == pid and state.get('make_identity') == identity:
        # GNU Make re-exec retains its PID but reconstructs its environment from
        # the original invocation. Rejoin only this same live Make's guardian.
        run = state['make_run']
        os.environ.update(CODE_FACTORY_VERIFY_RUN=run, CODE_FACTORY_VERIFY_ROOT=str(root),
                          CODE_FACTORY_VERIFY_TOKEN=state['token'], TMPDIR=str(Path(run) / 'tmp'))
        with isolated(root):
            return base64.b64encode(os.fsencode(run)).decode()
    if 'make_pid' in state:
        try:
            process_identity(state['make_pid'])
        except OSError:
            # A sequential invocation can arrive while the previous guardian is
            # draining its final helper. Wait for that owner, never steal a lock.
            try:
                descriptor = os.pidfd_open(state['pid'])
                try:
                    if process_identity(state['pid']) == state['identity']:
                        select.select([descriptor], [], [], 35)
                finally:
                    os.close(descriptor)
            except OSError:
                pass
    command = [sys.executable, str(root / 'scripts/verification_run.py'),
               '--retain-make-owner', str(pid), identity]
    process = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                               start_new_session=True)
    try:
        result = message(process.stdout).decode()
        if not result.startswith('owner:'):
            raise RuntimeError('Cannot acquire Make verification ownership')
        return result.removeprefix('owner:')
    except BaseException:
        process.terminate()
        process.wait(timeout=5)
        raise
    finally:
        process.stdout.close()


def field(encoded, key):
    run = Path(os.fsdecode(base64.b64decode(encoded, validate=True)))
    state = json.loads((run / 'owner.json').read_text())
    values = {
        'CODE_FACTORY_VERIFY_RUN': str(run),
        'CODE_FACTORY_VERIFY_ROOT': state['root'],
        'CODE_FACTORY_VERIFY_TOKEN': state['token'],
        'CODE_FACTORY_VERIFY_TMPDIR': str(run / 'tmp'),
        'TMPDIR': str(run / 'tmp'),
        'GIT_CEILING_DIRECTORIES': git_ceiling(run, state),
    }
    value = values[key]
    directory = Path(f"/proc/{state['pid']}/fd/{state['directory_fd']}") / 'make-fields'
    directory.mkdir(mode=0o700, exist_ok=True)
    with tempfile.NamedTemporaryFile(prefix=key + '-', dir=directory, delete=False) as output:
        # Make removes a terminal LF and preceding CR. Frame both so genuine
        # trailing CR/LF data remains intact, along with interior pathname bytes.
        output.write(os.fsencode(value) + b'\r\n')
        return output.name


def retain(root, pid, identity):
    owner = os.pidfd_open(pid)
    clients = {}
    listener = None
    try:
        if process_identity(pid) != identity or select.select([owner], [], [], 0)[0]:
            raise RuntimeError('Make verification process changed before ownership')
        with isolated(root, make_owner={'make_pid': pid, 'make_identity': identity}):
            listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            listener.bind('\0code-factory-verify-' + os.environ['CODE_FACTORY_VERIFY_TOKEN'])
            listener.listen()
            with selectors.DefaultSelector() as events:
                events.register(owner, selectors.EVENT_READ, 'owner')
                events.register(listener, selectors.EVENT_READ, 'listener')
                encoded = base64.b64encode(os.fsencode(os.environ['CODE_FACTORY_VERIFY_RUN']))
                os.write(sys.stdout.fileno(), b'owner:' + encoded + b'\n')
                sys.stdout.close()
                stopping = False
                deadline = None

                def remove(connection):
                    handle, _ = clients.pop(connection)
                    events.unregister(connection)
                    events.unregister(handle)
                    connection.close()
                    os.close(handle)

                def notify(number):
                    seen = set()
                    for handle, client_pid in clients.values():
                        if client_pid in seen:
                            continue
                        seen.add(client_pid)
                        try:
                            signal.pidfd_send_signal(handle, number)
                        except ProcessLookupError:
                            pass

                while not stopping or clients:
                    if deadline is not None and time.monotonic() >= deadline:
                        notify(signal.SIGKILL)
                        deadline = None
                    for item, _ in events.select(.05):
                        if item.data == 'owner':
                            events.unregister(owner)
                            events.unregister(listener)
                            listener.close()
                            listener = None
                            stopping = True
                            # Helpers own their command sessions and finish their
                            # journals before closing leases. Do not remove state
                            # or release the checkout lock ahead of that drain.
                            notify(signal.SIGTERM)
                            deadline = time.monotonic() + 30
                        elif item.data == 'listener':
                            # The same ready batch can report Make death first.
                            if listener is None:
                                continue
                            connection, _ = listener.accept()
                            handle = None
                            try:
                                connection.settimeout(1)
                                client_pid, uid, _ = struct.unpack(
                                    '3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
                                handle = os.pidfd_open(client_pid)
                                supplied = message(connection, timeout=1).decode()
                                if (uid != os.getuid() or process_identity(client_pid) != supplied
                                        or select.select([handle], [], [], 0)[0]):
                                    raise RuntimeError('Managed helper identity changed')
                                # Commit admission at this live-owner observation.
                                # A subsequent Make exit drains this lease even
                                # if the acknowledgement has not yet arrived.
                                if select.select([owner], [], [], 0)[0]:
                                    raise RuntimeError('Make exited before helper admission')
                                connection.sendall(b'1')
                                connection.setblocking(False)
                                clients[connection] = (handle, client_pid)
                                events.register(connection, selectors.EVENT_READ, connection)
                                events.register(handle, selectors.EVENT_READ, connection)
                            except (OSError, ValueError, RuntimeError):
                                connection.close()
                                if handle is not None:
                                    os.close(handle)
                        else:
                            connection = item.data
                            if connection not in clients:
                                continue
                            handle, _ = clients[connection]
                            try:
                                closed = item.fileobj == handle or not connection.recv(1)
                            except ConnectionError:
                                closed = True
                            if closed:
                                remove(connection)
    finally:
        if listener is not None:
            listener.close()
        for connection, (handle, _) in clients.items():
            connection.close()
            os.close(handle)
        os.close(owner)
