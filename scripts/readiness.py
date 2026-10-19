#!/usr/bin/env python3
"""Classify retained candidate evidence without authorizing a release.

Consume the strict candidate_record summary and existing producer formats.
Only fixed status codes and provenance enter the output, never input text/logs.
"""
import argparse
import json
import math
import pathlib
import re

import candidate_record
import release_assets

SHA = re.compile(r"^[0-9a-f]{40}$")
HASH = re.compile(r"^[0-9a-f]{64}$")
REFERENCE = re.compile(r"^(?:https://github\.com/Lephiziel/findrail/issues/[1-9][0-9]*|sha256:[0-9a-f]{64})$")
FRESHNESS_CASES = (
    "freshness_hostile_source_text_and_narrow_viewport",
    "freshness_failed_refresh_preserves_selected_quote_dirty_note_and_results",
)
MANUAL_CASES = ("install", "launch", "repeat-launch", "retrieval", "collection-restart",
                "refresh", "quit", "uninstall-data-preserved")
SCALE_FIXTURES = {
    "scale_10k": (10000, "a2e69b1a1a28c5617ae93f6295578f105730ca235b47e15604dc2a21c4ab1c08"),
    "scale_100k": (100000, "5680cc710acd926ad0114c69169eb4d31de428c657b27a5850ce2ef4aae6714a"),
}
KINDS = ("crash_recovery", *SCALE_FIXTURES, "windows_first_run",
         "apple_silicon_first_run", "voluntary_usage")


def require(condition):
    if not condition:
        raise ValueError("invalid evidence")


def valid_hash(value):
    return isinstance(value, str) and HASH.fullmatch(value) is not None


def duration(value):
    return type(value) in (int, float) and math.isfinite(value) and value >= 0


def _json(path):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            require(key not in result)
            result[key] = value
        return result

    def constant(_value):
        raise ValueError("non-finite JSON number")

    def finite(value):
        number = float(value)
        require(math.isfinite(number))
        return number

    with pathlib.Path(path).open("rb") as stream:
        raw = stream.read((1 << 20) + 1)
    require(len(raw) <= 1 << 20)
    value = json.loads(raw, object_pairs_hook=unique, parse_constant=constant, parse_float=finite)
    require(isinstance(value, dict))
    return value


def strict_candidate(candidate, expected_sha=None, expected_version=None):
    """Recheck retained coverage; a bare status=passed is never a summary."""
    sha, version = candidate["candidate_sha"], candidate["candidate_version"]
    require(isinstance(sha, str) and SHA.fullmatch(sha) is not None)
    require(expected_sha is None or sha == expected_sha)
    require(expected_version is None or version == expected_version)
    require(candidate.get("status") == "passed" and candidate.get("source_clean") is True)
    require(type(candidate.get("schema_version")) is int and
            candidate["schema_version"] == release_assets.contract_schema(version))
    require(type(candidate.get("client_api_version")) is int and candidate["client_api_version"] == 1)
    require(candidate.get("extension_version") == "0.1.0")
    archives = release_assets.expected_archives(version)
    assets = candidate["assets"]
    require(isinstance(assets, list) and all(isinstance(a, dict) for a in assets))
    names = [a.get("basename") for a in assets]
    require(all(isinstance(n, str) for n in names) and len(set(names)) == len(names))
    require(set(names) == set(archives) | {release_assets.VSIX, "SHA256SUMS.txt", "release-manifest.json"})
    require(all(valid_hash(a.get("sha256")) and type(a.get("size")) is int and a["size"] > 0 for a in assets))
    by_name = {a["basename"]: a for a in assets}
    native = candidate["native"]
    require(isinstance(native, list) and all(isinstance(n, dict) for n in native))
    require(sorted(n.get("target", "") for n in native) == sorted(release_assets.TARGETS))
    for record in native:
        target = record["target"]
        require(record.get("source_clean") is True)
        require((record.get("candidate_sha"), record.get("candidate_version"), record.get("status"),
                 record.get("source_clean"), record.get("observed_target")) == (sha, version, "passed", True, target))
        info = record["build_info"]
        require(info.get("dirty") is False and type(info.get("schema_version")) is int)
        require((info.get("revision"), info.get("version"), info.get("target"), info.get("dirty"),
                 info.get("schema_version")) == (sha, version, target, False, candidate["schema_version"]))
        require(valid_hash(record.get("binary_sha256")) and valid_hash(record.get("desktop_binary_sha256")))
        archive = record["archive"]
        require(archives.get(archive.get("basename")) == target and archive == by_name[archive["basename"]])
        candidate_record.require_installer_lifecycle(record)
    linux = next(n for n in native if n["target"] == "linux-amd64")
    candidate_record.require_resources(linux, sha)
    candidate_record.require_journey(candidate["journey"], sha, version, linux["binary_sha256"])
    common = next(c for c in candidate["journey"]["cases"] if c["case"] == "packaged_browser_journey.py")["details"]
    candidate_record.require_cases(common["cases"], FRESHNESS_CASES, "freshness browser")
    return {n["target"]: n for n in native}


