#!/usr/bin/env python3
"""Run a bounded, balanced repeated series using prepared Findrail binaries."""
import argparse
import ctypes
import hashlib
import json
import math
import os
import platform
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path

MAX_OUTPUT = 16 << 20
MAX_CHILDREN = 1
MAX_STDERR = 4096
OUTPUT_RESERVE = 65536


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def nearest(values, percentile):
    if not values:
        return None
    return sorted(values)[math.ceil(percentile * len(values)) - 1]


def summary(values):
    if not values:
        return {"p50": None, "p95": None}
    ordered = sorted(values)
    middle = len(ordered) // 2
    p50 = ordered[middle] if len(ordered) % 2 else (ordered[middle - 1] + ordered[middle]) / 2
    return {"p50": round(p50, 3), "p95": round(nearest(values, .95), 3)}


def repeat_order(roles, repeat_index):
    return list(roles) if repeat_index % 2 == 0 else list(reversed(roles))


def qualification(two_binaries, provenance_verified=False):
    if not two_binaries:
        return "unavailable"
    return "same_environment" if provenance_verified else "reference_only"


def clean_json(path):
    def pairs(values):
        result = {}
        for key, value in values:
            if key in result:
                raise ValueError("duplicate_json_key")
            result[key] = value
        return result
    def constant(_):
        raise ValueError("nonfinite_json")
    return json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=pairs, parse_constant=constant)


def validate_samples(sidecar, report, expected_sha, expected_count, expected_documents, expected_seed):
    if sidecar.get("schema") != 1 or sidecar.get("kind") != "search_warm_samples" or sidecar.get("status") != "success":
        raise ValueError("invalid_samples_status")
    if sidecar.get("units") != "ms" or sidecar.get("warmup") != {"excluded": True, "count_per_family": 1}:
        raise ValueError("invalid_samples_method")
    if sidecar.get("binary", {}).get("sha256") != expected_sha or report.get("binary", {}).get("sha256") != expected_sha:
        raise ValueError("binary_hash_mismatch")
    fixture_hash = report.get("fixture", {}).get("corpus_sha256")
    fixture = report.get("fixture", {})
    if (not isinstance(fixture_hash, str) or len(fixture_hash) != 64 or
            any(char not in "0123456789abcdef" for char in fixture_hash) or
            (fixture.get("version"), fixture.get("seed"), fixture.get("documents_requested")) !=
            (2, expected_seed, expected_documents) or
            report.get("workload", {}).get("indexed_documents") != expected_documents or
            sidecar.get("fixture", {}).get("corpus_sha256") != fixture_hash):
        raise ValueError("samples_fixture_mismatch")
    families = sidecar.get("families")
    if not isinstance(families, dict) or set(families) != {"common", "rare"}:
        raise ValueError("invalid_samples_families")
    checked = {}
    for family, data in families.items():
        values = data.get("samples") if isinstance(data, dict) else None
        if not isinstance(values, list) or len(values) != expected_count or data.get("count") != expected_count:
            raise ValueError("incomplete_samples_" + family)
        if any(type(value) not in (int, float) or not math.isfinite(value) or value < 0 for value in values):
            raise ValueError("invalid_sample_value_" + family)
        stats = summary(values)
        if data.get("p50") != stats["p50"] or data.get("p95") != stats["p95"]:
            raise ValueError("sample_percentile_mismatch_" + family)
        checked[family] = values
    return checked


def output_bytes(directory):
    return sum(path.stat().st_size for path in directory.iterdir() if path.is_file())


def configure_parent_death():
    """Linux child exits on abrupt orchestrator death; its shared group cleans descendants."""
    if platform.system() != "Linux":
        return
    parent = os.getppid()
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.prctl(1, signal.SIGTERM, 0, 0, 0) != 0:  # PR_SET_PDEATHSIG
        raise OSError(ctypes.get_errno(), "prctl parent-death signal failed")
    if os.getppid() != parent:
        os.kill(os.getpid(), signal.SIGTERM)


