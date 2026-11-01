import io
import argparse
import json
import pathlib
import subprocess
import sys
import tempfile
import time
import unittest
import urllib.error
import urllib.request
from unittest.mock import Mock, patch

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import smoke
import recovery_candidate


class RejectedRequestTests(unittest.TestCase):
    def setUp(self):
        self.request = urllib.request.Request('http://127.0.0.1:1/api/v1/sources',
                                             data=b'{}', method='POST')

    def test_windows_reset_still_requires_actual_http_error(self):
        rejection = urllib.error.HTTPError(self.request.full_url, 403, 'Forbidden', {}, io.BytesIO(b'{}'))
        try:
            with patch.object(smoke.sys, 'platform', 'win32'), \
                 patch.object(smoke.time, 'sleep'), \
                 patch.object(smoke.urllib.request, 'urlopen', side_effect=[ConnectionResetError(), rejection]) as open_request:
                with self.assertRaises(urllib.error.HTTPError) as caught:
                    smoke.rejected_request(self.request, timeout=4)
                self.assertIs(caught.exception, rejection)
                self.assertEqual(caught.exception.code, 403)
                self.assertEqual(open_request.call_count, 2)
                open_request.assert_called_with(self.request, timeout=4)
        finally:
            rejection.close()

    def test_persistent_windows_reset_fails_with_a_finite_budget(self):
        with patch.object(smoke.sys, 'platform', 'win32'), \
             patch.object(smoke.time, 'sleep'), \
             patch.object(smoke.urllib.request, 'urlopen', side_effect=ConnectionResetError()) as open_request:
            with self.assertRaises(ConnectionResetError):
                smoke.rejected_request(self.request, timeout=4)
            self.assertEqual(open_request.call_count, 5)

    def test_non_windows_reset_is_not_retried(self):
        with patch.object(smoke.sys, 'platform', 'linux'), \
             patch.object(smoke.urllib.request, 'urlopen', side_effect=ConnectionResetError()) as open_request:
            with self.assertRaises(ConnectionResetError):
                smoke.rejected_request(self.request, timeout=4)
            self.assertEqual(open_request.call_count, 1)

    def test_other_transport_failure_is_not_retried(self):
        with patch.object(smoke.sys, 'platform', 'win32'), \
             patch.object(smoke.urllib.request, 'urlopen', side_effect=TimeoutError()) as open_request:
            with self.assertRaises(TimeoutError):
                smoke.rejected_request(self.request, timeout=4)
            self.assertEqual(open_request.call_count, 1)

    def test_http_success_is_left_for_the_callers_rejection_assertion(self):
        response = object()
        with patch.object(smoke.sys, 'platform', 'win32'), \
             patch.object(smoke.urllib.request, 'urlopen', return_value=response) as open_request:
            self.assertIs(smoke.rejected_request(self.request, timeout=4), response)
            self.assertEqual(open_request.call_count, 1)


class RecoveryCandidateTests(unittest.TestCase):
    def test_failed_and_interrupted_runs_replace_previous_pass_and_keep_phase(self):
        for error, status in ((RuntimeError("private path and text"), "failed"),
                              (KeyboardInterrupt(), "interrupted")):
            with self.subTest(status=status), tempfile.TemporaryDirectory() as root:
                report_path = pathlib.Path(root) / "report.json"
                report_path.write_text('{"status":"passed"}')

                def fail(_args, report):
                    report["phase"] = "terminated_commit_unknown"
                    raise error

                argv = ["recovery_candidate.py", "--binary", sys.executable,
                        "--revision", "a" * 40, "--schema", "8",
                        "--work-dir", root, "--report", str(report_path)]
                with patch.object(sys, "argv", argv), \
                     patch.object(recovery_candidate, "run", side_effect=fail):
                    self.assertEqual(recovery_candidate.main(), 1)
                report = json.loads(report_path.read_text())
                self.assertEqual(report["status"], status)
                self.assertEqual(report["phase"], "terminated_commit_unknown")
                self.assertNotIn("private", report_path.read_text())
                self.assertEqual(list(pathlib.Path(root).iterdir()), [report_path])

    @unittest.skipUnless(sys.platform.startswith("linux"), "Linux process groups")
    def test_timeout_and_exited_leader_leave_no_running_descendant(self):
        for exit_early in (False, True):
            with self.subTest(exit_early=exit_early), tempfile.TemporaryDirectory() as root:
                heartbeat = pathlib.Path(root) / "heartbeat"
                stop = pathlib.Path(root) / "stop"
                worker = ("import pathlib,time;"
                          f"beat=pathlib.Path({str(heartbeat)!r});stop=pathlib.Path({str(stop)!r});"
                          "\nwhile not stop.exists():\n beat.write_text(str(time.monotonic_ns()));time.sleep(.02)")
                code = ("import os,pathlib,subprocess,sys,time;"
                        f"p=subprocess.Popen([sys.executable,'-c',{worker!r}]);"
                        f"\nwhile not pathlib.Path({str(heartbeat)!r}).exists():time.sleep(.005)\n" +
                        ("os._exit(0)" if exit_early else "time.sleep(30)"))
                try:
                    if exit_early:
                        recovery_candidate.invoke(pathlib.Path(sys.executable), ["-c", code], 3)
                    else:
                        with self.assertRaises(subprocess.TimeoutExpired):
                            recovery_candidate.invoke(pathlib.Path(sys.executable), ["-c", code], 1.5)
                    before = heartbeat.read_text()
                    time.sleep(.15)
                    self.assertEqual(heartbeat.read_text(), before,
                                     "candidate command left a running descendant")
                finally:
                    # Independent cooperative cleanup also bounds a failing test.
                    stop.touch()

    @unittest.skipUnless(sys.platform.startswith("linux"), "Linux candidate journey")
    def test_reopen_rejects_partial_prune_with_other_new_documents_committed(self):
        with tempfile.TemporaryDirectory() as root:
            args = argparse.Namespace(binary=pathlib.Path(sys.executable), binary_sha256="",
                                      work_dir=pathlib.Path(root), deadline=10,
                                      revision="a" * 40, schema=8)
            report = {"status": "failed"}
            doctor = {"revision": args.revision, "current_write_schema": 8, "index_schema": 8,
                      "checks": [{"id": name, "status": "ok"} for name in
                                 ("integrity_quick_check", "integrity_foreign_key_check")]}
            child = Mock(pid=12345)
            child.poll.return_value = None
            result = subprocess.CompletedProcess([], 0, json.dumps(doctor), "")
            with patch.object(recovery_candidate, "invoke", return_value=result), \
                 patch.object(recovery_candidate, "search_total", side_effect=[1, 1, 0, 1, 1, 1]), \
                 patch.object(recovery_candidate.subprocess, "Popen", return_value=child), \
                 patch.object(recovery_candidate, "has_open_database", return_value=True), \
                 patch.object(recovery_candidate, "process_cpu_ticks", return_value=1), \
                 patch.object(recovery_candidate, "kill_group") as cleanup:
                with self.assertRaisesRegex(RuntimeError, "neither the old nor new"):
                    recovery_candidate.run(args, report)
            self.assertEqual(report["status"], "failed")
            self.assertEqual(report["phase"], "reopened")
            self.assertEqual(cleanup.call_count, 2)


if __name__ == '__main__':
    unittest.main()
