#!/usr/bin/env python3
"""Fail-closed schema, verification dependency and credential-free CI checks."""
import copy
import os
import pathlib
import subprocess
import tempfile
import unittest

import specs
import user_flows as uf
from test_specs import ROOT, edit, fixture, render


class StrictGateTests(unittest.TestCase):
    def test_adoption_is_pinned_to_production_base_and_flow_list(self):
        files = fixture()
        pinned = {"base": specs.BASELINE, "flows": sorted(specs.ADOPTION_FLOWS)}
        edit(files, uf.CATALOG, lambda c: c.update(adoption=pinned))
        specs.validate(files)
        for adoption in ({**pinned, "base": "arbitrary"},
                         {**pinned, "flows": pinned["flows"] + ["example"]},
                         {**pinned, "flows": ["example"]},
                         {**pinned, "flows": ["install-connect"] * 14}, None):
            changed = copy.deepcopy(files)
            edit(changed, uf.CATALOG, lambda c: c.update(adoption=adoption))
            with self.subTest(adoption=adoption), self.assertRaisesRegex(uf.FlowError, "pinned base"):
                specs.validate(changed)
        before = copy.deepcopy(files)
        del before[uf.CATALOG]
        self.assertFalse(specs.analyze(before, files, specs.BASELINE)[1]['flow-example']['adopted'])

    def test_graph_schema_rejects_nonboolean_terminal_and_malformed_types(self):
        graph = uf.load(fixture(), uf.CATALOG)['flows'][0]['graph']
        for value in ('false', 'true', 0, 1, None, [], {}):
            malformed = copy.deepcopy(graph)
            malformed['nodes'][0]['terminal'] = value
            with self.subTest(terminal=value):
                self.assertTrue(specs.graph_errors(malformed))
                with self.assertRaises(uf.FlowError):
                    specs.flow_diagram(malformed)
        malformed_graphs = [None, [], {**graph, 'nodes': {}}, {**graph, 'nodes': [None]},
                            {**graph, 'edges': [None]}, {**graph, 'start': []}]
        for value in ([], {}, 1):
            malformed = copy.deepcopy(graph)
            malformed['nodes'][0]['scenario'] = value
            malformed_graphs.append(malformed)
        for malformed in malformed_graphs:
            self.assertTrue(specs.graph_errors(malformed))
        graph['nodes'][0]['terminal'] = False
        self.assertIn('N0["Read record #40;S1#41;"]', specs.flow_diagram(graph))

    def test_validation_rejects_graph_schema_without_crashing(self):
        files = fixture()
        edit(files, uf.CATALOG, lambda c: c['flows'][0].update(graph={'nodes': [None]}), rerender=False)
        with self.assertRaisesRegex(uf.FlowError, 'graph'):
            specs.validate(files)

    def test_required_tooling_suite_and_explicit_inputs_fail_closed(self):
        files = fixture()
        for invalid in (None, [], 'unit'):
            changed = copy.deepcopy(files)
            edit(changed, uf.CATALOG, lambda c: c['suites'].update(tooling=invalid), rerender=False)
            with self.assertRaisesRegex(uf.FlowError, 'tooling'):
                specs.validate(changed)
        edit(files, uf.CATALOG, lambda c: c['suites'].pop('tooling'), rerender=False)
        with self.assertRaisesRegex(uf.FlowError, 'tooling'):
            specs.validate(files)
        for inputs in ('app.py', ['../app.py'], ['/app.py'], ['missing.py'], [None]):
            files = fixture()
            edit(files, uf.CATALOG, lambda c: c['suites']['unit'].update(inputs=inputs))
            with self.subTest(inputs=inputs), self.assertRaisesRegex(uf.FlowError, 'suite inputs'):
                specs.validate(files)

    def test_maintenance_inputs_select_foundations_and_stale_fingerprints(self):
        before = fixture()
        edit(before, uf.CATALOG, lambda c: c['exclusions'].extend(
            {'glob': p, 'reason': 'Maintenance fixture'} for p in ('Makefile', 'scripts/verify.sh', 'AGENTS.md')))
        for path in ('Makefile', 'scripts/verify.sh', 'AGENTS.md', 'docs/specs/README.md'):
            after = copy.deepcopy(before)
            after[path] = after.get(path, b'') + b'\n# maintenance rule changed\n'
            first = specs.analyze(before, after, 'base')[1]
            self.assertEqual(set(first), {'glossary', 'ui', 'flow-example'})
            after[path] += b'# revised maintenance rule\n'
            second = specs.analyze(before, after, 'base')[1]
            with self.subTest(path=path):
                for subject in first:
                    self.assertNotEqual(first[subject]['fingerprint'], second[subject]['fingerprint'])

    def test_runner_changes_select_consumers_outside_source_mapping(self):
        before = fixture()
        before['scripts/run_unit.py'] = b'# shared test runner\n'
        catalog = uf.load(before, uf.CATALOG)
        other = copy.deepcopy(catalog['flows'][0])
        other.update(id='runner-owner', spec=uf.DIRECTORY + 'runner-owner.md', ui=[])
        other['implementation'].append({'path': 'scripts/run_unit.py', 'purpose': 'Shared runner'})
        catalog['flows'].append(other)
        catalog['suites']['unit']['commands'] = [['python3', './scripts/run_unit.py']]
        before[other['spec']] = before[catalog['flows'][0]['spec']]
        before[uf.CATALOG] = uf.canonical(catalog)
        render(before)
        after = copy.deepcopy(before)
        after['scripts/run_unit.py'] += b'# runner behavior changed\n'
        first = specs.analyze(before, after, 'base')[1]
        self.assertIn('ui', first)
        self.assertIn('flow-example', first)
        after['scripts/run_unit.py'] += b'# runner behavior changed again\n'
        second = specs.analyze(before, after, 'base')[1]
        for subject in ('ui', 'flow-example'):
            self.assertNotEqual(first[subject]['fingerprint'], second[subject]['fingerprint'])

    def test_explicit_directory_inputs_track_deleted_dependencies(self):
        before = fixture()
        before['runner/dependency.txt'] = b'old dependency\n'
        edit(before, uf.CATALOG, lambda c: (
            c['exclusions'].append({'glob': 'runner/**', 'reason': 'Runner data fixture'}),
            c['suites']['unit'].update(inputs=['runner'])
        ))
        after = copy.deepcopy(before)
        del after['runner/dependency.txt']
        edit(after, uf.CATALOG, lambda c: c['suites']['unit'].pop('inputs'))
        report, affected = specs.analyze(before, after, 'base')
        self.assertIn('ui', affected)
        self.assertTrue(any('verification input changed: runner/dependency.txt' in item['reasons'] for item in report['entities']))


