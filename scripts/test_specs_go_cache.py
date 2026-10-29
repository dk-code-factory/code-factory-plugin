#!/usr/bin/env python3
"""Fresh Go verification must exercise helpers compiled by test subprocesses."""
import json
import os
import pathlib
import re
import shlex
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]


def configured_go_commands():
    makefile = (ROOT / 'Makefile').read_text()
    recipe = re.search(r'^test:[^\n]*\n((?:\t[^\n]*\n)*)', makefile, re.MULTILINE).group(1)
    make_commands = []
    for line in recipe.splitlines():
        argv = shlex.split(line.strip())
        if argv[:5] == ['python3', 'scripts/verification_timing.py', 'command', 'correctness', '--']:
            argv = argv[5:]
        if argv[:2] == ['go', 'test']:
            make_commands.append(argv)
    catalog = json.loads((ROOT / 'docs/user-flows/catalog.json').read_text())
    return {'Makefile': make_commands, 'catalog': catalog['suites']['go']['commands']}


class GoCacheTests(unittest.TestCase):
    def test_make_and_catalog_disable_go_test_result_cache(self):
        for owner, commands in configured_go_commands().items():
            with self.subTest(owner=owner):
                self.assertEqual(len(commands), 1)
                command = commands[0]
                self.assertEqual(command[:2], ['go', 'test'])
                self.assertIn('-race', command)
                self.assertIn('-count=1', command)
                self.assertIn('./...', command)

    def test_configured_verifiers_reject_changed_subprocess_helper_after_warm_cache(self):
        # Copy the actual provider/client tests and subprocess build helper. No
        # application dependencies, installed provider, credentials or network
        # are needed; Go's existing compilation cache remains usable.
        with tempfile.TemporaryDirectory(prefix='cf-go-cache-') as directory, \
                tempfile.TemporaryDirectory(prefix='cf-go-cache-tmp-') as temporary:
            root = pathlib.Path(directory)
            for package in ('codex', 'testutil'):
                shutil.copytree(ROOT / 'internal' / package, root / 'internal' / package)
            module = (ROOT / 'go.mod').read_text().splitlines()
            (root / 'go.mod').write_text('\n'.join(
                line for line in module if line.startswith(('module ', 'go '))) + '\n')
            environment = {
                **os.environ, 'CODE_FACTORY_REAL_CODEX': '0', 'GOFLAGS': '-buildvcs=false',
                'GOENV': 'off', 'GOWORK': 'off', 'GOPROXY': 'off',
                'GOSUMDB': 'off', 'GOTOOLCHAIN': 'local', 'TMPDIR': str(temporary),
            }
            # A locally cached toolchain may be newer than the PATH launcher.
            # Discover only installed files; auto selection can require checksum
            # lookups even for an already unpacked toolchain. Keep both this
            # verifier and testutil's nested go build strictly offline/local.
            toolchain = subprocess.run(['go', 'env', 'GOROOT', 'GOMODCACHE'], cwd=root,
                                       env=environment, capture_output=True, text=True, timeout=30)
            self.assertEqual(toolchain.returncode, 0, toolchain.stdout + toolchain.stderr)
            goroot, module_cache = toolchain.stdout.splitlines()
            roots = [pathlib.Path(goroot)]
            roots.extend((pathlib.Path(module_cache) / 'golang.org').glob('toolchain@v*'))
            required = next(line.split()[1] for line in module if line.startswith('go '))
            minimum = tuple(map(int, required.split('.')))
            installed = []
            for candidate in roots:
                version = (candidate / 'VERSION').read_text().splitlines()[0]
                if re.fullmatch(r'go\d+\.\d+\.\d+', version):
                    number = tuple(map(int, version.removeprefix('go').split('.')))
                    if number >= minimum and (candidate / 'bin/go').is_file():
                        installed.append((number, candidate))
            self.assertTrue(installed, 'An installed Go ' + required + '+ toolchain is required')
            binary_directory = min(installed)[1] / 'bin'
            environment['PATH'] = str(binary_directory) + os.pathsep + environment['PATH']

            def run(command):
                return subprocess.run(command, cwd=root, env=environment,
                                      capture_output=True, text=True, timeout=120)

            def output(result):
                return result.stdout + result.stderr

            cached_command = ['go', 'test', '-race', './...']
            warm = run(cached_command)
            self.assertEqual(warm.returncode, 0, output(warm))
            repeat = run(cached_command)
            self.assertEqual(repeat.returncode, 0, output(repeat))
            self.assertIn('codefactory/internal/codex\t(cached)', repeat.stdout)

            helper = root / 'internal/testutil/fakecodex/main.go'
            original = helper.read_text()
            expected = '"id": "thread-1"'
            self.assertEqual(original.count(expected), 1)
            helper.write_text(original.replace(expected, '"id": "broken-thread"'))

            # Demonstrate the hidden input: only fakecodex changed, so ordinary
            # result caching still accepts the old provider assertion's success.
            stale = run(cached_command)
            self.assertEqual(stale.returncode, 0, output(stale))
            self.assertIn('codefactory/internal/codex\t(cached)', stale.stdout)

            commands = {tuple(command) for group in configured_go_commands().values()
                        for command in group}
            self.assertTrue(commands)
            for command in sorted(commands):
                with self.subTest(command=command):
                    fresh = run(command)
                    self.assertNotEqual(fresh.returncode, 0, output(fresh))
                    self.assertIn('--- FAIL: TestProcessProtocolAndDisconnect', fresh.stdout)
                    self.assertIn('broken-thread', fresh.stdout)
                    self.assertNotIn('codefactory/internal/codex\t(cached)', fresh.stdout)


if __name__ == '__main__':
    unittest.main()
