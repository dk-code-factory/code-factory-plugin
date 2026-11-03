"""Exercise verification ownership with real children, locks and Git discovery."""
import json
import os
from pathlib import Path
import select
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

from specs_lib.isolation import isolated
from test_specs_timing import cleanup_process


class IsolationTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name) / "checkout"
        self.root.mkdir()
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        self.env = dict(os.environ)
        for key in list(self.env):
            if key.startswith("CODE_FACTORY_VERIFY_"):
                self.env.pop(key)
        self.env["PYTHONPATH"] = str(Path(__file__).resolve().parent)

    def probe(self, root, body, *, env=None):
        script = "from specs_lib.isolation import isolated\nfrom pathlib import Path\nimport os,json,sys,time\nwith isolated(Path(sys.argv[1])):\n" + "\n".join("    " + line for line in body.splitlines())
        child_env = dict(env or self.env, PYTHONPATH=str(Path(__file__).resolve().parent))
        child = subprocess.Popen([sys.executable, "-c", script, str(root)], env=child_env,
                                 stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                 stderr=subprocess.PIPE, text=True)
        self.addCleanup(cleanup_process, child)
        return child

    def ready_line(self, child, timeout=5):
        # select followed by readline can still block on partial output.
        deadline = time.monotonic() + timeout
        data = b""
        while b"\n" not in data:
            remaining = deadline - time.monotonic()
            self.assertGreater(remaining, 0, 'child readiness timed out')
            self.assertTrue(select.select([child.stdout], [], [], remaining)[0],
                            'child readiness timed out')
            chunk = os.read(child.stdout.fileno(), 4096)
            self.assertTrue(chunk, 'child exited before readiness')
            data += chunk
        return data.split(b"\n", 1)[0].decode()

    def test_nested_ownership_reuses_private_state_and_restores_environment(self):
        previous = dict(os.environ)
        with isolated(self.root):
            temporary = Path(os.environ["TMPDIR"])
            self.assertTrue(temporary.is_dir())
            self.assertNotEqual(temporary, Path(self.directory.name))
            with isolated(self.root):
                self.assertEqual(Path(os.environ["TMPDIR"]), temporary)
            child = self.probe(self.root, "print(os.environ['TMPDIR'])", env=dict(os.environ))
            stdout, stderr = child.communicate(timeout=10)
            self.assertEqual(child.returncode, 0, stderr)
            self.assertEqual(stdout.strip(), str(temporary))
        self.assertFalse(temporary.exists())
        self.assertEqual(dict(os.environ), previous)

    def test_competing_checkout_fails_before_command_and_stale_lock_file_is_safe(self):
        with isolated(self.root):
            child = self.probe(self.root, "print('MUTATED')")
            stdout, stderr = child.communicate(timeout=10)
            self.assertNotEqual(child.returncode, 0)
            self.assertNotIn("MUTATED", stdout)
            self.assertIn("already owns this checkout", stderr)
        # The persistent inode contains no authority; the kernel lock does.
        (self.root / ".git/verification.lock").write_text("stale prior PID")
        with isolated(self.root):
            self.assertTrue(Path(os.environ["TMPDIR"]).is_dir())

    def test_stale_inherited_run_is_rejected(self):
        with isolated(self.root):
            env = dict(os.environ)
            env["CODE_FACTORY_VERIFY_TOKEN"] = "stale"
            child = self.probe(self.root, "print('MUTATED')", env=env)
            stdout, stderr = child.communicate(timeout=10)
            self.assertNotEqual(child.returncode, 0)
            self.assertNotIn("MUTATED", stdout)
            self.assertIn("Stale or malformed", stderr)

    def test_owner_death_rejects_stale_inheritance_and_fresh_run_reclaims_lock(self):
        owner = self.probe(self.root, "print(json.dumps({key:value for key,value in os.environ.items() if key.startswith('CODE_FACTORY_VERIFY_') or key=='TMPDIR'}),flush=True)\ntime.sleep(30)")
        def cleanup():
            if owner.poll() is None:
                owner.kill()
            owner.communicate(timeout=5)
        self.addCleanup(cleanup)
        inherited = json.loads(self.ready_line(owner))
        abandoned = Path(inherited['CODE_FACTORY_VERIFY_RUN'])
        owner.kill()
        owner.communicate(timeout=5)
        self.assertTrue(abandoned.exists())
        self.addCleanup(shutil.rmtree, abandoned)
        stale = self.probe(self.root, "print('MUTATED')", env=inherited)
        stdout, stderr = stale.communicate(timeout=5)
        self.assertNotEqual(stale.returncode, 0)
        self.assertNotIn('MUTATED', stdout)
        self.assertIn('Stale or malformed', stderr)
        with isolated(self.root):
            self.assertNotEqual(Path(os.environ['CODE_FACTORY_VERIFY_RUN']), abandoned)
        self.assertTrue(abandoned.exists(), 'fresh ownership must not clean another run')

    def test_unreaped_dead_owner_cannot_join_replacement_checkout_owner(self):
        body = "print(json.dumps(dict(os.environ)),flush=True)\nsys.stdin.readline()"
        owner = self.probe(self.root, body)
        inherited = json.loads(self.ready_line(owner))
        abandoned = Path(inherited['CODE_FACTORY_VERIFY_RUN'])
        self.addCleanup(shutil.rmtree, abandoned, True)
        owner.kill()
        deadline = time.monotonic() + 5
        while os.waitid(os.P_PID, owner.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT) is None:
            self.assertLess(time.monotonic(), deadline, 'owner did not exit')
            time.sleep(.01)
        # Keep A unreaped: /proc still exposes its original process identity.
        replacement = self.probe(self.root, body)
        current = json.loads(self.ready_line(replacement))
        self.assertNotEqual(current['CODE_FACTORY_VERIFY_RUN'], str(abandoned))
        stale = self.probe(self.root, "print('MUTATED')", env=inherited)
        stdout, stderr = stale.communicate(timeout=5)
        self.assertNotEqual(stale.returncode, 0)
        self.assertNotIn('MUTATED', stdout)
        self.assertIn('Stale or malformed', stderr)
        replacement.communicate(input='release\n', timeout=5)

    def test_inherited_root_is_required_and_must_match_its_owner(self):
        with isolated(self.root):
            for value in (None, str(self.root / 'wrong')):
                with self.subTest(root=value):
                    inherited = dict(os.environ)
                    inherited.pop('CODE_FACTORY_VERIFY_ROOT', None)
                    if value is not None:
                        inherited['CODE_FACTORY_VERIFY_ROOT'] = value
                    child = self.probe(self.root, "print('MUTATED')", env=inherited)
                    stdout, stderr = child.communicate(timeout=5)
                    self.assertNotEqual(child.returncode, 0)
                    self.assertNotIn('MUTATED', stdout)
                    self.assertIn('Stale or malformed', stderr)

    def test_startup_signal_defers_until_temporary_directory_can_be_cleaned(self):
        real_temporary = tempfile.TemporaryDirectory
        allocated = []
        def allocate(*args, **kwargs):
            directory = real_temporary(*args, **kwargs)
            allocated.append(Path(directory.name))
            os.kill(os.getpid(), signal.SIGTERM)
            return directory
        previous = dict(os.environ)
        with patch('specs_lib.isolation.tempfile.TemporaryDirectory', side_effect=allocate):
            with self.assertRaises(SystemExit) as stopped:
                with isolated(self.root):
                    self.fail('cancelled startup executed verifier work')
        self.assertEqual(stopped.exception.code, 143)
        self.assertTrue(allocated)
        self.assertTrue(all(not path.exists() for path in allocated))
        self.assertEqual(dict(os.environ), previous)
        with isolated(self.root):
            pass

    def test_unrelated_ancestor_git_marker_is_not_discovered(self):
        ancestor_root = Path(self.directory.name)
        subprocess.run(["git", "init", "-q", str(ancestor_root)], check=True)
        ancestor = ancestor_root / ".git"
        sentinel = ancestor / "sentinel"
        sentinel.write_text("unrelated")
        with patch.dict(os.environ, {"TMPDIR": str(ancestor_root)}), isolated(self.root):
            fixture = Path(tempfile.mkdtemp())
            # Prove this is a real discoverable ancestor, so the assertion would
            # catch a missing ceiling instead of passing with an invalid marker.
            unbounded = dict(os.environ)
            unbounded.pop("GIT_CEILING_DIRECTORIES", None)
            inherited = subprocess.check_output(["git", "rev-parse", "--show-toplevel"],
                                                 cwd=fixture, env=unbounded, text=True)
            self.assertEqual(Path(inherited.strip()), ancestor_root)
            result = subprocess.run(["git", "rev-parse", "--show-toplevel"], cwd=fixture,
                                    capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            subprocess.run(["git", "init", "-q", str(fixture)], check=True)
            result = subprocess.check_output(["git", "rev-parse", "--show-toplevel"], cwd=fixture, text=True)
            self.assertEqual(Path(result.strip()), fixture)
        self.assertEqual(sentinel.read_text(), "unrelated")

    def test_two_worktrees_complete_concurrently_with_distinct_state(self):
        subprocess.run(["git", "-C", str(self.root), "-c", "user.name=Test", "-c",
                        "user.email=test@example.invalid", "commit", "-qm", "fixture", "--allow-empty"], check=True)
        second = Path(self.directory.name) / "second"
        subprocess.run(["git", "-C", str(self.root), "worktree", "add", "-q", "--detach", str(second)], check=True)
        body = "print(json.dumps({'tmp':os.environ['TMPDIR'],'pid':os.getpid()}),flush=True)\nsys.stdin.readline()\nprint('completed')"
        a = self.probe(self.root, body)
        b = self.probe(second, body)
        first, other = json.loads(self.ready_line(a)), json.loads(self.ready_line(b))
        self.assertIsNone(a.poll())
        self.assertIsNone(b.poll())
        self.assertNotEqual(first["tmp"], other["tmp"])
        for process, state in ((a, first), (b, other)):
            stdout, stderr = process.communicate(input='release\n', timeout=10)
            self.assertEqual(process.returncode, 0, stderr)
            self.assertIn("completed", stdout)
            self.assertFalse(Path(state["tmp"]).exists())

    def test_interruption_releases_checkout_and_removes_only_owned_state(self):
        script = """from specs_lib.isolation import isolated
from specs_lib.processes import run_verification
from pathlib import Path
import os,sys
with isolated(Path(sys.argv[1])):
    print(os.environ['TMPDIR'],flush=True)
    command = 'import os,pathlib,sys,time; pathlib.Path(sys.argv[1]).write_text(str(os.getpid())); time.sleep(30)'
    run_verification([sys.executable,'-c',command,str(Path(os.environ['TMPDIR'])/'child-ready')],cwd=Path(sys.argv[1]))
"""
        child = subprocess.Popen([sys.executable, "-c", script, str(self.root)], env=self.env,
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.addCleanup(cleanup_process, child)
        temporary = Path(self.ready_line(child))
        # A real child has started after production recording handlers were
        # installed; cancellation must remove both its work and private state.
        deadline = time.monotonic() + 5
        ready = temporary / 'child-ready'
        while not ready.exists():
            self.assertLess(time.monotonic(), deadline)
            time.sleep(.01)
        verifier = int(ready.read_text())
        child.send_signal(signal.SIGTERM)
        child.communicate(timeout=10)
        self.assertEqual(child.returncode, 143)
        self.assertFalse(temporary.exists())
        with self.assertRaises(ProcessLookupError):
            os.kill(verifier, 0)
        self.assertTrue(self.root.exists())
        with isolated(self.root):
            pass

    def test_absent_and_partial_readiness_are_bounded_and_failure_reaps_child(self):
        for output in ('', "print('partial',end='',flush=True)"):
            with self.subTest(output=output):
                child = self.probe(self.root, output + "\ntime.sleep(60)")
                with self.assertRaisesRegex(AssertionError, 'readiness timed out'):
                    try:
                        self.ready_line(child, timeout=.1)
                    finally:
                        cleanup_process(child)
                self.assertIsNotNone(child.returncode)
                with self.assertRaises(ChildProcessError):
                    os.waitpid(child.pid, os.WNOHANG)


if __name__ == "__main__":
    unittest.main()
