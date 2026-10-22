#!/usr/bin/env python3
"""Historical directory scopes retain discovery and stale real CLI receipts."""
import copy
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

import specs
import user_flows as uf
from test_specs import ROOT, edit, fixture, put, render, write_tree


DIRECTORY = 'docs/runner-inputs'
CURRENT = [sys.executable, '-c',
           "import pathlib; assert not pathlib.Path('docs/runner-inputs/failing.py').exists(); print('CURRENT_RUNNER')"]
OBSOLETE = [sys.executable, '-c', "raise AssertionError('OBSOLETE_RUNNER')"]


class HistoricalInputTests(unittest.TestCase):
    def setUp(self):
        self.before = fixture()
        self.before[DIRECTORY + '/seed.py'] = b'# Existing discovery input.\n'
        edit(self.before, uf.CATALOG, lambda c: c['suites'].update(
            unit={'commands': [CURRENT], 'inputs': [DIRECTORY]},
            tooling={'commands': [[sys.executable, '-c', "print('MANDATORY_TOOLING')"]]}))
        self.after = copy.deepcopy(self.before)

    def remove_selector(self, variant):
        if variant == 'argv':
            edit(self.before, uf.CATALOG, lambda c: c['suites']['unit'].update(
                commands=[CURRENT + ['./' + DIRECTORY + '/']], inputs=[]))
            self.after = copy.deepcopy(self.before)
            edit(self.after, uf.CATALOG, lambda c: c['suites']['unit'].update(commands=[CURRENT]))
        elif variant == 'retired':
            catalog = uf.load(self.before, uf.CATALOG)
            retired = copy.deepcopy(catalog['flows'][0])
            retired.update(id='retired', spec='docs/user-flows/retired.md', suites=['retiring'])
            catalog['flows'].append(retired)
            catalog['suites']['retiring'] = {'commands': [OBSOLETE], 'inputs': [DIRECTORY]}
            catalog['suites']['unit'].pop('inputs')
            self.before[retired['spec']] = self.before['docs/user-flows/example.md']
            put(self.before, uf.CATALOG, catalog)
            render(self.before)
            self.after = copy.deepcopy(self.before)
            del self.after[retired['spec']]
            edit(self.after, uf.CATALOG, lambda c: (
                c.update(flows=[c['flows'][0]]), c['suites'].pop('retiring')))
            self.after['app.py'] += b'# Related implementation retained.\n'
            self.after['scripts/test_app.py'] += b'# Related assertion retained.\n'
        else:
            edit(self.before, uf.CATALOG, lambda c: c['suites']['unit'].update(commands=[OBSOLETE]))
            self.after = copy.deepcopy(self.before)
            edit(self.after, uf.CATALOG, lambda c: c['suites']['unit'].update(commands=[CURRENT], inputs=[]))
            if variant == 'renamed':
                def rename(catalog):
                    catalog['suites']['replacement'] = catalog['suites'].pop('unit')
                    catalog['flows'][0]['suites'] = ['replacement']
                edit(self.after, uf.CATALOG, rename)
                edit(self.after, specs.UI, lambda u: [e.update(suites=['replacement']) for e in u['entities']])

    def impact(self, before=None, after=None):
        return specs.analyze(self.before if before is None else before,
                             self.after if after is None else after, 'base')

    def test_removed_directory_scopes_cover_new_files_for_declared_argv_and_retired_suites(self):
        for variant in ('declared', 'argv', 'renamed', 'retired'):
            with self.subTest(variant=variant):
                self.setUp()
                self.remove_selector(variant)
                subject = 'flow-retired' if variant == 'retired' else 'ui'
                initial = self.impact()[1][subject]
                path = DIRECTORY + '/newly-discovered.py'
                self.after[path] = b'# Newly discovered input.\n'
                report, affected = self.impact()
                self.assertNotEqual(initial['fingerprint'], affected[subject]['fingerprint'])
                self.assertNotEqual(initial['dependencies'], affected[subject]['dependencies'])
                self.assertTrue(any('verification input changed: ' + path in item['reasons']
                                    for layer in report.values() for item in layer))
                self.assertNotIn(path, self.before)
                self.assertIsNone(uf.content_id(self.before, path))
                commands = [command for suite in affected[subject]['suites'].values()
                            for command in suite['commands']]
                self.assertIn(CURRENT, commands)
                self.assertNotIn(OBSOLETE, commands)

    def test_current_directory_scope_also_retains_deleted_baseline_paths(self):
        for selector in ('declared', 'argv'):
            with self.subTest(selector=selector):
                self.setUp()
                path = 'docs/new-inputs/deleted.py'
                self.before[path] = b'# Deleted before this selector was introduced.\n'
                self.before['docs/new-inputs/seed.py'] = b'# Retained input.\n'
                self.after = copy.deepcopy(self.before)
                del self.after[path]
                edit(self.after, uf.CATALOG, lambda c: c['suites']['unit'].update(
                    inputs=['docs/new-inputs'] if selector == 'declared' else [],
                    commands=[CURRENT] if selector == 'declared' else [CURRENT + ['./docs/new-inputs/']]))
                original = self.impact()[1]['ui']
                without_path = copy.deepcopy(self.before)
                del without_path[path]
                changed = self.impact(before=without_path)[1]['ui']
                self.assertNotEqual(original['fingerprint'], changed['fingerprint'])
                self.assertNotEqual(original['dependencies'], changed['dependencies'])
                self.assertNotIn(path, self.after)
                self.assertIsNone(uf.content_id(self.after, path))

    def test_renamed_directory_keeps_old_and_current_scopes_but_isolates_unrelated_paths(self):
        self.after['docs/new-inputs/seed.py'] = b'# New directory seed.\n'
        edit(self.after, uf.CATALOG, lambda c: c['suites']['unit'].update(inputs=['docs/new-inputs']))
        original = self.impact()[1]['ui']
        for directory in (DIRECTORY, 'docs/new-inputs', 'docs/unrelated'):
            after = copy.deepcopy(self.after)
            after[directory + '/discovered.py'] = b'# Discovery.\n'
            changed = self.impact(after=after)[1]['ui']
            with self.subTest(directory=directory):
                if directory == 'docs/unrelated':
                    self.assertEqual(original['fingerprint'], changed['fingerprint'])
                    self.assertEqual(original['dependencies'], changed['dependencies'])
                else:
                    self.assertNotEqual(original['fingerprint'], changed['fingerprint'])
                    self.assertNotEqual(original['dependencies'], changed['dependencies'])

    def cli(self, root, base, command, *arguments):
        result = subprocess.run([sys.executable, str(ROOT / 'scripts/specs.py'), command,
                                 '--root', str(root), '--base', base, '--json', *arguments],
                                env={**os.environ, 'CODE_FACTORY_REAL_CODEX': '0'},
                                capture_output=True, text=True)
        return result, json.loads(result.stdout)

    def repository(self, directory):
        root = pathlib.Path(directory) / 'repository'
        root.mkdir()
        write_tree(root, self.before)
        uf.git(root, 'init', '-q')
        uf.git(root, 'config', 'user.name', 'Isolated fixture')
        uf.git(root, 'config', 'user.email', 'fixture@example.invalid')
        uf.git(root, 'add', '.')
        uf.git(root, 'commit', '-qm', 'Isolated directory baseline')
        base = uf.revision(root, 'HEAD')
        for removed in self.before.keys() - self.after.keys():
            (root / removed).unlink()
        write_tree(root, self.after)
        return root, base

    def reconcile(self, root, base, directory):
        impact, detail = self.cli(root, base, 'impact')
        self.assertEqual(impact.returncode, 0, detail)
        notes = {'kind': 'refactor', **dict.fromkeys(
            ('description', 'implementation', 'tests'),
            'Reviewed the retained outcome, related implementation, and current fixture assertions after selector retirement.')}
        review = pathlib.Path(directory) / 'review.json'
        review.write_text(json.dumps(dict.fromkeys(detail['affected'], notes)))
        return self.cli(root, base, 'reconcile', '--review', str(review))

    def test_real_reconcile_then_addition_stales_receipt_even_after_selector_retirement(self):
        for variant in ('declared', 'argv', 'renamed', 'retired'):
            with self.subTest(variant=variant), tempfile.TemporaryDirectory() as directory:
                self.setUp()
                self.remove_selector(variant)
                root, base = self.repository(directory)
                verified, detail = self.reconcile(root, base, directory)
                self.assertEqual(verified.returncode, 0, detail)
                self.assertIn('CURRENT_RUNNER', verified.stderr)
                self.assertNotIn('OBSOLETE_RUNNER', verified.stderr)
                self.assertEqual(self.cli(root, base, 'check')[0].returncode, 0)
                subject = 'flow-retired' if variant == 'retired' else 'ui'
                receipt = root / specs.RECORDS / (subject + '.json')
                record = json.loads(receipt.read_text())
                (root / DIRECTORY / 'failing.py').write_text('# Newly discovered failing input.\n')
                direct = subprocess.run(CURRENT, cwd=root, capture_output=True, text=True)
                self.assertEqual(direct.returncode, 1)
                stale, detail = self.cli(root, base, 'check')
                self.assertEqual(stale.returncode, 1, detail)
                self.assertTrue(any('stale' in error for error in detail['errors']))
                changed = detail['affected'][subject]
                self.assertNotEqual(record['fingerprint'], changed['fingerprint'])
                self.assertNotEqual(record['dependencies'], changed['dependencies'])
                failed, detail = self.reconcile(root, base, directory)
                self.assertEqual(failed.returncode, 1, detail)
                self.assertNotIn('OBSOLETE_RUNNER', failed.stderr)

    def test_real_receipt_stales_for_deletion_rename_and_mode_of_new_historical_input(self):
        for operation in ('delete', 'rename', 'mode'):
            with self.subTest(operation=operation), tempfile.TemporaryDirectory() as directory:
                self.setUp()
                self.remove_selector('declared')
                path = DIRECTORY + '/introduced.py'
                self.after[path] = b'# First appears after the directory selector was removed.\n'
                root, base = self.repository(directory)
                verified, detail = self.reconcile(root, base, directory)
                self.assertEqual(verified.returncode, 0, detail)
                self.assertEqual(self.cli(root, base, 'check')[0].returncode, 0)
                local = root / path
                if operation == 'delete':
                    local.unlink()
                elif operation == 'rename':
                    local.rename(local.with_name('renamed.py'))
                else:
                    local.chmod(0o755)
                stale, detail = self.cli(root, base, 'check')
                self.assertEqual(stale.returncode, 1, detail)
                self.assertTrue(any('stale' in error for error in detail['errors']))


if __name__ == '__main__':
    unittest.main()
