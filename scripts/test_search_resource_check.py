#!/usr/bin/env python3
import importlib.util
import json
import os
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("resource_check", Path(__file__).with_name("search_resource_check.py"))
resource_check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(resource_check)


class GeneratorTests(unittest.TestCase):
    def test_paging_profiles_request_three_real_limits_and_stop_at_window(self):
        calls = []
        def open_page(url, timeout):
            from urllib.parse import urlparse, parse_qs
            from unittest.mock import Mock
            params = parse_qs(urlparse(url).query)
            limit = int(params['limit'][0]); offset = int(params.get('cursor', ['0'])[0])
            calls.append(limit)
            size = min(limit, 1000-offset)
            page = {'total': 1500, 'offset': offset,
                    'results': [{'id': str(i)} for i in range(offset, offset+size)],
                    'next_cursor': str(offset+size) if offset+size < 1000 else None,
                    'end_reason': 'window_limit' if offset+size == 1000 else None}
            response = Mock()
            response.__enter__ = lambda _: response
            response.__exit__ = lambda *args: None
            response.read = lambda cap: json.dumps(page).encode()
            return response
        from unittest.mock import patch
        with patch.object(resource_check.urllib.request, 'urlopen', side_effect=open_page):
            profile = resource_check.paging_profile('http://127.0.0.1', 1500, time.monotonic()+10)
        self.assertEqual(profile['window_limit'], 1000)
        self.assertEqual(profile['total'], 1500)
        self.assertEqual(set(calls), {20, 50, 100})
        self.assertTrue(all(v['returned'] == 1000 for v in profile['page_sizes'].values()))
        self.assertEqual({size: values['pages'] for size, values in profile['page_sizes'].items()},
                         {'20': 50, '50': 20, '100': 10})
        def reordered_page(url, timeout):
            from urllib.parse import urlparse, parse_qs
            response = open_page(url, timeout)
            if int(parse_qs(urlparse(url).query)['limit'][0]) == 50:
                payload = json.loads(response.read(1 << 20))
                payload['results'].reverse()
                response.read = lambda cap: json.dumps(payload).encode()
            return response
        with patch.object(resource_check.urllib.request, 'urlopen', side_effect=reordered_page):
            with self.assertRaisesRegex(RuntimeError, "order changed"):
                resource_check.paging_profile('http://127.0.0.1', 1500, time.monotonic()+10)

    def corpus(self, seed):
        with tempfile.TemporaryDirectory(prefix="findrail-fixture-test-") as temp:
            root = Path(temp) / "source"
            root.mkdir()
            total = resource_check.generate(root, 100, seed, 9999999999)
            self.assertGreater(max(p.stat().st_size for p in root.glob("*.md")), 65536)
            return resource_check.digest_file_manifest(root), total, len(list(root.iterdir()))

    def test_seed_identity_and_unicode_formats(self):
        first = self.corpus(42)
        self.assertEqual(first, self.corpus(42))
        self.assertNotEqual(first[0], self.corpus(43)[0])
        self.assertEqual(first[2], 100)
        self.assertLess(first[1], resource_check.MAX_BYTES)

    def test_generated_byte_budget_stops_before_over_budget_write(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory(prefix="resource-budget-test-") as temp:
            root = Path(temp)
            with patch.object(resource_check, "MAX_BYTES", 1):
                with self.assertRaisesRegex(ValueError, "corpus byte cap"):
                    resource_check.generate(root, 100, 42, time.monotonic()+10)
            self.assertEqual(list(root.iterdir()), [])

    def test_100k_profile_requires_explicit_opt_in(self):
        script = Path(__file__).with_name("search_resource_check.py")
        with tempfile.TemporaryDirectory(prefix="resource-large-gate-") as temp:
            report = Path(temp) / "not-created.json"
            result = subprocess.run([sys.executable, str(script), "--binary", sys.executable,
                                     "--documents", "100000", "--seed", "42", "--report", str(report)],
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(b"requires --allow-large", result.stderr)
            self.assertFalse(report.exists())

    def test_source_cycle_requires_scale_profile(self):
        script = Path(__file__).with_name("search_resource_check.py")
        with tempfile.TemporaryDirectory(prefix="resource-cycle-gate-") as temp:
            report = Path(temp) / "not-created.json"
            result = subprocess.run([sys.executable, str(script), "--binary", sys.executable,
                                     "--documents", "1000", "--seed", "42", "--report", str(report),
                                     "--source-cycle-boundaries"], stdout=subprocess.PIPE,
                                    stderr=subprocess.PIPE, check=False)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(b"requires the 10000- or 100000-document profile", result.stderr)
            self.assertFalse(report.exists())

    def test_failed_index_reports_failure_and_cleans_fixture(self):
        script = Path(__file__).with_name("search_resource_check.py")
        with tempfile.TemporaryDirectory(prefix="resource-failure-test-") as temp:
            root = Path(temp)
            binary = root / "failing-findrail"
            binary.write_text("#!/usr/bin/env python3\nraise SystemExit(1)\n", encoding="utf-8")
            binary.chmod(0o700)
            report = root / "report.json"
            before = set(Path(tempfile.gettempdir()).glob("findrail-resource-*"))
            result = subprocess.run([sys.executable, str(script), "--binary", str(binary), "--documents", "100", "--seed", "8", "--queries", "1", "--report", str(report)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
            after = set(Path(tempfile.gettempdir()).glob("findrail-resource-*"))
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(before, after)
            data = json.loads(report.read_text(encoding="utf-8"))
            self.assertEqual(data["status"], "failed")
            self.assertNotIn(str(root), report.read_text(encoding="utf-8"))
            self.assertNotIn(str(root), result.stderr.decode())

    def test_percentiles_and_lifecycle_file_sizes(self):
        self.assertEqual(resource_check.latency_summary([100, 1, 3, 2]), {"p50": 2.5, "p95": 100})
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "findrail.db").write_bytes(b"db")
            (root / "findrail.db-wal").write_bytes(b"wal")
            self.assertEqual(resource_check.database_sizes(root), {"findrail.db": 2, "findrail.db-wal": 3, "findrail.db-shm": 0})
            (root / "fixture.txt").write_bytes(b"input")
            (root / "nested").mkdir()
            self.assertEqual(resource_check.directory_file_bytes(root), 10)

    @unittest.skipUnless(os.name == "posix", "requires catchable POSIX SIGTERM")
    def test_cancel_stops_child_and_cleans_owned_directories(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            marker, report = root / "started", root / "report.json"
            script = Path(__file__).with_name("search_resource_check.py").resolve()
            # Replace only the index command with a controlled long-lived child;
            # exercise the real cancellation, subprocess and directory cleanup.
            child_code = f"import os,time,pathlib;pathlib.Path({str(marker)!r}).write_text(str(os.getpid()));time.sleep(60)"
            code = ("import importlib.util,signal,sys;"
                    f"s=importlib.util.spec_from_file_location('harness',{str(script)!r});"
                    "m=importlib.util.module_from_spec(s);s.loader.exec_module(m);"
                    "original=m.run;"
                    f"m.run=lambda argv,timeout:original([sys.executable,'-c',{child_code!r}],timeout);"
                    "signal.signal(signal.SIGTERM,m.canceled);sys.exit(m.main())")
            env = dict(os.environ, TMPDIR=str(root), TEMP=str(root), TMP=str(root))
            child = subprocess.Popen([sys.executable, "-c", code, "--binary", sys.executable, "--documents", "100", "--seed", "8", "--queries", "1", "--report", str(report)], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                deadline = time.monotonic() + 10
                while not marker.exists() and child.poll() is None and time.monotonic() < deadline:
                    time.sleep(.01)
                self.assertTrue(marker.exists(), "controlled index child did not start")
                pid = int(marker.read_text())
                child.send_signal(signal.SIGTERM)
                _, stderr = child.communicate(timeout=10)
                self.assertEqual(child.returncode, 1)
                self.assertEqual(json.loads(report.read_text())["status"], "failed")
                self.assertEqual(list(root.glob("findrail-resource-*")), [])
                with self.assertRaises(ProcessLookupError):
                    os.kill(pid, 0)
                self.assertNotIn(str(root), stderr.decode())
            finally:
                if child.poll() is None:
                    child.kill()
                    child.communicate()


if __name__ == "__main__":
    unittest.main()
