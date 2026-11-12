#!/usr/bin/env python3
"""Run complete Python verification inventories in bounded, isolated processes."""
import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]


def flatten(suite):
    for case in suite:
        if isinstance(case, unittest.TestSuite):
            yield from flatten(case)
        else:
            yield case


def discovered_cases(name):
    loader = unittest.TestLoader()
    suite = (loader.discover(str(ROOT / 'scripts'), pattern='test_specs*.py')
             if name == 'specs' else loader.loadTestsFromName('test_ci'))
    if loader.errors:
        raise ValueError('\n'.join(loader.errors))
    cases = sorted(flatten(suite), key=lambda case: case.id())
    names = [case.id() for case in cases]
    if not names or len(names) != len(set(names)):
        raise ValueError('Verification inventory must be nonempty and contain unique test IDs')
    return cases


def inventory(name):
    return [case.id() for case in discovered_cases(name)]


def inventory_digest(names):
    return hashlib.sha256("\n".join(names).encode()).hexdigest()


def partition(names, workers):
    if not 1 <= workers <= 4:
        raise ValueError('Worker count must be between 1 and 4')
    return [names[index::workers] for index in range(min(workers, len(names)))]


class RecordedResult(unittest.TextTestResult):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.started, self.completed, self.durations = [], [], []
        self.start_times = {}
        self.assigned_cases = []
        self.fixture_skipped = []
        self.fixture_skipped_ids = set()

    def startTest(self, test):
        self.started.append(test.id())
        self.start_times[test.id()] = time.monotonic()
        super().startTest(test)

    def stopTest(self, test):
        self.completed.append(test.id())
        self.durations.append({'test': test.id(), 'seconds': time.monotonic() - self.start_times[test.id()]})
        super().stopTest(test)

    def addSkip(self, test, reason):
        super().addSkip(test, reason)
        if isinstance(test, unittest.TestCase):
            return
        # Fixture skips use _ErrorHolder without per-case startTest/stopTest.
        # Account for that exact scope explicitly; unrelated omissions fail.
        scope = test.id()
        for case in self.assigned_cases:
            cls = type(case)
            if (scope in (f'setUpClass ({cls.__module__}.{cls.__qualname__})',
                          f'setUpModule ({cls.__module__})')
                    and case.id() not in self.fixture_skipped_ids):
                # Setup and its cleanup can each emit a legitimate skip event.
                # Keep both ordinary events above, but account for each case once.
                self.fixture_skipped_ids.add(case.id())
                self.fixture_skipped.append({'test': case.id(), 'scope': scope, 'reason': reason})


def worker(name, names, digest, destination):
    cases = discovered_cases(name)
    if inventory_digest([case.id() for case in cases]) != digest:
        raise ValueError('Worker discovery differs from the complete inventory')
    selected = {case.id(): case for case in cases}
    suite = unittest.TestSuite(selected[test] for test in names)

    def result_factory(*args, **kwargs):
        result = RecordedResult(*args, **kwargs)
        result.assigned_cases = list(suite)
        return result

    result = unittest.TextTestRunner(resultclass=result_factory).run(suite)
    data = {'started': result.started, 'completed': result.completed,
            'fixture_skipped': result.fixture_skipped,
            'durations': result.durations, 'successful': result.wasSuccessful(),
            'failures': len(result.failures), 'errors': len(result.errors),
            'skipped': len(result.skipped), 'expected_failures': len(result.expectedFailures),
            'unexpected_successes': len(result.unexpectedSuccesses)}
    destination.write_text(json.dumps(data) + '\n')
    return 0 if result.wasSuccessful() else 1


def complete_report(report, assigned):
    skipped = [row['test'] for row in report['fixture_skipped']]
    return (Counter(report['started']) == Counter(report['completed']) and
            Counter(report['completed']) + Counter(skipped) == Counter(assigned))


COORDINATOR_BOOTSTRAP = ("import sys; sys.path.insert(0, 'scripts'); import python_suites; "
                         "raise SystemExit(python_suites.coordinator_entry())")
WORKER_BOOTSTRAP = ("import sys; sys.path.insert(0, 'scripts'); import python_suites; "
                    "raise SystemExit(python_suites.worker_entry())")


def child_command(bootstrap, payload):
    # Fixed source-controlled entry code; all variable input is argv data.
    return [sys.executable, '-c', bootstrap, json.dumps(payload)]


def admitted_entry(function):
    from specs_lib.isolation import managed, enclosing_managed_group
    with managed(ROOT):
        if not enclosing_managed_group():
            raise RuntimeError('Python suite child requires an admitted verification group')
        return function(json.loads(sys.argv[1]))


def worker_entry():
    return admitted_entry(lambda data: worker(data['suite'], data['tests'],
                          data['inventory_sha256'], Path(data['destination'])))


def coordinate(name, workers):
    summary = ROOT / 'verification-results' / f'python-{name}.json'
    summary.unlink(missing_ok=True)
    return run(name, inventory(name), workers, summary=summary)


