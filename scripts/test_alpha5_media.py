import hashlib
import json
import pathlib
import tempfile
import unittest
from unittest import mock

from scripts import alpha5_media


class Alpha5MediaTests(unittest.TestCase):
    def test_names_are_explicit_and_bounded(self):
        self.assertEqual(alpha5_media.ASSETS, {
            "search-alpha5.png", "collection-alpha5.png", "freshness-alpha5.png",
            "workflow-alpha5.mp4", "capture-alpha5.json"})
        self.assertEqual(len(alpha5_media.ASSETS), 5)

    def test_start_command_uses_isolated_loopback_and_data_dir(self):
        self.assertEqual(alpha5_media.start_command(pathlib.Path("/tmp/findrail"),
            pathlib.Path("/tmp/index"), 12345), ["/tmp/findrail", "start",
            "--data-dir", "/tmp/index", "--no-open", "--sync-interval", "1s",
            "--addr", "127.0.0.1:12345"])

    def test_cleanup_escalates_after_timeout(self):
        proc = mock.Mock()
        proc.poll.return_value = None
        proc.wait.side_effect = [alpha5_media.subprocess.TimeoutExpired("server", 8), None]
        alpha5_media.stop_process(proc)
        proc.terminate.assert_called_once()
        proc.kill.assert_called_once()

    def test_metadata_rejects_wrong_provenance(self):
        with tempfile.TemporaryDirectory() as tmp:
            binary = pathlib.Path(tmp) / "findrail"
            binary.write_bytes(b"not a real executable")
            meta = pathlib.Path(tmp) / "asset.json"
            meta.write_text(json.dumps({"version": alpha5_media.VERSION,
                "source_revision": alpha5_media.SOURCE, "schema": 8,
                "archive_sha256": alpha5_media.ARCHIVE_SHA256,
                "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest()}))
            with mock.patch.object(alpha5_media.subprocess, "run", return_value=mock.Mock(stdout="findrail 0.1.0-alpha.4")):
                with self.assertRaises(ValueError):
                    alpha5_media.check_inputs(binary, meta)

    def test_metadata_requires_hashes(self):
        with tempfile.TemporaryDirectory() as tmp:
            binary = pathlib.Path(tmp) / "findrail"
            binary.write_bytes(b"x")
            meta = pathlib.Path(tmp) / "asset.json"
            meta.write_text(json.dumps({"version": alpha5_media.VERSION,
                "source_revision": alpha5_media.SOURCE, "schema": 8,
                "archive_sha256": alpha5_media.ARCHIVE_SHA256,
                "binary_sha256": "0" * 64}))
            with self.assertRaises(ValueError):
                alpha5_media.check_inputs(binary, meta)


if __name__ == "__main__":
    unittest.main()
