import argparse
import copy
import hashlib
import io
import json
import pathlib
import shutil
import sys
import stat
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


def _run(kind, run_id):
    job_names = (evidence.CANDIDATE_JOB_NAMES if kind == "candidate"
                 else evidence.PERFORMANCE_JOB_NAMES)
    jobs = [{"name": name, "run_attempt": 2, "status": "completed",
             "conclusion": "skipped" if name == "publish" else "success"}
            for name in sorted(job_names)]
    run = {"run_id": run_id, "run_attempt": 2, "workflow_id": "101" if kind == "candidate" else "202",
           "workflow_path": github.WORKFLOWS[kind], "event": "push" if kind == "candidate" else "workflow_dispatch",
           "head_branch": "main", "head_sha": SHA, "status": "completed", "conclusion": "success",
           "url": f"https://github.com/{evidence.REPOSITORY}/actions/runs/{run_id}",
           "jobs": jobs, "artifacts": []}
    if kind == "candidate":
        names = [f"release-assets-{SHA}", f"release-verification-{SHA}",
                 f"release-package-{SHA}-linux-amd64"]
    else:
        names = [f"performance-series-{SHA}-{run_id}", f"performance-soak-{SHA}-{run_id}"]
    for index, name in enumerate(names):
        artifact_id = str(int(run_id) * 10 + index + 1)
        run["artifacts"].append({"id": artifact_id, "name": name,
                                 "digest": "sha256:" + hashlib.sha256(name.encode()).hexdigest(),
                                 "size_in_bytes": 1024, "expired": False, "expires_at": FUTURE,
                                 "url": f"https://github.com/{evidence.REPOSITORY}/actions/runs/{run_id}/artifacts/{artifact_id}"})
    return run


def _metadata():
    candidate = _run("candidate", "110")
    performance = _run("performance", "120")
    return {"metadata_version": 1, "repository": evidence.REPOSITORY,
            "candidate_sha": SHA, "candidate_version": evidence.VERSION,
            "candidate_run": candidate, "performance_run": performance,
            "candidate_delivery_url": candidate["artifacts"][0]["url"]}


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


class FakeGitHubAPI:
    def __init__(self, *, bad_candidate_sha=False, bad_artifact_digest=False,
                 bad_artifact_size=False, bad_job=False):
        self.runs = {}
        self.artifacts = {}
        self.zip_bytes = {}
        self.downloaded = []
        self.bad_artifact_digest = bad_artifact_digest
        self.bad_artifact_size = bad_artifact_size
        for kind, run_id, workflow_id in (("candidate", "110", 101),
                                          ("performance", "120", 202)):
            names = ([f"release-assets-{SHA}", f"release-verification-{SHA}",
                      f"release-package-{SHA}-linux-amd64"] if kind == "candidate" else
                     [f"performance-series-{SHA}-{run_id}", f"performance-soak-{SHA}-{run_id}"])
            workflow_path = github.WORKFLOWS[kind]
            self.runs[run_id] = {
                "id": int(run_id), "workflow_id": workflow_id,
                "repository": {"full_name": evidence.REPOSITORY, "id": 900},
                "head_repository": {"full_name": evidence.REPOSITORY},
                "head_sha": "0" * 40 if bad_candidate_sha and kind == "candidate" else SHA,
                "head_branch": "main", "status": "completed", "conclusion": "success",
                "event": "push" if kind == "candidate" else "workflow_dispatch",
                "run_attempt": 2, "html_url": f"https://github.com/{evidence.REPOSITORY}/actions/runs/{run_id}",
                "path": workflow_path + "@refs/heads/main",
            }
            job_names = evidence.CANDIDATE_JOB_NAMES if kind == "candidate" else evidence.PERFORMANCE_JOB_NAMES
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
                    "workflow_run": {"id": int(run_id), "head_sha": SHA,
                                     "head_branch": "main", "repository_id": 900,
                                     "head_repository_id": 900},
                })
            self.artifacts[run_id] = artifact_records

    def json(self, path):
        path_no_query = path.split("?", 1)[0]
        if path_no_query.endswith("/alpha-release.yml"):
            return {"path": github.WORKFLOWS["candidate"], "id": 101, "state": "active"}
        if path_no_query.endswith("/performance-check.yml"):
            return {"path": github.WORKFLOWS["performance"], "id": 202, "state": "active"}
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
