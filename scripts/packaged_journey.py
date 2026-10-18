#!/usr/bin/env python3
"""Validate an already extracted Findrail candidate (never builds the product)."""
import argparse
import hashlib
import json
import os
import pathlib
import platform
import re
import shutil
import sqlite3
import subprocess
import sys
import tempfile

import package
from candidate_record import CaseRecorder, digest, run_identity
from upgrade_smoke import directory_digest

ROOT = pathlib.Path(__file__).resolve().parents[1]


def command(args, *, ok=True, timeout=120):
    result = subprocess.run(args, cwd=ROOT, capture_output=True, text=True,
                            encoding="utf-8", timeout=timeout)
    if (result.returncode == 0) != ok:
        raise AssertionError(f"command {args!r}: exit={result.returncode}\n{result.stdout}\n{result.stderr}")
    return result


def db_fingerprint(path):
    if not path.exists():
        return None
    connection = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    try:
        state = {"schema": connection.execute("PRAGMA user_version").fetchone()[0]}
        for table in ("sources", "documents", "document_pages"):
            try:
                state[table] = connection.execute(f"SELECT * FROM {table} ORDER BY 1").fetchall()
            except sqlite3.OperationalError:
                state[table] = None
        return hashlib.sha256(json.dumps(state, sort_keys=True, default=str).encode()).hexdigest()
    finally:
        connection.close()


def validate_candidate(binary, revision, version):
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("candidate SHA must be a full commit SHA")
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+-alpha\.[0-9]+", version):
        raise ValueError("candidate version must be an exact alpha version")
    build_info = binary.parent / "BUILD-INFO.json"
    metadata = json.loads(build_info.read_text(encoding="utf-8"))
    system = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}.get(platform.system())
    machine = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine().lower())
    target = f"{system}-{machine}"
    if (not system or not machine or metadata.get("dirty") is not False or
            (metadata.get("revision"), metadata.get("version"), metadata.get("target")) != (revision, version, target)):
        raise ValueError("BUILD-INFO.json must match candidate SHA/version, be clean, and target this runner")
    if package.build_info_schema(metadata) != package.schema_version():
        raise ValueError("candidate schema must match the checked-out source")
    actual = command([str(binary), "version"]).stdout.strip()
    if actual != f"findrail {version}":
        raise ValueError(f"candidate executable version mismatch: {actual!r}")
    return metadata


def doctor_integrity(binary, scratch):
    source, data = scratch / "integrity-fixture", scratch / "integrity-index"
    source.mkdir()
    (source / "note.md").write_text("Synthetic integrity fixture. 雪 😀", encoding="utf-8")
    command([str(binary), "index", "--data-dir", str(data), str(source)])
    before, db_before = directory_digest(data), digest(data / "findrail.db")
    ordinary = json.loads(command([str(binary), "doctor", "--data-dir", str(data), "--json"]).stdout)
    assert not any(check["id"].startswith("integrity") for check in ordinary["checks"])
    result = json.loads(command([str(binary), "doctor", "--data-dir", str(data), "--integrity", "--json"]).stdout)
    metadata = json.loads((binary.parent / "BUILD-INFO.json").read_text(encoding="utf-8"))
    assert result["version"] == metadata["version"] and result.get("revision") == metadata["revision"]
    assert result["provenance"] == "clean" and result["go_version"] in metadata["go_version"]
    checks = {check["id"]: check for check in result["checks"]}
    assert result["index_schema"] == result["current_write_schema"] == 8
    for phase in ("quick_check", "foreign_key_check"):
        check = checks["integrity_" + phase]
        assert check["status"] == "ok" and check["code"] == phase + "_completed", check
    after = directory_digest(data)
    assert digest(data / "findrail.db") == db_before, "doctor changed database bytes"
    # SQLite mode=ro can create empty WAL and bookkeeping SHM sidecars. Record
    # the actual full-directory fingerprints, rather than claiming byte-for-byte
    # directory identity or disabling WAL locking with unsafe immutable reads.
    assert all(p.name in ("findrail.db", "findrail.db-wal", "findrail.db-shm") for p in data.iterdir())
    wal = data / "findrail.db-wal"
    assert not wal.exists() or wal.stat().st_size == 0, "doctor produced WAL content"

    broken = scratch / "integrity-fk-conflict"
    shutil.copytree(data, broken)
    connection = sqlite3.connect(broken / "findrail.db")
    try:
        connection.execute("PRAGMA foreign_keys=OFF")
        connection.execute("UPDATE documents SET source_id='synthetic-missing-source'")
        connection.commit()
    finally:
        connection.close()
    broken_before = digest(broken / "findrail.db")
    failed = command([str(binary), "doctor", "--data-dir", str(broken), "--integrity", "--json"], ok=False)
    fk = next(check for check in json.loads(failed.stdout)["checks"] if check["id"] == "integrity_foreign_key_check")
    assert failed.returncode == 2 and fk["status"] == "error" and fk["code"] == "foreign_key_check_issue", fk
    assert digest(broken / "findrail.db") == broken_before

    incomplete = scratch / "integrity-incomplete"
    shutil.copytree(data, incomplete)
    connection = sqlite3.connect(incomplete / "findrail.db")
    try:
        connection.execute("PRAGMA writable_schema=ON")
        connection.execute("UPDATE sqlite_master SET sql='not valid SQL' WHERE name='documents'")
        connection.commit()
    finally:
        connection.close()
    incomplete_before = digest(incomplete / "findrail.db")
    failed = command([str(binary), "doctor", "--data-dir", str(incomplete), "--integrity", "--json"], ok=False)
    phases = [check for check in json.loads(failed.stdout)["checks"] if check["id"].startswith("integrity_")]
    assert failed.returncode == 2 and phases and all(check["status"] == "error" for check in phases), phases
    assert any(check["code"].endswith("_incomplete") for check in phases), phases
    assert digest(incomplete / "findrail.db") == incomplete_before
    return {"schema": 8, "stopped_directory_before_sha256": before, "stopped_directory_after_sha256": after,
            "database_bytes_unchanged": True, "full_directory_unchanged": before == after,
            "sqlite_readonly_sidecars_may_be_created": True, "structural_and_fk_completed": True,
            "fk_conflict_rejected": True, "incomplete_is_not_healthy": True, "ordinary_doctor_remains_lightweight": True}


