#!/usr/bin/env python3
"""Content-free progress and provenance for production candidate verification."""
import argparse
import hashlib
import json
import math
import os
import pathlib
import platform
import tempfile
import time

import package
import release_assets


# Mandatory acceptance coverage, independent of which cases a runner happens to emit.
JOURNEY_CASES = (
    'candidate_identity',
    'evaluate',
    'doctor_integrity_stopped_directory',
    'search_smoke.py',
    'mcp_smoke.py',
    'portable_smoke.py',
    'diagnostics_smoke.py',
    'ignore_policy_smoke.py',
    'schema2_schema6_published_alpha4_schema7_upgrade_restore_rollback',
    'desktop_launch_smoke.py',
    'diagnostics_browser_smoke.py',
    'evidence_collection_smoke.py',
    'portable_transfer_browser_smoke.py',
    'packaged_browser_journey.py',
    'passage_window_browser_smoke.py',
    'snapshot_browser_smoke.py',
    'persistent_collection_smoke.py',
    'search_preset_browser_smoke.py',
    'evidence_bundle_browser_smoke.py',
)
ALPHA5_JOURNEY_CASES = JOURNEY_CASES
BETA1_JOURNEY_CASES = JOURNEY_CASES + ('published_alpha5_schema8_same_schema_compatibility',)

TRANSFER_CASES = (
    'candidate_identity',
    'A_fresh_candidate_server_browser',
    'A_ui_folder_late_unicode_passages_pdf2',
    'A_saved_markdown_bundle_order_notes_offsets',
    'A_saved_note_commit_invalidates_cursor_without_mixing_pages',
    'A_preset_scope_late_preview_dirty_evidence',
    'snapshot_ui_dependency',
    'A_real_host_path_export_and_inspect',
    'B_independent_candidate_server_browser',
    'B_UI_inspect_explicit_frozen_import',
    'B_frozen_passages_pdf_no_live_actions',
    'B_save_export_frozen_bundle',
    'A_originals_deleted_B_unchanged_duplicate_preserves_name',
    'C_independent_candidate_server_browser',
    'C_UI_inspect_explicit_frozen_import',
    'C_unrelated_saved_collection',
    'B_to_C_explicit_compatible_mapping_uncertain_commit_reconciled',
    'C_path_media_hash_quote_page_and_live_identity_conflicts_atomic',
    'B_C_restart_exact_evidence_Forget_cascade_unrelated_preserved',
    'serve_demo_no_writable_routes_temporary_evidence_and_local_presets',
)

COMMON_BROWSER_CASES = (
    'empty_start_folder_paging_late_response_corpus_fence_quotes_reload_restart',
    'schema8_diagnostics_late_md_docx_manual_windows_pdf_title_fallback',
    'saved_bundle_notes_order_dirty_guard_and_cursor_navigation',
    'explicit_preset_file_and_host_path_source_export_jobs',
    'second_index_explicit_source_mapping_atomic_bundle_import',
    'preset_preview_explicit_remap_apply_without_search_then_search',
    'portable_inspect_claim_import_frozen_search_duplicate_restart_forget',
)

LINUX_RESOURCE_REPORTS = (
    'alpha5-resource-comparison.json',
    'source-baseline-resource-comparison.json',
    'source-cycle-resource-boundaries.json',
    'snapshot-resource-boundary.json',
)

INSTALLER_CASES = (
    'install', 'repeat-install', 'update-new-extraction', 'uninstall',
    'repeat-uninstall', 'data-and-originals-preserved',
    'reinstall-existing-data-reopen', 'foreign-collision-and-uninstall-refused',
)


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            value.update(block)
    return value.hexdigest()


def run_identity():
    return {"os": platform.platform(), "python": platform.python_version(),
            "run_url": (f"https://github.com/{os.environ['GITHUB_REPOSITORY']}/actions/runs/{os.environ['GITHUB_RUN_ID']}"
                        if os.environ.get("GITHUB_REPOSITORY") and os.environ.get("GITHUB_RUN_ID") else None),
            "run_attempt": os.environ.get("GITHUB_RUN_ATTEMPT")}


