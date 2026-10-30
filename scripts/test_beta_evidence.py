import argparse
import contextlib
import copy
import hashlib
import io
import json
import pathlib
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zipfile
from datetime import datetime, timedelta, timezone
from unittest import mock

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import acceptance_kit
import beta_evidence as evidence
import beta_evidence_github as github
import candidate_record
import manual_evidence
import readiness
import release_assets
import resource_series
import test_acceptance_kit
import test_release_assets


SHA = "2" * 40
BINARY_SHA = "a" * 64
BASELINE_BINARY_SHA = "b" * 64
FUTURE = (datetime.now(timezone.utc) + timedelta(days=30)).isoformat()


def _current_candidate_jobs():
    fixture = pathlib.Path(__file__).parent / "fixtures/alpha-release-readonly-jobs-b5846ae.json"
    value = json.loads(fixture.read_text(encoding="utf-8"))
    return [{"name": item["name"], "run_attempt": 2, "status": "completed",
             "conclusion": item["conclusion"]} for item in value["jobs"]]


def _run(kind, run_id):
    if kind == "candidate":
        jobs = _current_candidate_jobs()
    else:
        job_names = evidence.PERFORMANCE_JOB_NAMES if kind == "performance" else {"handoff"}
        jobs = [{"name": name, "run_attempt": 2, "status": "completed",
                 "conclusion": "success"} for name in sorted(job_names)]
    workflow_id = {"candidate": "101", "performance": "202", "handoff": "303"}[kind]
    event = "push" if kind == "candidate" else "workflow_dispatch"
    run = {"run_id": run_id, "run_attempt": 2, "workflow_id": workflow_id,
           "workflow_path": github.WORKFLOWS[kind], "event": event,
           "head_branch": "main", "head_sha": SHA, "status": "completed", "conclusion": "success",
           "url": f"https://github.com/{evidence.REPOSITORY}/actions/runs/{run_id}",
           "jobs": jobs, "artifacts": []}
    if kind == "candidate":
        names = [f"release-assets-{SHA}", f"release-verification-{SHA}",
                 f"release-package-{SHA}-linux-amd64"]
    elif kind == "performance":
        names = [f"performance-series-{SHA}-{run_id}", f"performance-soak-{SHA}-{run_id}"]
    else:
        names = [f"beta-evidence-handoff-{run_id}-2"]
    for index, name in enumerate(names):
        artifact_id = str(int(run_id) * 10 + index + 1)
        run["artifacts"].append({"id": artifact_id, "name": name,
                                 "digest": "sha256:" + hashlib.sha256(name.encode()).hexdigest(),
                                 "size_in_bytes": 1024, "expired": False, "expires_at": FUTURE,
                                 "url": f"https://github.com/{evidence.REPOSITORY}/actions/runs/{run_id}/artifacts/{artifact_id}"})
    return run


def _metadata(include_previous=False):
    candidate = _run("candidate", "110")
    performance = _run("performance", "120")
    metadata = {"metadata_version": 1, "repository": evidence.REPOSITORY,
                "candidate_sha": SHA, "candidate_version": evidence.VERSION,
                "candidate_run": candidate, "performance_run": performance,
                "candidate_delivery_url": candidate["artifacts"][0]["url"]}
    if include_previous:
        previous = _run("handoff", "130")
        previous["head_sha"] = "3" * 40
        metadata["previous_handoff_run"] = previous
        metadata["previous_handoff_artifact"] = previous["artifacts"][0]
    return metadata


def _replace_fixture_value(value, old, new):
    if isinstance(value, dict):
        return {key: _replace_fixture_value(item, old, new)
                for key, item in value.items()}
    if isinstance(value, list):
        return [_replace_fixture_value(item, old, new) for item in value]
    if isinstance(value, str):
        return value.replace(old, new)
    return value


def _manual_report(kind, target, candidate, native, reference, status="passed"):
    cases = [{"case": name, "status": "passed", "elapsed_seconds": 1}
             for name in manual_evidence.CASES]
    return {"kind": kind, "evidence_type": "manual", "status": status,
            "candidate_sha": candidate["candidate_sha"],
            "candidate_version": candidate["candidate_version"], "target": target,
            "archive_sha256": native[target]["archive"]["sha256"],
            "reference": reference, "cases": cases}


def _full_beta_fixture(root, *, initial_manual=True):
    root = pathlib.Path(root)
    alpha_root = root / "alpha-source"
    alpha_root.mkdir(parents=True)
    summary, _native = _summary_fixture(alpha_root)
    old_sha = summary["candidate_sha"]
    summary = _replace_fixture_value(summary, old_sha, SHA)
    summary = _replace_fixture_value(summary, "0.1.0-alpha.5", evidence.VERSION)

    beta_root = root / "beta-source"
    beta_root.mkdir()
    source, revision = test_release_assets.ReleaseAssetTests().candidate(
        beta_root, revision=SHA, version=evidence.VERSION, schema=evidence.SCHEMA)
    assets = root / "assets"
    manifest = release_assets.assemble(source, assets, evidence.VERSION, revision)
    by_target = {item["target"]: item for item in manifest["assets"] if item.get("target")}
    native_records = root / "beta-native-records"
    for index, record in enumerate(summary["native"]):
        actual = by_target[record["target"]]
        record = _replace_fixture_value(record, record["archive"]["sha256"], actual["sha256"])
        record["archive"] = {key: actual[key] for key in ("basename", "size", "sha256")}
        summary["native"][index] = record
        record_path = native_records / record["target"] / "verification.json"
        evidence.write_json(record_path, record)
    journey_names = {item["case"] for item in summary["journey"]["cases"]}
    summary["journey"]["cases"].extend(
        {"case": name, "status": "passed", "duration_seconds": 0.1}
        for name in candidate_record.BETA1_JOURNEY_CASES if name not in journey_names)

    metadata = _metadata(include_previous=False)
    browser_path = root / "beta-browser-report.json"
    evidence.write_json(browser_path, summary["journey"])
    summary = candidate_record.summarize(native_records, assets, browser_path,
                                         revision, evidence.VERSION)
    summary["run_url"] = metadata["candidate_run"]["url"]
    summary["run_attempt"] = str(metadata["candidate_run"]["run_attempt"])
    summary_path = root / "verification-summary.json"
    evidence.write_json(summary_path, summary)
    native = {item["target"]: item for item in summary["native"]}

    recovery_wrapper = _recovery(summary, native)
    recovery_path = root / "recovery-candidate.json"
    evidence.write_json(recovery_path, recovery_wrapper["measurement"])
    scale_paths = {}
    for kind, suffix in (("scale_10k", "10k"), ("scale_100k", "100k")):
        wrapper = _scale_record(summary, kind)
        path = root / f"scale-{suffix}.json"
        evidence.write_json(path, wrapper["measurement"])
        scale_paths[kind] = path

    binary_sha = native["linux-amd64"]["binary_sha256"]
    series_dir = root / "performance-series"
    _series_fixture(series_dir, candidate_sha=SHA, candidate_version=evidence.VERSION,
                    candidate_binary=binary_sha)
    soak_path = _soak_fixture(root / "performance-soak", candidate_sha=SHA,
                              candidate_version=evidence.VERSION, binary_sha=binary_sha)
    metadata_path = root / "trusted-run-metadata.json"
    evidence.write_json(metadata_path, metadata)
    kits_dir = root / "kits"
    evidence.prepare_kits(assets, metadata_path, SHA, evidence.VERSION, kits_dir)

    windows_report = root / "windows-report.json"
    linux_report = root / "linux-report.json"
    if initial_manual:
        evidence.write_json(windows_report, _manual_report(
            "windows_first_run", "windows-amd64", summary, native,
            "https://github.com/Lephiziel/findrail/issues/211"))
        linux_incomplete = _manual_report(
            "linux_first_run", "linux-amd64", summary, native, "", status="incomplete")
        for case in linux_incomplete["cases"]:
            case.update(status="not_run", elapsed_seconds=None)
        linux_incomplete["reference"] = ""
        evidence.write_json(linux_report, linux_incomplete)
    return {"root": root, "summary": summary, "native": native, "assets": assets,
            "summary_path": summary_path, "recovery_path": recovery_path,
            "series_dir": series_dir, "soak_path": soak_path,
            "scale_paths": scale_paths, "metadata": metadata,
            "metadata_path": metadata_path, "kits_dir": kits_dir,
            "windows_report": windows_report, "linux_report": linux_report}


def _summary_fixture(root):
    release_tests = test_release_assets.ReleaseAssetTests()
    records, assets, browser, sha, journey = release_tests.summary_fixture(root)
    common = next(case for case in journey["cases"]
                  if case["case"] == "packaged_browser_journey.py")["details"]
    common["cases"].extend({"case": name, "status": "passed", "elapsed_seconds": 0.1}
                           for name in readiness.FRESHNESS_CASES)
    browser.write_text(json.dumps(journey), encoding="utf-8")
    summary = candidate_record.summarize(records, assets, browser, sha, "0.1.0-alpha.5")
    native = {item["target"]: item for item in summary["native"]}
    return summary, native