def evaluate(binary, scratch):
    documents = ROOT / "examples/evaluation/documents"
    queries_file = ROOT / "examples/evaluation/queries.json"
    data = scratch / "evaluation-data"
    indexed = json.loads(command([str(binary), "index", "--data-dir", str(data), "--json", str(documents)]).stdout)
    source = indexed["source"]["id"]
    before = db_fingerprint(data / "findrail.db")
    result = json.loads(command([str(binary), "evaluate", "--data-dir", str(data), "--source", source,
                                 "--queries", str(queries_file), "--json"]).stdout)
    after = db_fingerprint(data / "findrail.db")
    assert before == after, "evaluate changed meaningful SQLite state"
    cases = result.get("results", [])
    assert len(cases) == 10 and result.get("passed") == 10 and result.get("failed") == 0, result
    assert [case.get("query") for case in cases] == [case["query"] for case in json.loads(queries_file.read_text(encoding="utf-8"))], result
    for case in cases:
        assert case["actual_paths"] == sorted(case["actual_paths"]), case
        assert case["expected_paths"] == sorted(case["expected_paths"]), case

    # A deliberately mismatched corpus must report every comparison before failing.
    negative = scratch / "negative-queries.json"
    entries = json.loads(queries_file.read_text(encoding="utf-8"))
    entries[0]["expected_paths"] = ["definitely-not-a-result.md"]
    negative.write_text(json.dumps(entries, ensure_ascii=False), encoding="utf-8")
    failed = command([str(binary), "evaluate", "--data-dir", str(data), "--source", source,
                      "--queries", str(negative), "--json"], ok=False)
    report = json.loads(failed.stdout)
    assert len(report.get("results", [])) == 10 and report.get("failed") == 1, report
    assert db_fingerprint(data / "findrail.db") == before

    missing = scratch / "missing-evaluation-index"
    command([str(binary), "evaluate", "--data-dir", str(missing), "--source", "missing",
             "--queries", str(queries_file), "--json"], ok=False)
    assert not missing.exists(), "missing-index evaluate created a data directory"

    # Legacy schema rejection must be read-only (schema-2 fixture is exercised by upgrade_smoke).
    legacy = scratch / "legacy-evaluate"
    legacy.mkdir()
    db = sqlite3.connect(legacy / "findrail.db")
    db.execute("PRAGMA user_version=2")
    db.execute("CREATE TABLE marker(value TEXT)")
    db.execute("INSERT INTO marker VALUES('unchanged')")
    db.commit(); db.close()
    legacy_before = hashlib.sha256((legacy / "findrail.db").read_bytes()).hexdigest()
    command([str(binary), "evaluate", "--data-dir", str(legacy), "--source", source,
             "--queries", str(queries_file), "--json"], ok=False)
    assert hashlib.sha256((legacy / "findrail.db").read_bytes()).hexdigest() == legacy_before

    many_docs, many_data = scratch / "many-matches", scratch / "many-match-index"
    many_docs.mkdir()
    for number in range(101):
        (many_docs / f"match-{number:03}.md").write_text("boundedmanymarker\n", encoding="utf-8")
    many_index = json.loads(command([str(binary), "index", "--data-dir", str(many_data), "--json", str(many_docs)]).stdout)
    many_queries = scratch / "many-queries.json"
    many_queries.write_text(json.dumps([{"query": "boundedmanymarker", "expected_paths": []}]), encoding="utf-8")
    too_many = command([str(binary), "evaluate", "--data-dir", str(many_data), "--source",
                        many_index["source"]["id"], "--queries", str(many_queries), "--json"], ok=False)
    assert "incomplete search results" in too_many.stderr, too_many.stderr

    # explain-path contract is ROOT followed by --path RELATIVE.
    explained = json.loads(command([str(binary), "explain-path", str(documents), "--path", "kahve.md", "--json"]).stdout)
    assert explained["results"][0]["path"] == "kahve.md", explained
    return {"case": "evaluate", "status": "passed", "comparisons": 10,
            "read_only_fingerprint": before, "negative_report_complete": True,
            "missing_index_noncreating": True, "legacy_index_unmodified": True,
            "over_100_matches_incomplete": True}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path, help="path to extracted findrail executable")
    parser.add_argument("--candidate-sha", required=True)
    parser.add_argument("--candidate-version", required=True)
    parser.add_argument("--alpha3-binary", type=pathlib.Path, required=True,
                        help="extracted published alpha.3 binary used to verify stopped-backup restore")
    parser.add_argument("--alpha4-binary", type=pathlib.Path, required=True,
                        help="extracted published alpha.4 binary used to verify schema-7 backup restore")
    parser.add_argument("--report", type=pathlib.Path, help="write per-case JSON report")
    parser.add_argument("--skip-browser", action="store_true")
    args = parser.parse_args()
    binary = args.binary.resolve()
    report = {"candidate_sha": args.candidate_sha, "candidate_version": args.candidate_version,
              "status": "running", **run_identity(), "browser_skipped": args.skip_browser, "cases": []}
    recorder = CaseRecorder(report, args.report)
    try:
        metadata = recorder.run("exact_native_candidate_identity",
                                lambda: validate_candidate(binary, args.candidate_sha, args.candidate_version))
        report.update(schema_version=metadata["schema_version"], target=metadata["target"],
                      source_clean=metadata["dirty"] is False, binary=binary.name,
                      binary_sha256=digest(binary), go=metadata.get("go_version"))
        with tempfile.TemporaryDirectory(prefix="findrail packaged journey ") as directory:
            scratch = pathlib.Path(directory)
            recorder.run("protected_evaluate_corpus_and_legacy_limit", lambda: evaluate(binary, scratch))
            recorder.run("doctor_integrity_stopped_directory", lambda: doctor_integrity(binary, scratch))
            for script in ["search_smoke.py", "mcp_smoke.py", "portable_smoke.py", "diagnostics_smoke.py", "ignore_policy_smoke.py"]:
                recorder.run(script, lambda script=script: run_script(script, binary, timeout=300))
            recorder.run("synthetic_schema2_published_alpha3_and_alpha4_upgrade_restore_rollback",
                         lambda: run_script("upgrade_smoke.py", binary, "--alpha3-binary",
                                            args.alpha3_binary.resolve(strict=True), "--alpha4-binary",
                                            args.alpha4_binary.resolve(strict=True), timeout=300))
            recorder.run("desktop_launch_smoke.py", lambda: run_script("desktop_launch_smoke.py", binary, timeout=90))
            browser_scripts = ["packaged_browser_journey.py", "persistent_collection_smoke.py",
                               "evidence_collection_smoke.py", "evidence_bundle_browser_smoke.py",
                               "search_preset_browser_smoke.py"]
            if args.skip_browser:
                report["cases"].extend({"case": script, "status": "skipped"} for script in browser_scripts)
            else:
                for script in browser_scripts:
                    def browser_operation(script=script):
                        if script != "packaged_browser_journey.py":
                            return run_script(script, binary, timeout=600)
                        details_path = scratch / "common-browser-cases.json"
                        try:
                            run_script(script, binary, "--report", details_path, timeout=600)
                        finally:
                            if details_path.is_file():
                                report["cases"][-1]["details"] = json.loads(details_path.read_text(encoding="utf-8"))
                    recorder.run(script, browser_operation)
        report["status"] = "incomplete" if args.skip_browser else "passed"
    except BaseException:
        report["status"] = "failed"
        raise
    finally:
        recorder.save()
        print(json.dumps(report, indent=2, ensure_ascii=False))


def run_script(script, binary, *arguments, timeout):
    command([sys.executable, str(ROOT / "scripts" / script), str(binary), *map(str, arguments)], timeout=timeout)


if __name__ == "__main__":
    main()
