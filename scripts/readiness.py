#!/usr/bin/env python3
"""Fail-closed beta/v1 readiness classification for retained evidence records.

Records contain provenance and bounded measurements only; this tool never accepts
or emits indexed content, browser profiles, or raw logs.
"""
import argparse
import json
import math
import pathlib
import re


SHA = re.compile(r"^[0-9a-f]{40}$")
HASH = re.compile(r"^[0-9a-f]{64}$")
SCALE_CASES = ("10k", "100k")
NATIVE_TARGETS = ("linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64")


def _json(path):
    path = pathlib.Path(path)
    if path.stat().st_size > 1 << 20:
        raise ValueError("record exceeds 1 MiB")
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError("record must be an object")
    return value


def _valid_duration(value):
    return type(value) in (int, float) and math.isfinite(value) and value >= 0


def _valid_cases(report, required, sha):
    cases = report.get("cases")
    if not isinstance(cases, list):
        return False
    names = []
    for case in cases:
        if not isinstance(case, dict):
            return False
        name = case.get("case")
        names.append(name)
        if (case.get("status") != "passed" or not _valid_duration(case.get("elapsed_seconds")) or
                case.get("candidate_sha", sha) != sha):
            return False
    return len(names) == len(set(names)) and set(required).issubset(names)


def classify(candidate, supplemental=()):
    """Return independent software and release decisions; malformed inputs fail closed."""
    sha = candidate.get("candidate_sha")
    version = candidate.get("candidate_version")
    software_ok = (candidate.get("status") == "passed" and isinstance(sha, str) and
                   SHA.fullmatch(sha) is not None and isinstance(version, str) and bool(version) and
                   candidate.get("source_clean") is True)
    gaps = []
    if not software_ok:
        gaps.append("strict packaged candidate summary is missing, failed, or malformed")

    # These are additional TASK20 records, not a replacement for candidate_record's
    # strict 19/20/7 cases and four bounded resource records.
    reports = {}
    if isinstance(supplemental, dict):
        supplemental = (supplemental,)
    for item in supplemental:
        if not isinstance(item, dict) or not isinstance(item.get("kind"), str):
            gaps.append("malformed supplemental evidence")
            continue
        kind = item["kind"]
        if kind in reports:
            gaps.append("duplicate " + kind + " evidence")
            continue
        reports[kind] = item
        if item.get("candidate_sha") != sha or item.get("candidate_version") != version:
            gaps.append(kind + " evidence belongs to a different candidate")
        for key in ("binary_sha256", "archive_sha256", "fixture_sha256"):
            if key in item and not HASH.fullmatch(item[key] if isinstance(item[key], str) else ""):
                gaps.append(kind + " evidence has invalid " + key)
        if not _valid_duration(item.get("elapsed_seconds")):
            gaps.append(kind + " evidence has missing/invalid duration")
        if item.get("status") != "passed":
            gaps.append(kind + " evidence is not passed")
        if "required_cases" in item and not _valid_cases(item, item["required_cases"], sha):
            gaps.append(kind + " evidence has missing, duplicate, failed, skipped, or interrupted cases")

    required = ("freshness_browser", "crash_recovery", "installer_native", "scale_10k", "scale_100k")
    for kind in required:
        if kind not in reports:
            gaps.append(kind + " evidence is pending")

    native = reports.get("installer_native", {})
    targets = native.get("targets", [])
    if native and (not isinstance(targets, list) or sorted(targets) != sorted(NATIVE_TARGETS) or
                   len(targets) != len(set(targets))):
        gaps.append("installer-native evidence does not cover exactly five supported targets")

    # A baseline may intentionally be another binary, but must declare its role.
    for kind in SCALE_CASES:
        record = reports.get("scale_" + kind, {})
        if record and record.get("fixture_sha256") and record.get("baseline") is not None:
            baseline = record["baseline"]
            if not isinstance(baseline, dict) or baseline.get("role") != "reference":
                gaps.append("scale baseline must be explicitly marked reference")

    blockers = [gap for gap in gaps if "different candidate" in gap or "malformed" in gap or
                "invalid" in gap or "duplicate" in gap or "exactly five" in gap or
                "not passed" in gap or "incomplete, skipped" in gap]
    software_status = "passed" if software_ok and not blockers and all(
        reports.get(kind, {}).get("status") == "passed" for kind in required[:3]) else "pending"
    manual_complete = all(reports.get(k, {}).get("manual") is True
                          for k in ("windows_first_run", "apple_silicon_first_run"))
    usage_complete = bool(reports.get("voluntary_usage", {}).get("reports"))
    if blockers:
        release_status = "blocked"
    elif not (software_status == "passed" and all("scale_" + x in reports for x in SCALE_CASES)
              and manual_complete and usage_complete):
        release_status = "pending"
    else:
        release_status = "eligible-for-owner-decision"
    return {
        "report_version": 1,
        "candidate_sha": sha if isinstance(sha, str) else None,
        "candidate_version": version if isinstance(version, str) else None,
        "software_status": software_status,
        "release_readiness": release_status,
        "final_release": False,
        "evidence": {"scale_10k": "passed" if reports.get("scale_10k", {}).get("status") == "passed" else "pending",
                     "scale_100k": "passed" if reports.get("scale_100k", {}).get("status") == "passed" else "pending",
                     "manual_windows": "reported" if reports.get("windows_first_run", {}).get("manual") is True else "pending",
                     "manual_apple_silicon": "reported" if reports.get("apple_silicon_first_run", {}).get("manual") is True else "pending",
                     "voluntary_usage": "reported" if usage_complete else "pending"},
        "gaps": gaps,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--candidate", required=True, type=pathlib.Path)
    parser.add_argument("--evidence", action="append", type=pathlib.Path, default=[])
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()
    result = classify(_json(args.candidate), [_json(path) for path in args.evidence])
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(result["software_status"] + ": " + result["release_readiness"])
    # Pending is a valid decision record (not a successful release gate). Only
    # malformed input should fail the workflow; publication checks remain separate.
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