def _scale_record(summary, kind):
    count, _fixture = readiness.SCALE_FIXTURES[kind]
    path = pathlib.Path(__file__).parents[1] / "docs/resource-reports" / f"2026-10-18-task17-{count}.json"
    record = json.loads(path.read_text(encoding="utf-8"))
    record.pop("comparison", None)
    record["binary"].update(revision=summary["candidate_sha"],
                            version=summary["candidate_version"],
                            sha256=next(item for item in summary["native"]
                                        if item["target"] == "linux-amd64")["binary_sha256"])
    return {"kind": kind, "measurement": record}


def _recovery(summary, native):
    return {"kind": "crash_recovery", "measurement": {
        "case": "candidate_refresh_sigkill", "status": "passed",
        "revision": summary["candidate_sha"], "schema": 8, "platform": "linux",
        "phase": "verified_old_or_new_commit_unknown",
        "binary_sha256": native["linux-amd64"]["binary_sha256"], "duration_ms": 25}}


def _series_fixture(directory, candidate_sha=SHA, candidate_version=evidence.VERSION,
                    candidate_binary=BINARY_SHA, baseline_binary=BASELINE_BINARY_SHA):
    directory = pathlib.Path(directory)
    (directory / "results").mkdir(parents=True, exist_ok=True)
    (directory / "provenance.txt").write_text(
        f"candidate_sha={candidate_sha}\nbaseline_sha={evidence.BASELINE_SHA}\n"
        "packager=production scripts/package.py; target=linux-amd64\n", encoding="utf-8")
    identities = {
        "candidate": {"sha256": candidate_binary, "revision": candidate_sha,
                      "version": candidate_version, "schema": 8},
        "baseline": {"sha256": baseline_binary, "revision": evidence.BASELINE_SHA,
                     "version": "0.1.0-alpha.5"},
    }
    runs, samples_by_key, corpora = [], {}, {}
    for count in (10000, 100000):
        corpus = hashlib.sha256(f"fixture-{count}".encode()).hexdigest()
        corpora[count] = corpus
        for repeat in (1, 2, 3):
            for role in ("candidate", "baseline"):
                sha = candidate_binary if role == "candidate" else baseline_binary
                revision = candidate_sha if role == "candidate" else evidence.BASELINE_SHA
                version = candidate_version if role == "candidate" else "0.1.0-alpha.5"
                report = {"schema": 1, "status": "success", "errors": [],
                          "binary": {"sha256": sha, "revision": revision,
                                     "version": version, "schema": 8},
                          "fixture": {"version": 2, "seed": 42,
                                      "documents_requested": count, "corpus_sha256": corpus},
                          "workload": {"indexed_documents": count, "skipped_documents": 0}}
                families = {}
                checked = {}
                for family in ("common", "rare"):
                    base = (10 if role == "candidate" else 20) + repeat
                    values = [float(base + index % 11) for index in range(100)]
                    summary = resource_series.summary(values)
                    families[family] = {"samples": values, "count": 100,
                                        "p50": summary["p50"], "p95": summary["p95"]}
                    checked[family] = values
                sidecar = {"schema": 1, "kind": "search_warm_samples", "status": "success",
                           "units": "ms", "warmup": {"excluded": True, "count_per_family": 1},
                           "binary": {"sha256": sha}, "fixture": {"corpus_sha256": corpus},
                           "families": families}
                stem = f"{count}-{repeat}-{role}"
                report_path = directory / "results" / f"{stem}.json"
                sample_path = directory / "results" / f"{stem}-samples.json"
                report_path.write_text(json.dumps(report), encoding="utf-8")
                sample_path.write_text(json.dumps(sidecar), encoding="utf-8")
                run = {"size": count, "repeat": repeat, "role": role,
                       "corpus_sha256": corpus, "status": "success", "reason": None,
                       "report": report_path.name, "samples": sample_path.name,
                       "report_sha256": evidence.file_digest(report_path),
                       "samples_sha256": evidence.file_digest(sample_path),
                       "observed_samples": {"common": 100, "rare": 100}}
                runs.append(run)
                samples_by_key[(count, repeat, role)] = checked

    summaries = []
    for count in (10000, 100000):
        for role in ("candidate", "baseline"):
            for family in ("common", "rare"):
                per_run = [resource_series.summary(samples_by_key[(count, repeat, role)][family])["p95"]
                           for repeat in (1, 2, 3)]
                pooled = [value for repeat in (1, 2, 3)
                          for value in samples_by_key[(count, repeat, role)][family]]
                summaries.append({"size": count, "role": role, "family": family,
                                  "completed_runs": 3, "expected_runs": 3,
                                  "median_run_p95": round(resource_series.nearest(per_run, .5), 3),
                                  "min_run_p95": min(per_run), "max_run_p95": max(per_run),
                                  "pooled_count": len(pooled),
                                  "pooled_p95": round(resource_series.nearest(pooled, .95), 3)})
    ratios = []
    for count in (10000, 100000):
        for repeat in (1, 2, 3):
            for family in ("common", "rare"):
                candidate_p95 = resource_series.summary(samples_by_key[(count, repeat, "candidate")][family])["p95"]
                baseline_p95 = resource_series.summary(samples_by_key[(count, repeat, "baseline")][family])["p95"]
                ratios.append({"size": count, "repeat": repeat, "family": family,
                               "candidate_to_baseline_p95": round(candidate_p95 / baseline_p95, 6)})
    series = {"schema": 1, "kind": "performance_series", "status": "passed",
              "qualification": "reference_only", "identity": identities,
              "environment": {"os": "Linux", "arch": "x86_64"},
              "measurement": {"order": "AB,BA,AB", "wall_deadline_seconds": 2700,
                              "child_concurrency_limit": 1,
                              "output_budget_bytes": resource_series.MAX_OUTPUT},
              "runs": runs, "summary": summaries, "paired_ratios": ratios,
              "errors": [], "observed_runs": 12, "expected_runs": 12}
    (directory / "results" / "series.json").write_text(json.dumps(series), encoding="utf-8")
    return series


def _soak_fixture(directory, candidate_sha=SHA, candidate_version=evidence.VERSION,
                  binary_sha=BINARY_SHA, elapsed=1800):
    directory = pathlib.Path(directory)
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "provenance.txt").write_text(
        f"candidate_sha={candidate_sha}\npackager=production scripts/package.py; target=linux-amd64\n",
        encoding="utf-8")
    cases = [{"case": name, "status": "passed", "generation": cycle}
             for cycle in range(1, 13)
             for name in ("paging", "refresh_cancel", "collection_restart_provenance", "cycle")]
    report = {"schema": 1, "kind": "mixed_load_soak", "status": "passed", "profile": "long",
              "source_sha": candidate_sha, "source_dirty": False,
              "binary": {"sha256": binary_sha, "revision": candidate_sha, "version": candidate_version},
              "clean_exit": True, "failure_codes": [],
              "counters": {"unexpected_errors": 0, "completed_cycles": 12, "restarts": 12},
              "elapsed_seconds": elapsed,
              "limits": {"target_duration_seconds": 1800, "duration_seconds": 1800},
              "workload": {"seed": 42, "documents": 100, "target_cycles": 12},
              "cases": cases, "environment": {"os": "Linux", "arch": "x86_64"}}
    path = directory / "report.json"
    path.write_text(json.dumps(report), encoding="utf-8")
    return path


