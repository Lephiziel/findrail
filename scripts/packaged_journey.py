#!/usr/bin/env python3
"""Validate an already extracted Findrail candidate (never builds the product)."""
import argparse
import hashlib
import json
import os
import pathlib
import platform
import re
import signal
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import time

import package
import release_assets
from candidate_record import CaseRecorder, digest, run_identity, require_transfer, require_common
from upgrade_smoke import directory_digest

ROOT = pathlib.Path(__file__).resolve().parents[1]


def stop_process_tree(process):
    """Drain the entire smoke group, including servers and driver children."""
    if os.name == "posix":
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            return
        try:
            process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            pass
        # The parent can exit before its Chrome/server children. Reap the group
        # even after wait() returns; no child may outlive a smoke boundary.
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    elif process.poll() is None:
        subprocess.run(["taskkill", "/PID", str(process.pid), "/T", "/F"],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10)
    process.wait(timeout=5)


def command(args, *, ok=True, timeout=120):
    # Files cannot fill a pipe while a browser smoke is blocked. Return only a
    # bounded tail, never accumulate complete browser/server logs in memory.
    with tempfile.TemporaryFile() as stdout, tempfile.TemporaryFile() as stderr:
        process = subprocess.Popen(args, cwd=ROOT, stdout=stdout, stderr=stderr,
                                   start_new_session=os.name == "posix")
        try:
            process.wait(timeout=timeout)
        finally:
            stop_process_tree(process)
        def tail(stream):
            stream.seek(0, os.SEEK_END)
            stream.seek(max(0, stream.tell() - 16384))
            return stream.read().decode("utf-8", errors="replace")
        result = subprocess.CompletedProcess(args, process.returncode, tail(stdout), tail(stderr))
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
    if version not in ("0.1.0-alpha.5", "0.1.0-beta.1"):
        raise ValueError("candidate version is not an explicitly supported rehearsal version")
    expected_schema = release_assets.contract_schema(version)
    if expected_schema != package.schema_version():
        raise ValueError("candidate contract must match the checked-out source schema")
    build_info = binary.parent / "BUILD-INFO.json"
    metadata = json.loads(build_info.read_text(encoding="utf-8"))
    system = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}.get(platform.system())
    machine = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine().lower())
    target = f"{system}-{machine}"
    if (not system or not machine or metadata.get("dirty") is not False or
            package.build_info_schema(metadata) != expected_schema or
            (metadata.get("revision"), metadata.get("version"), metadata.get("target")) != (revision, version, target)):
        raise ValueError("BUILD-INFO.json must match candidate SHA/version, be clean, and target this runner")
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


BROWSER_SMOKES = (
    "diagnostics_browser_smoke.py",
    "evidence_collection_smoke.py",
    "portable_transfer_browser_smoke.py",
    "packaged_browser_journey.py",  # one common source-cycle browser session
    "passage_window_browser_smoke.py",
    "snapshot_browser_smoke.py",
    "persistent_collection_smoke.py",
    "search_preset_browser_smoke.py",
    "evidence_bundle_browser_smoke.py",
)


class CaseReport:
    def __init__(self, report, destination=None):
        self.report = report
        self.checkpoint = CaseRecorder(report, destination)

    def run(self, name, operation):
        case = {"case": name, "status": "running"}
        self.report["cases"].append(case)
        self.checkpoint.save()
        started = time.monotonic()
        try:
            detail = operation()
            if isinstance(detail, dict):
                case.update(detail)
            case["status"] = "passed"
            return detail
        except BaseException as error:
            case["status"] = "failed"
            self.report["status"] = "failed"
            # The detailed subprocess tail goes to the local console. Artifacts
            # keep a bounded diagnosis rather than complete logs or fixture text.
            case["error"] = type(error).__name__
            if isinstance(error, subprocess.TimeoutExpired):
                case["timeout_seconds"] = error.timeout
            raise
        finally:
            case["duration_seconds"] = round(time.monotonic() - started, 3)
            self.checkpoint.save()


