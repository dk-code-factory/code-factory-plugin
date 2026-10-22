#!/usr/bin/env python3
"""Module imports, compatibility patch points and relocated tooling inputs."""
import copy
import inspect
import io
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import specs
import user_flows as uf
from test_specs import ROOT, fixture
from test_user_flows import fixture as legacy_fixture


class ModuleCompatibilityTests(unittest.TestCase):
    def test_modules_import_before_facades_without_initialization_cycles(self):
        modules = ('receipts', 'impact', 'rendering', 'maintenance', 'references', 'schema',
                   'processes', 'flow_receipts', 'flow_impact', 'flow_schema', 'flow_rendering',
                   'json_schema', 'git_snapshots', 'core')
        code = ("import importlib,sys; sys.path.insert(0," + repr(str(ROOT / 'scripts')) + "); "
                + "[importlib.import_module('specs_lib.'+name) for name in " + repr(modules) + "]; "
                + "import specs,user_flows; "
                + "assert specs.validate is importlib.import_module('specs_lib.schema').validate; "
                + "assert user_flows.snapshot is importlib.import_module('specs_lib.git_snapshots').snapshot; "
                + "print('compatible modules loaded')")
        result = subprocess.run([sys.executable, '-c', code], capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(result.stdout.strip(), 'compatible modules loaded')

    def test_public_helpers_still_resolve_patches_across_module_boundaries(self):
        with patch.object(specs, 'graph_errors', return_value=['patched graph validation']) as graph:
            with self.assertRaisesRegex(uf.FlowError, 'patched graph validation'):
                specs.flow_diagram({})
        graph.assert_called_once_with({})
        with patch.object(uf, 'git', return_value=b'fixture-commit\n') as git:
            self.assertEqual(uf.revision(pathlib.Path('/fixture'), 'HEAD'), 'fixture-commit')
        git.assert_called_once_with(pathlib.Path('/fixture'), 'rev-parse', '--verify', '--end-of-options', 'HEAD^{commit}')

    def test_reconcile_keeps_bound_callbacks_and_public_verifier_patch_point(self):
        signature = inspect.signature(uf.reconcile)
        self.assertIs(signature.parameters['analyzer'].default, uf.analyze)
        self.assertIs(signature.parameters['constraints'].default, uf.contract_errors)
        command = ['fixture-verifier', 'literal space']
        affected = {'example': {'description_changed': False, 'adopted': False, 'fingerprint': 'fixture-inputs',
                                'suites': {'unit': {'commands': [command, command]}}}}
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            review = root / 'review.json'
            review.write_text(json.dumps({'example': dict.fromkeys(
                ('description', 'implementation', 'tests'), 'Reviewed the preserved behavior and fixture assertions.')}))
            log = io.StringIO()
            with patch.object(uf, 'run_verification', return_value=subprocess.CompletedProcess(command, 0)) as verifier, \
                    patch.object(uf, 'snapshot', return_value={}) as snapshot:
                result = uf.reconcile(root, {}, affected, 'base', review, records='records/',
                                      analyzer=lambda before, after, base: ({}, affected), log_stream=log)
            self.assertEqual(result, {'reconciled': ['example']})
            verifier.assert_called_once_with(command, cwd=root, log_stream=log)
            self.assertEqual(snapshot.call_count, 2)
            record = json.loads((root / 'records/example.json').read_text())
            self.assertEqual(record['environment'], uf.FIXTURE_ENV)
            self.assertEqual([entry['command'] for entry in record['verification']], [command, command])

    def test_module_additions_changes_and_removals_stale_both_runner_fingerprints(self):
        path = 'scripts/specs_lib/future_feature.py'
        for factory, runner, subjects in ((fixture, specs.analyze, {'glossary', 'ui', 'flow-example'}),
                                           (legacy_fixture, uf.analyze, {'example'})):
            for change in ('add', 'modify', 'remove'):
                with self.subTest(factory=factory.__module__, change=change):
                    before = factory()
                    if change != 'add':
                        before[path] = b'# Initial operational module.\n'
                    after = copy.deepcopy(before)
                    if change == 'remove':
                        del after[path]
                    else:
                        after[path] = b'# New operational implementation.\n'
                    first = runner(before, after, 'base')[1]
                    self.assertEqual(set(first), subjects)
                    after[path] = b'# Revised operational implementation.\n'
                    refreshed = runner(before, after, 'base')[1]
                    self.assertEqual(set(refreshed), subjects)
                    for subject in subjects:
                        self.assertNotEqual(first[subject]['fingerprint'], refreshed[subject]['fingerprint'])
                    self.assertEqual(runner(after, after, 'base')[1], {})

    def test_operational_module_scope_does_not_cover_undeclared_data_files(self):
        for factory, validate in ((fixture, specs.validate), (legacy_fixture, uf.validate)):
            with self.subTest(factory=factory.__module__):
                files = factory()
                files['scripts/specs_lib/undeclared.json'] = b'{}\n'
                with self.assertRaisesRegex(uf.FlowError, 'Unmapped file'):
                    validate(files)


if __name__ == '__main__':
    unittest.main()
