"""Linux mutation evidence for one verification invocation; no persisted reuse."""
import ctypes
import os
from pathlib import Path
import struct

import user_flows as uf

# Writes, metadata changes, replacements and directory membership changes matter.
MASK = 0x0002 | 0x0004 | 0x0008 | 0x0040 | 0x0080 | 0x0100 | 0x0200 | 0x0400 | 0x0800
OVERFLOW = 0x4000
IGNORED = 0x8000
DONT_FOLLOW = 0x02000000
EVENT = struct.Struct('iIII')
MAX_DRAIN_READS = 64


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
        if self.fd < 0:
            raise uf.FlowError('Input mutation monitoring is unavailable')
        def incomplete(error):
            raise uf.FlowError('Input mutation monitoring could not cover every input') from error
        try:
            pending = [Path(root)]
            while pending:
                path = pending.pop()
                self.add(path, path.relative_to(root).as_posix())
                try:
                    with os.scandir(path) as entries:
                        pending.extend(Path(entry.path) for entry in entries
                                       if entry.is_dir(follow_symlinks=False)
                                       and not excluded(Path(entry.path).relative_to(root).as_posix()))
                except OSError as error:
                    incomplete(error)
        except BaseException:
            self.close()
            raise

    def add(self, path, relative=None, child=None):
        watch = self.libc.inotify_add_watch(self.fd, os.fsencode(path), MASK | DONT_FOLLOW)
        if watch < 0:
            raise uf.FlowError('Input mutation monitoring could not cover every input')
        self.paths.setdefault(watch, set()).add((relative, child))
        self.locations.setdefault(watch, Path(path))

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
        for _ in range(MAX_DRAIN_READS):
            try:
                data = os.read(self.fd, 65536)
            except BlockingIOError:
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
        if self.fd >= 0:
            os.close(self.fd)
            self.fd = -1