def execute(args):
    report = {"candidate_sha": args.candidate_sha, "candidate_version": args.candidate_version,
              "os": platform.platform(), "python": platform.python_version(),
              "browser_skipped": args.skip_browser, "status": "running", "cases": [], **run_identity()}
    cases = CaseReport(report, args.report)
    started = time.monotonic()
    try:
        def identity():
            binary = args.binary.resolve(strict=True)
            if not binary.is_file() or not os.access(binary, os.X_OK):
                raise ValueError("candidate binary is not executable")
            metadata = validate_candidate(binary, args.candidate_sha, args.candidate_version)
            with binary.open("rb") as stream:
                digest = hashlib.file_digest(stream, "sha256").hexdigest()
            report.update(schema_version=metadata["schema_version"], target=metadata["target"],
                          source_clean=True, binary=binary.name, binary_sha256=digest,
                          go=metadata.get("go_version"))
        cases.run("candidate_identity", identity)
        binary = args.binary.resolve(strict=True)
        with tempfile.TemporaryDirectory(prefix="Findrail packaged journey 雪 ") as directory:
            scratch = pathlib.Path(directory)
            cases.run("evaluate", lambda: evaluate(binary, scratch))
            cases.run("doctor_integrity_stopped_directory", lambda: doctor_integrity(binary, scratch))
            for smoke in ("search_smoke.py", "mcp_smoke.py", "portable_smoke.py", "diagnostics_smoke.py", "ignore_policy_smoke.py"):
                cases.run(smoke, lambda smoke=smoke: command([sys.executable, str(ROOT / "scripts" / smoke), str(binary)], timeout=300))
            cases.run("schema2_schema6_published_alpha4_schema7_upgrade_restore_rollback", lambda: command(
                [sys.executable, str(ROOT / "scripts/upgrade_smoke.py"), str(binary),
                 "--alpha3-binary", str(args.alpha3_binary.resolve(strict=True)),
                  "--alpha4-binary", str(args.alpha4_binary.resolve(strict=True))], timeout=300))
            if args.candidate_version == "0.1.0-beta.1":
                cases.run("published_alpha5_schema8_same_schema_compatibility", lambda: command(
                    [sys.executable, str(ROOT / "scripts/upgrade_smoke.py"), str(binary),
                     "--alpha5-binary", str(args.alpha5_binary.resolve(strict=True)), "--alpha5-only"], timeout=300))
            cases.run("desktop_launch_smoke.py", lambda: command(
                [sys.executable, str(ROOT / "scripts/desktop_launch_smoke.py"), str(binary)], timeout=90))
            for smoke in BROWSER_SMOKES:
                if args.skip_browser:
                    report["cases"].append({"case": smoke, "status": "skipped", "reason": "--skip-browser", "duration_seconds": 0})
                else:
                    def browser_case(smoke=smoke):
                        argv = [sys.executable, str(ROOT / "scripts" / smoke), str(binary)]
                        if smoke == "evidence_collection_smoke.py":
                            if not args.evidence_fixture:
                                raise ValueError("packaged collection case requires an explicit prebuilt --evidence-fixture")
                            fixture = args.evidence_fixture.resolve(strict=True)
                            if not fixture.is_file() or not os.access(fixture, os.X_OK):
                                raise ValueError("offline fixture writer is not executable")
                            with fixture.open("rb") as stream:
                                report["offline_fixture_sha256"] = hashlib.file_digest(stream, "sha256").hexdigest()
                            argv += ["--fixture-binary", str(fixture)]
                        child_report = scratch / "portable-transfer-report.json"
                        common_report = scratch / "common-browser-cases.json"
                        if smoke == "packaged_browser_journey.py":
                            argv += ["--report", str(common_report)]
                        if smoke == "portable_transfer_browser_smoke.py":
                            argv += ["--candidate-sha", args.candidate_sha, "--candidate-version", args.candidate_version,
                                     "--report", str(child_report)]
                        try:
                            command(argv, timeout=600)
                        finally:
                            if smoke == "packaged_browser_journey.py" and common_report.exists():
                                if common_report.stat().st_size > 65536:
                                    raise ValueError("oversized common browser report")
                                report["cases"][-1]["details"] = json.loads(common_report.read_text(encoding="utf-8"))
                            if smoke == "portable_transfer_browser_smoke.py" and child_report.exists():
                                if child_report.stat().st_size > 65536:
                                    raise ValueError("oversized transfer report")
                                report["portable_transfer"] = json.loads(child_report.read_text(encoding="utf-8"))
                        if smoke == "portable_transfer_browser_smoke.py":
                            require_transfer(report.get("portable_transfer", {}), args.candidate_sha, args.candidate_version, report["binary_sha256"])
                        if smoke == "packaged_browser_journey.py":
                            require_common(report["cases"][-1].get("details", {}), args.candidate_sha, report["binary_sha256"])

                    cases.run(smoke, browser_case)
        report["status"] = "incomplete" if args.skip_browser else "passed"
        return 0
    except Exception as error:
        report["status"] = "failed"
        print(f"Packaged journey failed: {error}", file=sys.stderr)
        return 1
    except BaseException:
        report["status"] = "interrupted"
        raise
    finally:
        report["duration_seconds"] = round(time.monotonic() - started, 3)
        cases.checkpoint.save()
        print(json.dumps(report, indent=2, ensure_ascii=False))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path, help="path to extracted findrail executable")
    parser.add_argument("--candidate-sha", required=True)
    parser.add_argument("--candidate-version", required=True)
    parser.add_argument("--alpha3-binary", type=pathlib.Path, required=True,
                        help="extracted published alpha.3 binary used to verify stopped-backup restore")
    parser.add_argument("--alpha4-binary", type=pathlib.Path, required=True,
                        help="extracted published alpha.4 binary used to verify schema-7 backup restore")
    parser.add_argument("--alpha5-binary", type=pathlib.Path,
                        help="extracted published alpha.5 binary used for beta same-schema compatibility")
    parser.add_argument("--report", type=pathlib.Path, help="write per-case JSON report, also on failure")
    parser.add_argument("--evidence-fixture", type=pathlib.Path,
                        help="explicit prebuilt offline fixture writer; required for full browser acceptance")
    parser.add_argument("--skip-browser", action="store_true", help="record browser cases as skipped; not full acceptance")
    return execute(parser.parse_args())


if __name__ == "__main__":
    sys.exit(main())
