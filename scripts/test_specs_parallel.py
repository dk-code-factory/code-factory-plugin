#!/usr/bin/env python3
"""Complete-inventory, process-isolation and failure controls for Python suites."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

import python_suites
from test_specs_isolation import ProbeSupervision


class ParallelSuiteTests(ProbeSupervision, unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.probe_drained = True
        self.addCleanup(self.cleanup_directory)
        self.root = Path(self.directory.name)
        scripts = self.root / 'scripts'
        scripts.mkdir()
        for name in ('python_suites.py', 'user_flows.py'):
            shutil.copyfile(Path(__file__).parent / name, scripts / name)
        shutil.copytree(Path(__file__).parent / 'specs_lib', scripts / 'specs_lib',
                        ignore=shutil.ignore_patterns('__pycache__'))
        self.tests = scripts / 'test_specs_fixture.py'
        self.tests.write_text('''import os,time,unittest
STATE = []
class Cases(unittest.TestCase):
 def test_a(self):
  STATE.append('changed'); time.sleep(.05); self.assertEqual(STATE, ['changed'])
 def test_b(self):
  time.sleep(.05); self.assertEqual(STATE, [])
 def test_c(self):
  self.assertTrue(True)
''')
        self.summary = self.root / 'verification-results/python-specs.json'

    def command(self, *args):
        return [sys.executable, str(self.root / 'scripts/python_suites.py'), 'specs', *args]

    def environment(self):
        # This is an independent fake checkout, even when the containing suite
        # runs under Make or reconciliation in the source checkout.
        return {key: value for key, value in os.environ.items()
                if not key.startswith('CODE_FACTORY_VERIFY_')}

    def execute(self, *args, timeout=15):
        return self.supervised_probe(self.command(*args), cwd=self.root,
                                     env=self.environment(), timeout=timeout)

    def test_partition_preserves_every_test_once_and_rejects_unbounded_workers(self):
        names = [f'test_{index}' for index in range(17)]
        for workers in range(1, 5):
            shards = python_suites.partition(names, workers)
            self.assertEqual(sorted(test for shard in shards for test in shard), sorted(names))
            self.assertEqual(len(shards), workers)
            self.assertLessEqual(max(map(len, shards)) - min(map(len, shards)), 1)
        self.assertEqual(python_suites.partition(['one'], 4), [['one']])
        for workers in (0, -1, 5):
            with self.assertRaises(ValueError):
                python_suites.partition(names, workers)

    def test_workers_execute_complete_inventory_with_independent_module_globals(self):
        listed = self.execute('--list')
        self.assertEqual(listed.returncode, 0, listed.stderr)
        names = json.loads(listed.stdout)
        self.assertEqual(len(names), 3)
        self.assertFalse(self.summary.exists())
        result = self.execute('--workers', '2')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        data = json.loads(self.summary.read_text())
        self.assertTrue(data['success'])
        self.assertEqual((data['discovered'], data['completed'], data['workers']), (3, 3, 2))
        self.assertEqual(sorted(row['test'] for row in data['durations']), names)
        self.assertTrue(all(row['seconds'] >= 0 for row in data['durations']))
        # Serial execution observes the mutation, proving the parallel run did
        # not merely skip the second assertion or share its worker globals.
        serial = self.execute('--workers', '1')
        self.assertNotEqual(serial.returncode, 0)
        self.assertFalse(json.loads(self.summary.read_text())['success'])

    def test_failed_subtests_and_missing_worker_reports_cannot_supply_success(self):
        for body in ('with self.subTest(case=1): self.assertEqual(1, 2)', 'os._exit(0)'):
            with self.subTest(body=body):
                self.tests.write_text('import os,unittest\nclass Cases(unittest.TestCase):\n def test_a(self):\n  ' + body + '\n')
                result = self.execute()
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertFalse(json.loads(self.summary.read_text())['success'])

    def test_non_skipped_omissions_and_duplicate_execution_fail_real_workers(self):
        for body in ('return result', 'super().run(result);return super().run(result)'):
            with self.subTest(body=body):
                self.tests.write_text('import unittest\ndef skip_cleanup():raise unittest.SkipTest("cleanup skip")\nclass Cases(unittest.TestCase):\n'
                    ' def run(self,result=None):' + body + '\n'
                    ' def test_a(self):self.assertTrue(True)\n'
                    'class Skipped(unittest.TestCase):\n'
                    ' @classmethod\n def setUpClass(cls):\n  cls.addClassCleanup(skip_cleanup);raise unittest.SkipTest("known scope")\n'
                    ' def test_b(self):self.fail("skipped")\n')
                result = self.execute('--workers', '1')
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertFalse(json.loads(self.summary.read_text())['success'])

    def test_import_errors_and_empty_inventories_invalidate_previous_summary(self):
        self.summary.parent.mkdir()
        for source in ('raise RuntimeError("broken discovery")\n', 'import unittest\n'):
            with self.subTest(source=source):
                self.summary.write_text('{"success":true}')
                self.tests.write_text(source)
                result = self.execute()
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.summary.exists())

    def hanging_fixture(self):
        self.tests.write_text(HANGING_FIXTURE)

    def test_helper_timeout_drains_term_ignoring_descendants(self):
        self.hanging_fixture()
        # An unrelated process must survive the probe's exact-child drain.
        unrelated = subprocess.Popen([sys.executable, '-c', 'import time;time.sleep(60)'])
        try:
            with self.assertRaises(subprocess.TimeoutExpired):
                self.execute('--workers', '1', timeout=2)
            self.assertTrue(self.probe_drained)
            self.assertIsNone(unrelated.poll())
            self.assert_exited(json.loads((self.root / 'pids.json').read_text()))
        finally:
            unrelated.kill()
            unrelated.wait(timeout=5)

    def assert_exited(self, pids):
        for pid in pids:
            with self.subTest(pid=pid):
                try:
                    state = Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split()[0]
                except FileNotFoundError:
                    continue
                self.assertIn(state, ('Z', 'X'), 'ordinary descendant survived cleanup')

    def test_enclosing_timeout_and_cancellation_drain_same_group(self):
        for mode in ('timeout', 'interrupt', 'coordinator-death'):
            with self.subTest(mode=mode):
                self.hanging_fixture()
                (self.root / 'pids.json').unlink(missing_ok=True)
                (self.root / 'child-ready').unlink(missing_ok=True)
                result = self.supervised_probe([sys.executable, '-c', ENCLOSING_PROBE,
                    str(self.root), mode], cwd=self.root, env=self.environment(), timeout=12)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assert_exited(json.loads((self.root / 'pids.json').read_text()))

    def test_direct_coordinator_death_retains_supervisor_cleanup(self):
        self.hanging_fixture()
        code = r"""
