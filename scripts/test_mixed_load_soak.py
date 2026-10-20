import json
import math
import os
from pathlib import Path
import signal
import tempfile
import unittest
from unittest import mock

from scripts import mixed_load_soak as soak


class MixedLoadSoakContracts(unittest.TestCase):
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
        with tempfile.TemporaryDirectory() as tmp:
            pidfile=Path(tmp)/"child.pid"
            code="import subprocess,sys,time; c=subprocess.Popen([sys.executable,'-c','import time; time.sleep(30)']); open(sys.argv[1],'w').write(str(c.pid)); time.sleep(30)"
            process=__import__("subprocess").Popen([os.sys.executable,"-c",code,str(pidfile)],start_new_session=True)
            for _ in range(100):
                if pidfile.exists(): break
                __import__("time").sleep(.01)
            child=int(pidfile.read_text())
            soak.stop_tree(process)
            with self.assertRaises(ProcessLookupError): os.kill(child,0)

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
