import json
import math
import os
from pathlib import Path
import subprocess
import tempfile
import time
import unittest
import urllib.error
from unittest import mock

from scripts import mixed_load_soak as soak


class MixedLoadSoakContracts(unittest.TestCase):
    def setUp(self):
        self.context_token = soak.ACTIVE_CONTEXT.set((0, 0, "setup"))

    def tearDown(self):
        soak.ACTIVE_CONTEXT.reset(self.context_token)

    @staticmethod
    def sync_status(source_id, state="idle", success="success-1", *, phase="done", committed=True, partial=False, sequence=1):
        return {"source_id": source_id, "state": state, "last_success_at": success,
                "progress": {"phase": phase, "committed": committed, "partial": partial, "sequence": sequence}}

    def test_sync_barrier_waits_for_all_successful_fixture_scans(self):
        class Clock:
            value = 0.0
            def now(self): return self.value
            def sleep(self, amount): self.value += amount

        clock = Clock()
        responses = [
            {"enabled": True, "sources": []},
            {"enabled": True, "sources": [self.sync_status("a")]},
            {"enabled": True, "sources": [self.sync_status("a"), self.sync_status("b")]},
        ]
        calls = []
        result = soak.wait_for_sync(lambda _timeout: calls.append(1) or responses.pop(0), ("a", "b"), 10,
                                    now=clock.now, sleep=clock.sleep)
        self.assertEqual(len(calls), 3)
        self.assertEqual(set(result), {"a", "b"})
        marker = {"a": result["a"]}
        stale_scan = {"enabled": True, "sources": [self.sync_status("a", sequence=1)]}
        with self.assertRaisesRegex(soak.SoakFailure, "sync_timeout"):
            soak.wait_for_sync(lambda _timeout: stale_scan, ("a",), 2, timeout=1,
                               after_success=marker, now=clock.now, sleep=clock.sleep)

    def test_sync_barrier_rejects_incomplete_progress_and_bounds_timeout_and_stop(self):
        class Clock:
            value = 0.0
            def now(self): return self.value
            def sleep(self, amount): self.value += amount

        clock = Clock()
        responses = [
            {"enabled": True, "sources": [self.sync_status("a", phase="scanning")]},
            {"enabled": True, "sources": [self.sync_status("a", "error")]},
        ]
        with self.assertRaisesRegex(soak.SoakFailure, "sync_failed"):
            soak.wait_for_sync(lambda _timeout: responses.pop(0), ("a",), 10,
                               now=clock.now, sleep=clock.sleep)
        clock.value = 0
        with self.assertRaisesRegex(soak.SoakFailure, "sync_timeout"):
            soak.wait_for_sync(lambda _timeout: {"enabled": True, "sources": []}, ("a",), 2,
                               timeout=1, now=clock.now, sleep=clock.sleep)
        self.assertLessEqual(clock.value, 1)
        with self.assertRaisesRegex(soak.SoakFailure, "process_failed"):
            soak.wait_for_sync(lambda _timeout: {}, ("a",), 10, stopped=lambda: True,
                               now=clock.now, sleep=clock.sleep)

    def test_paging_stale_restarts_whole_window_without_mixing_ids(self):
        pages = []
        restarts = []
        calls = []
        def fetch(cursor, _timeout):
            calls.append(cursor)
            if cursor is None and len(calls) == 1:
                return 200, {"total": 2, "offset": 0, "results": [{"id": "old-1"}], "next_cursor": "old"}, 1.0
            if cursor == "old":
                return 409, {"code": "cursor_stale"}, 2.0
            if cursor is None:
                return 200, {"total": 2, "offset": 0, "results": [{"id": "new-1"}], "next_cursor": "new"}, 3.0
            return 200, {"total": 2, "offset": 1, "results": [{"id": "new-2"}], "next_cursor": None}, 4.0
        ids, saved = soak.collect_paging_window(fetch, 2, 100, now=lambda: 0,
                                                 record_latency=pages.append,
                                                 record_restart=lambda: restarts.append(1))
        self.assertEqual(ids, ["new-1", "new-2"])
        self.assertEqual(saved, "new")
        self.assertEqual(calls, [None, "old", None, "new"])
        self.assertEqual(len(restarts), 1)
        self.assertEqual(pages, [1.0, 2.0, 3.0, 4.0])

    def test_paging_stale_budget_unknown_409_and_bad_page_fail_closed(self):
        calls = []
        def stale(_cursor, _timeout):
            calls.append(1)
            if len(calls) % 2:
                return 200, {"total": 2, "offset": 0, "results": [{"id": f"attempt-{len(calls)}"}], "next_cursor": "next"}, 1
            return 409, {"code": "cursor_stale"}, 1
        restarts = []
        with self.assertRaises(soak.SoakFailure) as caught:
            soak.collect_paging_window(stale, 2, 100, now=lambda: 0, record_restart=lambda: restarts.append(1))
        self.assertEqual(caught.exception.code, "paging_invariant")
        self.assertEqual(caught.exception.api_error_code, "cursor_stale")
        self.assertEqual(len(restarts), 2)
        for bad_page in ({"code": "unknown"}, {"total": 2, "offset": 0, "results": "not-a-list"}):
            with self.subTest(bad_page=bad_page):
                with self.assertRaises(soak.SoakFailure):
                    soak.collect_paging_window(lambda _c, _t: (409 if "code" in bad_page else 200, bad_page, 1),
                                               2, 10, now=lambda: 0)

    def test_http_failure_keeps_safe_context_without_body_or_request_text(self):
        soak.set_context("paging", 17, 17)
        secret = "token-value-and-user-query"
        error = urllib.error.HTTPError("http://127.0.0.1/api?cursor=" + secret, 409, "conflict", {}, None)
        error.read = lambda _limit: json.dumps({"code": "untrusted_code", "error": secret}).encode()
        with mock.patch.object(soak.HTTP_OPENER, "open", side_effect=error):
            with self.assertRaises(soak.SoakFailure) as caught:
                soak.http("http://127.0.0.1/api", 1)
        failure = soak.failure_case(caught.exception)
        rendered = json.dumps(failure)
        self.assertEqual((failure["cycle"], failure["generation"], failure["case"]), (17, 17, "paging"))
        self.assertEqual((failure["http_status"], failure["api_error_code"]), (409, "unknown"))
        self.assertNotIn(secret, rendered)
        self.assertNotIn("untrusted_code", rendered)

    def test_failed_report_finalization_preserves_partial_metrics_and_clean_status(self):
        report = {"status": "failed", "clean_exit": True,
                  "counters": {"operations": {}, "completed_cycles": 1, "canceled_refreshes": 1}}
        samples = {"paging": [1.0, 3.0], "refresh": [7.0]}
        soak.finalize_metrics(report, samples)
        self.assertEqual(report["status"], "failed")
        self.assertTrue(report["clean_exit"])
        self.assertEqual(report["counters"]["operations"], {"paging": 2, "refresh": 1})
        self.assertEqual(report["latency_ms"]["paging"]["count"], 2)
        report["clean_exit"] = False
        soak.fail_if_unclean(report)
        self.assertNotEqual(report["status"], "passed")

    def test_long_duration_and_hard_deadline_boundary_are_deterministic(self):
        self.assertEqual(soak.profile_limits("long", 1800), (1800, 1860))
        self.assertEqual(soak.profile_limits("short", 1800), (180, 180))
        self.assertEqual(soak.profile_limits("long", 3540), (3540, 3600))
        with self.assertRaisesRegex(soak.SoakFailure, "setup_failed"):
            soak.profile_limits("long", 3541)
        target_end = 1800
        self.assertTrue(soak.cycle_is_required("long", 13, 12, target_end, 1799.9))
        self.assertFalse(soak.cycle_is_required("long", 13, 12, target_end, 1800))
        self.assertTrue(soak.cycle_is_required("long", 12, 12, target_end, 1800))
        self.assertFalse(soak.cycle_is_required("short", 4, 3, target_end, 1))
        self.assertEqual(soak.remaining_time(3600, now=3540), 15)

    def test_workflow_python_environment_prevents_bytecode_but_detects_real_dirty_files(self):
        workflow = Path(__file__).resolve().parents[1] / ".github/workflows/performance-check.yml"
        text = workflow.read_text()
        self.assertEqual(text.count("PYTHONDONTWRITEBYTECODE: '1'"), 2)
        self.assertGreaterEqual(text.count('git -C candidate status --porcelain'), 4)
        self.assertGreaterEqual(text.count('git -C baseline status --porcelain'), 2)
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            subprocess.run(["git", "init", "-q"], cwd=root, check=True)
            subprocess.run(["git", "config", "user.email", "ci@example.invalid"], cwd=root, check=True)
            subprocess.run(["git", "config", "user.name", "CI"], cwd=root, check=True)
            module = root / "package.py"
            module.write_text("value = 1\n")
            subprocess.run(["git", "add", "package.py"], cwd=root, check=True)
            subprocess.run(["git", "commit", "-qm", "fixture"], cwd=root, check=True)
            env = {**os.environ, "PYTHONDONTWRITEBYTECODE": "1"}
            subprocess.run([os.sys.executable, "-c", "import package"], cwd=root, env=env, check=True)
            self.assertFalse((root / "__pycache__").exists())
            module.write_text("value = 2\n")
            self.assertTrue(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True).strip())

    def test_report_is_atomic_json_and_rejects_nonfinite(self):
        with tempfile.TemporaryDirectory() as tmp:
            target = Path(tmp) / "out.json"
            soak.atomic_report(target, {"schema": 1, "value": 2})
            self.assertEqual(json.loads(target.read_text()), {"schema": 1, "value": 2})
            with self.assertRaises(ValueError):
                soak.atomic_report(target, {"value": math.nan})

    def test_http_allows_only_explicit_fence_codes(self):
        error = soak.urllib.error.HTTPError("http://127.0.0.1", 409, "conflict", {}, None)
        error.read = lambda _limit: b'{"code":"cursor_stale"}'
        with mock.patch.object(soak.HTTP_OPENER, "open", side_effect=error):
            status, obj = soak.http("http://127.0.0.1/api", 1, allowed=(409,))
        self.assertEqual((status, obj["code"]), (409, "cursor_stale"))
        error = soak.urllib.error.HTTPError("http://127.0.0.1", 500, "server", {}, None)
        error.read = lambda _limit: b'{"error":"not included"}'
        with mock.patch.object(soak.HTTP_OPENER, "open", side_effect=error):
            with self.assertRaises(soak.SoakFailure) as caught:
                soak.http("http://127.0.0.1/api", 1, allowed=(409,))
        self.assertEqual(caught.exception.http_status, 500)

    def test_http_rejects_non_loopback_destinations(self):
        with mock.patch.object(soak.HTTP_OPENER,"open") as opened:
            with self.assertRaises(soak.SoakFailure): soak.http("https://example.invalid/api",1)
            opened.assert_not_called()

    def test_subprocess_timeout_is_bounded_and_nonzero_result_retained(self):
        rc, out = soak.invoke([os.sys.executable, "-c", "print('ok')"], 2)
        self.assertEqual((rc, out), (0, b"ok\n"))
        rc, _ = soak.invoke([os.sys.executable, "-c", "raise SystemExit(7)"], 2)
        self.assertEqual(rc, 7)
        with self.assertRaises(soak.SoakFailure):
            soak.invoke([os.sys.executable, "-c", "import time; time.sleep(2)"], .05)

    def test_output_cap_and_metric_types(self):
        with self.assertRaises(soak.SoakFailure):
            soak.invoke([os.sys.executable, "-c", "print('x' * 1100000)"], 3)
        self.assertIsNone(soak.proc_metrics(-1)["rss_bytes"])
        self.assertEqual(soak.parse_rss_kb("123"), 123 * 1024)
        for value in ("nan", "inf", "-1", "", None):
            self.assertIsNone(soak.parse_rss_kb(value))

    def test_stop_process_tree_reaps_child(self):
        if os.name != "posix": self.skipTest("process groups are POSIX-specific")
        with tempfile.TemporaryDirectory() as tmp:
            pidfile=Path(tmp)/"child.pid"
            code="import subprocess,sys; c=subprocess.Popen([sys.executable,'-c','import time; time.sleep(30)']); open(sys.argv[1],'w').write(str(c.pid))"
            try: soak.invoke([os.sys.executable,"-c",code,str(pidfile)],2)
            except soak.SoakFailure as error: self.assertEqual(error.code,"process_failed")
            child=int(pidfile.read_text())
            for _ in range(300):
                try: os.kill(child,0)
                except ProcessLookupError: break
                stat=Path(f"/proc/{child}/stat")
                if stat.exists() and stat.read_text().split()[2]=="Z": break
                time.sleep(.01)
            else: self.fail("orphan descendant survived invoke process-group cleanup")

    def test_unclean_restart_cannot_be_reported_as_passed(self):
        report={"status":"passed","clean_exit":True,"failure_codes":[],"counters":{"unexpected_errors":0}}
        with mock.patch.object(soak,"stop_tree",return_value=False):
            self.assertFalse(soak.record_stop(report,object()))
        soak.fail_if_unclean(report)
        self.assertFalse(report["clean_exit"])
        self.assertEqual(report["status"],"failed")
        self.assertEqual(report["failure_codes"],["process_failed"])
        self.assertEqual(report["counters"]["unexpected_errors"],1)

    def test_restart_stop_result_is_not_discarded(self):
        report={"clean_exit":True}
        with mock.patch.object(soak,"stop_tree",return_value=False):
            clean=soak.record_stop(report,object())
        self.assertFalse(clean)
        self.assertFalse(report["clean_exit"])

    def test_profile_bounds_and_codes_are_stable(self):
        self.assertEqual(soak.FAILURES["paging"], "paging_invariant")
        self.assertEqual(soak.FAILURES["cancel"], "cancel_invariant")
        parser = __import__("argparse").ArgumentParser()
        parser.add_argument("--duration-seconds", type=int)
        self.assertEqual(parser.parse_args(["--duration-seconds", "3601"]).duration_seconds, 3601)

    def test_output_cannot_overwrite_candidate_binary(self):
        with tempfile.TemporaryDirectory() as tmp:
            binary=Path(tmp)/"findrail"
            binary.write_text("#!/bin/sh\nexit 0\n")
            binary.chmod(0o700)
            args=type("Args",(),{"binary":binary,"output":binary,"profile":"short","duration_seconds":1800,"documents":100,"seed":1,"concurrency":1})()
            with self.assertRaises(soak.SoakFailure) as caught: soak.run(args)
            self.assertEqual(caught.exception.code,"unsafe_workspace")
            self.assertEqual(binary.read_text(),"#!/bin/sh\nexit 0\n")

    def test_schedule_is_deterministic_and_lifecycle_boundaries_are_periodic(self):
        expected=("concurrent_reads","paging","successful_refresh","canceled_refresh","saved_collection_restart")
        self.assertEqual(soak.cycle_schedule(1),expected)
        self.assertEqual(soak.cycle_schedule(3),expected+("isolated_forget_readd",))
        self.assertEqual(soak.cycle_schedule(3),soak.cycle_schedule(3))

    def test_wall_deadline_expires_without_unbounded_wait(self):
        self.assertEqual(soak.remaining_time(20,10),10)
        self.assertEqual(soak.remaining_time(100,0),15)
        with self.assertRaises(soak.SoakFailure) as caught: soak.remaining_time(10,10)
        self.assertEqual(caught.exception.code,"deadline_exceeded")


if __name__ == "__main__":
    unittest.main()
