#!/usr/bin/env python3
"""Linux abrupt-stop journey for an explicitly supplied Findrail candidate.

This script never builds or locates an application binary implicitly. It runs
the exact --binary argument against isolated synthetic source/data directories.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time


def atomic_report(path: Path, report: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".tmp")
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        json.dump(report, stream, separators=(",", ":"), sort_keys=True)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def invoke(binary: Path, args: list[str], timeout: float) -> subprocess.CompletedProcess:
    return subprocess.run([str(binary), *args], stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE, timeout=timeout, check=False)


def search_total(binary: Path, data: Path, query: str, timeout: float) -> int:
    result = invoke(binary, ["search", "--data-dir", str(data), "--json", query], timeout)
    if result.returncode:
        raise RuntimeError("candidate search failed")
    return int(json.loads(result.stdout)["total"])


def process_cpu_ticks(pid: int) -> int:
    fields = Path(f"/proc/{pid}/stat").read_text().split()
    return int(fields[13]) + int(fields[14])


def has_open_database(pid: int, db: Path) -> bool:
    wanted = str(db.resolve())
    try:
        for fd in Path(f"/proc/{pid}/fd").iterdir():
            try:
                if os.readlink(fd) == wanted:
                    return True
            except OSError:
                continue
    except OSError:
        return False
    return False


def run(args: argparse.Namespace) -> dict:
    if not sys.platform.startswith("linux"):
        raise RuntimeError("candidate SIGKILL journey is Linux-specific")
    binary = args.binary.resolve(strict=True)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise RuntimeError("candidate binary must be an executable file")
    digest = sha256_file(binary)
    if args.binary_sha256 and digest != args.binary_sha256:
        raise RuntimeError("candidate binary SHA-256 does not match the supplied identity")

    args.work_dir.mkdir(parents=True, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix="findrail-recovery-", dir=args.work_dir))
    data = work / "data"
    source = work / "synthetic-source"
    source.mkdir(mode=0o700)
    report = {"case": "candidate_refresh_sigkill", "status": "failed",
              "duration_ms": 0, "phase": "preparing", "binary_sha256": digest,
              "revision": args.revision, "schema": args.schema, "platform": "linux"}
    started = time.monotonic()
    proc = None
    try:
        note = source / "note.txt"
        note.write_text("candidateoldtoken\n" + "safe synthetic filler " * 500_000,
                        encoding="utf-8")
        seed = invoke(binary, ["index", "--data-dir", str(data), "--max-bytes",
                              "33554432", str(source)], args.deadline)
        if seed.returncode:
            raise RuntimeError("candidate could not seed the synthetic index")
        if search_total(binary, data, "candidateoldtoken", args.deadline) != 1:
            raise RuntimeError("candidate seed state did not verify")

        # Change indexed text and add/remove inventory entries before starting
        # the exact supplied executable. Whether its SQLite commit completed at
        # the kill point is deliberately treated as unknown.
        note.write_text("candidatenewtoken\n" + "different synthetic filler " * 500_000,
                        encoding="utf-8")
        (source / "added.txt").write_text("candidateaddedtoken", encoding="utf-8")
        before = set(source.iterdir())
        report["phase"] = "candidate_index_started"
        proc = subprocess.Popen([str(binary), "index", "--data-dir", str(data),
                                 "--max-bytes", "33554432", str(source)],
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                start_new_session=True)
        deadline = time.monotonic() + args.deadline
        observed = False
        while time.monotonic() < deadline:
            if proc.poll() is not None:
                raise RuntimeError("candidate completed before controlled termination")
            try:
                active = has_open_database(proc.pid, data / "findrail.db")
                cpu = process_cpu_ticks(proc.pid)
            except (FileNotFoundError, ProcessLookupError):
                continue
            if active and cpu > 0 and set(source.iterdir()) == before:
                observed = True
                break
        if not observed:
            raise RuntimeError("candidate did not reach the bounded active-process observation")
        report["phase"] = "terminated_commit_unknown"
        os.killpg(proc.pid, signal.SIGKILL)
        try:
            proc.wait(timeout=args.deadline)
        except subprocess.TimeoutExpired:
            os.killpg(proc.pid, signal.SIGKILL)
            raise RuntimeError("candidate process group did not terminate before deadline")

        report["phase"] = "reopened"
        doctor = invoke(binary, ["doctor", "--data-dir", str(data), "--integrity", "--json"], args.deadline)
        if doctor.returncode:
            raise RuntimeError("candidate doctor could not inspect the reopened index")
        doctor_report = json.loads(doctor.stdout)
        if doctor_report.get("revision") != args.revision:
            raise RuntimeError("candidate build revision differs from the supplied identity")
        if doctor_report.get("current_write_schema") != args.schema:
            raise RuntimeError("candidate write schema differs from the supplied identity")
        if doctor_report.get("index_schema") != args.schema:
            raise RuntimeError("reopened index schema differs from the supplied identity")
        checks = {item.get("id"): item.get("status") for item in doctor_report.get("checks", [])}
        if checks.get("integrity_quick_check") != "ok" or checks.get("integrity_foreign_key_check") != "ok":
            raise RuntimeError("read-only doctor integrity or foreign-key check did not pass")
        # The two possible complete snapshots are accepted. Mixed states are
        # rejected by checking the changed and added documents together.
        old = search_total(binary, data, "candidateoldtoken", args.deadline)
        new = search_total(binary, data, "candidatenewtoken", args.deadline)
        added = search_total(binary, data, "candidateaddedtoken", args.deadline)
        if (old, new, added) not in ((1, 0, 0), (0, 1, 1)):
            raise RuntimeError("reopened candidate index is neither the old nor new complete snapshot")
        report["status"] = "passed"
        report["phase"] = "verified_old_or_new_commit_unknown"
    finally:
        if proc is not None and proc.poll() is None:
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            try:
                proc.wait(timeout=args.deadline)
            except subprocess.TimeoutExpired:
                pass
        report["duration_ms"] = int((time.monotonic() - started) * 1000)
        # Deliberately leave synthetic data in place for diagnosis; caller
        # chooses work_dir and can remove only this uniquely created directory.
    return report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path,
                        help="exact extracted candidate executable; never rebuilt here")
    parser.add_argument("--binary-sha256", default="")
    parser.add_argument("--revision", required=True)
    parser.add_argument("--schema", required=True, type=int)
    parser.add_argument("--work-dir", required=True, type=Path,
                        help="dedicated parent for synthetic data and isolated index")
    parser.add_argument("--report", required=True, type=Path,
                        help="atomic content-free JSON report destination")
    parser.add_argument("--deadline", type=float, default=45.0)
    args = parser.parse_args()
    report = {"case": "candidate_refresh_sigkill", "status": "failed",
              "duration_ms": 0, "phase": "unexecuted", "revision": args.revision,
              "schema": args.schema, "platform": "linux"}
    started = time.monotonic()
    try:
        report["binary_sha256"] = sha256_file(args.binary.resolve(strict=True))
        report = run(args)
    except Exception as exc:
        # Only a stable classification enters evidence, never command output,
        # paths, exception text, or indexed content.
        report["status"] = "failed"
        report["phase"] = "journey_failed"
        report["failure_code"] = type(exc).__name__
        report["duration_ms"] = int((time.monotonic() - started) * 1000)
    atomic_report(args.report, report)
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
