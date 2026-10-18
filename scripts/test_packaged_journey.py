import contextlib
import io
import json
import os
import subprocess
import time
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
        sha, version = "a" * 40, "0.1.0-alpha.3"
        good = {"revision": sha, "version": version, "dirty": False, "target": "linux-amd64", "schema_version": journey.package.schema_version()}
        cases = [({}, f"findrail {version}", True),
                 ({"dirty": True}, f"findrail {version}", False),
                 ({"dirty": None}, f"findrail {version}", False),
                 ({"schema_version": 7}, f"findrail {version}", False),
                 ({"schema_version": None}, f"findrail {version}", False),
                 ({"target": "linux-arm64"}, f"findrail {version}", False),
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


class ReportTests(unittest.TestCase):
    def args(self, root, skip=False):
        binary = root / "findrail"
        binary.write_text("placeholder")
        binary.chmod(0o755)
        return SimpleNamespace(binary=binary, candidate_sha="a" * 40,
                               candidate_version="0.1.0-alpha.5", alpha3_binary=binary,
                               alpha4_binary=binary, report=root / "report.json", skip_browser=skip)

    def execute(self, args, operation=None):
        with patch.object(journey, "validate_candidate", return_value={"schema_version": 8, "target": "linux-amd64"}), \
             patch.object(journey, "evaluate", return_value={"comparisons": 10}), \
             patch.object(journey, "command", side_effect=operation), \
             contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            status = journey.execute(args)
        return status, json.loads(args.report.read_text())

    def test_failed_identity_writes_report_and_runs_no_smokes(self):
        with tempfile.TemporaryDirectory() as temp:
            args = self.args(pathlib.Path(temp))
            with patch.object(journey, "validate_candidate", side_effect=ValueError("wrong schema")), \
                 patch.object(journey, "command") as command, \
                 contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
                self.assertEqual(journey.execute(args), 1)
            command.assert_not_called()
            report = json.loads(args.report.read_text())
            self.assertEqual(report["status"], "failed")
            self.assertEqual([(c["case"], c["status"]) for c in report["cases"]], [("candidate_identity", "failed")])

    def test_skip_is_incomplete_and_each_browser_case_is_recorded(self):
        with tempfile.TemporaryDirectory() as temp:
            status, report = self.execute(self.args(pathlib.Path(temp), skip=True))
            self.assertEqual(status, 0)
            self.assertEqual(report["status"], "incomplete")
            skipped = [c for c in report["cases"] if c["status"] == "skipped"]
            self.assertEqual([c["case"] for c in skipped], list(journey.BROWSER_SMOKES))
            self.assertTrue(all(c["reason"] == "--skip-browser" for c in skipped))
            self.assertTrue(all("duration_seconds" in c for c in report["cases"]))

    def test_failed_smoke_stops_and_preserves_completed_cases(self):
        def fail_mcp(argv, **kwargs):
            if any(str(arg).endswith("mcp_smoke.py") for arg in argv):
                raise subprocess.TimeoutExpired(argv, 300)
        with tempfile.TemporaryDirectory() as temp:
            status, report = self.execute(self.args(pathlib.Path(temp)), fail_mcp)
            self.assertEqual(status, 1)
            self.assertEqual(report["status"], "failed")
            last = report["cases"][-1]
            self.assertEqual((last["case"], last["status"], last["timeout_seconds"]), ("mcp_smoke.py", "failed", 300))
            self.assertTrue(all(c["status"] == "passed" for c in report["cases"][:-1]))
            self.assertFalse(any(c["case"] in journey.BROWSER_SMOKES for c in report["cases"]))

    def test_missing_browser_is_a_failure_not_full_acceptance(self):
        def unavailable(argv, **kwargs):
            if any(str(arg).endswith(journey.BROWSER_SMOKES[0]) for arg in argv):
                raise AssertionError("real Chromium/Chrome executable not found")
        with tempfile.TemporaryDirectory() as temp:
            status, report = self.execute(self.args(pathlib.Path(temp)), unavailable)
            self.assertEqual(status, 1)
            self.assertEqual(report["status"], "failed")
            self.assertEqual(report["cases"][-1]["case"], journey.BROWSER_SMOKES[0])
            self.assertEqual(report["cases"][-1]["status"], "failed")

    def test_zero_exit_cannot_certify_incomplete_or_wrong_candidate_transfer(self):
        for status, sha in (("incomplete", "a" * 40), ("passed", "b" * 40)):
            def write_report(argv, **kwargs):
                if any(str(arg).endswith("portable_transfer_browser_smoke.py") for arg in argv):
                    path = pathlib.Path(argv[argv.index("--report") + 1])
                    path.write_text(json.dumps({"status": status, "candidate_sha": sha,
                        "candidate_version": "0.1.0-alpha.5", "cases": [{"case": "synthetic", "status": "passed"}]}))
            with self.subTest(status=status, sha=sha), tempfile.TemporaryDirectory() as temp:
                result, report = self.execute(self.args(pathlib.Path(temp)), write_report)
                self.assertEqual(result, 1)
                self.assertEqual(report["status"], "failed")
                self.assertEqual(report["cases"][-1]["case"], "portable_transfer_browser_smoke.py")
                self.assertEqual(report["cases"][-1]["status"], "failed")
                self.assertEqual(report["portable_transfer"]["candidate_sha"], sha)


class CommandTests(unittest.TestCase):
    def test_nonzero_exit_propagates_and_output_tail_is_bounded(self):
        with self.assertRaises(AssertionError):
            journey.command([sys.executable, "-c", "import sys;sys.exit(7)"])
        result = journey.command([sys.executable, "-c", "print('x'*100000)"], timeout=10)
        self.assertLessEqual(len(result.stdout), 16384)
        self.assertTrue(result.stdout.endswith("x\n"))

    @unittest.skipUnless(os.name == "posix", "POSIX smoke process groups")
    def test_timeout_drains_server_descendant_and_releases_listener(self):
        import socket
        with tempfile.TemporaryDirectory() as temp:
            state = pathlib.Path(temp) / "listener.json"
            child = (
                "import json,pathlib,socket,time;"
                "s=socket.socket();s.bind(('127.0.0.1',0));s.listen();"
                f"pathlib.Path({str(state)!r}).write_text(json.dumps(s.getsockname()));"
                "time.sleep(120)"
            )
            parent = f"import subprocess,sys,time;subprocess.Popen([sys.executable,'-c',{child!r}]);time.sleep(120)"
            with self.assertRaises(subprocess.TimeoutExpired):
                journey.command([sys.executable, "-c", parent], timeout=1)
            self.assertTrue(state.exists(), "child never reached the actual listener")
            host, port = json.loads(state.read_text())
            with socket.socket() as probe:
                self.assertNotEqual(probe.connect_ex((host, port)), 0, "orphan server still accepts connections")

    @unittest.skipUnless(os.name == "posix", "POSIX smoke process groups")
    def test_success_also_reaps_leftover_children(self):
        # A smoke that accidentally forgets its server cannot leak it into the
        # next case even when its own process reports success.
        with tempfile.TemporaryDirectory() as temp:
            marker = pathlib.Path(temp) / "late-marker"
            child = f"import pathlib,time;time.sleep(1);pathlib.Path({str(marker)!r}).touch()"
            parent = f"import subprocess,sys;subprocess.Popen([sys.executable,'-c',{child!r}])"
            journey.command([sys.executable, "-c", parent], timeout=10)
            time.sleep(1.1)
            self.assertFalse(marker.exists())


if __name__ == "__main__":
    unittest.main()
