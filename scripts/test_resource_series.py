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

    def test_samples_require_expected_count_and_binary_identity(self):
        report = {"binary": {"sha256": "a" * 64},
                  "fixture": {"corpus_sha256": "b" * 64, "version": 2, "seed": 42, "documents_requested": 100},
                  "workload": {"indexed_documents": 100}}
        sidecar = {"schema": 1, "kind": "search_warm_samples", "status": "success",
                   "units": "ms", "warmup": {"excluded": True, "count_per_family": 1},
                   "binary": {"sha256": "a" * 64}, "fixture": {"corpus_sha256": "b" * 64},
                   "families": {name: {"samples": [1.0, 2.0], "count": 2,
                                        "p50": 1.5, "p95": 2.0} for name in ("common", "rare")}}
        self.assertEqual(series.validate_samples(sidecar, report, "a" * 64, 2, 100, 42)["common"], [1.0, 2.0])
        for malformed, expected_sha in (({**sidecar, "families": {}}, "a" * 64), (sidecar, "c" * 64)):
            with self.assertRaises(ValueError):
                series.validate_samples(malformed, report, expected_sha, 2, 100, 42)
        empty = json.loads(json.dumps(sidecar))
        empty["families"]["rare"]["samples"] = []
        empty["families"]["rare"]["count"] = 0
        with self.assertRaisesRegex(ValueError, "incomplete_samples"):
            series.validate_samples(empty, report, "a" * 64, 2, 100, 42)

    def test_parent_death_configuration_is_linux_scoped(self):
        from unittest.mock import patch
        with patch.object(series.platform, "system", return_value="Darwin"):
            self.assertIsNone(series.configure_parent_death())

    def test_output_budget_overrun_returns_failure(self):
        from unittest.mock import patch
        binary_hash = series.digest(Path(__file__))
        fixture_hash = "b" * 64
        report = {"status": "success", "errors": [], "binary": {"sha256": binary_hash},
                  "fixture": {"corpus_sha256": fixture_hash, "documents_requested": 100, "seed": 42, "version": 2},
                  "workload": {"indexed_documents": 100}}
        sidecar = {"schema": 1, "kind": "search_warm_samples", "status": "success", "units": "ms",
                   "warmup": {"excluded": True, "count_per_family": 1}, "binary": {"sha256": binary_hash},
                   "fixture": {"corpus_sha256": fixture_hash},
                   "families": {key: {"samples": [1.0], "count": 1, "p50": 1.0, "p95": 1.0}
                                for key in ("common", "rare")}}
        def child_output(command, _deadline):
            report_path = Path(command[command.index("--report") + 1])
            samples_path = Path(command[command.index("--samples-report") + 1])
            report_path.write_text(json.dumps(report))
            samples_path.write_text(json.dumps(sidecar))
            return b""
        with tempfile.TemporaryDirectory() as temp:
            out = Path(temp) / "out"
            with patch.object(series, "MAX_OUTPUT", 1), patch.object(series, "run_child", side_effect=child_output):
                result = series.main(["--binary", __file__, "--output", str(out), "--documents", "100",
                                      "--repeats", "1", "--samples-per-family", "1"])
            self.assertEqual(result, 1)
            result_json = json.loads((out / "series.json").read_text())
            self.assertEqual(result_json["status"], "incomplete")
            self.assertIn("output_budget_exceeded", result_json["errors"])

    @unittest.skipUnless(__import__("sys").platform.startswith("linux"), "requires Linux parent-death signal")
    def test_orchestrator_sigkill_reaps_running_harness_child(self):
        import os, signal, subprocess, sys, time
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            marker = root / "child.pid"
            binary = root / "slow-binary"
            binary.write_text("#!/usr/bin/env python3\nimport os,pathlib,sys,time\n"
                              f"pathlib.Path({str(marker)!r}).write_text(str(os.getpid()))\n"
                              "time.sleep(60)\n", encoding="utf-8")
            binary.chmod(0o700)
            output = root / "series"
            child_pid = None
            proc = subprocess.Popen([sys.executable, str(Path(series.__file__)), "--binary", str(binary),
                                     "--documents", "100", "--repeats", "1", "--samples-per-family", "1",
                                     "--deadline-seconds", "90", "--output", str(output)],
                                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            try:
                until = time.monotonic() + 15
                while not marker.exists() and proc.poll() is None and time.monotonic() < until:
                    time.sleep(.02)
                self.assertTrue(marker.exists(), "benchmark child did not start")
                child_pid = int(marker.read_text())
                os.kill(proc.pid, signal.SIGKILL)
                proc.wait(timeout=5)
                until = time.monotonic() + 8
                state = "unknown"
                while time.monotonic() < until:
                    try:
                        state = Path(f"/proc/{child_pid}/stat").read_text().split()[2]
                        if state == "Z":
                            break
                    except FileNotFoundError:
                        state = None
                        break
                    time.sleep(.05)
                self.assertIn(state, (None, "Z"), "grandchild remains live after orchestrator death")
            finally:
                if proc.poll() is None:
                    proc.kill(); proc.wait()
                # Reap the process tree before TemporaryDirectory removes the
                # output directory. The harness child has its own process group.
                if child_pid is not None:
                    try:
                        os.killpg(child_pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass

                    def process_group_is_live(pgid):
                        for stat_path in Path("/proc").glob("[0-9]*/stat"):
                            try:
                                fields = stat_path.read_text().rsplit(")", 1)[1].split()
                                if int(fields[2]) == pgid and fields[0] != "Z":
                                    return True
                            except (FileNotFoundError, ProcessLookupError, ValueError, IndexError):
                                continue
                        return False

                    deadline = time.monotonic() + 8
                    while time.monotonic() < deadline:
                        if not process_group_is_live(child_pid):
                            break
                        time.sleep(.05)
                    else:
                        self.fail("harness process tree did not stop before temporary directory cleanup")

    def test_child_error_is_fixed_and_cleanup(self):
        import sys
        with self.assertRaisesRegex(RuntimeError, "child_failed_7"):
            series.run_child([sys.executable, "-c", "raise SystemExit(7)"], __import__("time").monotonic() + 5)

    @unittest.skipUnless(__import__("os").name == "posix", "requires POSIX process groups")
    def test_failed_group_leader_exit_still_stops_descendant(self):
        import os, sys, time
        with tempfile.TemporaryDirectory() as temp:
            marker = Path(temp) / "descendant.pid"
            code = ("import pathlib,subprocess,sys,time; "
                    f"p=subprocess.Popen([sys.executable,'-c','import time; time.sleep(60)']); "
                    f"pathlib.Path({str(marker)!r}).write_text(str(p.pid)); "
                    "time.sleep(.1); sys.exit(7)")
            with self.assertRaisesRegex(RuntimeError, "child_failed_7"):
                series.run_child([sys.executable, "-c", code], time.monotonic() + 10)
            pid = int(marker.read_text())
            deadline = time.monotonic() + 2
            state = "unknown"
            while time.monotonic() < deadline:
                try:
                    state = Path(f"/proc/{pid}/stat").read_text().split()[2]
                    if state == "Z": break
                except FileNotFoundError:
                    state = None
                    break
                time.sleep(.02)
            self.assertIn(state, (None, "Z"), "descendant survived its failed process-group leader")

    def test_timeout_cleans_process_group(self):
        import sys, time
        with self.assertRaises(TimeoutError):
            series.run_child([sys.executable, "-c", "import time; time.sleep(30)"], time.monotonic() + .05)

    def test_contract_limit_validation(self):
        with self.assertRaises(SystemExit):
            series.main(["--binary", __file__, "--output", "/tmp/no-write", "--samples-per-family", "101"])

    def test_output_and_child_caps(self):
        self.assertEqual(series.MAX_OUTPUT, 16 << 20)
        self.assertEqual(series.MAX_CHILDREN, 1)
        self.assertEqual(series.MAX_STDERR, 4096)


if __name__ == "__main__":
    unittest.main()
