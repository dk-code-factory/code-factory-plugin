import copy
import json
import unittest
from pathlib import Path
import subprocess
import sys
import tempfile

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


if __name__ == "__main__":
    unittest.main()
