#!/usr/bin/env python3
"""Offline, privacy-preserving intake for voluntary human evidence drafts."""
import argparse
import json
import math
import pathlib
import sys

import readiness

MAX_JSON = 1 << 20
MAX_USAGE = 100
CASES = readiness.MANUAL_CASES
COMMON = {"kind", "reference", "candidate_sha", "candidate_version", "target", "archive_sha256",
          "evidence_type", "cases", "return_use", "retrieval_one", "retrieval_two",
          "collection_restart", "status", "obstacle", "observer", "date", "notes",
          "schema_version", "client_api_version", "manifest_sha256", "checksums_sha256"}


def fail(condition):
    if not condition:
        raise ValueError("invalid report")


def pairs(items):
    result = {}
    for key, value in items:
        fail(key not in result)
        result[key] = value
    return result


def parse(path):
    with pathlib.Path(path).open("rb") as stream:
        raw = stream.read(MAX_JSON + 1)
    fail(len(raw) <= MAX_JSON)
    def finite(value):
        number = float(value)
        fail(math.isfinite(number))
        return number
    value = json.loads(raw, object_pairs_hook=pairs,
                       parse_constant=lambda _: (_ for _ in ()).throw(ValueError("invalid report")),
                       parse_float=finite)
    fail(type(value) is dict)
    return value, raw


def bounded_text(value, maximum=2000):
    fail(value is None or (type(value) is str and len(value) <= maximum))


def reference(value):
    fail(type(value) is str and readiness.REFERENCE.fullmatch(value) is not None)


def validate(report, candidate, native):
    fail(set(report) <= COMMON)
    kind = report.get("kind")
    fail(kind in ("windows_first_run", "apple_silicon_first_run", "linux_first_run", "voluntary_usage"))
    fail(report.get("candidate_sha") == candidate["candidate_sha"] and
         report.get("candidate_version") == candidate["candidate_version"])
    # TASK24's generated kit carries these additional identity fields when present.
    if "schema_version" in report:
        fail(type(report["schema_version"]) is int and report["schema_version"] == candidate["schema_version"])
    if "client_api_version" in report:
        fail(type(report["client_api_version"]) is int and report["client_api_version"] == candidate["client_api_version"])
    assets = {item["basename"]: item["sha256"] for item in candidate["assets"]}
    if "manifest_sha256" in report:
        fail(report["manifest_sha256"] == assets["release-manifest.json"])
    if "checksums_sha256" in report:
        fail(report["checksums_sha256"] == assets["SHA256SUMS.txt"])
    for field in ("observer", "date", "notes"):
        bounded_text(report.get(field), 500)
    if not (kind == "linux_first_run" and report.get("reference") == "" and
            report.get("status") in ("not_run", "incomplete")):
        reference(report.get("reference"))
    bounded_text(report.get("obstacle"))
    if kind == "voluntary_usage":
        fail(set(report) <= {"kind", "reference", "candidate_sha", "candidate_version", "evidence_type",
                             "return_use", "retrieval_one", "retrieval_two", "collection_restart", "obstacle",
                             "status", "observer", "date", "notes"})
        fail(report.get("evidence_type") == "manual")
        overall_status = report.get("status", "passed")
        fail(overall_status in ("passed", "failed", "not_run", "incomplete"))
        fail(type(report.get("return_use")) is bool)
        fail(report.get("retrieval_one") in ("found", "not_found", "not_tried"))
        fail(report.get("retrieval_two") in ("found", "not_found", "not_tried"))
        fail(report.get("collection_restart") in ("restored", "not_restored", "not_tried"))
        if overall_status == "failed":
            return "failed"
        complete = (overall_status == "passed" and report.get("return_use") is True and
                    report.get("retrieval_one") != "not_tried" and report.get("retrieval_two") != "not_tried" and
                    report.get("collection_restart") != "not_tried")
        return "complete" if complete else "incomplete"
    target = {"windows_first_run": "windows-amd64", "apple_silicon_first_run": "darwin-arm64",
              "linux_first_run": "linux-amd64"}[kind]
    fail(report.get("target") == target and report.get("evidence_type") == "manual")
    fail(report.get("archive_sha256") == native[target]["archive"]["sha256"])
    overall_status = report.get("status")
    fail(overall_status in ("passed", "failed", "not_run", "incomplete"))
    cases = report.get("cases")
    fail(type(cases) is list and len(cases) == len(CASES) and all(type(c) is dict and set(c) <= {"case", "id", "status", "elapsed_seconds", "obstacle", "observation"} for c in cases))
    names = [c.get("case", c.get("id")) for c in cases]
    fail(all(not ("case" in c and "id" in c) for c in cases) and names == list(CASES))
    complete = overall_status == "passed"
    has_failure = overall_status == "failed"
    for case in cases:
        fail(case.get("status") in ("passed", "failed", "not_run"))
        bounded_text(case.get("obstacle", case.get("observation")), 2000)
        elapsed = case.get("elapsed_seconds")
        fail(elapsed is None or (type(elapsed) in (int, float) and math.isfinite(elapsed) and elapsed >= 0))
        complete = complete and case["status"] == "passed" and elapsed is not None
        has_failure = has_failure or case["status"] == "failed"
    return "failed" if has_failure else "complete" if complete else "incomplete"


