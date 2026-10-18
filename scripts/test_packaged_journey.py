import json
import pathlib
import sys
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import packaged_journey as journey


class CandidateIdentityTests(unittest.TestCase):
    def test_identity_requires_clean_native_exact_version(self):
        sha, version = "a" * 40, "0.1.0-alpha.5"
        good = {"revision": sha, "version": version, "dirty": False, "target": "linux-amd64", "schema_version": 8}
        cases = [({}, f"findrail {version}", True),
                 ({"dirty": True}, f"findrail {version}", False),
                 ({"dirty": None}, f"findrail {version}", False),
                 ({"target": "linux-arm64"}, f"findrail {version}", False),
                 ({"schema_version": 7}, f"findrail {version}", False),
                 ({"schema_version": None}, f"findrail {version}", False),
                 ({"schema_version": True}, f"findrail {version}", False),
                 ({"revision": "b" * 40}, f"findrail {version}", False),
                 ({"version": "0.1.0-alpha.30"}, f"findrail {version}", False),
                 ({}, "findrail 0.1.0-alpha.30", False)]
        with tempfile.TemporaryDirectory() as temp:
            binary = pathlib.Path(temp) / "findrail"
            for changes, output, accepted in cases:
                with self.subTest(changes=changes, output=output):
                    (binary.parent / "BUILD-INFO.json").write_text(json.dumps(good | changes))
                    with patch.object(journey.platform, "system", return_value="Linux"), \
                         patch.object(journey.platform, "machine", return_value="x86_64"), \
                         patch.object(journey, "command", return_value=SimpleNamespace(stdout=output)):
                        if accepted:
                            self.assertEqual(journey.validate_candidate(binary, sha, version), good)
                        else:
                            with self.assertRaises(ValueError): journey.validate_candidate(binary, sha, version)


class CaseRecordTests(unittest.TestCase):
    def test_failed_case_keeps_prior_results_and_omits_exception_payload(self):
        with tempfile.TemporaryDirectory() as temp:
            path = pathlib.Path(temp) / "report.json"
            report = {"status": "running", "cases": []}
            recorder = journey.CaseRecorder(report, path)
            recorder.run("first", lambda: {"count": 3})
            def failure():
                self.assertEqual(json.loads(path.read_text())["cases"][-1]["status"], "running")
                raise RuntimeError("private index /secret/path and indexed text")
            with self.assertRaises(RuntimeError): recorder.run("second", failure)
            saved = json.loads(path.read_text())
            self.assertEqual(saved["status"], "failed")
            self.assertEqual([case["status"] for case in saved["cases"]], ["passed", "failed"])
            self.assertEqual(saved["cases"][0]["details"], {"count": 3})
            self.assertEqual(saved["cases"][1]["error_type"], "RuntimeError")
            self.assertNotIn("secret", path.read_text())
            self.assertFalse(path.with_suffix(".json.tmp").exists())

    def test_interruption_is_recorded_and_propagated(self):
        report = {"status": "running", "cases": []}
        recorder = journey.CaseRecorder(report, None)
        def interrupted(): raise KeyboardInterrupt()
        with self.assertRaises(KeyboardInterrupt): recorder.run("cancel", interrupted)
        self.assertEqual(report["status"], "failed")
        self.assertEqual(report["cases"][0]["error_type"], "KeyboardInterrupt")


if __name__ == "__main__":
    unittest.main()