class CaseRecorder:
    """Keep failed/interrupted progress; omit exception text, paths and output."""
    def __init__(self, report, destination=None):
        self.report, self.destination = report, destination

    def save(self):
        if self.destination:
            self.destination.parent.mkdir(parents=True, exist_ok=True)
            temporary = self.destination.with_suffix(self.destination.suffix + ".tmp")
            temporary.write_text(json.dumps(self.report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
            temporary.replace(self.destination)

    def run(self, name, operation):
        case = {"case": name, "status": "running"}
        self.report["cases"].append(case)
        self.save()
        started = time.monotonic()
        try:
            result = operation()
            if result is not None: case["details"] = result
            case["status"] = "passed"
            return result
        except BaseException as exc:
            case.update(status="failed", error_type=type(exc).__name__)
            self.report["status"] = "failed"
            raise
        finally:
            case["elapsed_seconds"] = round(time.monotonic() - started, 3)
            self.save()


def native(directory, revision, version, target, status):
    record = {"candidate_sha": revision, "candidate_version": version, "target": target,
              "status": "failed", "source_clean": None, **run_identity()}
    name = next(name for name, item in release_assets.expected_archives(version).items() if item == target)
    archive = directory / name
    if archive.is_file():
        record["archive"] = {"basename": name, "sha256": digest(archive), "size": archive.stat().st_size}
        try:
            package.verify_archive(archive, target, version)
            with tempfile.TemporaryDirectory(prefix="findrail-record-") as temp:
                root = package.extract_verified(archive, pathlib.Path(temp) / "extract")
                metadata = json.loads((root / "BUILD-INFO.json").read_text(encoding="utf-8"))
                record.update(source_clean=metadata.get("dirty") is False, build_info=metadata)
                if (metadata.get("revision"), metadata.get("version"), metadata.get("target"), metadata.get("dirty")) != (revision, version, target, False):
                    raise ValueError("native record provenance differs from candidate")
                binary = root / ("findrail.exe" if target.startswith("windows-") else "findrail")
                record["binary_sha256"] = digest(binary)
                installer = read_record(directory / f"install-lifecycle-{target}.json")
                desktop = root / ("findrail-desktop.exe" if target.startswith("windows-") else
                                  "Findrail.app/Contents/MacOS/Findrail" if target.startswith("darwin-") else "findrail-desktop")
                record["desktop_binary_sha256"] = digest(desktop)
                record["installer_lifecycle"] = installer
                require_installer_lifecycle(record)
            resources = {p.name: read_record(p) for p in sorted(directory.glob("*resource*.json"))}
            if any(item.get("status", "success") != "success" for item in resources.values()):
                raise ValueError("resource measurement is incomplete")
            record["resources"] = resources
            system = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}.get(platform.system())
            machine = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine().lower())
            record["observed_target"] = f"{system}-{machine}"
            if target != record["observed_target"]:
                raise ValueError("archive target does not match native runner")
            if status == "success": record["status"] = "passed"
        except (ValueError, OSError, KeyError) as error:
            record["error_type"] = type(error).__name__
    return record


def read_record(path):
    if path.stat().st_size > 1 << 20:
        raise ValueError("verification record exceeds byte limit")
    with path.open("rb") as stream:
        raw = stream.read((1 << 20) + 1)
    if len(raw) > 1 << 20:
        raise ValueError("verification record exceeds byte limit")
    result = json.loads(raw)
    if not isinstance(result, dict):
        raise ValueError("verification record must be an object")
    return result


def require_installer_lifecycle(record):
    target = record.get("target", "")
    system, architecture = target.split("-", 1)
    expected_os = {"linux": "Linux", "darwin": "Darwin", "windows": "Windows"}.get(system)
    machines = {"amd64": {"x86_64", "amd64"}, "arm64": {"aarch64", "arm64"}}.get(architecture)
    report = record.get("installer_lifecycle")
    if not isinstance(report, dict):
        raise ValueError("native installer lifecycle report is missing")
    for value in (record.get("desktop_binary_sha256"), report.get("binary_sha256"),
                  report.get("archive_sha256")):
        if not isinstance(value, str) or len(value) != 64 or any(c not in "0123456789abcdef" for c in value):
            raise ValueError("native installer lifecycle digest is missing or invalid")
    if (not expected_os or not machines or report.get("candidate_sha") != record.get("candidate_sha") or
            report.get("candidate_version") != record.get("candidate_version") or report.get("os") != expected_os or
            report.get("status") != "passed" or
            str(report.get("architecture", "")).lower() not in machines or
            report.get("archive_sha256") != record.get("archive", {}).get("sha256") or
            report.get("binary_sha256") != record.get("desktop_binary_sha256") or
            report.get("scope") != "disposable owned destination; synthetic documents/data only"):
        raise ValueError("native installer lifecycle provenance is incomplete")
    cases = report.get("cases")
    if (not isinstance(cases, list) or any(not isinstance(case, dict) for case in cases) or
            [case.get("id") for case in cases] != list(INSTALLER_CASES)):
        raise ValueError("native installer lifecycle cases are missing or out of order")
    for case in cases:
        duration = case.get("duration_ms")
        if case.get("status") != "passed" or type(duration) is not int or duration < 0:
            raise ValueError("native installer lifecycle case is incomplete")