def safe_manual_cases(report):
    """Export ordered outcomes and timings, never free-form observations."""
    return [{"case": case.get("case", case.get("id")), "status": case["status"],
             "elapsed_seconds": case.get("elapsed_seconds")}
            for case in report["cases"]]


def safe_usage_observation(report, status):
    return {"reference": report["reference"], "evidence_type": report["evidence_type"],
            "return_use": report["return_use"], "retrieval_one": report["retrieval_one"],
            "retrieval_two": report["retrieval_two"], "collection_restart": report["collection_restart"],
            "status": status}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--candidate", required=True, type=pathlib.Path)
    parser.add_argument("--report", required=True, action="append", type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args(argv)
    out = args.output
    supplemental = out / "supplemental.json"
    linux_observations = out / "linux-observations.json"
    usage_observations_path = out / "usage-observations.json"
    # Remove prior success before processing: failed reruns must never leave stale evidence.
    supplemental.unlink(missing_ok=True)
    linux_observations.unlink(missing_ok=True)
    usage_observations_path.unlink(missing_ok=True)
    issues, refs = [], []
    records, kinds = [], set()
    linux_records, usage_records = [], []
    linux_failed = usage_failed = 0
    counts = {"received": len(args.report), "complete": 0, "incomplete": 0, "failed": 0, "mismatch": 0}
    candidate = native = None
    try:
        raw_candidate, _ = parse(args.candidate)
        native = readiness.strict_candidate(raw_candidate)
        candidate = raw_candidate
        fail(1 <= len(args.report) <= MAX_USAGE)
    except (ValueError, KeyError, TypeError, OSError, RecursionError):
        counts["failed"] = len(args.report)
        issues.append("candidate_invalid")
    if candidate is not None:
        for path in args.report:
            try:
                report, _ = parse(path)
                identity = (report.get("candidate_sha"), report.get("candidate_version"))
                kind = report.get("kind")
                expected_target = {"windows_first_run": "windows-amd64",
                                   "apple_silicon_first_run": "darwin-arm64",
                                   "linux_first_run": "linux-amd64"}.get(kind)
                assets = {item["basename"]: item["sha256"] for item in candidate["assets"]}
                native_identity_mismatch = expected_target is not None and (
                    report.get("target") != expected_target or
                    report.get("archive_sha256") != native[expected_target]["archive"]["sha256"])
                summary_identity_mismatch = (
                    ("schema_version" in report and report["schema_version"] != candidate["schema_version"]) or
                    ("client_api_version" in report and report["client_api_version"] != candidate["client_api_version"]) or
                    ("manifest_sha256" in report and report["manifest_sha256"] != assets["release-manifest.json"]) or
                    ("checksums_sha256" in report and report["checksums_sha256"] != assets["SHA256SUMS.txt"]))
                if (identity != (candidate["candidate_sha"], candidate["candidate_version"]) or
                        native_identity_mismatch or summary_identity_mismatch):
                    counts["mismatch"] += 1
                    issues.append("candidate_mismatch")
                    continue
                outcome = validate(report, candidate, native)
                ref = report["reference"]
                if ref and ref in refs:
                    issues.append("duplicate_reference")
                    counts["failed"] += 1
                    continue
                if ref:
                    refs.append(ref)
                kinds.add(kind)
                if outcome == "failed":
                    counts["failed"] += 1
                    issues.append("report_failed")
                    if kind == "linux_first_run":
                        linux_failed += 1
                        linux_records.append({"status": "failed", "target": report["target"],
                                              "archive_sha256": report["archive_sha256"], "reference": ref,
                                              "cases": safe_manual_cases(report)})
                    elif kind == "voluntary_usage":
                        usage_failed += 1
                        usage_records.append(safe_usage_observation(report, "failed"))
                    continue
                if outcome == "complete":
                    counts["complete"] += 1
                    # Export only fields accepted by readiness; no prose or draft markers.
                    if kind == "voluntary_usage":
                        record = {key: report[key] for key in ("reference", "evidence_type", "return_use", "retrieval_one", "retrieval_two", "collection_restart")}
                        usage_records.append({**record, "status": "complete"})
                    elif kind == "linux_first_run":
                        linux_records.append({"status": "complete", "target": report["target"],
                                              "archive_sha256": report["archive_sha256"], "reference": ref,
                                              "cases": safe_manual_cases(report)})
                    else:
                        record = {key: report[key] for key in ("evidence_type", "target", "archive_sha256", "reference", "cases")}
                        record.update(status="passed", candidate_sha=candidate["candidate_sha"], candidate_version=candidate["candidate_version"])
                        record["cases"] = [{"case": c.get("case", c.get("id")), "status": "passed", "elapsed_seconds": c["elapsed_seconds"]} for c in report["cases"]]
                    if kind != "linux_first_run":
                        records.append({"kind": kind, **record})
                else:
                    counts["incomplete"] += 1
                    issues.append("report_incomplete")
                    if kind == "linux_first_run":
                        linux_records.append({"status": "incomplete", "target": report["target"],
                                              "archive_sha256": report["archive_sha256"], "reference": ref,
                                              "cases": safe_manual_cases(report)})
                    elif kind == "voluntary_usage":
                        usage_records.append(safe_usage_observation(report, "incomplete"))
            except (ValueError, KeyError, TypeError, OSError, RecursionError):
                counts["failed"] += 1
                issues.append("report_invalid")
    # Usage drafts are independently counted and preserved; supplemental format groups them.
    usage = [r for r in records if r["kind"] == "voluntary_usage"]
    if len(kinds) > 1:
        issues.append("mixed_report_kinds")
    ready = (counts["failed"] == counts["incomplete"] == counts["mismatch"] == 0 and
             counts["complete"] > 0 and len(kinds) == 1 and "linux_first_run" not in kinds)
    if ready:
        kind = next(iter(kinds))
        payload = ({"kind": kind, "candidate_sha": candidate["candidate_sha"],
                    "candidate_version": candidate["candidate_version"], "reports": usage} if kind == "voluntary_usage" else records[0])
        if kind != "voluntary_usage":
            payload = records[0]
        out.mkdir(parents=True, exist_ok=True)
        supplemental.write_text(json.dumps(payload, indent=2, sort_keys=True, allow_nan=False) + "\n", encoding="utf-8")
    linux_only = (kinds == {"linux_first_run"} and counts["mismatch"] == 0 and
                  counts["failed"] == linux_failed and
                  counts["complete"] + counts["incomplete"] + linux_failed == counts["received"] and
                  bool(linux_records))
    linux_complete = linux_only and counts["incomplete"] == counts["failed"] == 0 and counts["complete"] > 0
    usage_observations_only = (kinds == {"voluntary_usage"} and not ready and
                               counts["mismatch"] == 0 and counts["failed"] == usage_failed and
                               counts["complete"] + counts["incomplete"] + usage_failed == counts["received"] and
                               bool(usage_records))
    outputs = ["supplemental.json"] if ready else []
    if linux_only:
        payload = {"format_version": 1, "kind": "linux_first_run",
                   "candidate_sha": candidate["candidate_sha"],
                   "candidate_version": candidate["candidate_version"],
                   "observations": linux_records}
        out.mkdir(parents=True, exist_ok=True)
        linux_observations.write_text(json.dumps(payload, indent=2, sort_keys=True, allow_nan=False) + "\n", encoding="utf-8")
        outputs.append("linux-observations.json")
    if usage_observations_only:
        payload = {"format_version": 1, "kind": "voluntary_usage",
                   "candidate_sha": candidate["candidate_sha"],
                   "candidate_version": candidate["candidate_version"],
                   "observations": usage_records}
        out.mkdir(parents=True, exist_ok=True)
        usage_observations_path.write_text(json.dumps(payload, indent=2, sort_keys=True, allow_nan=False) + "\n", encoding="utf-8")
        outputs.append("usage-observations.json")
    if ready or linux_complete:
        status = "recorded"
    elif counts["failed"] or counts["mismatch"] or len(kinds) > 1:
        status = "blocked"
    elif counts["complete"] or counts["incomplete"]:
        status = "incomplete"
    else:
        status = "blocked"
    summary = {"status": status, "counts": counts, "issues": sorted(set(issues)), "references": sorted(set(refs)),
               "outputs": outputs, "release_decision": False}
    out.mkdir(parents=True, exist_ok=True)
    (out / "intake.json").write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(json.dumps(summary, sort_keys=True))
    return 0 if ready or linux_complete else 1


if __name__ == "__main__":
    sys.exit(main())