import json,os,pathlib,select,signal,subprocess,sys,time,unittest
root=pathlib.Path.cwd();check=unittest.TestCase()
p=subprocess.Popen([sys.executable,'scripts/python_suites.py','specs','--workers','1'])
deadline=time.monotonic()+5
while not (root/'pids.json').exists():
 check.assertIsNone(p.poll());check.assertLess(time.monotonic(),deadline);time.sleep(.001)
descriptors=[os.pidfd_open(pid) for pid in json.loads((root/'pids.json').read_text())]
children=pathlib.Path(f'/proc/{p.pid}/task/{p.pid}/children').read_text().split()
check.assertEqual(len(children),1)
os.kill(int(children[0]),signal.SIGKILL)
check.assertNotEqual(p.wait(timeout=5),0)
pending=set(descriptors);deadline=time.monotonic()+2
while pending:
 remaining=deadline-time.monotonic();check.assertGreater(remaining,0)
 pending.difference_update(select.select(list(pending),[],[],remaining)[0])
for descriptor in descriptors:os.close(descriptor)
"""
        result = self.supervised_probe([sys.executable, '-c', code], cwd=self.root,
                                       env=self.environment(), timeout=12)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_internal_cli_modes_and_unadmitted_bootstraps_reject_before_discovery(self):
        self.tests.write_text('raise AssertionError("discovery must not run")\n')
        self.summary.parent.mkdir()
        self.summary.write_text('{"previous":true}')
        # This probe owns the fake checkout while contenders have no inherited
        # admission. Rejection must preserve both the owner and previous report.
        code = r"""
