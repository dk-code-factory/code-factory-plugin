"""Git snapshots for local specification tooling."""
import hashlib
import io
import subprocess


def git(root, *args, input=None):
    import user_flows as uf
    result = subprocess.run(["git", "-C", str(root), *args], capture_output=True, input=input)
    if result.returncode:
        raise uf.FlowError("Git comparison failed; provide an available base/head commit (fetch it in CI).")
    return result.stdout


def revision(root, ref):
    import user_flows as uf
    return uf.git(root, "rev-parse", "--verify", "--end-of-options", ref + "^{commit}").decode().strip()


class Snapshot(dict):
    def __init__(self, files, modes):
        super().__init__(files)
        self.modes = modes


def content_id(files, path):
    if path not in files:
        return None
    return (getattr(files, "modes", {}).get(path, "100644"), hashlib.sha256(files[path]).hexdigest())


def snapshot(root, ref=None):
    import user_flows as uf
    if ref:
        entries = []
        for row in uf.git(root, "ls-tree", "-r", "-z", "--full-tree", ref).split(b"\0"):
            if not row:
                continue
            metadata, path = row.split(b"\t", 1)
            mode, kind, oid = metadata.split()
            if mode not in (b"100644", b"100755") or kind != b"blob":
                raise uf.FlowError("Only regular files are supported in flow inputs: " + path.decode())
            entries.append((mode.decode(), oid, path.decode()))
        # Read exact blobs. git archive would apply export-ignore/export-subst
        # attributes and could hide or transform committed comparison inputs.
        stream = io.BytesIO(uf.git(root, "cat-file", "--batch", input=b"".join(oid + b"\n" for _, oid, _ in entries)))
        files, modes = {}, {}
        for mode, _, path in entries:
            _, _, size = stream.readline().split()
            files[path] = stream.read(int(size))
            stream.read(1)
            modes[path] = mode
        return uf.Snapshot(files, modes)
    names = uf.git(root, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
    files, modes = {}, {}
    for name in set(names.decode().split("\0")) - {""}:
        # Git collapses an untracked embedded repository to a directory entry.
        # Its contents have not been enumerated with the outer repository's
        # ignore rules, so silently dropping that entry would hide new inputs.
        if name.endswith("/"):
            raise uf.FlowError("Untracked directories are unsupported in flow inputs: " + name)
        path = root / name
        if path.is_symlink():
            raise uf.FlowError("Symlinks are unsupported in flow inputs: " + name)
        if path.is_file():
            files[name] = path.read_bytes()
            # Git records only the owner's executable bit, irrespective of the
            # group/other execute permissions retained by the filesystem.
            modes[name] = "100755" if path.stat().st_mode & 0o100 else "100644"
    return uf.Snapshot(files, modes)
