import copy
from contextlib import redirect_stdout
import io
import json
import unittest
from pathlib import Path
import subprocess
import sys
import tempfile
from unittest.mock import patch

import benchmark_browser as benchmark

from benchmark_browser import discover, inventory, validate_run


class BrowserBenchmarkTests(unittest.TestCase):
    def report(self):
        return {"suites": [{"specs": [{"title": "scenario", "tests": [{
            "expectedStatus": "passed", "status": "expected",
            "results": [{"status": "passed", "retry": 0}],
        }]}]}], "errors": []}

    def test_file_move_preserves_inventory_and_duplicates_count(self):
        report = self.report()
        expected = inventory(report)
        moved = {"suites": [{"suites": report["suites"]}], "errors": []}
        validate_run(moved, expected)
        moved["suites"].append(copy.deepcopy(report["suites"][0]))
        with self.assertRaises(ValueError):
            validate_run(moved, expected)

    def test_failure_skip_retry_and_missing_test_cannot_pass(self):
        for status in ("failed", "skipped", "timedOut", "interrupted"):
            report = self.report()
            expected = inventory(report)
            report["suites"][0]["specs"][0]["tests"][0]["results"][0]["status"] = status
            with self.assertRaises(ValueError):
                validate_run(report, expected)
        for change in ("retry", "missing", "error"):
            report = self.report()
            expected = inventory(report)
            test = report["suites"][0]["specs"][0]["tests"][0]
            if change == "retry":
                test["results"][0]["retry"] = 1
            elif change == "missing":
                report["suites"] = []
            else:
                report["errors"] = [{"message": "discovery failed"}]
            with self.assertRaises(ValueError):
                validate_run(report, expected)


    def test_report_requires_one_project_and_result_and_expected_status(self):
        for change in ("zero_projects", "multiple_projects", "zero_results", "multiple_results",
                       "expected_failure", "unexpected_status"):
            with self.subTest(change=change):
                report = self.report()
                expected = inventory(report)
                spec = report["suites"][0]["specs"][0]
                test = spec["tests"][0]
                if change == "zero_projects":
                    spec["tests"] = []
                elif change == "multiple_projects":
                    spec["tests"].append(copy.deepcopy(test))
                elif change == "zero_results":
                    test["results"] = []
                elif change == "multiple_results":
                    test["results"].append(copy.deepcopy(test["results"][0]))
                elif change == "expected_failure":
                    test["expectedStatus"] = "failed"
                else:
                    test["status"] = "unexpected"
                self.assertEqual(inventory(report), expected, "execution cardinality must not change titles")
                with self.assertRaises(ValueError):
                    validate_run(report, expected)

    def test_report_rejects_extra_distinct_title(self):
        report = self.report()
        expected = inventory(report)
        extra = copy.deepcopy(report["suites"][0]["specs"][0])
        extra["title"] = "unexpected scenario"
        report["suites"][0]["specs"].append(extra)
        with self.assertRaises(ValueError):
            validate_run(report, expected)


    def test_nonzero_discovery_retains_both_diagnostics(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            with self.assertRaisesRegex(RuntimeError, "discovery exited 3") as raised:
                discover([sys.executable, "-c",
                          "import sys; print('partial report'); print('missing import', file=sys.stderr); sys.exit(3)"],
                         output)
            self.assertEqual((output / "discovery.json").read_text(), "partial report\n")
            self.assertEqual((output / "discovery.stderr.log").read_text(), "missing import\n")
            self.assertIn(str(output / "discovery.json"), str(raised.exception))
            self.assertIn(str(output / "discovery.stderr.log"), str(raised.exception))


    def run_main(self, output, *, metrics="1.25 512", report=None, exit_code=0,
                 discovery=None, discovery_exit=0, baseline=None):
        calls = []
        valid = self.report()
        discovery = valid if discovery is None else discovery
        argv = ["benchmark", "--output", str(output)]
        if baseline is not None:
            baseline_path = output.parent / "baseline.json"
            baseline_path.write_text(baseline if isinstance(baseline, str) else json.dumps(baseline))
            argv.extend(["--baseline-inventory", str(baseline_path)])

        def run(command, *, stdout, stderr, cwd, env):
            self.assertEqual(env["CODE_FACTORY_REAL_CODEX"], "0")
            self.assertEqual(command.count("--retries=0"), 1)
            if "--list" in command:
                stdout.write(discovery if isinstance(discovery, str) else json.dumps(discovery))
                stderr.write("discovery diagnostic")
                return subprocess.CompletedProcess(command, discovery_exit)
            else:
                calls.append(int(next(arg.split("=")[1] for arg in command if arg.startswith("--workers="))))
                sample = len(calls)
                raw_report = report if sample == 2 and report is not None else valid
                stdout.write(raw_report if isinstance(raw_report, str) else json.dumps(raw_report))
                stderr.write(f"sample {sample} diagnostic")
                Path(command[command.index("-o") + 1]).write_text(metrics if sample == 2 else "1.25 512")
                if sample == 2:
                    return subprocess.CompletedProcess(command, exit_code)
            return subprocess.CompletedProcess(command, 0)

        def identity(command, **kwargs):
            if command[:2] == ["git", "diff"]:
                return b""
            return "fixture-version\n"

        failure = None
        with patch.object(sys, "argv", argv), \
                patch.object(benchmark.subprocess, "run", side_effect=run), \
                patch.object(benchmark.subprocess, "check_output", side_effect=identity), \
                redirect_stdout(io.StringIO()):
            try:
                benchmark.main()
            except (ValueError, RuntimeError) as error:
                failure = error
        return calls, failure

    def test_main_runs_complete_worker_order_and_accepts_zero_metrics(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "evidence"
            calls, failure = self.run_main(output, metrics="0 0")
            self.assertIsNone(failure)
            self.assertEqual(calls, [1, 2, 2, 1])
            summary = json.loads((output / "summary.json").read_text())
            self.assertEqual([run["outcome"] for run in summary["runs"]], ["passed"] * 4)
            self.assertEqual(summary["runs"][1]["seconds"], 0)
            self.assertEqual(summary["runs"][1]["max_process_rss_kib"], 0)
            self.assertEqual(json.loads((output / "inventory.json").read_text()), ["scenario"])
            for sample, workers in enumerate(calls, 1):
                run_dir = output / f"run-{sample}-workers-{workers}"
                self.assertEqual(json.loads((run_dir / "report.json").read_text()), self.report())
                self.assertEqual((run_dir / "stderr.log").read_text(), f"sample {sample} diagnostic")

    def test_main_stops_on_execution_report_or_metric_failure_and_retains_evidence(self):
        bad_report = self.report()
        bad_report["suites"][0]["specs"][0]["tests"][0]["results"][0]["status"] = "skipped"
        cases = [{"exit_code": 19}, {"report": bad_report}, {"report": "invalid JSON"},
                 {"report": {"suites": [], "errors": []}}]
        cases += [{"metrics": value} for value in
                  ("", "1", "1 2 3", "bad 100", "1 bad", "nan 100", "inf 100",
                   "-inf 100", "-1 100", "1 -100")]
        for options in cases:
            with self.subTest(options=options), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / "evidence"
                calls, failure = self.run_main(output, **options)
                self.assertIsInstance(failure, (ValueError, RuntimeError))
                self.assertEqual(calls, [1, 2], "failed sample must stop later invocations")
                summary = json.loads((output / "summary.json").read_text(),
                                     parse_constant=lambda value: self.fail(f"Nonstandard JSON number: {value}"))
                self.assertEqual([run["outcome"] for run in summary["runs"]], ["passed", "failed"])
                self.assertEqual(summary["runs"][1]["exit_code"], options.get("exit_code", 0))
                self.assertNotIn("seconds", summary["runs"][1])
                self.assertNotIn("max_process_rss_kib", summary["runs"][1])
                self.assertEqual(sorted(path.name for path in output.glob("run-*")),
                                 ["run-1-workers-1", "run-2-workers-2"])
                failed = output / "run-2-workers-2"
                expected_report = options.get("report", self.report())
                self.assertEqual((failed / "report.json").read_text(),
                                 expected_report if isinstance(expected_report, str) else json.dumps(expected_report))
                self.assertEqual((failed / "stderr.log").read_text(), "sample 2 diagnostic")
                self.assertEqual((failed / "time.txt").read_text(), options.get("metrics", "1.25 512"))
                self.assertEqual((output / "discovery.stderr.log").read_text(), "discovery diagnostic")
                self.assertEqual(json.loads((output / "discovery.json").read_text()), self.report())


    def test_main_accepts_matching_baseline_inventory(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "evidence"
            # File/suite nesting may differ without changing the title multiset.
            baseline = {"suites": [{"suites": self.report()["suites"]}], "errors": []}
            calls, failure = self.run_main(output, baseline=baseline)
            self.assertIsNone(failure)
            self.assertEqual(calls, [1, 2, 2, 1])
            self.assertTrue(all(run["outcome"] == "passed"
                                for run in json.loads((output / "summary.json").read_text())["runs"]))

    def test_main_rejects_discovery_and_baseline_before_any_sample(self):
        errors = self.report()
        errors["errors"] = [{"message": "discovery error"}]
        mismatch = self.report()
        mismatch["suites"][0]["specs"][0]["title"] = "different scenario"
        duplicate = self.report()
        duplicate["suites"].append(copy.deepcopy(duplicate["suites"][0]))
        empty = {"suites": [], "errors": []}
        cases = [{"discovery": empty}, {"discovery": errors},
                 {"discovery": "malformed discovery"}, {"discovery_exit": 3},
                 {"baseline": mismatch}, {"baseline": duplicate},
                 {"baseline": empty}, {"baseline": errors}, {"baseline": "malformed baseline"}]
        for options in cases:
            with self.subTest(options=options), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / "evidence"
                calls, failure = self.run_main(output, **options)
                self.assertIsInstance(failure, (ValueError, RuntimeError))
                self.assertEqual(calls, [], "invalid inventory must prevent every sample")
                self.assertEqual(sorted(path.name for path in output.iterdir()),
                                 ["discovery.json", "discovery.stderr.log"])
                discovery = options.get("discovery", self.report())
                self.assertEqual((output / "discovery.json").read_text(),
                                 discovery if isinstance(discovery, str) else json.dumps(discovery))
                self.assertEqual((output / "discovery.stderr.log").read_text(), "discovery diagnostic")


if __name__ == "__main__":
    unittest.main()
