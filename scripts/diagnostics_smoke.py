#!/usr/bin/env python3
"""Compiled synthetic journey for committed source indexing reports."""
import json
import pathlib
import subprocess
import sys
import tempfile

binary = pathlib.Path(sys.argv[1]).resolve()
with tempfile.TemporaryDirectory(prefix="findrail-diagnostics-") as temporary:
    base = pathlib.Path(temporary)
    data, source = base / "index", base / "source"
    source.mkdir()
    (source / "note.md").write_text("synthetic committed note", encoding="utf-8")
    (source / "picture.png").write_bytes(b"synthetic unsupported image")
    (source / "secret-token.txt").write_text("must never be sampled", encoding="utf-8")
    # Exercise an inventory much larger than the report's retained sample.
    for index in range(240):
        (source / f"synthetic-{index:04d}.png").write_bytes(b"synthetic unsupported image")
    hidden = source / ".private"
    hidden.mkdir()
    (hidden / "not-enumerated.md").write_text("hidden", encoding="utf-8")

    def run(*args, ok=True):
        result = subprocess.run([str(binary), *map(str, args)], text=True, capture_output=True)
        if ok and result.returncode:
            raise AssertionError(f"command failed: {args}: {result.stderr}")
        if not ok and not result.returncode:
            raise AssertionError(f"command unexpectedly succeeded: {args}")
        return result

    indexed = json.loads(run("index", "--data-dir", data, "--json", source).stdout)
    sid = indexed["source"]["id"]
    summary_args = ("source-report", "--data-dir", data, "--source", sid, "--json")
    summary = json.loads(run(*summary_args).stdout)
    assert summary["available"] is True
    assert "picture.png" not in json.dumps(summary)
    details = json.loads(run(*summary_args, "--show-paths").stdout)
    report = details["report"]
    assert report["committed"] and report["complete"]
    assert report["indexed_documents"] == report["updated_documents"] + report["unchanged_documents"]
    assert report["skipped_files"] >= 242 and report["pruned_directories"] >= 1
    assert len(report.get("examples", [])) <= 200 and report["examples_omitted"] > 0
    encoded = json.dumps(report, ensure_ascii=False).encode()
    assert len(encoded) <= 65536
    paths = [item["path"] for item in report.get("examples", [])]
    assert "picture.png" in paths and "secret-token.txt" not in paths
    # A new process reads the same committed record without scanning the source.
    restarted = json.loads(run(*summary_args).stdout)
    assert restarted["report"]["id"] == report["id"]
    run("forget", "--data-dir", data, sid)
    run(*summary_args, ok=False)
    print(f"diagnostics smoke: {report['observed_files']} observed files, {report['skipped_files']} skipped, {len(paths)} retained examples, {len(encoded)} report bytes; restart/redaction/cascade passed")
