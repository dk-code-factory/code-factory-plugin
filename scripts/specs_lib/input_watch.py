"""Linux mutation evidence for one verification invocation; no persisted reuse."""
import ctypes
from contextlib import contextmanager
import errno
import fcntl
import os
from pathlib import Path
import signal
import stat
import struct
import sys
import threading

import user_flows as uf

# Writes, metadata changes, replacements and directory membership changes matter.
MASK = 0x0002 | 0x0004 | 0x0008 | 0x0040 | 0x0080 | 0x0100 | 0x0200 | 0x0400 | 0x0800
OVERFLOW = 0x4000
IGNORED = 0x8000
DONT_FOLLOW = 0x02000000
EVENT = struct.Struct('iIII')
MAX_DRAIN_READS = 64


@contextmanager
def lease_update():
    previous = signal.pthread_sigmask(signal.SIG_BLOCK, {signal.SIGIO, signal.SIGINT, signal.SIGTERM})
    try:
        yield
    finally:
        signal.pthread_sigmask(signal.SIG_SETMASK, previous)


def stamp(value):
    return (value.st_dev, value.st_ino, value.st_mode, value.st_size,
            value.st_mtime_ns, value.st_ctime_ns)


class ReadLeases:
    """One process-wide SIGIO reservation, shared by overlapping input watches.

    Inotify cannot see mapped writes. A read lease refuses existing writers
    (including writable shared mappings) and notifies before a later writer can
    open the inode. Release broken leases in the handler so even a synchronous
    writer in this process can finish; its watch remains permanently invalid.
    """
    def __init__(self):
        self.files = {}
        self.users = 0
        self.previous = None
        self.process = os.getpid()
        self.libc = None

    def start(self):
        if self.users and self.process != os.getpid():
            raise uf.FlowError('Input read lease ownership was inherited across fork')
        self.process = os.getpid()
        if threading.current_thread() is not threading.main_thread():
            raise uf.FlowError('Input read leases require the main thread')
        if signal.SIGIO in signal.pthread_sigmask(signal.SIG_BLOCK, set()):
            raise uf.FlowError('Input read leases require unblocked SIGIO')
        if not self.users:
            self.previous = signal.getsignal(signal.SIGIO)
            if self.previous != signal.SIG_DFL or signal.SIGIO in signal.sigpending():
                raise uf.FlowError('Input read leases require unused SIGIO')
            signal.signal(signal.SIGIO, self.interrupted)
        elif signal.getsignal(signal.SIGIO) != self.interrupted:
            raise uf.FlowError('Input read lease signal coverage changed')
        self.users += 1

    def interrupted(self, number, frame):
        # Registry changes run with SIGIO blocked. No exception may interrupt a
        # caller while it holds an unrelated descriptor or a publication lock.
        with lease_update():
            for entry in self.files.values():
                if entry['leased']:
                    try:
                        if fcntl.fcntl(entry['fd'], fcntl.F_GETLEASE) != fcntl.F_RDLCK:
                            entry['broken'] = True
                            self.unlock(entry)
                    except OSError:
                        entry['broken'] = True

    def unlock(self, entry):
        try:
            fcntl.fcntl(entry['fd'], fcntl.F_SETLEASE, fcntl.F_UNLCK)
        except OSError as error:
            # A breaker may already have forced release after the kernel's
            # grace period. Unlocking an absent lease reports EAGAIN on Linux.
            if (error.errno != errno.EAGAIN or
                    fcntl.fcntl(entry['fd'], fcntl.F_GETLEASE) != fcntl.F_UNLCK):
                raise
        entry['leased'] = False

    def trusted_system_file(self, descriptor, value):
        # The unprivileged verifier cannot lease distro-owned tools. Trust the
        # administrator, on maintained local filesystems only; never fall back
        # for user-owned inputs, a conflicting writer or writable system files.
        if value.st_uid != 0 or value.st_mode & 0o022:
            return False
        if self.libc is None:
            self.libc = ctypes.CDLL(None, use_errno=True)
            self.libc.fstatfs.argtypes = [ctypes.c_int, ctypes.c_void_p]
            self.libc.fstatfs.restype = ctypes.c_int
        storage = ctypes.create_string_buffer(256)
        if self.libc.fstatfs(descriptor, storage):
            return False
        return ctypes.c_long.from_buffer(storage).value & 0xFFFFFFFF in {
            0xEF53, 0x58465342, 0x9123683E, 0x794C7630, 0x01021994}

    def acquire(self, path):
        previous = signal.pthread_sigmask(signal.SIG_BLOCK, {signal.SIGIO})
        descriptor = None
        try:
            descriptor = os.open(path, os.O_RDONLY | os.O_NONBLOCK | os.O_NOFOLLOW | os.O_CLOEXEC)
            value = os.fstat(descriptor)
            if not stat.S_ISREG(value.st_mode) or stamp(value) != stamp(path.lstat()):
                raise uf.FlowError('Input read lease pathname changed: ' + str(path))
            key = (value.st_dev, value.st_ino)
            entry = self.files.get(key)
            if entry is None:
                entry = {'fd': descriptor, 'stamp': stamp(value), 'users': 0,
                         'leased': False, 'broken': False, 'path': path}
                try:
                    fcntl.fcntl(descriptor, fcntl.F_SETLEASE, fcntl.F_RDLCK)
                    entry['leased'] = True
                except OSError as error:
                    if (error.errno not in (errno.EACCES, errno.EPERM) or
                            not self.trusted_system_file(descriptor, value)):
                        raise uf.FlowError('Input read lease unavailable: ' + str(path)) from error
                self.files[key] = entry
                descriptor = None
            if entry['broken'] or entry['stamp'] != stamp(value):
                raise uf.FlowError('Input changed before read lease admission: ' + str(path))
            # A duplicate descriptor is not an acquired owner. Finish closing
            # it before incrementing; a close error must not strand a reference.
            if descriptor is not None:
                duplicate, descriptor = descriptor, None
                os.close(duplicate)
            entry['users'] += 1
            return key
        finally:
            original = sys.exception()
            try:
                if descriptor is not None:
                    try:
                        os.close(descriptor)
                    except BaseException:
                        if original is None:
                            raise
            finally:
                signal.pthread_sigmask(signal.SIG_SETMASK, previous)

    def check(self, keys):
        if (self.process != os.getpid() or signal.getsignal(signal.SIGIO) != self.interrupted or
                signal.SIGIO in signal.pthread_sigmask(signal.SIG_BLOCK, set())):
            raise uf.FlowError('Input read lease signal coverage changed')
        for key in keys:
            entry = self.files[key]
            if (entry['broken'] or stamp(os.fstat(entry['fd'])) != entry['stamp'] or
                    (entry['leased'] and
                     fcntl.fcntl(entry['fd'], fcntl.F_GETLEASE) != fcntl.F_RDLCK)):
                raise uf.FlowError('Prepared verification inputs changed during execution: '
                                   + str(entry['path']) + ' (read lease)')

    def release(self, keys, observed_failure):
        error = None
        with lease_update():
            inherited = self.process != os.getpid()
            if not inherited:
                self.interrupted(signal.SIGIO, None)
            for key in keys:
                entry = self.files[key]
                try:
                    if not observed_failure and (entry['broken'] or stamp(os.fstat(entry['fd'])) != entry['stamp']):
                        error = error or uf.FlowError('Input read lease changed during release')
                except BaseException as failure:
                    error = error or failure
                entry['users'] -= 1
                if not entry['users']:
                    try:
                        # A fork child closes its duplicate without unlocking
                        # the parent's shared open-file-description lease.
                        if entry['leased'] and not inherited:
                            self.unlock(entry)
                    except BaseException as failure:
                        error = error or failure
                    finally:
                        try:
                            os.close(entry['fd'])
                        except BaseException as failure:
                            error = error or failure
                        del self.files[key]
            # A break queued during release must not become a fatal default
            # SIGIO after the last owner restores the original handler.
            if not inherited and signal.SIGIO in signal.sigpending():
                self.interrupted(signal.SIGIO, None)
                signal.sigtimedwait({signal.SIGIO}, 0)
                error = error or uf.FlowError('Input read lease broke during release')
            self.users -= 1
            if not self.users:
                signal.signal(signal.SIGIO, self.previous)
        if error is not None:
            raise error