class FixtureCIEnvironmentTests(unittest.TestCase):
    def test_ci_overrides_environment_and_command_line_real_provider_opt_ins(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / 'Makefile').write_bytes((ROOT / 'Makefile').read_bytes())
            (root / 'scripts').mkdir()
            script = root / 'scripts/verify.sh'
            script.write_text('#!/bin/sh\nprintf "%s" "$CODE_FACTORY_REAL_CODEX"\n')
            script.chmod(0o755)
            for args in ([], ['CODE_FACTORY_REAL_CODEX=1']):
                result = subprocess.run(['make', '--no-print-directory', 'ci', 'FLOW_BASE=main', *args],
                                        cwd=root, env={**os.environ, 'CODE_FACTORY_REAL_CODEX': '1'}, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.splitlines()[-1], '0')

    def test_verify_script_forces_fixture_submakes_and_direct_commands(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root / 'scripts').mkdir()
            (root / 'scripts/verify.sh').write_bytes((ROOT / 'scripts/verify.sh').read_bytes())
            tools = root / 'tools'
            tools.mkdir()
            for command in ('make', 'python3'):
                path = tools / command
                path.write_text('#!/bin/sh\nprintf "%s:%s:%s\\n" "' + command + '" "$CODE_FACTORY_REAL_CODEX" "$*"\n')
                path.chmod(0o755)
            result = subprocess.run(['sh', 'scripts/verify.sh'], cwd=root,
                                    env={**os.environ, 'PATH': str(tools) + ':' + os.environ['PATH'],
                                         'CODE_FACTORY_REAL_CODEX': '1', 'MAKEFLAGS': 'CODE_FACTORY_REAL_CODEX=1'},
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            for line in result.stdout.splitlines():
                self.assertEqual(line.split(':')[1], '0')
                if line.startswith('make:'):
                    self.assertIn('CODE_FACTORY_REAL_CODEX=0', line)

    def test_real_provider_target_remains_explicit(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            executable = root / 'go'
            executable.write_text('#!/bin/sh\nprintf "%s" "$CODE_FACTORY_REAL_CODEX"\n')
            executable.chmod(0o755)
            result = subprocess.run(['make', '--no-print-directory', '-f', str(ROOT / 'Makefile'), 'test-real-provider'],
                                    cwd=root, env={**os.environ, 'PATH': str(root) + ':' + os.environ['PATH']}, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.splitlines()[-1], '1')


if __name__ == '__main__':
    unittest.main()