def require_cases(cases, required, label):
    if not isinstance(cases, list) or any(not isinstance(case, dict) for case in cases):
        raise ValueError(label + " case list is unavailable")
    names = [case.get("case") for case in cases]
    if any(not isinstance(name, str) for name in names) or len(set(names)) != len(names) or not set(required).issubset(names):
        raise ValueError(label + " has missing or duplicate required cases")
    for case in cases:
        duration = case.get("duration_seconds", case.get("elapsed_seconds"))
        if (case.get("status") != "passed" or type(duration) not in (int, float) or
                not math.isfinite(duration) or duration < 0):
            raise ValueError(label + " contains an incomplete or unmeasured case")


def require_resources(record, revision):
    resources = record.get("resources", {})
    if not isinstance(resources, dict) or not set(LINUX_RESOURCE_REPORTS).issubset(resources):
        raise ValueError("Linux resource evidence is incomplete")
    for name in LINUX_RESOURCE_REPORTS[:3]:
        report = resources[name]
        if (not isinstance(report, dict) or report.get("status") != "success" or
                report.get("binary", {}).get("sha256") != record["binary_sha256"] or
                report.get("binary", {}).get("revision") != revision):
            raise ValueError("resource evidence is incomplete or used another candidate")
    for name, reference in ((LINUX_RESOURCE_REPORTS[0], "1d47f9d54ff81ab566fcf3f8c0c6bf9af86a33f7"),
                            (LINUX_RESOURCE_REPORTS[1], "6b157fe3a6fe2f3f3808732ce72a8e74a118f763")):
        comparison = resources[name].get("comparison", {})
        if (comparison.get("same_host") is not True or comparison.get("fixture_sha256_matches") is not True or
                comparison.get("binary", {}).get("revision") != reference):
            raise ValueError("resource comparison used a different baseline or workload")
    metrics = resources[LINUX_RESOURCE_REPORTS[2]].get("metrics", {})
    paging, bundle = metrics.get("paging_window", {}), metrics.get("evidence_bundle_boundary", {})
    if (paging.get("returned") != 500 or paging.get("beyond_237") is not True or
            bundle.get("codec_boundary_selected_text_bytes") != 2 << 20 or
            bundle.get("accepted_selected_text_bytes") != 1 << 20 or
            bundle.get("one_byte_over_quota_status") != 413 or bundle.get("persistent_collections_created") != 0):
        raise ValueError("resource boundary profile is incomplete")
    snapshot = resources[LINUX_RESOURCE_REPORTS[3]]
    if (snapshot.get("binary_sha256") != record["binary_sha256"] or
            snapshot.get("document_text_bytes") != 8 << 20 or type(snapshot.get("archive_bytes")) is not int or
            snapshot["archive_bytes"] <= 0 or type(snapshot.get("elapsed_seconds")) not in (int, float) or
            not math.isfinite(snapshot["elapsed_seconds"]) or snapshot["elapsed_seconds"] < 0):
        raise ValueError("snapshot resource boundary is incomplete")


def require_journey(browser, revision, version, binary_sha):
    if ((browser.get("candidate_sha"), browser.get("candidate_version"), browser.get("status"), browser.get("browser_skipped")) !=
            (revision, version, "passed", False) or browser.get("source_clean") is not True or
            browser.get("schema_version") != package.schema_version() or browser.get("target") != "linux-amd64" or
            browser.get("binary_sha256") != binary_sha):
        raise ValueError("browser journey is incomplete or used another packaged candidate")
    fixture = browser.get("offline_fixture_sha256", "")
    if not isinstance(fixture, str) or len(fixture) != 64 or any(char not in "0123456789abcdef" for char in fixture):
        raise ValueError("offline fixture provenance is missing")
    required = BETA1_JOURNEY_CASES if version == "0.1.0-beta.1" else ALPHA5_JOURNEY_CASES
    require_cases(browser.get("cases"), required, "candidate journey")
    require_transfer(browser.get("portable_transfer", {}), revision, version, binary_sha)
    common = next(case for case in browser["cases"] if case["case"] == "packaged_browser_journey.py").get("details", {})
    require_common(common, revision, binary_sha)