class BetaEvidenceTests(unittest.TestCase):
    def test_assemble_refuses_existing_outputs_without_removing_user_data(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            output_dir = root / "handoff"
            reports_zip = root / "reports.zip"
            inputs = {"assets_dir": root / "assets",
                      "verification_summary": root / "verification.json",
                      "recovery": root / "recovery.json",
                      "performance_series_dir": root / "series",
                      "performance_soak": root / "soak/report.json",
                      "scale_10k": root / "scale-10k.json",
                      "scale_100k": root / "scale-100k.json",
                      "run_metadata": root / "metadata.json",
                      "kits_dir": root / "kits",
                      "windows_report": None, "linux_report": None,
                      "diagnostics": None}
            args = argparse.Namespace(candidate_sha=SHA, candidate_version=evidence.VERSION,
                                      output_dir=output_dir, reports_zip=reports_zip, **inputs)

            output_dir.mkdir()
            sentinel = output_dir / "keep.txt"
            sentinel.write_text("existing handoff", encoding="utf-8")
            with self.assertRaisesRegex(evidence.EvidenceError, "output_exists"):
                evidence.assemble(args)
            self.assertEqual(sentinel.read_text(encoding="utf-8"), "existing handoff")

            sentinel.unlink()
            output_dir.rmdir()
            reports_zip.write_bytes(b"existing reports")
            with self.assertRaisesRegex(evidence.EvidenceError, "output_exists"):
                evidence.assemble(args)
            self.assertEqual(reports_zip.read_bytes(), b"existing reports")

    def test_trusted_metadata_requires_exact_runs_attempt_jobs_and_artifacts(self):
        metadata = _metadata()
        self.assertIs(evidence.validate_trusted_metadata(metadata, SHA, evidence.VERSION), metadata)
        mutations = (
            lambda item: item.update(repository="someone/fork"),
            lambda item: item.update(candidate_sha="0" * 40),
            lambda item: item.update(candidate_version="0.1.0-beta.2"),
            lambda item: item["candidate_run"].update(event="pull_request"),
            lambda item: item["candidate_run"].update(workflow_path=".github/workflows/other.yml"),
            lambda item: item["candidate_run"].update(head_branch="task/branch"),
            lambda item: item["candidate_run"].update(status="in_progress"),
            lambda item: item["candidate_run"]["jobs"][0].update(run_attempt=1),
            lambda item: item["candidate_run"]["jobs"][0].update(conclusion="failure"),
            lambda item: item["candidate_run"]["jobs"][0].update(conclusion="skipped"),
            lambda item: item["performance_run"]["jobs"][0].update(conclusion="failure"),
            lambda item: item["candidate_run"]["artifacts"][0].update(expired=True),
            lambda item: item["candidate_run"]["artifacts"][0].update(expires_at="2000-01-01T00:00:00Z"),
            lambda item: item["candidate_run"]["artifacts"].pop(),
            lambda item: item["performance_run"]["artifacts"].append(
                copy.deepcopy(item["performance_run"]["artifacts"][0])),
            lambda item: item["candidate_run"]["artifacts"][0].update(id="1", url="https://evil.invalid/x"),
        )
        for mutate in mutations:
            bad = copy.deepcopy(metadata)
            mutate(bad)
            with self.subTest(change=mutate), self.assertRaises(evidence.EvidenceError):
                evidence.validate_trusted_metadata(bad, SHA, evidence.VERSION)
        refreshed = _metadata(include_previous=True)
        self.assertEqual(refreshed["previous_handoff_run"]["head_sha"], "3" * 40)
        evidence.validate_trusted_metadata(refreshed, SHA, evidence.VERSION)
        bad_previous = copy.deepcopy(refreshed)
        bad_previous["previous_handoff_artifact"] = copy.deepcopy(
            refreshed["previous_handoff_artifact"])
        bad_previous["previous_handoff_run"]["artifacts"][0]["digest"] = \
            "sha256:" + "0" * 64
        with self.assertRaises(evidence.EvidenceError):
            evidence.validate_trusted_metadata(bad_previous, SHA, evidence.VERSION)

    def test_candidate_job_graph_accepts_current_read_only_and_explicit_legacy_graphs(self):
        current = _metadata()
        fixture = json.loads((pathlib.Path(__file__).parent /
                              "fixtures/alpha-release-readonly-jobs-b5846ae.json").read_text(
                                  encoding="utf-8"))
        self.assertEqual({job["name"] for job in current["candidate_run"]["jobs"]},
                         {job["name"] for job in fixture["jobs"]})
        evidence.validate_trusted_metadata(current, SHA, evidence.VERSION)

        invalid_graphs = (
            ("missing native", lambda jobs: jobs.remove(
                next(job for job in jobs if job["name"].startswith("native-packages (linux-amd64")))),
            ("skipped browser", lambda jobs: next(
                job for job in jobs if job["name"] == "packaged-browser-journey").update(conclusion="skipped")),
            ("skipped verifier", lambda jobs: next(
                job for job in jobs if job["name"] == "aggregate-and-verify").update(conclusion="skipped")),
            ("skipped final verification", lambda jobs: next(
                job for job in jobs if job["name"] ==
                "reusable-final-verification / verify-assets").update(conclusion="skipped")),
            ("wrong attempt", lambda jobs: next(
                job for job in jobs if job["name"] == "promotion-preflight").update(run_attempt=1)),
            ("unknown extra", lambda jobs: jobs.append({
                "name": "unreviewed-job", "run_attempt": 2,
                "status": "completed", "conclusion": "success"})),
            ("write job ran", lambda jobs: next(
                job for job in jobs if job["name"] == "promotion-publish").update(conclusion="success")),
        )
        for label, mutate in invalid_graphs:
            bad = copy.deepcopy(current)
            mutate(bad["candidate_run"]["jobs"])
            with self.subTest(graph=label), self.assertRaises(evidence.EvidenceError):
                evidence.validate_trusted_metadata(bad, SHA, evidence.VERSION)

        historical = _metadata()
        historical["candidate_run"]["jobs"] = [
            {"name": name, "run_attempt": 2, "status": "completed",
             "conclusion": "skipped" if name == "publish" else "success"}
            for name in sorted(evidence.CANDIDATE_LEGACY_JOB_NAMES)
        ]
        evidence.validate_trusted_metadata(historical, SHA, evidence.VERSION)

    def test_metadata_requires_candidate_and_performance_run_ids_to_be_decimal(self):
        metadata = _metadata()
        metadata["candidate_run"]["run_id"] = "01"
        with self.assertRaises(evidence.EvidenceError):
            evidence.validate_trusted_metadata(metadata, SHA, evidence.VERSION)

    def test_readiness_passes_technical_evidence_and_keeps_exact_three_human_gaps(self):
        with tempfile.TemporaryDirectory() as temp:
            summary, native = _summary_fixture(pathlib.Path(temp))
            supplemental = [_recovery(summary, native), _scale_record(summary, "scale_10k"),
                            _scale_record(summary, "scale_100k")]
            result = readiness.classify(summary, supplemental, candidate_sha=summary["candidate_sha"],
                                        candidate_version=summary["candidate_version"])
            self.assertEqual(result["software_status"], "passed")
            self.assertEqual(result["release_readiness"], "pending")
            self.assertFalse(result["final_release"])
            self.assertEqual(set(result["gaps"]), {"windows_first_run_pending",
                                                   "apple_silicon_first_run_pending",
                                                   "voluntary_usage_pending"})
            evidence._expected_readiness(result, summary["candidate_sha"],
                                         summary["candidate_version"], False)

    def test_synthetic_assemble_verify_and_refresh_preserve_verified_technical_evidence(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            fixture = _full_beta_fixture(root, initial_manual=False)
            summary, native = fixture["summary"], fixture["native"]
            # These are the real production validators, not compatibility mocks.
            production_manifest = release_assets.verify(
                fixture["assets"], evidence.VERSION, SHA)
            production_native = readiness.strict_candidate(summary, SHA, evidence.VERSION)
            self.assertEqual(len(summary["assets"]), 8)
            self.assertEqual(len(production_manifest["assets"]), 6)
            self.assertEqual(set(production_native),
                             set(release_assets.expected_archives(evidence.VERSION).values()))

            handoff = root / "initial-handoff"
            reports_zip = root / "initial-reports.zip"
            diagnostics = root / "initial-diagnostics.json"
            assemble_args = argparse.Namespace(
                candidate_sha=SHA, candidate_version=evidence.VERSION,
                assets_dir=fixture["assets"], verification_summary=fixture["summary_path"],
                recovery=fixture["recovery_path"],
                performance_series_dir=fixture["series_dir"],
                performance_soak=fixture["soak_path"],
                scale_10k=fixture["scale_paths"]["scale_10k"],
                scale_100k=fixture["scale_paths"]["scale_100k"],
                run_metadata=fixture["metadata_path"], kits_dir=fixture["kits_dir"],
                output_dir=handoff, reports_zip=reports_zip,
                windows_report=None, linux_report=None, diagnostics=diagnostics)
            assembled = evidence.assemble(assemble_args)
            verified = evidence.verify(argparse.Namespace(
                candidate_sha=SHA, candidate_version=evidence.VERSION,
                assets_dir=fixture["assets"], handoff_dir=handoff,
                reports_zip=reports_zip))
            self.assertEqual(assembled["report_zip_sha256"], verified["report_zip_sha256"])

            # Promotion repeats both the source-run provenance and the exact
            # successful outer handoff-artifact contract before any write.
            promotion_bundle = root / "promotion-artifact"
            promotion_bundle.mkdir()
            shutil.copytree(handoff, promotion_bundle / "handoff")
            shutil.copyfile(reports_zip, promotion_bundle / "beta-evidence-reports.zip")
            shutil.copyfile(diagnostics, promotion_bundle / "diagnostics.json")
            evidence.write_json(promotion_bundle / "result.json", assembled)
            promotion_metadata = copy.deepcopy(fixture["metadata"])
            promotion_run = _run("handoff", "130")
            promotion_metadata["promotion_handoff_run"] = promotion_run
            promotion_metadata["promotion_handoff_artifact"] = promotion_run["artifacts"][0]
            promotion_metadata_path = root / "promotion-run-metadata.json"
            evidence.write_json(promotion_metadata_path, promotion_metadata)
            promoted_verify = evidence.verify(argparse.Namespace(
                candidate_sha=SHA, candidate_version=evidence.VERSION,
                assets_dir=fixture["assets"],
                handoff_dir=promotion_bundle / "handoff",
                reports_zip=promotion_bundle / "beta-evidence-reports.zip",
                trusted_run_metadata=promotion_metadata_path,
                handoff_artifact_dir=promotion_bundle))
            self.assertEqual(promoted_verify["status"], "passed")
            failed_result = dict(assembled, status="failed")
            evidence.write_json(promotion_bundle / "result.json", failed_result)
            with self.assertRaisesRegex(evidence.EvidenceError, "promotion_handoff_result_invalid"):
                evidence.verify(argparse.Namespace(
                    candidate_sha=SHA, candidate_version=evidence.VERSION,
                    assets_dir=fixture["assets"],
                    handoff_dir=promotion_bundle / "handoff",
                    reports_zip=promotion_bundle / "beta-evidence-reports.zip",
                    trusted_run_metadata=promotion_metadata_path,
                    handoff_artifact_dir=promotion_bundle))
            evidence.write_json(promotion_bundle / "result.json", assembled)
            initial_index, _ = evidence.load_json(handoff / "evidence-index.json")
            self.assertEqual(len(initial_index["candidate"]["assets"]), 8)
            self.assertEqual(initial_index["readiness"]["human_pending"], [
                "windows_first_run", "apple_silicon_first_run", "voluntary_usage"])
            self.assertEqual((handoff / "publication-notes.md").read_bytes(),
                             (pathlib.Path(__file__).parents[1] /
                              "docs/release-notes-beta1.md").read_bytes())

            # Model the exact extracted layout of the successful Actions artifact.
            source_artifacts = root / "source-artifacts"
            (source_artifacts / "verification").mkdir(parents=True)
            shutil.copyfile(fixture["summary_path"],
                            source_artifacts / "verification/verification-summary.json")
            (source_artifacts / "linux-package").mkdir()
            shutil.copyfile(fixture["recovery_path"],
                            source_artifacts / "linux-package/recovery-candidate.json")
            shutil.copytree(fixture["series_dir"], source_artifacts / "performance-series")
            shutil.copytree(fixture["soak_path"].parent, source_artifacts / "performance-soak")
            previous_bundle = root / "previous-bundle"
            previous_bundle.mkdir()
            shutil.copytree(handoff, previous_bundle / "handoff")
            shutil.copyfile(reports_zip, previous_bundle / "beta-evidence-reports.zip")
            shutil.copyfile(diagnostics, previous_bundle / "diagnostics.json")
            evidence.write_json(previous_bundle / "result.json", assembled)

            previous_zip = io.BytesIO()
            with zipfile.ZipFile(previous_zip, "w", zipfile.ZIP_DEFLATED) as archive:
                for path in sorted(previous_bundle.rglob("*")):
                    if path.is_file():
                        archive.write(path, path.relative_to(previous_bundle).as_posix())
            previous_artifact_digest = hashlib.sha256(previous_zip.getvalue()).hexdigest()
            metadata = _metadata(include_previous=True)
            metadata["previous_handoff_artifact"].update(
                digest="sha256:" + previous_artifact_digest,
                size_in_bytes=len(previous_zip.getvalue()))
            metadata["previous_handoff_run"]["artifacts"][0].update(
                digest="sha256:" + previous_artifact_digest,
                size_in_bytes=len(previous_zip.getvalue()))
            refreshed_metadata = root / "refresh-run-metadata.json"
            evidence.write_json(refreshed_metadata, metadata)

            windows_report = _manual_report(
                "windows_first_run", "windows-amd64", summary, native,
                "https://github.com/Lephiziel/findrail/issues/211")
            linux_report = _manual_report(
                "linux_first_run", "linux-amd64", summary, native,
                "https://github.com/Lephiziel/findrail/issues/212")
            windows_path, linux_path = root / "windows-passed.json", root / "linux-passed.json"
            evidence.write_json(windows_path, windows_report)
            evidence.write_json(linux_path, linux_report)
            refreshed = root / "refreshed-handoff"
            refreshed_zip = root / "refreshed-reports.zip"
            refresh_args = argparse.Namespace(
                candidate_sha=SHA, candidate_version=evidence.VERSION,
                previous_bundle_dir=previous_bundle, source_artifacts_dir=source_artifacts,
                assets_dir=fixture["assets"], run_metadata=refreshed_metadata,
                output_dir=refreshed, reports_zip=refreshed_zip,
                windows_report=windows_path, linux_report=linux_path,
                diagnostics=root / "refreshed-diagnostics.json")
            original_run = evidence.subprocess.run
            with mock.patch.object(evidence.subprocess, "run", wraps=original_run) as run_spy:
                refresh_result = evidence.refresh(refresh_args)
            self.assertEqual(len(run_spy.call_args_list), 1)
            readiness_command = run_spy.call_args.args[0]
            self.assertEqual(pathlib.Path(readiness_command[2]).name, "readiness.py")
            self.assertNotIn("search_resource_check.py", " ".join(readiness_command))

            refreshed_verify = evidence.verify(argparse.Namespace(
                candidate_sha=SHA, candidate_version=evidence.VERSION,
                assets_dir=fixture["assets"], handoff_dir=refreshed,
                reports_zip=refreshed_zip))
            self.assertEqual(refresh_result["report_zip_sha256"],
                             refreshed_verify["report_zip_sha256"])
            self.assertEqual(evidence._refresh_technical_paths(handoff),
                             evidence._refresh_technical_paths(refreshed))
            refreshed_index, _ = evidence.load_json(refreshed / "evidence-index.json")
            self.assertEqual(refreshed_index["source_runs"], initial_index["source_runs"])
            self.assertEqual(len(refreshed_index["candidate"]["assets"]), 8)
            self.assertEqual(refreshed_index["refresh_provenance"][
                "previous_handoff_run"]["run_id"], "130")
            self.assertEqual(refreshed_index["refresh_provenance"][
                "previous_handoff_artifact"]["digest"], "sha256:" + previous_artifact_digest)
            self.assertEqual(refreshed_index["readiness"]["human_pending"], [
                "apple_silicon_first_run", "voluntary_usage"])
            notes = (refreshed / "publication-notes.md").read_text(encoding="utf-8")
            self.assertIn("A matching Windows amd64 first-run report", notes)
            self.assertNotIn("manual status remains pending", notes)
            self.assertIn("Apple Silicon first-run and voluntary-use evidence remain pending", notes)

            # Recompute all outer hashes so the independent verifier reaches the
            # Linux case semantics instead of rejecting a stale manifest/ZIP.
            linux_observations_path = refreshed / "manual-evidence/linux-observations.json"
            observations, _ = evidence.load_json(linux_observations_path)
            observation = observations["observations"][0]
            observation["status"] = "complete"
            observation["cases"][0]["status"] = "failed"
            evidence.write_json(linux_observations_path, observations)
            evidence.write_json(refreshed / "PACKAGE-MANIFEST.json", {
                "manifest_version": 1, "files": evidence._manifest_entries(refreshed)})
            invalid_zip = root / "rehashed-failed-linux.zip"
            evidence._zip_reports(refreshed, invalid_zip)
            with self.assertRaisesRegex(evidence.EvidenceError,
                                       "linux_observations_invalid"):
                evidence.verify(argparse.Namespace(
                    candidate_sha=SHA, candidate_version=evidence.VERSION,
                    assets_dir=fixture["assets"], handoff_dir=refreshed,
                    reports_zip=invalid_zip))

    def test_independent_verify_rejects_rehashed_provenance_and_manual_summary_contradictions(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            fixture = _full_beta_fixture(root, initial_manual=True)
            handoff, reports_zip = root / "handoff", root / "reports.zip"
            evidence.assemble(argparse.Namespace(
                candidate_sha=SHA, candidate_version=evidence.VERSION,
                assets_dir=fixture["assets"], verification_summary=fixture["summary_path"],
                recovery=fixture["recovery_path"],
                performance_series_dir=fixture["series_dir"],
                performance_soak=fixture["soak_path"],
                scale_10k=fixture["scale_paths"]["scale_10k"],
                scale_100k=fixture["scale_paths"]["scale_100k"],
                run_metadata=fixture["metadata_path"], kits_dir=fixture["kits_dir"],
                output_dir=handoff, reports_zip=reports_zip,
                windows_report=fixture["windows_report"],
                linux_report=fixture["linux_report"], diagnostics=None))

            command = [sys.executable, "-B",
                       str(pathlib.Path(__file__).parents[1] / "scripts/beta_evidence.py"),
                       "verify", "--candidate-sha", SHA,
                       "--candidate-version", evidence.VERSION,
                       "--assets-dir", str(fixture["assets"]),
                       "--handoff-dir", str(handoff), "--reports-zip", str(reports_zip)]

            def verify():
                result = subprocess.run(command, capture_output=True, text=True, check=False)
                return result.returncode, json.loads(result.stdout)

            def reseal():
                evidence.write_json(handoff / "PACKAGE-MANIFEST.json", {
                    "manifest_version": 1, "files": evidence._manifest_entries(handoff)})
                reports_zip.unlink(missing_ok=True)
                evidence._zip_reports(handoff, reports_zip)

            result_code, result = verify()
            self.assertEqual((result_code, result["status"]), (0, "passed"))
            mutations = (
                ("evidence-index.json", lambda value: value["source_runs"]["candidate"].update(
                    head_sha="0" * 40)),
                ("evidence-index.json", lambda value: value["source_runs"]["performance"].update(
                    head_branch="other-branch")),
                ("main-verification/verification-summary.json",
                 lambda value: value.update(run_attempt="99")),
                ("evidence-index.json", lambda value: next(
                    artifact for artifact in value["source_runs"]["candidate_artifacts"]
                    if artifact["name"] == f"release-assets-{SHA}"
                ).update(url=f"https://github.com/{evidence.REPOSITORY}/actions/runs/120/artifacts/1101")),
                ("evidence-index.json", lambda value: value["candidate"].update(
                    candidate_delivery_url=next(
                        artifact["url"] for artifact in
                        value["source_runs"]["candidate_artifacts"]
                        if artifact["name"] == f"release-verification-{SHA}"))),
                ("evidence-index.json", lambda value: value["readiness"].update(
                    human_pending=[], release_readiness="passed", software_status="failed")),
                ("manual-evidence/intake-summary.json", lambda value: value.update(
                    candidate_sha="0" * 40, windows_first_run="failed",
                    linux_first_run="complete")),
                ("evidence-index.json", lambda value: value["platform_summary"][
                    "windows-amd64"].update(manual_first_run="failed")),
            )
            for relative, mutate in mutations:
                with self.subTest(path=relative, mutation=mutate):
                    path = handoff / relative
                    original = path.read_bytes()
                    changed = json.loads(original)
                    mutate(changed)
                    evidence.write_json(path, changed)
                    reseal()
                    result_code, result = verify()
                    self.assertNotEqual(result_code, 0)
                    self.assertEqual(result["status"], "failed")
                    path.write_bytes(original)
                    reseal()
            result_code, result = verify()
            self.assertEqual((result_code, result["status"]), (0, "passed"))

    def test_incomplete_manual_intake_remains_consistent_and_verifiable(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            fixture = _full_beta_fixture(root, initial_manual=True)
            windows, _ = evidence.load_json(fixture["windows_report"])
            windows["status"] = "incomplete"
            for case in windows["cases"]:
                case.update(status="not_run", elapsed_seconds=None)
            evidence.write_json(fixture["windows_report"], windows)
            handoff, reports_zip = root / "incomplete-handoff", root / "incomplete.zip"
            result = evidence.assemble(argparse.Namespace(
                candidate_sha=SHA, candidate_version=evidence.VERSION,
                assets_dir=fixture["assets"], verification_summary=fixture["summary_path"],
                recovery=fixture["recovery_path"],
                performance_series_dir=fixture["series_dir"],
                performance_soak=fixture["soak_path"],
                scale_10k=fixture["scale_paths"]["scale_10k"],
                scale_100k=fixture["scale_paths"]["scale_100k"],
                run_metadata=fixture["metadata_path"], kits_dir=fixture["kits_dir"],
                output_dir=handoff, reports_zip=reports_zip,
                windows_report=fixture["windows_report"],
                linux_report=fixture["linux_report"], diagnostics=None))
            verified = evidence.verify(argparse.Namespace(
                candidate_sha=SHA, candidate_version=evidence.VERSION,
                assets_dir=fixture["assets"], handoff_dir=handoff,
                reports_zip=reports_zip))
            self.assertEqual(result["report_zip_sha256"], verified["report_zip_sha256"])
            index, _ = evidence.load_json(handoff / "evidence-index.json")
            intake, _ = evidence.load_json(handoff / "manual-evidence/intake-summary.json")
            self.assertEqual(intake["windows_first_run"], "incomplete")
            self.assertEqual(intake["linux_first_run"], "incomplete")
            self.assertEqual(index["readiness"]["human_pending"], [
                "windows_first_run", "apple_silicon_first_run", "voluntary_usage"])
            self.assertEqual(index["platform_summary"]["windows-amd64"]["manual_first_run"],
                             "incomplete")

    def test_extracted_scale_records_reuse_readiness_checks_and_reject_mutations(self):
        with tempfile.TemporaryDirectory() as temp:
            summary, native = _summary_fixture(pathlib.Path(temp))
            wrapper = _scale_record(summary, "scale_10k")
            path = pathlib.Path(temp) / "scale.json"
            path.write_text(json.dumps(wrapper["measurement"]), encoding="utf-8")
            record, raw = evidence._read_scale(path, "scale_10k", summary, native)
            self.assertEqual(record["workload"]["indexed_documents"], 10000)
            self.assertTrue(raw)
            mutated = copy.deepcopy(record)
            mutated["metrics"]["refresh_cycle"]["canceled_scan_preserved_previous_content"] = False
            bad_path = pathlib.Path(temp) / "bad-scale.json"
            bad_path.write_text(json.dumps(mutated), encoding="utf-8")
            with self.assertRaises(evidence.EvidenceError):
                evidence._read_scale(bad_path, "scale_10k", summary, native)

    def test_series_requires_all_candidate_and_baseline_runs_samples_and_matching_summary(self):
        with tempfile.TemporaryDirectory() as temp:
            directory = pathlib.Path(temp) / "series"
            series = _series_fixture(directory)
            result, environment = evidence._validate_series(directory, SHA, evidence.VERSION, BINARY_SHA)
            self.assertEqual(result["qualification"], "reference_only")
            self.assertEqual(environment["os"], "Linux")

            path = directory / "results/10000-1-candidate-samples.json"
            path.write_text(path.read_text(encoding="utf-8") + " ", encoding="utf-8")
            with self.assertRaisesRegex(evidence.EvidenceError, "series_file_digest_mismatch"):
                evidence._validate_series(directory, SHA, evidence.VERSION, BINARY_SHA)

            _series_fixture(directory)
            series_path = directory / "results/series.json"
            changed = json.loads(series_path.read_text(encoding="utf-8"))
            changed["summary"][0]["median_run_p95"] += 1
            series_path.write_text(json.dumps(changed), encoding="utf-8")
            with self.assertRaisesRegex(evidence.EvidenceError, "series_summary_mismatch"):
                evidence._validate_series(directory, SHA, evidence.VERSION, BINARY_SHA)

            _series_fixture(directory)
            changed = json.loads(series_path.read_text(encoding="utf-8"))
            changed["runs"].pop()
            series_path.write_text(json.dumps(changed), encoding="utf-8")
            with self.assertRaises(evidence.EvidenceError):
                evidence._validate_series(directory, SHA, evidence.VERSION, BINARY_SHA)

    def test_soak_requires_completed_long_duration_clean_cycles(self):
        with tempfile.TemporaryDirectory() as temp:
            path = _soak_fixture(pathlib.Path(temp) / "soak")
            report, environment = evidence._validate_soak(path, SHA, evidence.VERSION, BINARY_SHA)
            self.assertEqual(report["elapsed_seconds"], 1800)
            self.assertEqual(environment["arch"], "x86_64")
            short = _soak_fixture(path.parent, elapsed=1799)
            with self.assertRaises(evidence.EvidenceError):
                evidence._validate_soak(short, SHA, evidence.VERSION, BINARY_SHA)
            bad = json.loads(path.read_text(encoding="utf-8"))
            bad["cases"] = bad["cases"][:4]
            path.write_text(json.dumps(bad), encoding="utf-8")
            with self.assertRaises(evidence.EvidenceError):
                evidence._validate_soak(path, SHA, evidence.VERSION, BINARY_SHA)

    def test_soak_accepts_large_historical_cycle_shape_but_rejects_over_limit_json(self):
        with tempfile.TemporaryDirectory() as temp:
            path = _soak_fixture(pathlib.Path(temp) / "soak")
            report = json.loads(path.read_text(encoding="utf-8"))
            report["cases"] = [
                {"case": name, "status": "passed", "generation": cycle}
                for cycle in range(1, 2008)
                for name in ("paging", "refresh_cancel",
                             "collection_restart_provenance", "cycle")]
            report["counters"].update(completed_cycles=2007, restarts=2007)
            path.write_text(json.dumps(report), encoding="utf-8")
            self.assertGreater(path.stat().st_size, evidence.MAX_PROVENANCE)
            evidence._validate_soak(path, SHA, evidence.VERSION, BINARY_SHA)
            staged = pathlib.Path(temp) / "staged"
            evidence._copy_tree(path.parent, staged / "performance-soak",
                                max_total=evidence.MAX_SOAK_REPORT,
                                max_file=evidence.MAX_SOAK_REPORT)
            entries = evidence._manifest_entries(staged)
            self.assertGreater(next(item["size"] for item in entries
                                    if item["path"] == "performance-soak/report.json"),
                               evidence.MAX_PROVENANCE)
            reports_zip = pathlib.Path(temp) / "large-soak.zip"
            evidence._zip_reports(staged, reports_zip)
            evidence._verify_reports_zip(staged, reports_zip)

            report["padding"] = "x" * evidence.MAX_SOAK_REPORT
            path.write_text(json.dumps(report), encoding="utf-8")
            with self.assertRaises(evidence.EvidenceError):
                evidence._validate_soak(path, SHA, evidence.VERSION, BINARY_SHA)
            with self.assertRaises(evidence.EvidenceError):
                evidence._copy_tree(path.parent, pathlib.Path(temp) / "too-large",
                                    max_total=evidence.MAX_SOAK_REPORT,
                                    max_file=evidence.MAX_SOAK_REPORT)

    def test_passed_windows_notes_use_the_real_wrapped_frozen_file(self):
        source = pathlib.Path(__file__).parents[1] / "docs/release-notes-beta1.md"
        first = evidence._publication_notes(source, {"windows_outcome": "complete"})
        second = evidence._publication_notes(source, {"windows_outcome": "complete"})
        self.assertEqual(first, second)
        text = first.decode("utf-8")
        self.assertIn("A matching Windows amd64 first-run report", text)
        self.assertNotIn("manual status remains pending", text)
        self.assertNotIn("This base text does not assert matching beta.1 manual", text)
        self.assertIn("Apple Silicon first-run and voluntary-use evidence remain pending", text)
        self.assertIn("Historical alpha.5 Linux observations are not beta.1 evidence", text)

    def test_dispatch_report_inputs_are_read_from_event_file_without_echoing_json(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            secret = "synthetic-sensitive-report-marker"
            report = json.dumps({"kind": "windows_first_run", "notes": secret})
            event = root / "event.json"
            event.write_text(json.dumps({"inputs": {"windows_report_json": report,
                                                     "linux_report_json": ""}}),
                             encoding="utf-8")
            output = root / "manual-inputs"
            github_output = root / "github-output"
            args = argparse.Namespace(event_path=event, output_dir=output,
                                      github_output=github_output)
            stdout = io.StringIO()
            with contextlib.redirect_stdout(stdout):
                evidence._write_manual_inputs(args)
            windows_path = output / "windows-report.json"
            self.assertEqual(windows_path.read_text(encoding="utf-8"), report)
            self.assertNotIn(secret, stdout.getvalue())
            self.assertNotIn(secret, github_output.read_text(encoding="utf-8"))
            self.assertIn(str(windows_path), github_output.read_text(encoding="utf-8"))

            oversized_event = root / "oversized-event.json"
            oversized_report = json.dumps({"notes": "x" * evidence.MAX_MANUAL_JSON})
            oversized_event.write_text(json.dumps({"inputs": {
                "windows_report_json": oversized_report, "linux_report_json": ""}}),
                encoding="utf-8")
            with self.assertRaisesRegex(evidence.EvidenceError, "manual_report_too_large"):
                evidence._write_manual_inputs(argparse.Namespace(
                    event_path=oversized_event, output_dir=root / "oversized-inputs",
                    github_output=None))

    def test_manual_intake_sanitizes_real_reports_and_blocks_failures_or_mismatches(self):
        with tempfile.TemporaryDirectory() as temp:
            summary, native = _summary_fixture(pathlib.Path(temp))
            cases = [{"case": name, "status": "not_run", "elapsed_seconds": None,
                      "observation": "private note should not survive"} for name in manual_evidence.CASES]
            report = {"kind": "windows_first_run", "evidence_type": "manual", "status": "incomplete",
                      "candidate_sha": summary["candidate_sha"],
                      "candidate_version": summary["candidate_version"], "target": "windows-amd64",
                      "archive_sha256": native["windows-amd64"]["archive"]["sha256"],
                      "reference": "https://github.com/Lephiziel/findrail/issues/210",
                      "observer": "synthetic observer", "notes": "synthetic private prose", "cases": cases}
            windows_path = pathlib.Path(temp) / "windows.json"
            windows_path.write_text(json.dumps(report), encoding="utf-8")
            prepared = evidence._prepare_manual_inputs(windows_path, None, summary, native)
            self.assertEqual(prepared["windows_outcome"], "incomplete")
            self.assertNotIn("private prose", json.dumps(prepared))
            self.assertNotIn("private note", json.dumps(prepared))

            failed = copy.deepcopy(report)
            failed["status"] = "failed"
            windows_path.write_text(json.dumps(failed), encoding="utf-8")
            with self.assertRaisesRegex(evidence.EvidenceError, "manual_report_failed"):
                evidence._prepare_manual_inputs(windows_path, None, summary, native)
            mismatch = copy.deepcopy(report)
            mismatch["candidate_sha"] = "0" * 40
            windows_path.write_text(json.dumps(mismatch), encoding="utf-8")
            with self.assertRaises(evidence.EvidenceError):
                evidence._prepare_manual_inputs(windows_path, None, summary, native)

    def test_linux_observations_are_sanitized_separate_and_not_a_readiness_gate(self):
        with tempfile.TemporaryDirectory() as temp:
            summary, native = _summary_fixture(pathlib.Path(temp))
            report = {"kind": "linux_first_run", "evidence_type": "manual", "status": "incomplete",
                      "candidate_sha": summary["candidate_sha"],
                      "candidate_version": summary["candidate_version"], "target": "linux-amd64",
                      "archive_sha256": native["linux-amd64"]["archive"]["sha256"],
                      "reference": "", "notes": "do not retain this text",
                      "cases": [{"case": case, "status": "not_run", "elapsed_seconds": None,
                                 "observation": "private observation"} for case in manual_evidence.CASES]}
            path = pathlib.Path(temp) / "linux.json"
            path.write_text(json.dumps(report), encoding="utf-8")
            prepared = evidence._prepare_manual_inputs(None, path, summary, native)
            sanitized = prepared["linux"]
            evidence._validate_linux_observations(sanitized, summary, native)
            self.assertNotIn("do not retain", json.dumps(sanitized))
            before = readiness.classify(summary, [_recovery(summary, native),
                                                   _scale_record(summary, "scale_10k"),
                                                   _scale_record(summary, "scale_100k")])
            after = readiness.classify(summary, [_recovery(summary, native),
                                                  _scale_record(summary, "scale_10k"),
                                                  _scale_record(summary, "scale_100k")])
            self.assertEqual(before["evidence"], after["evidence"])
            inconsistent = copy.deepcopy(sanitized)
            inconsistent["observations"][0]["status"] = "complete"
            inconsistent["observations"][0]["reference"] = "https://github.com/Lephiziel/findrail/issues/213"
            inconsistent["observations"][0]["cases"] = [
                {"case": case, "status": "failed" if index == 0 else "passed",
                 "elapsed_seconds": 1} for index, case in enumerate(manual_evidence.CASES)]
            with self.assertRaisesRegex(evidence.EvidenceError, "linux_observations_invalid"):
                evidence._validate_linux_observations(inconsistent, summary, native)
            failed = copy.deepcopy(report)
            failed["status"] = "failed"
            failed["reference"] = "https://github.com/Lephiziel/findrail/issues/212"
            path.write_text(json.dumps(failed), encoding="utf-8")
            with self.assertRaisesRegex(evidence.EvidenceError, "manual_report_failed"):
                evidence._prepare_manual_inputs(None, path, summary, native)

    def test_matching_windows_pass_changes_only_windows_readiness(self):
        with tempfile.TemporaryDirectory() as temp:
            summary, native = _summary_fixture(pathlib.Path(temp))
            report = {"kind": "windows_first_run", "evidence_type": "manual", "status": "passed",
                      "candidate_sha": summary["candidate_sha"],
                      "candidate_version": summary["candidate_version"], "target": "windows-amd64",
                      "archive_sha256": native["windows-amd64"]["archive"]["sha256"],
                      "reference": "https://github.com/Lephiziel/findrail/issues/211",
                      "cases": [{"case": case, "status": "passed", "elapsed_seconds": 1}
                                for case in manual_evidence.CASES]}
            path = pathlib.Path(temp) / "windows.json"
            path.write_text(json.dumps(report), encoding="utf-8")
            manual = evidence._prepare_manual_inputs(path, None, summary, native)
            result = readiness.classify(summary, [_recovery(summary, native),
                                                   _scale_record(summary, "scale_10k"),
                                                   _scale_record(summary, "scale_100k"),
                                                   manual["windows"]])
            self.assertEqual(result["evidence"]["windows_first_run"], "passed")
            self.assertEqual(result["evidence"]["apple_silicon_first_run"], "pending")
            self.assertEqual(result["evidence"]["voluntary_usage"], "pending")
            evidence._expected_readiness(result, summary["candidate_sha"],
                                         summary["candidate_version"], True)

    def test_beta_acceptance_kit_hashes_are_checked_not_self_compared(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            assets = test_acceptance_kit.verified_synthetic_bundle(root / "assets")
            manifest = release_assets.verify(assets, evidence.VERSION, SHA)
            delivery = f"https://github.com/{evidence.REPOSITORY}/actions/runs/110/artifacts/1101"
            kit = root / "windows.zip"
            acceptance_kit.build(assets, "windows-amd64", kit, delivery)
            manifest_sha = evidence.file_digest(assets / "release-manifest.json")
            checksums_sha = evidence.file_digest(assets / "SHA256SUMS.txt")
            evidence.verify_kit(kit, assets, manifest, SHA, evidence.VERSION, "windows-amd64", delivery,
                                manifest_sha, checksums_sha)
            with zipfile.ZipFile(kit) as source:
                members = {name: source.read(name) for name in source.namelist()}
            candidate = json.loads(members["candidate.json"])
            candidate["manifest_sha256"] = "0" * 64
            members["candidate.json"] = json.dumps(candidate).encode()
            with zipfile.ZipFile(kit, "w", zipfile.ZIP_DEFLATED) as target:
                for name, content in members.items():
                    target.writestr(name, content)
            with self.assertRaisesRegex(evidence.EvidenceError, "acceptance_kit_identity_mismatch"):
                evidence.verify_kit(kit, assets, manifest, SHA, evidence.VERSION, "windows-amd64", delivery,
                                    manifest_sha, checksums_sha)

    def test_release_asset_validation_rejects_changed_bytes_and_wrong_candidate_sha(self):
        with tempfile.TemporaryDirectory() as temp:
            assets = test_acceptance_kit.verified_synthetic_bundle(pathlib.Path(temp) / "assets")
            evidence.validate_assets(assets, SHA, evidence.VERSION)
            linux_archive = assets / f"findrail_{evidence.VERSION}_linux-amd64.tar.gz"
            linux_archive.write_bytes(linux_archive.read_bytes() + b"tamper")
            with self.assertRaisesRegex(evidence.EvidenceError, "release_assets_invalid"):
                evidence.validate_assets(assets, SHA, evidence.VERSION)
            test_acceptance_kit.verified_synthetic_bundle(assets)
            with self.assertRaisesRegex(evidence.EvidenceError, "release_assets_invalid"):
                evidence.validate_assets(assets, "3" * 40, evidence.VERSION)

    def test_zip_reader_rejects_traversal_symlink_duplicate_and_bomb_members(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            cases = []
            traversal = root / "traversal.zip"
            with zipfile.ZipFile(traversal, "w") as archive:
                archive.writestr("../escape", "x")
            cases.append(traversal)
            link = root / "link.zip"
            info = zipfile.ZipInfo("link")
            info.external_attr = (stat.S_IFLNK | 0o777) << 16
            with zipfile.ZipFile(link, "w") as archive:
                archive.writestr(info, "target")
            cases.append(link)
            duplicate = root / "duplicate.zip"
            with zipfile.ZipFile(duplicate, "w") as archive:
                archive.writestr("same", "a")
                archive.writestr("same", "b")
            cases.append(duplicate)
            bomb = root / "bomb.zip"
            with zipfile.ZipFile(bomb, "w", zipfile.ZIP_DEFLATED) as archive:
                archive.writestr("large", b"0" * 10000)
            cases.append(bomb)
            for path in cases:
                with self.subTest(path=path.name), self.assertRaises(evidence.EvidenceError):
                    evidence._zip_members(path, total_limit=100, member_limit=100, max_members=8)

    def test_workflow_is_dispatch_only_read_only_and_uploads_bounded_reports(self):
        workflow = (pathlib.Path(__file__).parents[1] / ".github/workflows/beta-evidence.yml").read_text()
        self.assertIn("workflow_dispatch:", workflow)
        self.assertIn("contents: read", workflow)
        self.assertIn("actions: read", workflow)
        self.assertIn("retention-days: 30", workflow)
        self.assertIn("if: always()", workflow)
        self.assertNotIn("contents: write", workflow)
        self.assertNotIn("go build", workflow)
        self.assertNotIn("go run", workflow)
        self.assertNotIn("gh release create", workflow)
        self.assertNotIn("VERSION=", workflow)
        self.assertIn('--event-path "$GITHUB_EVENT_PATH"', workflow)
        self.assertNotIn("WINDOWS_REPORT_JSON: ${{ inputs.windows_report_json }}", workflow)
        self.assertIn("mode:", workflow)
        self.assertIn("previous-handoff-run-id", workflow)
        self.assertIn("beta_evidence.py refresh", workflow)
        self.assertIn("refresh-verify-result.json", workflow)


class FakeGitHubAPI:
    def __init__(self, *, bad_candidate_sha=False, bad_artifact_digest=False,
                 bad_artifact_size=False, bad_job=False, main_sha=SHA):
        self.runs = {}
        self.artifacts = {}
        self.zip_bytes = {}
        self.downloaded = []
        self.main_sha = main_sha
        self.bad_artifact_digest = bad_artifact_digest
        self.bad_artifact_size = bad_artifact_size
        for kind, run_id, workflow_id in (("candidate", "110", 101),
                                          ("performance", "120", 202),
                                          ("handoff", "130", 303)):
            names = ([f"release-assets-{SHA}", f"release-verification-{SHA}",
                      f"release-package-{SHA}-linux-amd64"] if kind == "candidate" else
                     [f"performance-series-{SHA}-{run_id}", f"performance-soak-{SHA}-{run_id}"]
                     if kind == "performance" else
                     [f"beta-evidence-handoff-{run_id}-2"])
            workflow_path = github.WORKFLOWS[kind]
            head_sha = "3" * 40 if kind == "handoff" else SHA
            self.runs[run_id] = {
                "id": int(run_id), "workflow_id": workflow_id,
                "repository": {"full_name": evidence.REPOSITORY, "id": 900},
                "head_repository": {"full_name": evidence.REPOSITORY},
                "head_sha": "0" * 40 if bad_candidate_sha and kind == "candidate" else head_sha,
                "head_branch": "main", "status": "completed", "conclusion": "success",
                "event": "push" if kind == "candidate" else "workflow_dispatch",
                "run_attempt": 2, "html_url": f"https://github.com/{evidence.REPOSITORY}/actions/runs/{run_id}",
                "path": workflow_path + "@refs/heads/main",
            }
            if kind == "candidate":
                jobs = _current_candidate_jobs()
            else:
                job_names = (evidence.PERFORMANCE_JOB_NAMES if kind == "performance"
                             else {"handoff"})
                jobs = [{"name": name, "run_attempt": 2, "status": "completed",
                         "conclusion": "success"} for name in sorted(job_names)]
            if bad_job and kind == "candidate":
                jobs = jobs[:-1]
            self.runs[run_id]["jobs"] = jobs
            artifact_records = []
            for index, name in enumerate(names):
                artifact_id = int(run_id) * 10 + index + 1
                buffer = io.BytesIO()
                with zipfile.ZipFile(buffer, "w", zipfile.ZIP_DEFLATED) as archive:
                    archive.writestr("payload.txt", name)
                contents = buffer.getvalue()
                self.zip_bytes[str(artifact_id)] = contents
                digest = hashlib.sha256(contents).hexdigest()
                artifact_records.append({
                    "id": artifact_id, "name": name,
                    "size_in_bytes": len(contents) +
                    (1 if bad_artifact_size and index == 0 and kind == "candidate" else 0),
                    "digest": "sha256:" + ("f" * 64 if bad_artifact_digest and index == 0 and kind == "candidate" else digest),
                    "expired": False, "expires_at": FUTURE,
                    "workflow_run": {"id": int(run_id), "head_sha": head_sha,
                                     "head_branch": "main", "repository_id": 900,
                                     "head_repository_id": 900},
                })
            self.artifacts[run_id] = artifact_records

    def json(self, path):
        path_no_query = path.split("?", 1)[0]
        if path_no_query == "/git/ref/heads/main":
            return {"object": {"type": "commit", "sha": self.main_sha}}
        if path_no_query.endswith("/alpha-release.yml"):
            return {"path": github.WORKFLOWS["candidate"], "id": 101, "state": "active"}
        if path_no_query.endswith("/performance-check.yml"):
            return {"path": github.WORKFLOWS["performance"], "id": 202, "state": "active"}
        if path_no_query.endswith("/beta-evidence.yml"):
            return {"path": github.WORKFLOWS["handoff"], "id": 303, "state": "active"}
        if path_no_query.endswith("/jobs"):
            run_id = path_no_query.split("/")[-2]
            jobs = self.runs[run_id]["jobs"]
            return {"total_count": len(jobs), "jobs": jobs}
        if path_no_query.endswith("/artifacts"):
            run_id = path_no_query.split("/")[-2]
            artifacts = self.artifacts[run_id]
            return {"total_count": len(artifacts), "artifacts": artifacts}
        if path_no_query.startswith("/actions/runs/"):
            run_id = path_no_query.rsplit("/", 1)[-1]
            return self.runs[run_id]
        raise AssertionError("unexpected fake API path")

    def download_artifact(self, artifact_id, destination):
        artifact_id = str(artifact_id)
        self.downloaded.append(artifact_id)
        contents = self.zip_bytes[artifact_id]
        pathlib.Path(destination).write_bytes(contents)
        return hashlib.sha256(contents).hexdigest(), len(contents)


class BetaEvidenceGitHubTests(unittest.TestCase):
    def test_artifact_zip_extraction_accepts_explicit_directories_and_rejects_path_collisions(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            archive_path = root / "explicit-directory.zip"
            directory = zipfile.ZipInfo("payload/")
            directory.create_system = 3
            directory.external_attr = (stat.S_IFDIR | 0o755) << 16
            with zipfile.ZipFile(archive_path, "w", zipfile.ZIP_DEFLATED) as archive:
                archive.writestr(directory, b"")
                archive.writestr("payload/report.json", b"{}")
            extracted, _ = github._extract_zip(archive_path, root / "explicit-directory")
            self.assertEqual((extracted / "payload/report.json").read_bytes(), b"{}")

            collision_path = root / "file-directory-collision.zip"
            with zipfile.ZipFile(collision_path, "w", zipfile.ZIP_DEFLATED) as archive:
                archive.writestr("payload", b"file")
                archive.writestr("payload/report.json", b"{}")
            with self.assertRaisesRegex(github.PreflightError, "unsafe_artifact_zip"):
                github._extract_zip(collision_path, root / "file-directory-collision")

    def test_preflight_downloads_only_explicit_run_artifact_ids_after_full_metadata_check(self):
        fake = FakeGitHubAPI()
        with tempfile.TemporaryDirectory() as temp, mock.patch.object(github, "GitHubAPI", return_value=fake):
            root = pathlib.Path(temp)
            result = github.preflight(SHA, evidence.VERSION, "110", "120",
                                     root / "downloads", root / "trusted-run-metadata.json",
                                     token="x" * 30)
            self.assertEqual(result["artifacts_downloaded"], 5)
            self.assertEqual(set(fake.downloaded), {"1101", "1102", "1103", "1201", "1202"})
            metadata = json.loads((root / "trusted-run-metadata.json").read_text(encoding="utf-8"))
            self.assertEqual(metadata["candidate_run"]["run_id"], "110")
            self.assertEqual(metadata["performance_run"]["run_attempt"], 2)
            for name in ("assets", "verification", "linux-package", "performance-series", "performance-soak"):
                self.assertTrue((root / "downloads" / name / "payload.txt").is_file())

    def test_preflight_refresh_binds_previous_handoff_id_digest_and_workflow_sha(self):
        fake = FakeGitHubAPI()
        with tempfile.TemporaryDirectory() as temp, mock.patch.object(github, "GitHubAPI", return_value=fake):
            root = pathlib.Path(temp)
            result = github.preflight(
                SHA, evidence.VERSION, "110", "120", root / "downloads",
                root / "trusted-run-metadata.json", token="x" * 30,
                previous_handoff_run_id="130")
            self.assertEqual(result["artifacts_downloaded"], 6)
            self.assertEqual(set(fake.downloaded),
                             {"1101", "1102", "1103", "1201", "1202", "1301"})
            metadata = json.loads((root / "trusted-run-metadata.json").read_text())
            self.assertEqual(metadata["previous_handoff_run"]["head_sha"], "3" * 40)
            artifact = metadata["previous_handoff_artifact"]
            self.assertEqual(artifact["id"], "1301")
            self.assertEqual(artifact["digest"], fake.artifacts["130"][0]["digest"])
            self.assertTrue((root / "downloads/previous-handoff/payload.txt").is_file())

    def test_promotion_metadata_pins_current_main_all_run_attempts_and_handoff_digest(self):
        fake = FakeGitHubAPI()
        fake.runs['130']['head_sha'] = SHA
        for artifact in fake.artifacts['130']:
            artifact['workflow_run']['head_sha'] = SHA
        digest = fake.artifacts['130'][0]['digest']
        with mock.patch.object(github, 'GitHubAPI', return_value=fake):
            metadata = github.promotion_metadata(
                SHA, evidence.VERSION, '110', 2, '120', 2, '130', 2, digest,
                token='x' * 30)
            self.assertEqual(metadata['candidate_run']['run_attempt'], 2)
            self.assertEqual(metadata['performance_run']['run_attempt'], 2)
            self.assertEqual(metadata['promotion_handoff_run']['head_sha'], SHA)
            self.assertEqual(metadata['promotion_handoff_artifact']['digest'], digest)
            with self.assertRaisesRegex(github.PreflightError, 'run_attempt_mismatch'):
                github.promotion_metadata(
                    SHA, evidence.VERSION, '110', 1, '120', 2, '130', 2, digest,
                    token='x' * 30)
            with self.assertRaisesRegex(github.PreflightError, 'handoff_artifact_digest_mismatch'):
                github.promotion_metadata(
                    SHA, evidence.VERSION, '110', 2, '120', 2, '130', 2,
                    'sha256:' + 'f' * 64, token='x' * 30)

    def test_promotion_preflight_downloads_handoff_by_exact_run_and_artifact_id(self):
        fake = FakeGitHubAPI()
        fake.runs['130']['head_sha'] = SHA
        for artifact in fake.artifacts['130']:
            artifact['workflow_run']['head_sha'] = SHA
        digest = fake.artifacts['130'][0]['digest']
        with tempfile.TemporaryDirectory() as temp, mock.patch.object(github, 'GitHubAPI', return_value=fake):
            root = pathlib.Path(temp)
            result = github.preflight(
                SHA, evidence.VERSION, '110', '120', root / 'downloads',
                root / 'trusted-run-metadata.json', token='x' * 30,
                candidate_run_attempt=2, performance_run_attempt=2,
                handoff_run_id='130', handoff_run_attempt=2,
                expected_handoff_artifact_digest=digest, require_main_head=True)
            self.assertEqual(result['artifacts_downloaded'], 6)
            self.assertEqual(result['handoff_artifact_id'], '1301')
            self.assertIn('1301', fake.downloaded)
            self.assertTrue((root / 'downloads/handoff-bundle/payload.txt').is_file())
            metadata = json.loads((root / 'trusted-run-metadata.json').read_text())
            evidence.validate_trusted_metadata(metadata, SHA, evidence.VERSION)
            self.assertEqual(metadata['promotion_handoff_run']['run_id'], '130')
            self.assertEqual(metadata['promotion_handoff_run']['run_attempt'], 2)
            self.assertEqual(metadata['promotion_handoff_artifact'],
                             metadata['promotion_handoff_run']['artifacts'][0])

            original = pathlib.Path(__file__).parents[1] / 'internal/betapromotion/promotion_test.go'
            go_test = root / 'promotion_test.go'
            bridge_test = r'''
func TestPythonPreflightMetadataRoundTrip(t *testing.T) {
	request := validRequest()
	request.CandidateRun = RunIdentity{ID: "110", Attempt: 2}
	request.PerformanceRun = RunIdentity{ID: "120", Attempt: 2}
	request.HandoffRun = RunIdentity{ID: "130", Attempt: 2}
	request.HandoffDigest = DIGEST
	if err := verifyMetadata(METADATA_PATH, request); err != nil {
		t.Fatalf("Python preflight metadata was rejected by Go: %v", err)
	}
}
'''.replace('DIGEST', json.dumps(digest)).replace(
                'METADATA_PATH', json.dumps(str(root / 'trusted-run-metadata.json')))
            go_test.write_text(original.read_text(encoding='utf-8') + bridge_test,
                               encoding='utf-8')
            overlay = root / 'overlay.json'
            overlay.write_text(json.dumps({'Replace': {str(original.resolve()): str(go_test)}}),
                               encoding='utf-8')
            subprocess.run(['go', 'test', '-overlay=' + str(overlay), '-run',
                            '^TestPythonPreflightMetadataRoundTrip$',
                            './internal/betapromotion'], cwd=pathlib.Path(__file__).parents[1],
                           check=True, capture_output=True, text=True, timeout=180)

    def test_promotion_preflight_rejects_main_movement_or_handoff_from_another_candidate(self):
        fake = FakeGitHubAPI(main_sha='f' * 40)
        with tempfile.TemporaryDirectory() as temp, mock.patch.object(github, 'GitHubAPI', return_value=fake):
            root = pathlib.Path(temp)
            digest = fake.artifacts['130'][0]['digest']
            with self.assertRaisesRegex(github.PreflightError, 'main_moved'):
                github.promotion_metadata(
                    SHA, evidence.VERSION, '110', 2, '120', 2, '130', 2,
                    digest, token='x' * 30, require_main_head=True)
            self.assertEqual(fake.downloaded, [])

            fake.main_sha = SHA
            fake.runs['130']['head_sha'] = SHA
            for artifact in fake.artifacts['130']:
                artifact['workflow_run']['head_sha'] = SHA
            fake.runs['130']['head_sha'] = '4' * 40
            for artifact in fake.artifacts['130']:
                artifact['workflow_run']['head_sha'] = '4' * 40
            with self.assertRaisesRegex(github.PreflightError, 'handoff_candidate_sha_mismatch'):
                github.promotion_metadata(
                    SHA, evidence.VERSION, '110', 2, '120', 2, '130', 2,
                    digest, token='x' * 30, require_main_head=True)
            self.assertEqual(fake.downloaded, [])

    def test_preflight_refuses_wrong_head_before_download_and_checks_download_digest(self):
        wrong_head = FakeGitHubAPI(bad_candidate_sha=True)
        with tempfile.TemporaryDirectory() as temp, mock.patch.object(github, "GitHubAPI", return_value=wrong_head):
            root = pathlib.Path(temp)
            with self.assertRaisesRegex(github.PreflightError, "candidate_run_identity_invalid"):
                github.preflight(SHA, evidence.VERSION, "110", "120",
                                 root / "downloads", root / "trusted-run-metadata.json",
                                 token="x" * 30)
            self.assertEqual(wrong_head.downloaded, [])
            self.assertFalse((root / "downloads").exists())

        bad_digest = FakeGitHubAPI(bad_artifact_digest=True)
        with tempfile.TemporaryDirectory() as temp, mock.patch.object(github, "GitHubAPI", return_value=bad_digest):
            root = pathlib.Path(temp)
            with self.assertRaisesRegex(github.PreflightError, "artifact_digest_mismatch"):
                github.preflight(SHA, evidence.VERSION, "110", "120",
                                 root / "downloads", root / "trusted-run-metadata.json",
                                 token="x" * 30)
            self.assertFalse((root / "downloads").exists())
            self.assertFalse((root / "trusted-run-metadata.json").exists())

        bad_size = FakeGitHubAPI(bad_artifact_size=True)
        with tempfile.TemporaryDirectory() as temp, mock.patch.object(github, "GitHubAPI", return_value=bad_size):
            root = pathlib.Path(temp)
            with self.assertRaisesRegex(github.PreflightError, "artifact_size_mismatch"):
                github.preflight(SHA, evidence.VERSION, "110", "120",
                                 root / "downloads", root / "trusted-run-metadata.json",
                                 token="x" * 30)
            self.assertFalse((root / "downloads").exists())
            self.assertFalse((root / "trusted-run-metadata.json").exists())

    def test_preflight_rejects_incomplete_job_set_before_artifact_transfer(self):
        fake = FakeGitHubAPI(bad_job=True)
        with tempfile.TemporaryDirectory() as temp, mock.patch.object(github, "GitHubAPI", return_value=fake):
            root = pathlib.Path(temp)
            with self.assertRaises(github.PreflightError):
                github.preflight(SHA, evidence.VERSION, "110", "120",
                                 root / "downloads", root / "trusted-run-metadata.json",
                                 token="x" * 30)
            self.assertEqual(fake.downloaded, [])


if __name__ == "__main__":
    unittest.main()