def coordinator_entry():
    return admitted_entry(lambda data: coordinate(data['suite'], data['workers']))


def run(name, names, workers, *, summary):
    started = time.monotonic()
    assignments = partition(names, workers)
    processes, reports = [], []
    interrupted = None
    handlers = {}

    def interrupt(number, frame):
        nonlocal interrupted
        interrupted = interrupted or number

    summary.unlink(missing_ok=True)
    try:
        for number in (signal.SIGINT, signal.SIGTERM):
            handlers[number] = signal.signal(number, interrupt)
        with tempfile.TemporaryDirectory(prefix='python-suite-') as directory:
            directory = Path(directory)
            try:
                for index, assigned in enumerate(assignments):
                    if interrupted:
                        break
                    result = directory / f'{index}.json'
                    log = directory / f'{index}.log'
                    with log.open('wb') as output:
                        # Workers retain the enclosing verifier's process group.
                        # Its existing cancellation cleanup therefore includes
                        # every worker and ordinary descendant.
                        process = subprocess.Popen(child_command(WORKER_BOOTSTRAP, {
                            'suite': name, 'tests': assigned, 'destination': str(result),
                            'inventory_sha256': inventory_digest(names)}), cwd=ROOT,
                            stdout=output, stderr=subprocess.STDOUT)
                    processes.append((process, assigned, result, log))
                while any(process.poll() is None for process, *_ in processes):
                    if interrupted:
                        break
                    time.sleep(.02)
            finally:
                if interrupted or sys.exc_info()[0] is not None:
                    for process, *_ in processes:
                        if process.poll() is None:
                            process.terminate()
                    deadline = time.monotonic() + 5
                    while any(process.poll() is None for process, *_ in processes) and time.monotonic() < deadline:
                        time.sleep(.02)
                    for process, *_ in processes:
                        if process.poll() is None:
                            process.kill()
                for process, *_ in processes:
                    process.wait()
            valid = len(processes) == len(assignments) and not interrupted
            for index, (process, assigned, result, log) in enumerate(processes):
                print(f'Python {name} worker {index + 1}/{len(assignments)}: {len(assigned)} tests', flush=True)
                sys.stdout.write(log.read_text(errors='replace'))
                try:
                    report = json.loads(result.read_text())
                    complete = complete_report(report, assigned)
                    valid = valid and complete and process.returncode == 0 and report['successful'] is True
                    reports.append(report)
                except (OSError, ValueError, KeyError, TypeError):
                    valid = False
            executed = [test for report in reports for test in report['completed']]
            fixture_skipped = [row for report in reports for row in report['fixture_skipped']]
            accounted = executed + [row['test'] for row in fixture_skipped]
            valid = valid and Counter(accounted) == Counter(names) and not interrupted
            data = {'version': 1, 'suite': name, 'workers': len(assignments),
                    'inventory_sha256': inventory_digest(names),
                    'discovered': len(names), 'completed': len(accounted),
                    'individually_completed': len(executed), 'fixture_skipped': fixture_skipped,
                    'success': valid, 'interrupted': interrupted,
                    'seconds': time.monotonic() - started,
                    'counts': {key: sum(report[key] for report in reports) for key in
                               ('failures', 'errors', 'skipped', 'expected_failures', 'unexpected_successes')},
                    'durations': sorted([row for report in reports for row in report['durations']],
                                        key=lambda row: row['seconds'], reverse=True)}
            summary.parent.mkdir(parents=True, exist_ok=True)
            with tempfile.NamedTemporaryFile(mode='w', dir=summary.parent, delete=False) as output:
                temporary = Path(output.name)
                try:
                    json.dump(data, output, indent=2)
                    output.write('\n')
                    output.close()
                    temporary.replace(summary)
                finally:
                    temporary.unlink(missing_ok=True)
            print(f'Python {name}: {len(accounted)}/{len(names)} tests accounted in {data["seconds"]:.3f}s', flush=True)
            return 128 + interrupted if interrupted else 0 if valid else 1
    finally:
        for number, handler in handlers.items():
            signal.signal(number, handler)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('suite', choices=('ci', 'specs'))
    parser.add_argument('--workers', type=int, default=2, choices=range(1, 5))
    parser.add_argument('--list', action='store_true')
    args = parser.parse_args()
    os.chdir(ROOT)
    if args.list:
        print(json.dumps(inventory(args.suite)))
        return 0
    from specs_lib.processes import run_verification
    from specs_lib.isolation import managed, enclosing_managed_group
    with managed(ROOT):
        # Admitted Make/reconciliation helpers keep the existing supervised
        # group through its final KILL. Direct CLI use creates exactly one.
        if enclosing_managed_group():
            return coordinate(args.suite, args.workers)
        result = run_verification(child_command(COORDINATOR_BOOTSTRAP, {
            'suite': args.suite, 'workers': args.workers}),
            cwd=ROOT, timeout=float('inf'))
    return result.returncode if result.returncode >= 0 else 128 - result.returncode


if __name__ == '__main__':
    sys.exit(main())