def require_transfer(transfer, revision, version, binary_sha):
    if ((transfer.get("candidate_sha"), transfer.get("candidate_version"), transfer.get("status"), transfer.get("binary_sha256")) !=
            (revision, version, "passed", binary_sha)):
        raise ValueError("portable transfer used another candidate or did not complete")
    require_cases(transfer.get("cases"), TRANSFER_CASES, "portable transfer")


def require_common(common, revision, binary_sha):
    if ((common.get("candidate_sha"), common.get("status"), common.get("binary_sha256")) != (revision, "passed", binary_sha) or
            common.get("chrome") != "139.0.7258.66" or common.get("chromedriver") != "139.0.7258.66"):
        raise ValueError("common browser did not exercise the pinned candidate/browser")
    require_cases(common.get("cases"), COMMON_BROWSER_CASES, "common browser")


def summarize(native_directory, assets, browser_report, revision, version):
    manifest = release_assets.verify(assets, version, revision)
    records = [read_record(path) for path in sorted(native_directory.rglob("verification.json"))]
    if sorted(record.get("target", "") for record in records) != sorted(package.TARGETS):
        raise ValueError("exactly one native record per target is required")
    for record in records:
        if (record.get("candidate_sha"), record.get("candidate_version"), record.get("status"), record.get("source_clean")) != (revision, version, "passed", True):
            raise ValueError("native record is incomplete or belongs to another candidate")
        archive = record["archive"]
        expected = next(name for name, target in release_assets.expected_archives(version).items() if target == record["target"])
        if archive["basename"] != expected:
            raise ValueError("native archive name differs from its target")
        actual = assets / archive["basename"]
        if archive["sha256"] != digest(actual) or archive["size"] != actual.stat().st_size:
            raise ValueError("native record archive differs from the aggregated asset")
        require_installer_lifecycle(record)
        with tempfile.TemporaryDirectory(prefix="findrail-installer-identity-") as temporary:
            extracted = package.extract_verified(actual, pathlib.Path(temporary) / "extract")
            target = record["target"]
            desktop = extracted / ("findrail-desktop.exe" if target.startswith("windows-") else
                                   "Findrail.app/Contents/MacOS/Findrail" if target.startswith("darwin-") else
                                   "findrail-desktop")
            cli = extracted / ("findrail.exe" if target.startswith("windows-") else "findrail")
            if (record.get("desktop_binary_sha256") != digest(desktop) or
                    record.get("binary_sha256") != digest(cli)):
                raise ValueError("native executable digests differ from the aggregated archive")
    browser = read_record(browser_report)
    linux = next(record for record in records if record["target"] == "linux-amd64")
    require_resources(linux, revision)
    require_journey(browser, revision, version, linux["binary_sha256"])
    return {"candidate_sha": revision, "candidate_version": version, "status": "passed",
            "schema_version": manifest["schema_version"], "client_api_version": manifest["client_api_version"],
            "extension_version": manifest["extension_version"], "source_clean": True, **run_identity(),
            "native": records, "journey": browser,
            "assets": [{"basename": path.name, "sha256": digest(path), "size": path.stat().st_size}
                       for path in sorted(assets.iterdir())]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("native", "summary"))
    parser.add_argument("--candidate-sha", required=True)
    parser.add_argument("--candidate-version", required=True)
    parser.add_argument("--native-directory", required=True, type=pathlib.Path)
    parser.add_argument("--target", choices=package.TARGETS)
    parser.add_argument("--status", default="failure")
    parser.add_argument("--assets", type=pathlib.Path)
    parser.add_argument("--browser-report", type=pathlib.Path)
    parser.add_argument("--report", type=pathlib.Path, required=True)
    args = parser.parse_args()
    if args.mode == "native":
        if not args.target: parser.error("native requires --target")
        result = native(args.native_directory, args.candidate_sha, args.candidate_version, args.target, args.status)
    else:
        if not args.assets or not args.browser_report: parser.error("summary requires --assets and --browser-report")
        result = summarize(args.native_directory, args.assets, args.browser_report, args.candidate_sha, args.candidate_version)
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(result, sort_keys=True, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