import json,os,pathlib,subprocess,sys
sys.path.insert(0,'scripts')
from specs_lib.isolation import managed
import python_suites
root=pathlib.Path.cwd()
env={k:v for k,v in os.environ.items() if not k.startswith('CODE_FACTORY_VERIFY_')}
base=[sys.executable,'scripts/python_suites.py','specs']
with managed(root):
 for args in (['--coordinator'],['--worker-result','forged.json','forged.test']):
  p=subprocess.run(base+args,env=env,capture_output=True,text=True,timeout=3)
  assert p.returncode==2 and 'unrecognized arguments' in p.stderr,p.stderr
 for bootstrap in (python_suites.COORDINATOR_BOOTSTRAP,python_suites.WORKER_BOOTSTRAP):
  p=subprocess.run(python_suites.child_command(bootstrap,{}),env=env,capture_output=True,text=True,timeout=3)
  assert p.returncode!=0 and 'already owns this checkout' in p.stderr,p.stderr
 p=subprocess.run(base,env=env,capture_output=True,text=True,timeout=3)
 assert p.returncode!=0 and 'already owns this checkout' in p.stderr,p.stderr
assert json.loads((root/'verification-results/python-specs.json').read_text())=={'previous':True}
assert not (root/'forged.json').exists()
"""
        result = self.supervised_probe([sys.executable, '-c', code], cwd=self.root,
                                       env=self.environment(), timeout=12)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        # Even without a competing owner, private entry needs group admission.
        result = self.supervised_probe(python_suites.child_command(
            python_suites.COORDINATOR_BOOTSTRAP, {}), cwd=self.root,
            env=self.environment(), timeout=5)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('requires an admitted verification group', result.stderr)
        self.assertEqual(json.loads(self.summary.read_text()), {'previous': True})

    def test_fixture_import_and_method_skips_preserve_unittest_success(self):
        fixtures = {
            'class': 'class Cases(unittest.TestCase):\n @classmethod\n def setUpClass(cls):raise unittest.SkipTest("class skip")\n def test_a(self):self.fail("skipped")\n def test_b(self):self.fail("skipped")\n',
            'module': 'def setUpModule():raise unittest.SkipTest("module skip")\nclass Cases(unittest.TestCase):\n def test_a(self):self.fail("skipped")\n def test_b(self):self.fail("skipped")\n',
            'class_cleanup': 'def cleanup():raise unittest.SkipTest("class cleanup skip")\nclass Cases(unittest.TestCase):\n @classmethod\n def setUpClass(cls):\n  cls.addClassCleanup(cleanup)\n  raise unittest.SkipTest("class setup skip")\n def test_a(self):self.fail("skipped")\n def test_b(self):self.fail("skipped")\n',
            'module_cleanup': 'def cleanup():raise unittest.SkipTest("module cleanup skip")\ndef setUpModule():\n unittest.addModuleCleanup(cleanup)\n raise unittest.SkipTest("module setup skip")\nclass Cases(unittest.TestCase):\n def test_a(self):self.fail("skipped")\n def test_b(self):self.fail("skipped")\n',
            'import': 'raise unittest.SkipTest("import skip")\n',
            'method': 'class Cases(unittest.TestCase):\n @unittest.skip("method skip")\n def test_a(self):self.fail("skipped")\n @unittest.expectedFailure\n def test_b(self):self.fail("expected")\n',
        }
        for mode, source in fixtures.items():
            for workers in (1, 2):
                with self.subTest(mode=mode, workers=workers):
                    self.tests.write_text('import unittest\n' + source)
                    serial = self.supervised_probe([sys.executable, '-m', 'unittest', 'discover',
                        '-s', 'scripts', '-p', 'test_specs*.py'], cwd=self.root,
                        env=self.environment(), timeout=5)
                    self.assertEqual(serial.returncode, 0, serial.stderr)
                    result = self.execute('--workers', str(workers))
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    data = json.loads(self.summary.read_text())
                    self.assertTrue(data['success'])
                    self.assertEqual(data['completed'], data['discovered'])
                    self.assertEqual(len(data['fixture_skipped']),
                                     data['discovered'] if mode in ('class', 'module', 'class_cleanup', 'module_cleanup') else 0)
                    if mode in ('class', 'module', 'class_cleanup', 'module_cleanup'):
                        events_per_worker = 2 if mode.endswith('_cleanup') else 1
                        self.assertEqual(data['counts']['skipped'], events_per_worker * workers)
                    if mode == 'method':
                        self.assertEqual(data['counts']['expected_failures'], 1)

    def test_completeness_rejects_omitted_duplicate_and_unrelated_skip_ids(self):
        names = ['a', 'b']
        valid = {'started': ['a'], 'completed': ['a'],
                 'fixture_skipped': [{'test': 'b'}]}
        self.assertTrue(python_suites.complete_report(valid, names))
        for report in (
            {'started': ['a'], 'completed': ['a'], 'fixture_skipped': []},
            {'started': names, 'completed': names + ['b'], 'fixture_skipped': []},
            {'started': ['a'], 'completed': ['a'], 'fixture_skipped': [{'test': 'c'}]},
            {'started': names, 'completed': names, 'fixture_skipped': [{'test': 'b'}]},
            {'started': names, 'completed': ['a'], 'fixture_skipped': [{'test': 'b'}]},
        ):
            with self.subTest(report=report):
                self.assertFalse(python_suites.complete_report(report, names))


HANGING_FIXTURE = r"""
import json,os,pathlib,signal,subprocess,sys,time,unittest
class Cases(unittest.TestCase):
 def test_wait(self):
  signal.signal(signal.SIGTERM,signal.SIG_IGN)
  child=subprocess.Popen([sys.executable,'-c','import pathlib,signal,time;signal.signal(signal.SIGTERM,signal.SIG_IGN);pathlib.Path("child-ready").touch();time.sleep(60)'])
  deadline=time.monotonic()+5
  while not pathlib.Path('child-ready').exists():
   if time.monotonic()>deadline:raise RuntimeError('child not ready')
   time.sleep(.001)
  pathlib.Path('pids.partial').write_text(json.dumps([os.getpid(),child.pid]))
  pathlib.Path('pids.partial').replace('pids.json')
  time.sleep(60)
