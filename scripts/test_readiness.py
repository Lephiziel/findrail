import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
import readiness


class ReadinessTests(unittest.TestCase):
    def setUp(self):
        self.sha = "a" * 40
        self.candidate = {"candidate_sha": self.sha, "candidate_version": "0.1.0-alpha.5",
                          "status": "passed", "source_clean": True}

    def report(self, kind, **overrides):
        item = {"kind": kind, "candidate_sha": self.sha, "candidate_version": "0.1.0-alpha.5",
                "status": "passed", "elapsed_seconds": 2.0, "binary_sha256": "b" * 64,
                "archive_sha256": "c" * 64, "fixture_sha256": "d" * 64}
        item.update(overrides)
        return item

    def test_no_synthetic_summary_can_set_final_release_or_mask_missing_evidence(self):
        result = readiness.classify(self.candidate)
        self.assertEqual(result["software_status"], "pending")
        self.assertEqual(result["release_readiness"], "pending")
        self.assertFalse(result["final_release"])
        self.assertTrue(any("scale_100k" in gap for gap in result["gaps"]))

    def test_mismatched_sha_duplicate_and_failed_records_block(self):
        mismatch = self.report("crash_recovery", candidate_sha="e" * 40)
        failed = self.report("freshness_browser", status="failed")
        result = readiness.classify(self.candidate, [mismatch, failed, failed])
        self.assertEqual(result["release_readiness"], "blocked")

    def test_missing_duplicate_skipped_and_invalid_durations_do_not_pass(self):
        item = self.report("freshness_browser", elapsed_seconds=float("nan"))
        result = readiness.classify(self.candidate, [item, item])
        self.assertEqual(result["software_status"], "pending")
        self.assertTrue(any("duplicate" in gap for gap in result["gaps"]))

    def test_incomplete_case_coverage_fails(self):
        item = self.report("crash_recovery", required_cases=["recovery"],
                           cases=[{"case": "recovery", "status": "skipped", "elapsed_seconds": 1}])
        result = readiness.classify(self.candidate, [item])
        self.assertTrue(any("interrupted" in gap for gap in result["gaps"]))

    def test_native_target_set_is_exact(self):
        item = self.report("installer_native", targets=list(readiness.NATIVE_TARGETS[:-1]))
        result = readiness.classify(self.candidate, [item])
        self.assertTrue(any("five supported" in gap for gap in result["gaps"]))

    def test_other_baseline_is_allowed_only_as_reference(self):
        item = self.report("scale_10k", baseline={"sha256": "e" * 64, "role": "reference"})
        result = readiness.classify(self.candidate, [item])
        self.assertFalse(any("baseline" in gap for gap in result["gaps"]))
        item["baseline"]["role"] = "candidate"
        self.assertTrue(any("reference" in gap for gap in readiness.classify(self.candidate, [item])["gaps"]))


if __name__ == "__main__":
    unittest.main()