def check_recovery(item, candidate, native):
    record = item["measurement"]
    require((record.get("case"), record.get("status"), record.get("revision"), record.get("schema"),
             record.get("platform"), record.get("phase"), record.get("binary_sha256")) ==
            ("candidate_refresh_sigkill", "passed", candidate["candidate_sha"], candidate["schema_version"],
             "linux", "verified_old_or_new_commit_unknown", native["linux-amd64"]["binary_sha256"]))
    require(type(record.get("duration_ms")) is int and 0 <= record["duration_ms"] <= 125000)


def check_scale(item, candidate, native):
    record = item["measurement"]
    count, fixture_hash = SCALE_FIXTURES[item["kind"]]
    require(record.get("schema") == 1 and record.get("status") == "success" and record.get("errors") == [])
    binary, fixture, workload, metrics = (record[k] for k in ("binary", "fixture", "workload", "metrics"))
    require((binary.get("revision"), binary.get("version"), binary.get("schema"), binary.get("sha256")) ==
            (candidate["candidate_sha"], candidate["candidate_version"], candidate["schema_version"],
             native["linux-amd64"]["binary_sha256"]))
    require((fixture.get("version"), fixture.get("seed"), fixture.get("documents_requested"), fixture.get("corpus_sha256")) ==
            (2, 42, count, fixture_hash))
    require(workload.get("indexed_documents") == count and workload.get("skipped_documents") == 0)
    require(record["environment"].get("os") == "Linux" and record["environment"].get("arch") in ("x86_64", "amd64"))
    for name in ("index_wall_ms", "server_startup_ms", "first_query_after_server_start_ms",
                 "database_bytes_after_index_before_search"):
        require(duration(metrics.get(name)))
    require(metrics.get("known_result_shape") is True and type(metrics.get("search_repeats")) is int and metrics["search_repeats"] >= 2)
    for name in ("p50", "p95"):
        require(duration(metrics["warm_http_request_latency_ms"].get(name)))
    paging = metrics["paging_window"]
    require((paging.get("window_limit"), paging.get("returned"), paging.get("total")) == (1000, 1000, count // 2))
    for limit in ("20", "50", "100"):
        page = paging["page_sizes"][limit]
        require(page.get("returned") == 1000 and duration(page.get("max_response_bytes")) and 0 < page["max_response_bytes"] <= 512 << 10)
        require(duration(page["latency_ms"].get("p95")))
    refresh = metrics["refresh_cycle"]
    require(refresh.get("canceled_scan_preserved_previous_content") is True and refresh.get("concurrent_read_errors") == 0)
    require(type(refresh.get("concurrent_read_samples")) is int and refresh["concurrent_read_samples"] > 0)
    for name in ("successful_refresh_wall_ms", "canceled_refresh_wall_ms"):
        require(duration(refresh.get(name)))
    if "comparison" in record:
        comparison = record["comparison"]
        require(item.get("baseline_role") == "reference" and comparison.get("same_host") is True and
                comparison.get("fixture_sha256_matches") is True)
        require(valid_hash(comparison["binary"].get("sha256")) and
                isinstance(comparison["binary"].get("revision"), str) and SHA.fullmatch(comparison["binary"]["revision"]) is not None)


def check_manual(item, candidate, native):
    target = "windows-amd64" if item["kind"] == "windows_first_run" else "darwin-arm64"
    require(item.get("evidence_type") == "manual" and item.get("status") == "passed" and item.get("target") == target)
    require((item.get("candidate_sha"), item.get("candidate_version"), item.get("archive_sha256")) ==
            (candidate["candidate_sha"], candidate["candidate_version"], native[target]["archive"]["sha256"]))
    require(isinstance(item.get("reference"), str) and REFERENCE.fullmatch(item["reference"]) is not None)
    candidate_record.require_cases(item.get("cases"), MANUAL_CASES, "manual first run")


def check_usage(item, candidate):
    require((item.get("candidate_sha"), item.get("candidate_version")) ==
            (candidate["candidate_sha"], candidate["candidate_version"]))
    records = item["reports"]
    require(isinstance(records, list) and 0 < len(records) <= 100 and all(isinstance(r, dict) for r in records))
    references = []
    for record in records:
        reference = record.get("reference")
        require(isinstance(reference, str) and REFERENCE.fullmatch(reference) is not None)
        references.append(reference)
        require(record.get("evidence_type") == "manual" and record.get("return_use") is True and
                record.get("retrieval_one") in ("found", "not_found") and record.get("retrieval_two") in ("found", "not_found") and
                record.get("collection_restart") in ("restored", "not_restored"))
    require(len(references) == len(set(references)))


def classify(candidate, supplemental=(), *, candidate_sha=None, candidate_version=None):
    """Missing evidence is pending; supplied invalid evidence is blocked."""
    evidence = {k: "pending" for k in ("strict_candidate", "freshness_browser", "installer_native", *KINDS)}
    gaps, blocked, software_failed = [], False, False
    native = None
    try:
        native = strict_candidate(candidate, candidate_sha, candidate_version)
        for kind in ("strict_candidate", "freshness_browser", "installer_native"):
            evidence[kind] = "passed"
    except (ValueError, KeyError, TypeError, AttributeError, StopIteration):
        blocked = True
        software_failed = True
        evidence["strict_candidate"] = "invalid"
        gaps.append("strict_candidate_invalid")
    if not isinstance(supplemental, (tuple, list)) or len(supplemental) > len(KINDS):
        supplemental = ()
        blocked = True
        gaps.append("supplemental_invalid")
    seen = set()
    for item in supplemental:
        kind = item.get("kind") if isinstance(item, dict) else None
        if not isinstance(kind, str) or kind not in KINDS or kind in seen:
            blocked = True
            if isinstance(kind, str) and kind in KINDS:
                evidence[kind] = "invalid"
            if kind == "crash_recovery":
                software_failed = True
            gaps.append("supplemental_invalid_or_duplicate")
            continue
        seen.add(kind)
        if native is None:
            continue
        try:
            if kind == "crash_recovery":
                check_recovery(item, candidate, native)
            elif kind in SCALE_FIXTURES:
                check_scale(item, candidate, native)
            elif kind.endswith("first_run"):
                check_manual(item, candidate, native)
            else:
                check_usage(item, candidate)
            evidence[kind] = "passed"
        except (ValueError, KeyError, TypeError, AttributeError):
            blocked = True
            if kind == "crash_recovery":
                software_failed = True
            evidence[kind] = "invalid"
            gaps.append(kind + "_invalid")
    for kind in KINDS:
        if kind not in seen:
            gaps.append(kind + "_pending")
    software = "failed" if software_failed else "passed" if evidence["crash_recovery"] == "passed" else "pending"
    release = "blocked" if blocked else "eligible-for-owner-decision" if all(v == "passed" for v in evidence.values()) else "pending"
    return {"report_version": 1, "candidate_sha": candidate.get("candidate_sha") if native else None,
            "candidate_version": candidate.get("candidate_version") if native else None,
            "software_status": software, "release_readiness": release, "final_release": False,
            "evidence": evidence, "gaps": gaps}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--candidate", required=True, type=pathlib.Path)
    parser.add_argument("--candidate-sha", required=True)
    parser.add_argument("--candidate-version", required=True)
    parser.add_argument("--recovery", type=pathlib.Path)
    parser.add_argument("--evidence", action="append", type=pathlib.Path, default=[])
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()
    try:
        require(len(args.evidence) <= len(KINDS))
        records = [_json(path) for path in args.evidence]
        if args.recovery:
            records.append({"kind": "crash_recovery", "measurement": _json(args.recovery)})
        result = classify(_json(args.candidate), records, candidate_sha=args.candidate_sha,
                          candidate_version=args.candidate_version)
    except (ValueError, OSError, RecursionError):
        result = classify({})
        result["gaps"] = ["input_invalid"]
    args.output.parent.mkdir(parents=True, exist_ok=True)
    temporary = args.output.with_suffix(args.output.suffix + ".tmp")
    temporary.write_text(json.dumps(result, indent=2, sort_keys=True, allow_nan=False) + "\n", encoding="utf-8")
    temporary.replace(args.output)
    print(json.dumps(result, sort_keys=True, allow_nan=False))
    return 1 if result["release_readiness"] == "blocked" else 0


if __name__ == "__main__":
    raise SystemExit(main())