LEASES = ReadLeases()


def resolution_chain(path):
    """Return every lookup edge, including symlink redirects, without losing aliases."""
    pending = list(Path(path).absolute().parts[1:])
    current, edges, links = Path('/'), [], 0
    while pending:
        name = pending.pop(0)
        if name == '..':
            current = current.parent
            continue
        if name == '.':
            continue
        edges.append((current, name))
        current = current / name
        if current.is_symlink():
            links += 1
            if links > 40:
                raise uf.FlowError('External input symlink resolution is uncertain')
            target = Path(os.readlink(current))
            target = target if target.is_absolute() else current.parent / target
            pending = list(target.parts[1:]) + pending
            current = Path('/')
    return edges, current


class InputWatch:
    def __init__(self, root, excluded):
        self.root, self.excluded = root, excluded
        self.libc = ctypes.CDLL(None, use_errno=True)
        self.libc.inotify_init1.argtypes = [ctypes.c_int]
        self.libc.inotify_init1.restype = ctypes.c_int
        self.libc.inotify_add_watch.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_uint32]
        self.libc.inotify_add_watch.restype = ctypes.c_int
        self.fd = self.libc.inotify_init1(os.O_NONBLOCK | os.O_CLOEXEC)
        self.paths = {}
        self.locations = {}
        self.leases = set()
        self.lease_user = False
        self.observed_failure = False
        if self.fd < 0:
            raise uf.FlowError('Input mutation monitoring is unavailable')
        def incomplete(error):
            raise uf.FlowError('Input mutation monitoring could not cover every input') from error
        try:
            # start() validates the caller's mask before reserving SIGIO.
            previous = signal.pthread_sigmask(signal.SIG_BLOCK, {signal.SIGINT, signal.SIGTERM})
            try:
                LEASES.start()
                self.lease_user = True
            finally:
                signal.pthread_sigmask(signal.SIG_SETMASK, previous)
            # Bind the checkout's pathname before walking any of its inodes.
            # A restored ancestor swap need not modify the watched root itself.
            self.external(root, contents=False)
            pending = [Path(root)]
            while pending:
                path = pending.pop()
                self.add(path, path.relative_to(root).as_posix())
                try:
                    with os.scandir(path) as entries:
                        for entry in entries:
                            relative = Path(entry.path).relative_to(root).as_posix()
                            if not excluded(relative):
                                if entry.is_dir(follow_symlinks=False):
                                    pending.append(Path(entry.path))
                                elif entry.is_file(follow_symlinks=False):
                                    self.add(Path(entry.path), relative)
                except OSError as error:
                    incomplete(error)
        except BaseException:
            try:
                self.close()
            except BaseException:
                pass
            raise

    def add(self, path, relative=None, child=None):
        with lease_update():
            watch = self.libc.inotify_add_watch(self.fd, os.fsencode(path), MASK | DONT_FOLLOW)
            if watch < 0:
                raise uf.FlowError('Input mutation monitoring could not cover every input')
            self.paths.setdefault(watch, set()).add((relative, child))
            self.locations.setdefault(watch, Path(path))
            if stat.S_ISREG(Path(path).lstat().st_mode):
                key = LEASES.acquire(Path(path))
                if key in self.leases:
                    LEASES.files[key]['users'] -= 1
                else:
                    self.leases.add(key)

    def external(self, path, *, contents=True):
        edges, resolved = resolution_chain(path)
        for parent, name in edges:
            if parent.is_dir():
                self.add(parent, child=name)
            selected = parent / name
            if selected.is_symlink():
                self.add(selected)
        if resolved.exists() and (contents or not resolved.is_dir()):
            self.add(resolved)

    def check(self):
        if self.observed_failure:
            raise uf.FlowError('Input mutation monitoring was already invalidated')
        try:
            self.check_current()
        except BaseException:
            self.observed_failure = True
            raise

    def check_current(self):
        LEASES.check(self.leases)
        for _ in range(MAX_DRAIN_READS):
            try:
                data = os.read(self.fd, 65536)
            except BlockingIOError:
                LEASES.check(self.leases)
                return
            if not data:
                raise uf.FlowError('Input mutation monitoring ended unexpectedly')
            offset = 0
            while offset < len(data):
                if len(data) - offset < EVENT.size:
                    raise uf.FlowError('Input mutation monitoring returned incomplete evidence')
                watch, mask, _, length = EVENT.unpack_from(data, offset)
                end = offset + EVENT.size + length
                if end > len(data) or mask & (OVERFLOW | IGNORED) or watch not in self.paths:
                    raise uf.FlowError('Input mutation monitoring lost coverage')
                name = os.fsdecode(data[offset + EVENT.size:end].split(b'\0', 1)[0])
                for parent, child in self.paths[watch]:
                    if child is not None:
                        relevant = not name or name == child
                    else:
                        relative = (Path(parent) / name).as_posix() if parent is not None else None
                        relevant = relative is None or not self.excluded(relative)
                    if relevant:
                        changed = self.locations[watch] / name if name else self.locations[watch]
                        raise uf.FlowError('Prepared verification inputs changed during execution: '
                                           + str(changed) + ' (event ' + hex(mask) + ')')
                offset = end
        raise uf.FlowError('Input mutation monitoring did not quiesce within its drain bound')

    def close(self):
        try:
            if self.fd >= 0:
                descriptor, self.fd = self.fd, -1
                os.close(descriptor)
        finally:
            if self.lease_user:
                self.lease_user = False
                try:
                    LEASES.release(self.leases, self.observed_failure)
                finally:
                    self.leases.clear()