def stop(child):
    if child.poll() is None:
        if os.name == "posix":
            try:
                os.killpg(child.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
        else:
            child.terminate()
        try:
            child.wait(timeout=3)
        except subprocess.TimeoutExpired:
            if os.name == "posix":
                try:
                    os.killpg(child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            else:
                child.kill()
            child.wait(timeout=3)


def run_child(command, deadline):
    if time.monotonic() >= deadline:
        raise TimeoutError("wall_deadline")
    with tempfile.TemporaryFile() as stdout, tempfile.TemporaryFile() as stderr:
        child = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=stdout,
                                 stderr=stderr, start_new_session=(os.name == "posix"),
                                 preexec_fn=configure_parent_death if platform.system() == "Linux" else None)
        try:
            child.wait(timeout=max(.01, deadline - time.monotonic()))
            if child.returncode:
                stderr.seek(0, os.SEEK_END); size = stderr.tell()
                stderr.seek(max(0, size - MAX_STDERR))
                raise RuntimeError("child_failed_" + str(child.returncode) + ":" +
                                   hashlib.sha256(stderr.read(MAX_STDERR)).hexdigest())
            stdout.seek(0)
            if os.fstat(stdout.fileno()).st_size > 65536:
                raise RuntimeError("child_stdout_limit")
            return stdout.read(65536)
        except subprocess.TimeoutExpired:
            raise TimeoutError("child_deadline")
        finally:
            stop(child)


def main(argv=None):
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--binary", required=True, type=Path)
    p.add_argument("--baseline", type=Path)
    p.add_argument("--binary-sha256")
    p.add_argument("--baseline-sha256")
    p.add_argument("--output", required=True, type=Path)
    p.add_argument("--documents", nargs="+", type=int, choices=(100, 1000, 10000, 100000), default=(10000, 100000))
    p.add_argument("--repeats", type=int, default=3)
    p.add_argument("--samples-per-family", type=int, default=100)
    p.add_argument("--seed", type=int, default=42)
    p.add_argument("--allow-large", action="store_true")
    p.add_argument("--deadline-seconds", type=int, default=2700)
    a = p.parse_args(argv)
    if not 1 <= a.repeats <= 3 or not 1 <= a.samples_per_family <= 100: p.error("repeats must be 1..3 and samples 1..100")
    if not 1 <= a.deadline_seconds <= 3600: p.error("deadline must be 1..3600 seconds")
    if 100000 in a.documents and not a.allow_large: p.error("100000 documents requires --allow-large")
    if len(set(a.documents)) != len(a.documents): p.error("document sizes must not repeat")
    binaries = {"candidate": a.binary.resolve(strict=True)}
    if a.baseline: binaries["baseline"] = a.baseline.resolve(strict=True)
    identities = {}
    for role, path in binaries.items():
        actual = digest(path)
        expected = a.binary_sha256 if role == "candidate" else a.baseline_sha256
        if expected and expected.lower() != actual: p.error(role + " SHA-256 mismatch")
        identities[role] = {"sha256": actual, "version": "unavailable", "revision": "unavailable",
                            "dirty": "unavailable", "toolchain": "unavailable"}
    env = {"os": platform.system(), "arch": platform.machine(), "python": platform.python_version(),
           "processor": (platform.processor() or "unavailable")[:100]}
    order = ["candidate", "baseline"] if a.baseline else ["candidate"]
    records, errors = [], []
    output = a.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    if any(output.iterdir()):
        p.error("output directory must be empty")
    deadline = time.monotonic() + a.deadline_seconds
    try:
        for size in a.documents:
            for repeat in range(a.repeats):
                roles = repeat_order(order, repeat)
                for role in roles:
                    stem = f"{size}-{repeat + 1}-{role}"
                    report_path = output / (stem + ".json")
                    samples_path = output / (stem + "-samples.json")
                    run_dir = Path(tempfile.mkdtemp(prefix="findrail-series-"))
                    cmd = [sys.executable, str(Path(__file__).with_name("search_resource_check.py")),
                           "--binary", str(binaries[role]), "--documents", str(size), "--seed", str(a.seed),
                           "--queries", str(a.samples_per_family), "--report", str(report_path),
                           "--samples-report", str(samples_path)]
                    if size == 100000: cmd.append("--allow-large")
                    started = time.monotonic()
                    try:
                        run_child(cmd, deadline)
                        report = clean_json(report_path)
                        sidecar = clean_json(samples_path)
                        observed = report.get("binary", {})
                        identities[role].update({"version": observed.get("version", "unavailable"),
                                                 "revision": observed.get("revision", "unavailable"),
                                                 "go_version": observed.get("go_version", "unavailable"),
                                                 "schema": observed.get("schema", "unavailable")})
                        status, reason = report.get("status"), None
                        if status != "success" or report.get("errors") != []:
                            reason = "child_report_failed"
                        checked_samples = validate_samples(sidecar, report, identities[role]["sha256"],
                                                           a.samples_per_family, size, a.seed)
                        prior = next((r for r in records if r["size"] == size), None)
                        if prior and prior.get("corpus_sha256") != report.get("fixture", {}).get("corpus_sha256"):
                            reason = "corpus_mismatch"
                        if reason: status = "incomplete"
                        rec = {"size": size, "repeat": repeat + 1, "role": role,
                               "corpus_sha256": report.get("fixture", {}).get("corpus_sha256"),
                               "duration_seconds": round(time.monotonic() - started, 3),
                               "status": status, "reason": reason,
                               "report": report_path.name if report_path.exists() else None,
                               "samples": samples_path.name if samples_path.exists() else None}
                        for key in ("report", "samples"):
                            if rec[key]: rec[key + "_sha256"] = digest(output / rec[key])
                        records.append(rec)
                        rec["observed_samples"] = {key: len(values) for key, values in checked_samples.items()}
                    except (OSError, ValueError, RuntimeError, TimeoutError, RecursionError) as exc:
                        code = str(exc).split(":", 1)[0][:80] or "child_error"
                        errors.append(code)
                        rec = {"size": size, "repeat": repeat + 1, "role": role,
                               "corpus_sha256": None, "duration_seconds": round(time.monotonic() - started, 3),
                               "status": "incomplete", "reason": code,
                               "report": report_path.name if report_path.exists() else None,
                               "samples": samples_path.name if samples_path.exists() else None}
                        for key in ("report", "samples"):
                            if rec[key]: rec[key + "_sha256"] = digest(output / rec[key])
                        records.append(rec)
                        if isinstance(exc, TimeoutError) or code in ("wall_deadline", "child_deadline", "output_budget_exceeded"):
                            raise
                    finally:
                        import shutil
                        shutil.rmtree(run_dir, ignore_errors=True)
                    if output_bytes(output) > MAX_OUTPUT - OUTPUT_RESERVE:
                        for path in (report_path, samples_path):
                            path.unlink(missing_ok=True)
                        if records and records[-1]["size"] == size and records[-1]["repeat"] == repeat + 1 and records[-1]["role"] == role:
                            records.pop()
                        raise RuntimeError("output_budget_exceeded")
    except KeyboardInterrupt:
        errors.append("interrupted")
    except (OSError, ValueError, RuntimeError, TimeoutError, RecursionError) as exc:
        errors.append(str(exc).split(":", 1)[0][:80])
    summaries = []
    for size in a.documents:
        for role in order:
            for family in ("common", "rare"):
                runs = []
                pooled = []
                for record in records:
                    if record["size"] != size or record["role"] != role or record.get("status") != "success" or not record.get("samples"): continue
                    try:
                        data = clean_json(output / record["samples"])
                        values = data["families"][family]["samples"]
                        if any(type(v) not in (int, float) or not math.isfinite(v) or v < 0 for v in values): raise ValueError()
                        record[family + "_count"] = len(values)
                        record[family + "_p95"] = data["families"][family]["p95"]
                        runs.append(data["families"][family]["p95"])
                        pooled.extend(values)
                    except (KeyError, TypeError, ValueError):
                        record["reason"] = "malformed_samples"
                summaries.append({"size": size, "role": role, "family": family,
                                  "completed_runs": len(runs), "expected_runs": a.repeats,
                                  "median_run_p95": round(nearest(runs, .5), 3) if runs else None,
                                  "min_run_p95": min(runs) if runs else None, "max_run_p95": max(runs) if runs else None,
                                  "pooled_count": len(pooled), "pooled_p95": round(nearest(pooled, .95), 3) if pooled else None})
    pair_ratios = []
    if a.baseline:
        for size in a.documents:
            for repeat in range(1, a.repeats + 1):
                for family in ("common", "rare"):
                    pair = {r["role"]: r.get(family + "_p95") for r in records if r["size"] == size and r["repeat"] == repeat}
                    if pair.get("baseline") and pair.get("candidate"):
                        pair_ratios.append({"size": size, "repeat": repeat, "family": family,
                                            "candidate_to_baseline_p95": round(pair["candidate"] / pair["baseline"], 6)})
    # Build flags, dirty state, and toolchain provenance cannot be certified by
    # the resource report; never imply a causal source comparison.
    comparable = False
    complete = len(records) == len(a.documents) * a.repeats * len(order) and not errors and all(r["status"] == "success" for r in records)
    obj = {"schema": 1, "kind": "performance_series", "status": "passed" if complete else "incomplete" if records else "failed",
           "identity": identities, "environment": env, "qualification": qualification(len(order) == 2, comparable),
           "measurement": {"method": "warm-process HTTP latency", "order": "AB,BA,AB" if a.baseline else "candidate only",
                            "warmup": "first request per family excluded", "os_page_cache": "not reset; not cold-cache", "child_concurrency_limit": MAX_CHILDREN,
                           "wall_deadline_seconds": a.deadline_seconds, "output_budget_bytes": MAX_OUTPUT},
           "runs": records, "summary": summaries, "paired_ratios": pair_ratios,
           "errors": errors, "observed_runs": len(records), "expected_runs": len(a.documents) * a.repeats * len(order)}
    raw = json.dumps(obj, sort_keys=True, separators=(",", ":"), allow_nan=False).encode() + b"\n"
    if output_bytes(output) + len(raw) > MAX_OUTPUT:
        obj["status"] = "incomplete"
        if "output_budget_exceeded" not in obj["errors"]: obj["errors"].append("output_budget_exceeded")
        raw = json.dumps(obj, sort_keys=True, separators=(",", ":"), allow_nan=False).encode() + b"\n"
    (output / "series.json").write_bytes(raw)
    return 0 if complete and obj["status"] == "passed" and "output_budget_exceeded" not in obj["errors"] else 1


if __name__ == "__main__":
    def canceled(_signum, _frame):
        raise KeyboardInterrupt("interrupted")
    signal.signal(signal.SIGTERM, canceled)
    raise SystemExit(main())
