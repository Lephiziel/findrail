import importlib.util
import json
import math
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("resource_series", Path(__file__).with_name("resource_series.py"))
series = importlib.util.module_from_spec(spec)
spec.loader.exec_module(series)


class SeriesTests(unittest.TestCase):
    def test_nearest_rank(self):
        self.assertEqual(series.nearest([9, 1, 3, 2], .95), 9)
        self.assertEqual(series.summary([1, 2, 3, 4]), {"p50": 2.5, "p95": 4})

    def test_binary_order_and_unverified_provenance(self):
        roles = ["candidate", "baseline"]
        self.assertEqual([series.repeat_order(roles, i) for i in range(3)],
                         [["candidate", "baseline"], ["baseline", "candidate"], ["candidate", "baseline"]])
        self.assertEqual(series.qualification(True), "reference_only")
        self.assertEqual(series.qualification(True, True), "same_environment")
        self.assertEqual(series.qualification(False), "unavailable")

    def test_json_rejects_duplicate_nonfinite_and_malformed(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / "x.json"
            for text in ('{"x":1,"x":2}', '{"x":NaN}', '{'):
                path.write_text(text)
                with self.assertRaises((ValueError, json.JSONDecodeError)):
                    series.clean_json(path)

    def test_child_error_is_fixed_and_cleanup(self):
        import sys
        with self.assertRaisesRegex(RuntimeError, "child_failed_7"):
            series.run_child([sys.executable, "-c", "raise SystemExit(7)"], __import__("time").monotonic() + 5)

    def test_timeout_cleans_process_group(self):
        import sys, time
        with self.assertRaises(TimeoutError):
            series.run_child([sys.executable, "-c", "import time; time.sleep(30)"], time.monotonic() + .05)

    def test_contract_limit_validation(self):
        with self.assertRaises(SystemExit):
            series.main(["--binary", __file__, "--output", "/tmp/no-write", "--samples-per-family", "101"])

    def test_output_and_child_caps(self):
        self.assertEqual(series.MAX_OUTPUT, 16 << 20)
        self.assertEqual(series.MAX_CHILDREN, 6)
        self.assertEqual(series.MAX_STDERR, 4096)


if __name__ == "__main__":
    unittest.main()
