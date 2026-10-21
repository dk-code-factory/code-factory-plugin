#!/usr/bin/env python3
"""Reports preserve analysis scope and point reviewers at actual mapped anchors."""
import copy
import json
import pathlib
import shlex
import subprocess
import sys
import tempfile
import unittest

import specs
import user_flows as uf
from specs_lib import gaps as gap_inventory
from specs_lib.reporting import build_report, format_report, report_json
from test_specs import ROOT, edit, fixture, put, render, write_tree


class ReportingTests(unittest.TestCase):
    def setUp(self):
        self.before = fixture()
        self.after = copy.deepcopy(self.before)
        self.after['app.py'] += b'# A reviewed implementation refactor.\n'
        self.report, self.affected = specs.analyze(self.before, self.after, 'effective-base')

    def summary(self, **selection):
        return build_report(self.before, self.after, self.report, self.affected,
                            'effective-base', **selection)

    def coverage_fixture(self, version, *, unrelated=False):
        files = fixture()
        ui, catalog = uf.load(files, specs.UI), uf.load(files, uf.CATALOG)
        ui['version'] = catalog['version'] = version
        reason = 'Widget recovery still lacks an assertion.'
        def gap(identifier, text):
            return text if version == 1 else {
                'version': 1, 'id': identifier, 'kind': 'unverified-behavior',
                'severity': 'P2', 'owner': 'ui-specification-maintainer', 'reason': text,
                'next_verification': 'Exercise a failed widget operation and assert retained draft and error.'}
        ui['coverage_groups'] = [{'name': 'Widget recovery', 'entities': ['W-EXAMPLE'],
                                  'assertions': [], 'gap': gap('gap-coverage-recovery-01', reason)}]
        if unrelated:
            ui['coverage_groups'].append({'name': 'Independent page', 'entities': ['PAGE-EXAMPLE'],
                'assertions': [], 'gap': gap('gap-coverage-independent-01', 'Independent page layout needs verification.')})
        put(files, specs.UI, ui)
        put(files, uf.CATALOG, catalog)
        render(files)
        specs.validate(files)
        return files

    def test_report_deduplicates_reasons_and_shared_actual_assertion_anchors(self):
        summary = self.summary()
        self.assertEqual(summary['artifacts'], [{'path': 'app.py', 'change': 'modified',
                         'before_mode': '100644', 'after_mode': '100644'}])
        self.assertEqual(len(summary['reasons']), len(set(summary['reasons'])))
        shared = [row for row in summary['references'] if row['role'] == 'assertion'
                  and row['path'] == 'scripts/test_app.py']
        self.assertEqual(len(shared), 1)
        self.assertEqual(shared[0]['anchor'], 'def test_perform():')
        self.assertEqual(shared[0]['snapshots'], ['after', 'before'])
        self.assertEqual(shared[0]['subjects'], ['flow-example', 'ui'])
        for review in summary['reviews']:
            for index in review['reference_ids']:
                self.assertIn(review['subject'], summary['references'][index]['subjects'])
        self.assertNotIn('fingerprint', report_json(summary))

    def test_renamed_source_retains_old_and_current_mappings_and_coverage_limits(self):
        self.after['renamed.py'] = self.after.pop('app.py')
        edit(self.after, uf.CATALOG, lambda c: c['flows'][0]['implementation'][0].update(path='renamed.py'))
        edit(self.after, specs.UI, lambda u: [r.update(path='renamed.py') for e in u['entities']
                                            for r in e['implementation'] if r['path'] == 'app.py'])
        edit(self.after, specs.UI, lambda u: u['entities'][2]['gaps'].append('Keyboard coverage remains unverified.'))
        report, affected = specs.analyze(self.before, self.after, 'effective-base')
        summary = build_report(self.before, self.after, report, affected, 'effective-base')
        refs = {row['path']: row for row in summary['references'] if row['role'] == 'implementation'}
        self.assertEqual(refs['app.py']['snapshots'], ['before'])
        self.assertEqual(refs['renamed.py']['snapshots'], ['after'])
        self.assertEqual(refs['renamed.py']['anchor'], 'def perform():')
        self.assertTrue(any(gap['reason'] == 'Keyboard coverage remains unverified.'
                            for review in summary['reviews'] for gap in review['gaps']))
        self.assertEqual({row['path']: row['change'] for row in summary['artifacts']}['app.py'], 'deleted')

    def test_selected_snapshots_preserve_comparison_and_omit_worktree_writes(self):
        for selection in ({'staged': True}, {'head': 'selected-head'}):
            with self.subTest(selection=selection):
                summary = self.summary(merge_base=True, root='/tmp/repo with spaces', **selection)
                self.assertEqual([row['action'] for row in summary['next_commands']], ['validate', 'check'])
                commands = {row['action']: row['argv'] for row in summary['next_commands']}
                self.assertIn('--root=/tmp/repo with spaces', commands['check'])
                self.assertIn('--base=effective-base', commands['check'])
                self.assertIn('--merge-base', commands['check'])
                flag = '--staged' if selection.get('staged') else '--head=selected-head'
                self.assertIn(flag, commands['check'])
                self.assertIn(flag, commands['validate'])
                self.assertNotIn('--merge-base', commands['validate'])
        with self.assertRaises(ValueError):
            self.summary(staged=True, head='HEAD')

    def test_worktree_next_commands_are_exact_argv_with_review_prerequisite(self):
        summary = self.summary(review_path='/tmp/review $(unsafe); name.json')
        self.assertEqual([row['action'] for row in summary['next_commands']],
                         ['render', 'validate', 'reconcile', 'check'])
        reconcile = summary['next_commands'][2]
        self.assertIn('--review=/tmp/review $(unsafe); name.json', reconcile['argv'])
        self.assertIn('Prepare substantive review JSON', reconcile['requires'])
        self.assertEqual(shlex.split(shlex.join(reconcile['argv'])), reconcile['argv'])
        self.assertIn('--base=effective-base', reconcile['argv'])

    def test_snapshot_modes_and_output_bounds_are_deterministic_without_mutation(self):
        original = copy.deepcopy((self.before, self.after, self.report, self.affected))
        first = self.summary()
        self.assertEqual(report_json(first), report_json(self.summary()))
        self.assertEqual(json.loads(report_json(first)), first)
        self.assertEqual((self.before, self.after, self.report, self.affected), original)
        human = format_report(first, max_items=1, max_chars=350)
        self.assertLessEqual(len(human), 350)
        self.assertIn('JSON', human)
        with self.assertRaises(ValueError):
            format_report(first, max_items=0)
        old = uf.Snapshot({'mode.py': b'identical'}, {'mode.py': '100644'})
        new = uf.Snapshot({'mode.py': b'identical'}, {'mode.py': '100755'})
        summary = build_report(old, new, {'terms': [], 'entities': [], 'flows': []}, {}, 'base')
        self.assertEqual(summary['artifacts'][0]['change'], 'mode')
        self.assertEqual([row['action'] for row in summary['next_commands']], ['validate', 'check'])

    def test_flow_report_excludes_independent_assertions_and_preserves_gap_reasons(self):
        before = fixture()
        before['scripts/test_island.py'] = b'def test_island():\n    assert True\n'
        ui = uf.load(before, specs.UI)
        island = copy.deepcopy(ui['entities'][3])
        island.update(id='W-ISLAND', assertions=['ISLAND'])
        ui['entities'].append(island)
        ui['instances'].append({'id': 'island', 'type': 'W-ISLAND', 'pane': 'P-EXAMPLE', 'repeat': 'one'})
        ui['assertions'].append({'id': 'ISLAND', 'path': 'scripts/test_island.py',
                                 'anchor': 'def test_island():', 'assertion': 'Independent widget coverage.'})
        put(before, specs.UI, ui)
        render(before)
        after = copy.deepcopy(before)
        after['docs/user-flows/example.md'] += b'\nReviewed independent flow wording.\n'
        edit(after, uf.CATALOG, lambda catalog: catalog['flows'][0]['gaps'].append({
            'scenario': 'S1', 'reason': 'Real-host acceptance remains unverified.'}))
        report, affected = specs.analyze(before, after, 'base')
        summary = build_report(before, after, report, affected, 'base')
        self.assertEqual([review['subject'] for review in summary['reviews']], ['flow-example'])
        self.assertNotIn('scripts/test_island.py', {row['path'] for row in summary['references']})
        self.assertIn({'id': 'flow:example', 'scenario': 'S1',
                       'reason': 'Real-host acceptance remains unverified.'}, summary['reviews'][0]['gaps'])

    def test_presentation_accepts_structured_and_legacy_ui_gap_reasons(self):
        # The pure presenter consumes both explicit inventory encodings; registry
        # version validation remains the caller's responsibility.
        ui = uf.load(self.after, specs.UI)
        ui['entities'][2]['gaps'] = ['Keyboard acceptance remains unverified.',
            {'reason': 'Real host event acceptance remains unverified.', 'scenario': 'S3'}]
        put(self.after, specs.UI, ui)
        summary = self.summary()
        gaps = next(row for row in summary['reviews'] if row['subject'] == 'ui')['gaps']
        self.assertIn({'id': 'ui:W-EXAMPLE', 'scenario': '',
                       'reason': 'Keyboard acceptance remains unverified.'}, gaps)
        self.assertIn({'id': 'ui:W-EXAMPLE', 'scenario': 'S3',
                       'reason': 'Real host event acceptance remains unverified.'}, gaps)

    def test_coverage_group_only_gap_reaches_reviews_and_unique_human_inventory(self):
        for version in (1, 2):
            with self.subTest(version=version):
                before = self.coverage_fixture(version)
                after = copy.deepcopy(before)
                after['app.py'] += b'# Reviewed implementation change.\n'
                report, affected = specs.analyze(before, after, 'base')
                self.assertEqual(set(affected), {'ui', 'flow-example'})
                summary = build_report(before, after, report, affected, 'base')
                original = copy.deepcopy((before, after, report, affected))
                for review in summary['reviews']:
                    self.assertEqual(len(review['gaps']), 1)
                    gap = review['gaps'][0]
                    self.assertEqual(gap['id'], 'coverage:Widget recovery')
                    self.assertEqual(gap['reason'], 'Widget recovery still lacks an assertion.')
                    self.assertEqual(gap['snapshots'], ['after', 'before'])
                    self.assertEqual(gap['entities'], ['W-EXAMPLE'])
                    self.assertEqual(gap['assertion_ids'], [])
                    self.assertEqual(gap['reference_ids'], [])
                    if version == 2:
                        self.assertEqual(gap['gap_id'], 'gap-coverage-recovery-01')
                        self.assertEqual(gap['severity'], 'P2')
                        self.assertTrue(gap['next_verification'])
                    else:
                        self.assertNotIn('gap_id', gap)
                        self.assertNotIn('severity', gap)
                if version == 2:
                    self.assertEqual(gap_inventory.report(uf.load(after, specs.UI),
                                     uf.load(after, uf.CATALOG))['counts']['total'], 1)
                human = format_report(summary)
                self.assertIn('Coverage gaps (1):', human)
                self.assertIn('Widget recovery still lacks an assertion.', human)
                self.assertEqual(report_json(summary), report_json(build_report(before, after, report, affected, 'base')))
                self.assertEqual((before, after, report, affected), original)

    def test_coverage_group_scope_follows_selected_ui_closure_and_adds_its_actual_anchors(self):
        for version in (1, 2):
            with self.subTest(version=version):
                before = self.coverage_fixture(version, unrelated=True)
                before['scripts/test_page.py'] = b'def test_page_recovery():\n    assert True\n'
                ui = uf.load(before, specs.UI)
                ui['assertions'].append({'id': 'PAGE-RECOVERY', 'path': 'scripts/test_page.py',
                    'anchor': 'def test_page_recovery():', 'assertion': 'An actual group assertion anchor.'})
                ui['entities'][0]['assertions'].append('PAGE-RECOVERY')
                ui['coverage_groups'][0]['assertions'] = ['PAGE-RECOVERY']
                put(before, specs.UI, ui)
                render(before)
                after = copy.deepcopy(before)
                after['docs/user-flows/example.md'] += b'\nReviewed flow-only wording.\n'
                report, affected = specs.analyze(before, after, 'base')
                self.assertEqual(set(affected), {'flow-example'})
                self.assertNotIn('ui:PAGE-EXAMPLE', affected['flow-example']['dependencies'])
                summary = build_report(before, after, report, affected, 'base')
                groups = summary['reviews'][0]['gaps']
                self.assertEqual([gap['id'] for gap in groups], ['coverage:Widget recovery'])
                self.assertEqual(groups[0]['assertion_ids'], ['PAGE-RECOVERY'])
                self.assertEqual(len(groups[0]['reference_ids']), 1)
                reference = summary['references'][groups[0]['reference_ids'][0]]
                self.assertEqual(reference['path'], 'scripts/test_page.py')
                self.assertEqual(reference['anchor'], 'def test_page_recovery():')
                self.assertEqual(reference['snapshots'], ['after', 'before'])
                self.assertEqual(sum(row['path'] == 'scripts/test_page.py' for row in summary['references']), 1)
                self.assertIn('Coverage gaps (1):', format_report(summary))
                self.assertNotIn('Independent page layout', format_report(summary))

    def test_coverage_group_only_gap_survives_actual_machine_and_human_cli_reports(self):
        for version in (1, 2):
            with self.subTest(version=version), tempfile.TemporaryDirectory() as directory:
                before = self.coverage_fixture(version)
                root = pathlib.Path(directory)
                write_tree(root, before)
                for args in [('init', '-q'), ('config', 'user.name', 'Report fixture'),
                             ('config', 'user.email', 'fixture@example.invalid'),
                             ('add', '.'), ('commit', '-qm', 'Report baseline')]:
                    uf.git(root, *args)
                (root / 'app.py').write_bytes(before['app.py'] + b'# Reviewed implementation change.\n')
                command = [sys.executable, str(ROOT / 'scripts/specs.py'), 'report',
                           '--root', str(root), '--base', 'HEAD']
                machine = subprocess.run(command + ['--json'], capture_output=True, text=True, timeout=10)
                self.assertEqual(machine.returncode, 0, machine.stdout + machine.stderr)
                result = json.loads(machine.stdout)
                self.assertEqual({review['subject'] for review in result['reviews']}, {'ui', 'flow-example'})
                self.assertTrue(all(review['gaps'][0]['id'] == 'coverage:Widget recovery'
                                    for review in result['reviews']))
                human = subprocess.run(command, capture_output=True, text=True, timeout=10)
                self.assertEqual(human.returncode, 0, human.stdout + human.stderr)
                self.assertIn('Coverage gaps (1):', human.stdout)
                self.assertIn('Widget recovery still lacks an assertion.', human.stdout)

    def test_changed_coverage_group_preserves_old_and_current_gap_and_assertion_evidence(self):
        for version in (1, 2):
            with self.subTest(version=version):
                before = self.coverage_fixture(version)
                edit(before, specs.UI, lambda ui: ui['coverage_groups'][0].update(assertions=['ASSERT']))
                after = copy.deepcopy(before)
                after['scripts/test_recovery.py'] = b'def test_current_recovery():\n    assert True\n'
                ui = uf.load(after, specs.UI)
                ui['assertions'].append({'id': 'CURRENT-RECOVERY', 'path': 'scripts/test_recovery.py',
                    'anchor': 'def test_current_recovery():', 'assertion': 'Current recovery assertion.'})
                ui['entities'][3]['assertions'].append('CURRENT-RECOVERY')
                group = ui['coverage_groups'][0]
                group.update(entities=['W-ACTION'], assertions=['CURRENT-RECOVERY'])
                if version == 2:
                    group['gap']['reason'] = 'The revised recovery path still needs verification.'
                else:
                    group['gap'] = 'The revised recovery path still needs verification.'
                put(after, specs.UI, ui)
                render(after)
                report, affected = specs.analyze(before, after, 'base')
                summary = build_report(before, after, report, affected, 'base')
                review = next(row for row in summary['reviews'] if row['subject'] == 'flow-example')
                records = {tuple(gap['snapshots']): gap for gap in review['gaps']}
                self.assertEqual(set(records), {('before',), ('after',)})
                old, current = records[('before',)], records[('after',)]
                self.assertEqual(old['entities'], ['W-EXAMPLE'])
                self.assertEqual(old['reason'], 'Widget recovery still lacks an assertion.')
                self.assertEqual(old['assertion_ids'], ['ASSERT'])
                self.assertEqual(current['entities'], ['W-ACTION'])
                self.assertEqual(current['reason'], 'The revised recovery path still needs verification.')
                self.assertEqual(current['assertion_ids'], ['CURRENT-RECOVERY'])
                old_reference = summary['references'][old['reference_ids'][0]]
                current_reference = summary['references'][current['reference_ids'][0]]
                self.assertEqual(old_reference['path'], 'scripts/test_app.py')
                self.assertEqual(current_reference['path'], 'scripts/test_recovery.py')
                if version == 2:
                    self.assertEqual(old['gap_id'], current['gap_id'])
                self.assertEqual(report_json(summary), report_json(build_report(before, after, report, affected, 'base')))


if __name__ == '__main__':
    unittest.main()