"""

ENCLOSING_PROBE = r"""
import ctypes,json,os,pathlib,select,signal,subprocess,sys,time,unittest
from unittest.mock import patch
sys.path.insert(0,'scripts')
from specs_lib import processes
from specs_lib.isolation import managed
check=unittest.TestCase();root=pathlib.Path(sys.argv[1]);mode=sys.argv[2]
# Observe runner cleanup before the outer safety supervisor drains leftovers.
assert ctypes.CDLL(None,use_errno=True).prctl(36,1,0,0,0)==0
real_killpg=os.killpg;real_waitid=os.waitid;injected=[];triggered=[];descriptors=[]
def killpg(group,number):
 if number==signal.SIGTERM and (root/'pids.json').exists():
  for pid in json.loads((root/'pids.json').read_text()):
   check.assertEqual(os.getpgid(pid),group,'worker escaped the enclosing group')
   descriptors.append(os.pidfd_open(pid))
  os.kill(group,signal.SIGSTOP);injected.append(group)
 return real_killpg(group,number)
def waitid(*args,**kwargs):
 if mode!='timeout' and not triggered and (root/'pids.json').exists():
  triggered.append(True)
  if mode=='interrupt':os.kill(os.getpid(),signal.SIGINT)
  else:os.kill(args[1],signal.SIGKILL)
 return real_waitid(*args,**kwargs)
with managed(root),patch.object(processes.os,'killpg',side_effect=killpg),patch.object(processes.os,'waitid',side_effect=waitid):
 try:
  result=processes.run_verification([sys.executable,'scripts/python_suites.py','specs','--workers','1'],cwd=root,timeout=2)
  check.assertEqual(mode,'coordinator-death');check.assertNotEqual(result.returncode,0)
 except subprocess.TimeoutExpired:check.assertEqual(mode,'timeout')
 except KeyboardInterrupt:check.assertEqual(mode,'interrupt')
 check.assertTrue(injected)
 pending=set(descriptors);deadline=time.monotonic()+2
 while pending:
  remaining=deadline-time.monotonic()
  check.assertGreater(remaining,0,'ordinary TERM-ignoring descendant survived enclosing cleanup')
  pending.difference_update(select.select(list(pending),[],[],remaining)[0])
 for descriptor in descriptors:os.close(descriptor)
"""


if __name__ == '__main__':
    unittest.main()
