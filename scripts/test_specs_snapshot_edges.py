#!/usr/bin/env python3
"""Exercise filesystem edges against Git's actual comparison snapshots."""
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

import user_flows as uf
from test_specs import fixture, write_tree


ENTRYPOINT = pathlib.Path(__file__).with_name("specs.py")


class SnapshotEdgeTests(unittest.TestCase):
    def repository(self, mode=0o644):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = pathlib.Path(temporary.name)
        write_tree(root, fixture())
        (root / "app.py").chmod(mode)
        uf.git(root, "init", "-q")
        uf.git(root, "config", "user.name", "Snapshot fixture")
        uf.git(root, "config", "user.email", "fixture@example.invalid")
        uf.git(root, "config", "core.filemode", "true")
        uf.git(root, "add", ".")
        uf.git(root, "commit", "-qm", "Snapshot baseline")
        return root, uf.revision(root, "HEAD")

    def command(self, root, base, *selection, command="check", status=0):
        result = subprocess.run(
            [sys.executable, str(ENTRYPOINT), command, "--root", str(root),
             "--base", base, "--json", *selection], capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, status, result.stdout + result.stderr)
        self.assertNotIn("Traceback", result.stderr)
        return json.loads(result.stdout)

    def test_worktree_mode_uses_owner_execute_bit_only(self):
        root, _ = self.repository()
        for mode in (0o644, 0o645, 0o654, 0o655, 0o744, 0o745, 0o754, 0o755):
            with self.subTest(mode=oct(mode)):
                (root / "app.py").chmod(mode)
                uf.git(root, "add", "app.py")
                indexed = uf.snapshot(root, uf.git(root, "write-tree").decode().strip())
                actual = uf.snapshot(root)
                expected = "100755" if mode & 0o100 else "100644"
                self.assertEqual(indexed.modes["app.py"], expected)
                self.assertEqual(actual.modes["app.py"], expected)
                self.assertEqual(uf.content_id(actual, "app.py"), uf.content_id(indexed, "app.py"))

    def test_mode_changes_separate_worktree_index_and_committed_selection(self):
        for original, changed in ((0o755, 0o655), (0o655, 0o755)):
            with self.subTest(original=oct(original), changed=oct(changed)):
                root, base = self.repository(original)
                original_bytes = (root / "app.py").read_bytes()
                (root / "app.py").chmod(changed)
                self.assertIn(b"mode change", uf.git(root, "diff", "--summary", "--", "app.py"))
                self.assertEqual((root / "app.py").read_bytes(), original_bytes)
                worktree = self.command(root, base, status=1)
                self.assertIn("app.py", worktree["affected"]["flow-example"]["implementation"])
                self.assertEqual(self.command(root, base, "--staged")["affected"], {})
                self.assertEqual(self.command(root, base, "--head", base)["affected"], {})

                uf.git(root, "add", "app.py")
                (root / "app.py").chmod(original)
                self.assertEqual(self.command(root, base)["affected"], {})
                self.assertIn("flow-example", self.command(root, base, "--staged", status=1)["affected"])
                uf.git(root, "commit", "-qm", "Change executable mode")
                head = uf.revision(root, "HEAD")
                self.assertIn("flow-example", self.command(root, base, "--head", head, status=1)["affected"])
                self.assertEqual(self.command(root, base)["affected"], {})

    def test_untracked_embedded_repository_fails_closed_only_in_worktree(self):
        root, base = self.repository()
        nested = root / "nested"
        nested.mkdir()
        uf.git(nested, "init", "-q")
        (nested / "new_source.py").write_text("def newly_added_work():\n    return 1\n")
        self.assertIn(b"nested/\0", uf.git(root, "ls-files", "--others", "--exclude-standard", "-z"))
        with self.assertRaisesRegex(uf.FlowError, "Untracked directories.*nested/"):
            uf.snapshot(root)
        for command in ("check", "impact", "render"):
            report = self.command(root, base, command=command, status=1)
            self.assertIn("Untracked directories", "\n".join(report["errors"]))
        self.assertEqual(self.command(root, base, "--staged")["affected"], {})
        self.assertEqual(self.command(root, base, "--head", base)["affected"], {})

    def test_ordinary_untracked_files_and_git_ignores_keep_their_scope(self):
        root, base = self.repository()
        scratch = root / "scratch"
        scratch.mkdir()
        (scratch / "new_source.py").write_text("new source\n")
        (scratch / "ignored.py").write_text("ignored source\n")
        (root / ".git/info/exclude").write_text("scratch/ignored.py\n")
        files = uf.snapshot(root)
        self.assertIn("scratch/new_source.py", files)
        self.assertNotIn("scratch/ignored.py", files)
        report = self.command(root, base, status=1)
        self.assertIn("Unmapped file (map it or justify an exclusion): scratch/new_source.py", "\n".join(report["errors"]))
        nested = root / "nested"
        nested.mkdir()
        uf.git(nested, "init", "-q")
        (nested / "new_source.py").write_text("ignored embedded source\n")
        (root / ".git/info/exclude").write_text("scratch/\nnested/\n")
        self.assertNotIn("scratch/new_source.py", uf.snapshot(root))
        self.assertEqual(self.command(root, base)["affected"], {})

    def test_tracked_gitlinks_retain_regular_file_snapshot_rejection(self):
        root, base = self.repository()
        (root / "nested").mkdir()
        uf.git(root, "update-index", "--add", "--cacheinfo", "160000," + base + ",nested")
        names = uf.git(root, "ls-files", "--cached", "--others", "--exclude-standard", "-z").split(b"\0")
        self.assertIn(b"nested", names)
        self.assertNotIn(b"nested/", names)
        indexed = uf.git(root, "write-tree").decode().strip()
        with self.assertRaisesRegex(uf.FlowError, "Only regular files.*nested"):
            uf.snapshot(root, indexed)
        report = self.command(root, base, "--staged", status=1)
        self.assertIn("Only regular files", "\n".join(report["errors"]))
        uf.git(root, "commit", "-qm", "Register unsupported gitlink")
        head = uf.revision(root, "HEAD")
        report = self.command(root, base, "--head", head, status=1)
        self.assertIn("Only regular files", "\n".join(report["errors"]))


if __name__ == "__main__":
    unittest.main()
