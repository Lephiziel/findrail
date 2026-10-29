#!/usr/bin/env python3
"""Assemble and verify a bounded, offline beta.1 evidence handoff.

GitHub run/artifact authenticity is established by beta_evidence_github.py in
the read-only workflow. This module only validates explicit local inputs and
never downloads, builds, or executes an application binary.
"""
import argparse
import hashlib
import json
import math
import os
import pathlib
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import zipfile
from datetime import datetime, timezone

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import acceptance_fixture
import acceptance_kit
import candidate_record
import manual_evidence
import package
import readiness
import release_assets
import resource_series

REPOSITORY = "Lephiziel/findrail"
VERSION = "0.1.0-beta.1"
SCHEMA = 8
CLIENT_API = 1
VSIX_VERSION = "0.1.0"
BASELINE_SHA = "cb53ee9638f69b47b386921e41368f3b66ceddde"
SHA_RE = re.compile(r"^[0-9a-f]{40}$")
HASH_RE = re.compile(r"^[0-9a-f]{64}$")
ARTIFACT_DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
RUN_ID_RE = re.compile(r"^[1-9][0-9]{0,19}$")

# Existing producer bounds, plus an aggregate ceiling for extracted artifact
# contents and the reports-only handoff. These comfortably exceed TASK 21/23
# outputs while keeping untrusted JSON and archives finite.
MAX_JSON = 16 << 20
MAX_MANUAL_JSON = 8 << 10
MAX_INPUT_FILES = 256
MAX_INPUT_TREE = 512 << 20
MAX_REPORT_TREE = 64 << 20
MAX_HANDOFF_TREE = 128 << 20
MAX_FILE = 32 << 20
MAX_ARCHIVE_MEMBER = 64 << 20
MAX_ARCHIVE_TOTAL = 256 << 20
MAX_ZIP_RATIO = 1000
MAX_ZIP_MEMBERS = 512
MAX_REPORTS_ZIP = 128 << 20
MAX_SCALE_JSON = 4 << 20
MAX_PROVENANCE = 128 << 10

CANDIDATE_TARGET_RUNNERS = {
    "linux-amd64": "ubuntu-latest",
    "linux-arm64": "ubuntu-24.04-arm",
    "darwin-amd64": "macos-15-intel",
    "darwin-arm64": "macos-14",
    "windows-amd64": "windows-latest",
}
CANDIDATE_JOB_NAMES = {
    "validate-source",
    "source-checks / ui",
    "source-checks / race",
    "source-checks / check (ubuntu-latest)",
    "source-checks / check (macos-latest)",
    "source-checks / check (windows-latest)",
    "installed-vsix",
    "packaged-browser-journey",
    "aggregate-and-verify",
    "reusable-final-verification / verify-assets",
    "release-state",
    "publish",
    *(f"native-packages ({target}, {runner})" for target, runner in CANDIDATE_TARGET_RUNNERS.items()),
}
PERFORMANCE_JOB_NAMES = {"series", "soak"}


class EvidenceError(ValueError):
    """A content-free, stable validation failure."""

    def __init__(self, code="input_invalid"):
        super().__init__(code)
        self.code = code


def require(condition, code="input_invalid"):
    if not condition:
        raise EvidenceError(code)


def sha256_bytes(value):
    return hashlib.sha256(value).hexdigest()


def _check_components(path, *, allow_missing=False):
    """Reject symlinks in the lexical path, including existing parents."""
    path = pathlib.Path(os.path.abspath(os.fspath(path)))
    current = pathlib.Path(path.anchor)
    for part in path.parts[1:]:
        current = current / part
        try:
            info = current.lstat()
        except FileNotFoundError:
            if allow_missing:
                continue
            raise EvidenceError("input_missing") from None
        if stat.S_ISLNK(info.st_mode):
            raise EvidenceError("unsafe_path")
    return path


def _regular_file(path, maximum=MAX_FILE):
    path = _check_components(path)
    info = path.lstat()
    require(stat.S_ISREG(info.st_mode) and not stat.S_ISLNK(info.st_mode), "unsafe_file")
    require(info.st_size <= maximum, "input_too_large")
    return path


def _safe_json_bytes(raw, maximum=MAX_JSON):
    require(len(raw) <= maximum, "input_too_large")

    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result, "duplicate_json_key")
            result[key] = value
        return result

    def parse_float(value):
        number = float(value)
        require(math.isfinite(number), "nonfinite_json")
        return number

    def parse_int(value):
        require(len(value.lstrip("-")) <= 32, "json_number_too_large")
        return int(value)

    def parse_constant(_value):
        raise EvidenceError("nonfinite_json")

    try:
        parsed = json.loads(raw.decode("utf-8"), object_pairs_hook=pairs,
                            parse_float=parse_float, parse_int=parse_int,
                            parse_constant=parse_constant)
    except EvidenceError:
        raise
    except (UnicodeDecodeError, json.JSONDecodeError, RecursionError, ValueError):
        raise EvidenceError("invalid_json") from None
    require(type(parsed) is dict, "json_object_required")
    return parsed


def load_json(path, maximum=MAX_JSON):
    path = _regular_file(path, maximum)
    with path.open("rb") as stream:
        raw = stream.read(maximum + 1)
    return _safe_json_bytes(raw, maximum), raw


def write_json(path, value):
    raw = (json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2,
                      allow_nan=False) + "\n").encode("utf-8")
    path = pathlib.Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(raw)
    return raw


def file_digest(path, maximum=None):
    path = _regular_file(path, maximum or MAX_FILE)
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def safe_tree(root, *, max_files=MAX_INPUT_FILES, max_total=MAX_INPUT_TREE,
              max_file=MAX_FILE):
    root = _check_components(root)
    require(root.is_dir() and not root.is_symlink(), "unsafe_directory")
    files, seen, total = [], set(), 0
    for current, directories, names in os.walk(root, topdown=True, followlinks=False):
        base = pathlib.Path(current)
        for name in list(directories):
            path = base / name
            info = path.lstat()
            require(stat.S_ISDIR(info.st_mode) and not stat.S_ISLNK(info.st_mode), "unsafe_tree")
            rel = path.relative_to(root).as_posix()
            _safe_relative(rel)
        for name in names:
            path = base / name
            info = path.lstat()
            require(stat.S_ISREG(info.st_mode) and not stat.S_ISLNK(info.st_mode), "unsafe_tree")
            relative = path.relative_to(root).as_posix()
            _safe_relative(relative)
            folded = relative.casefold()
            require(folded not in seen, "duplicate_path")
            seen.add(folded)
            require(info.st_size <= max_file, "input_too_large")
            total += info.st_size
            require(total <= max_total, "input_too_large")
            files.append((relative, path, info.st_size))
            require(len(files) <= max_files, "too_many_files")
    return sorted(files)


def _safe_relative(name):
    p = pathlib.PurePosixPath(name)
    require(bool(name) and not p.is_absolute() and p.as_posix() == name and
            all(part not in ("", ".", "..") for part in name.split("/")) and
            "\\" not in name and ":" not in name and "\x00" not in name,
            "unsafe_path")


def _zip_members(path, *, total_limit=MAX_ARCHIVE_TOTAL,
                 member_limit=MAX_ARCHIVE_MEMBER, max_members=MAX_ZIP_MEMBERS):
    """Inspect ZIP metadata before reading/extracting any member."""
    path = _regular_file(path, MAX_INPUT_TREE)
    try:
        with zipfile.ZipFile(path) as archive:
            infos = archive.infolist()
            require(0 < len(infos) <= max_members, "unsafe_zip")
            total = 0
            names = set()
            for item in infos:
                raw_name = item.filename
                directory = item.is_dir()
                name = raw_name[:-1] if directory and raw_name.endswith("/") else raw_name
                _safe_relative(name)
                folded = name.casefold()
                require(folded not in names, "duplicate_zip_member")
                names.add(folded)
                require(not item.flag_bits & 1, "encrypted_zip")
                require(item.compress_type in (zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED), "unsafe_zip")
                mode = item.external_attr >> 16
                kind = stat.S_IFMT(mode)
                require(kind in (0, stat.S_IFREG, stat.S_IFDIR), "unsafe_zip_member")
                require((not directory and kind != stat.S_IFDIR) or
                        (directory and kind in (0, stat.S_IFDIR)), "unsafe_zip_member")
                require(item.file_size <= member_limit, "zip_member_too_large")
                total += item.file_size
                require(total <= total_limit, "zip_bomb")
                compressed = item.compress_size
                require(item.file_size == 0 or compressed > 0, "zip_bomb")
                if compressed:
                    require(item.file_size <= compressed * MAX_ZIP_RATIO, "zip_bomb")
            if path.stat().st_size:
                require(total <= path.stat().st_size * MAX_ZIP_RATIO, "zip_bomb")
            return [(item.filename[:-1] if item.is_dir() and item.filename.endswith("/") else item.filename,
                     item.file_size, item) for item in infos]
    except EvidenceError:
        raise
    except (OSError, zipfile.BadZipFile, RuntimeError, EOFError):
        raise EvidenceError("unsafe_zip") from None


def _tar_members(path):
    path = _regular_file(path, MAX_INPUT_TREE)
    try:
        with __import__("tarfile").open(path, "r:gz") as archive:
            members = archive.getmembers()
            require(0 < len(members) <= MAX_ZIP_MEMBERS, "unsafe_archive")
            names, total = set(), 0
            result = []
            for item in members:
                require(item.isfile() or item.isdir(), "unsafe_archive_member")
                name = item.name[:-1] if item.isdir() and item.name.endswith("/") else item.name
                _safe_relative(name)
                folded = name.casefold()
                require(folded not in names, "duplicate_archive_member")
                names.add(folded)
                require(item.size >= 0 and item.size <= MAX_ARCHIVE_MEMBER, "archive_member_too_large")
                total += item.size
                require(total <= MAX_ARCHIVE_TOTAL, "archive_too_large")
                result.append((name, item.size, item.isdir()))
            compressed = path.stat().st_size
            require(total == 0 or (compressed > 0 and total <= compressed * MAX_ZIP_RATIO), "archive_bomb")
            return result
    except EvidenceError:
        raise
    except (OSError, __import__("tarfile").TarError, EOFError):
        raise EvidenceError("unsafe_archive") from None


def verify_archive_bounds(path):
    if pathlib.Path(path).name.endswith((".zip", ".vsix")):
        _zip_members(path)
    else:
        _tar_members(path)


def validate_assets(assets_dir, candidate_sha, candidate_version):
    require(candidate_version == VERSION and SHA_RE.fullmatch(candidate_sha) is not None,
            "candidate_identity_mismatch")
    files = safe_tree(assets_dir, max_files=16, max_total=MAX_INPUT_TREE,
                      max_file=MAX_INPUT_TREE)
    expected = set(release_assets.expected_archives(candidate_version)) | {
        release_assets.VSIX, "release-manifest.json", "SHA256SUMS.txt"}
    require({name for name, _, _ in files} == expected, "asset_set_mismatch")
    for name in expected:
        path = pathlib.Path(assets_dir) / name
        if name.endswith((".zip", ".tar.gz")) or name == release_assets.VSIX:
            verify_archive_bounds(path)
    manifest_json, _ = load_json(pathlib.Path(assets_dir) / "release-manifest.json", 1 << 20)
    try:
        manifest = release_assets.verify(assets_dir, candidate_version, candidate_sha,
                                         VSIX_VERSION)
    except (ValueError, KeyError, OSError, zipfile.BadZipFile, __import__("tarfile").TarError):
        raise EvidenceError("release_assets_invalid") from None
    require(manifest_json == manifest, "release_manifest_invalid")
    return manifest


def validate_trusted_metadata(metadata, candidate_sha, candidate_version):
    """Check the normalized output of the read-only GitHub API preflight."""
    require(type(metadata) is dict and metadata.get("metadata_version") == 1,
            "run_metadata_invalid")
    require(metadata.get("repository") == REPOSITORY and
            metadata.get("candidate_sha") == candidate_sha and
            metadata.get("candidate_version") == candidate_version,
            "run_metadata_mismatch")
    candidate = metadata.get("candidate_run")
    performance = metadata.get("performance_run")
    _validate_normalized_run(candidate, "candidate", candidate_sha)
    _validate_normalized_run(performance, "performance", candidate_sha)
    require(candidate["workflow_path"] == ".github/workflows/alpha-release.yml" and
            candidate["event"] == "push", "candidate_run_invalid")
    require(performance["workflow_path"] == ".github/workflows/performance-check.yml" and
            performance["event"] == "workflow_dispatch", "performance_run_invalid")
    expected_candidate = {
        f"release-assets-{candidate_sha}",
        f"release-verification-{candidate_sha}",
        f"release-package-{candidate_sha}-linux-amd64",
    }
    expected_performance = {
        f"performance-series-{candidate_sha}-{performance['run_id']}",
        f"performance-soak-{candidate_sha}-{performance['run_id']}",
    }
    _validate_artifacts(candidate, expected_candidate)
    _validate_artifacts(performance, expected_performance)
    candidate_assets = _artifact_by_name(candidate, f"release-assets-{candidate_sha}")
    expected_url = (f"https://github.com/{REPOSITORY}/actions/runs/"
                    f"{candidate['run_id']}/artifacts/{candidate_assets['id']}")
    require(metadata.get("candidate_delivery_url") == expected_url,
            "candidate_delivery_url_invalid")
    return metadata


def _validate_normalized_run(run, kind, candidate_sha):
    require(type(run) is dict and RUN_ID_RE.fullmatch(str(run.get("run_id", ""))) is not None,
            f"{kind}_run_invalid")
    require(type(run.get("run_attempt")) is int and run["run_attempt"] > 0 and
            run.get("workflow_id") and str(run["workflow_id"]).isdigit(),
            f"{kind}_run_invalid")
    run_id = str(run["run_id"])
    require((run.get("head_sha"), run.get("head_branch"), run.get("status"),
             run.get("conclusion"), run.get("url")) ==
            (candidate_sha, "main", "completed", "success",
             f"https://github.com/{REPOSITORY}/actions/runs/{run_id}"),
            f"{kind}_run_mismatch")
    required_jobs = CANDIDATE_JOB_NAMES if kind == "candidate" else PERFORMANCE_JOB_NAMES
    jobs = run.get("jobs")
    require(type(jobs) is list, f"{kind}_jobs_invalid")
    by_name = {}
    for job in jobs:
        require(type(job) is dict and type(job.get("name")) is str and
                job["name"] not in by_name, f"{kind}_jobs_invalid")
        by_name[job["name"]] = job
        require(job.get("run_attempt") == run["run_attempt"] and
                job.get("status") == "completed", f"{kind}_attempt_mismatch")
        if kind == "candidate" and job["name"] == "publish":
            require(job.get("conclusion") in ("success", "skipped"), "candidate_job_failed")
        else:
            require(job.get("conclusion") == "success", f"{kind}_job_failed")
    require(set(by_name) == required_jobs, f"{kind}_jobs_incomplete")
    artifacts = run.get("artifacts")
    require(type(artifacts) is list, f"{kind}_artifacts_invalid")


def _validate_artifacts(run, expected_names):
    artifacts = run["artifacts"]
    names = [a.get("name") if type(a) is dict else None for a in artifacts]
    require(all(type(name) is str for name in names) and len(names) == len(set(names)),
            "artifact_name_ambiguous")
    require(all(re.fullmatch(r"[A-Za-z0-9_.-]{1,128}", name) is not None for name in names),
            "artifact_name_invalid")
    by_name = {a["name"]: a for a in artifacts}
    ids = [str(item.get("id", "")) for item in artifacts if type(item) is dict]
    require(len(ids) == len(set(ids)), "artifact_id_ambiguous")
    for name in expected_names:
        artifact = by_name.get(name)
        require(type(artifact) is dict, "artifact_missing")
        require(RUN_ID_RE.fullmatch(str(artifact.get("id", ""))) is not None and
                ARTIFACT_DIGEST_RE.fullmatch(str(artifact.get("digest", ""))) is not None,
                "artifact_identity_invalid")
        require(type(artifact.get("size_in_bytes")) is int and
                0 < artifact["size_in_bytes"] <= MAX_INPUT_TREE,
                "artifact_size_invalid")
        require(artifact.get("expired") is False and
                _future_timestamp(artifact.get("expires_at")), "artifact_expired")
        require(artifact.get("url") ==
                f"https://github.com/{REPOSITORY}/actions/runs/{run['run_id']}/artifacts/{artifact['id']}",
                "artifact_url_invalid")


def _future_timestamp(value):
    if type(value) is not str:
        return False
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        return parsed.tzinfo is not None and parsed > datetime.now(timezone.utc)
    except (ValueError, TypeError):
        return False


def _artifact_by_name(run, name):
    found = [item for item in run["artifacts"] if item["name"] == name]
    require(len(found) == 1, "artifact_missing_or_ambiguous")
    return found[0]


def _validate_summary(summary, assets_dir, sha, version):
    try:
        native = readiness.strict_candidate(summary, sha, version)
    except (ValueError, KeyError, TypeError, AttributeError, StopIteration):
        raise EvidenceError("strict_candidate_invalid") from None
    manifest = validate_assets(assets_dir, sha, version)
    manifest_assets = {item["basename"]: item for item in manifest["assets"]}
    summary_assets = summary.get("assets", [])
    require(type(summary_assets) is list and len(summary_assets) == len(manifest_assets),
            "summary_assets_invalid")
    seen = set()
    for item in summary_assets:
        require(type(item) is dict and item.get("basename") not in seen,
                "summary_assets_invalid")
        seen.add(item.get("basename"))
        expected = manifest_assets.get(item.get("basename"))
        require(expected is not None and
                (item.get("size"), item.get("sha256")) ==
                (expected["size"], expected["sha256"]), "summary_asset_mismatch")
    require(seen == set(manifest_assets), "summary_assets_invalid")
    return native, manifest


def extract_candidate(assets_dir, summary_path, sha, version, output_dir):
    output_dir = _check_components(output_dir, allow_missing=True)
    _require_output_separate(output_dir, (assets_dir, summary_path))
    summary, _ = load_json(summary_path)
    native, manifest = _validate_summary(summary, assets_dir, sha, version)
    archive_name = next(name for name, target in release_assets.expected_archives(version).items()
                        if target == "linux-amd64")
    archive = pathlib.Path(assets_dir) / archive_name
    linux = native["linux-amd64"]
    record = linux["archive"]
    require((record.get("basename"), record.get("size"), record.get("sha256")) ==
            (archive_name, archive.stat().st_size, file_digest(archive)),
            "linux_archive_mismatch")
    require(not pathlib.Path(output_dir).exists(), "extraction_destination_exists")
    try:
        package.verify_archive(archive, "linux-amd64", version, manifest["schema_version"])
        root = package.extract_verified(archive, output_dir)
    except (ValueError, OSError, __import__("tarfile").TarError):
        raise EvidenceError("linux_archive_invalid") from None
    binary = root / "findrail"
    digest = file_digest(binary, MAX_ARCHIVE_MEMBER)
    require(digest == linux.get("binary_sha256"), "linux_binary_hash_mismatch")
    return root, digest


def prepare_kits(assets_dir, metadata_path, sha, version, output_dir):
    output_dir = _check_components(output_dir, allow_missing=True)
    _require_output_separate(output_dir, (assets_dir, metadata_path))
    metadata, _ = load_json(metadata_path)
    validate_trusted_metadata(metadata, sha, version)
    manifest = validate_assets(assets_dir, sha, version)
    require(not pathlib.Path(output_dir).exists(), "kits_destination_exists")
    output_dir = pathlib.Path(output_dir)
    output_dir.mkdir(parents=True)
    delivery_url = metadata["candidate_delivery_url"]
    built = []
    try:
        for target in ("linux-amd64", "windows-amd64", "darwin-arm64"):
            destination = output_dir / f"{target}.zip"
            acceptance_kit.build(assets_dir, target, destination, delivery_url)
            verify_archive_bounds(destination)
            verify_kit(destination, assets_dir, manifest, sha, version, target, delivery_url,
                       file_digest(pathlib.Path(assets_dir) / "release-manifest.json"),
                       file_digest(pathlib.Path(assets_dir) / "SHA256SUMS.txt"))
            built.append(destination)
    except (ValueError, OSError, KeyError, zipfile.BadZipFile):
        shutil.rmtree(output_dir, ignore_errors=True)
        raise EvidenceError("acceptance_kit_invalid") from None
    return built


def verify_kit(path, assets_dir, manifest, sha, version, target, delivery_url,
               manifest_sha, checksums_sha):
    members = _zip_members(path, total_limit=8 << 20, member_limit=2 << 20, max_members=32)
    names = {name for name, size, _ in members}
    expected_names = {
        "START_HERE.md", "EXPECTED_RESULTS.md", "candidate.json", "REPORT.md",
        "report.json", "VOLUNTARY_USAGE_TEMPLATE.json", *acceptance_fixture.files().keys(),
    }
    require(names == expected_names, "acceptance_kit_layout_invalid")
    try:
        with zipfile.ZipFile(path) as archive:
            candidate = _safe_json_bytes(archive.read("candidate.json"), 1 << 20)
            report = _safe_json_bytes(archive.read("report.json"), 1 << 20)
    except EvidenceError:
        raise
    except (OSError, zipfile.BadZipFile, KeyError):
        raise EvidenceError("acceptance_kit_invalid") from None
    target_archive = next(item for item in manifest["assets"] if item.get("target") == target)
    require((candidate.get("kind"), candidate.get("candidate_sha"),
             candidate.get("candidate_version"), candidate.get("schema_version"),
             candidate.get("client_api_version"), candidate.get("target"),
             candidate.get("archive"), candidate.get("manifest_sha256"),
             candidate.get("checksums_sha256"), candidate.get("delivery_method"),
             candidate.get("release_url")) ==
            ("source_candidate", sha, version, SCHEMA, CLIENT_API, target,
             {"basename": target_archive["basename"], "size_bytes": target_archive["size"],
               "sha256": target_archive["sha256"]},
              manifest_sha, checksums_sha,
              delivery_url, None), "acceptance_kit_identity_mismatch")
    kind = {"linux-amd64": "linux_first_run", "windows-amd64": "windows_first_run",
            "darwin-arm64": "apple_silicon_first_run"}[target]
    require((report.get("kind"), report.get("evidence_type"), report.get("status"),
             report.get("candidate_sha"), report.get("candidate_version"),
             report.get("target"), report.get("archive_sha256"), report.get("reference")) ==
            (kind, "manual", "not_run", sha, version, target,
             target_archive["sha256"], ""), "acceptance_kit_report_invalid")
    cases = report.get("cases")
    require(type(cases) is list and len(cases) == len(acceptance_kit.CASES) and
            [case.get("id") for case in cases] == [case_id for case_id, _ in acceptance_kit.CASES] and
            all(case.get("status") == "not_run" and case.get("elapsed_seconds") is None and
                case.get("observation") == "" for case in cases), "acceptance_kit_report_invalid")
    try:
        with tempfile.TemporaryDirectory(prefix=".verify-kit-", dir=pathlib.Path(path).parent) as temp:
            expected_path = pathlib.Path(temp) / "expected.zip"
            acceptance_kit.build(assets_dir, target, expected_path, delivery_url)
            with zipfile.ZipFile(path) as actual_zip, zipfile.ZipFile(expected_path) as expected_zip:
                require(set(actual_zip.namelist()) == set(expected_zip.namelist()),
                        "acceptance_kit_content_invalid")
                for name in expected_zip.namelist():
                    require(actual_zip.read(name) == expected_zip.read(name),
                            "acceptance_kit_content_invalid")
    except EvidenceError:
        raise
    except (OSError, ValueError, KeyError, zipfile.BadZipFile):
        raise EvidenceError("acceptance_kit_content_invalid") from None


def _read_scale(path, kind, candidate, native):
    record, raw = load_json(path, MAX_SCALE_JSON)
    try:
        readiness.check_scale({"kind": kind, "measurement": record}, candidate, native)
    except (ValueError, KeyError, TypeError, AttributeError):
        raise EvidenceError("scale_record_invalid") from None
    count = readiness.SCALE_FIXTURES[kind][0]
    fixture = record.get("fixture", {})
    workload = record.get("workload", {})
    metrics = record.get("metrics", {})
    rare_count = (count - 1) // 97 + 1
    common_count = count // 2
    require(record.get("errors") == [] and
            (workload.get("indexed_documents"), workload.get("updated_documents"),
             workload.get("unchanged_documents"), workload.get("skipped_documents"),
             workload.get("synthetic_pdf_pages")) ==
            (count, count, 0, 0, count // 4), "scale_workload_invalid")
    require((record.get("binary", {}).get("schema"), record.get("binary", {}).get("revision"),
             record.get("binary", {}).get("version")) ==
            (SCHEMA, candidate["candidate_sha"], candidate["candidate_version"]),
            "scale_binary_invalid")
    require((fixture.get("version"), fixture.get("seed"), fixture.get("documents_requested")) ==
            (2, 42, count), "scale_fixture_invalid")
    _positive_int(fixture.get("generated_text_input_bytes"))
    _positive_int(fixture.get("generated_source_file_bytes"))
    _hash(fixture.get("corpus_sha256"))
    require(metrics.get("synthetic_query_ids") == ["common", "rare"] and
            metrics.get("known_result_shape") is True and
            metrics.get("result_count_min") == rare_count and
            metrics.get("result_count_max") == common_count and
            metrics.get("search_repeats") == 10,
            "scale_query_shape_invalid")
    warm = metrics.get("warm_queries", {})
    require(set(warm) == {"common", "rare"}, "scale_query_shape_invalid")
    for name in ("common", "rare"):
        item = warm[name]
        require(item.get("repeats") == 5, "scale_query_shape_invalid")
        _latency_summary(item.get("latency_ms"))
    paging = metrics.get("paging_window", {})
    require((paging.get("window_limit"), paging.get("total"), paging.get("returned")) ==
            (1000, common_count, 1000), "scale_paging_invalid")
    for page_size in (20, 50, 100):
        page = paging.get("page_sizes", {}).get(str(page_size), {})
        expected_pages = math.ceil(1000 / page_size)
        require((page.get("pages"), page.get("returned"), page.get("window_complete"),
                 page.get("sample_count"), page.get("warmup_requests")) ==
                (expected_pages, 1000, False, expected_pages, 0), "scale_paging_invalid")
        require(type(page.get("max_response_bytes")) is int and
                0 < page["max_response_bytes"] <= 512 << 10, "scale_paging_invalid")
        _latency_summary(page.get("latency_ms"))
    refresh = metrics.get("refresh_cycle", {})
    require(refresh.get("successful_refresh_samples") == 1 and
            refresh.get("successful_refresh_updated_documents") == 1 and
            refresh.get("canceled_refresh_samples") == 1 and
            type(refresh.get("canceled_refresh_exit_code")) is int and
            refresh["canceled_refresh_exit_code"] != 0 and
            refresh.get("canceled_scan_preserved_previous_content") is True and
            refresh.get("concurrent_read_errors") == 0 and
            type(refresh.get("concurrent_read_samples")) is int and
            refresh["concurrent_read_samples"] > 0,
            "scale_refresh_invalid")
    for field in ("successful_refresh_wall_ms", "canceled_refresh_wall_ms"):
        _duration(refresh.get(field))
    boundary = metrics.get("evidence_bundle_boundary", {})
    require((boundary.get("codec_boundary_selected_text_bytes"),
             boundary.get("codec_boundary_entries"), boundary.get("codec_boundary_status"),
             boundary.get("accepted_selected_text_bytes"), boundary.get("accepted_entries"),
             boundary.get("one_byte_over_quota_status"),
             boundary.get("persistent_collections_created")) ==
            (2 << 20, 32, "quota_conflict", 1 << 20, 16, 413, 0),
            "scale_evidence_quota_invalid")
    _positive_int(boundary.get("request_bytes"))
    require(type(boundary.get("preview_response_bytes")) is int and
            0 < boundary["preview_response_bytes"] <= 256 << 10,
            "scale_evidence_quota_invalid")
    _duration(boundary.get("preview_wall_ms"))
    for field in ("index_wall_ms", "server_startup_ms", "first_query_after_server_start_ms",
                  "database_bytes_after_index_before_search"):
        _duration(metrics.get(field))
    environment = record.get("environment", {})
    require(environment.get("os") == "Linux" and
            environment.get("arch") in ("x86_64", "amd64"), "scale_environment_invalid")
    return record, raw


def _duration(value):
    require(type(value) in (int, float) and math.isfinite(value) and value >= 0,
            "measurement_invalid")


def _positive_int(value):
    require(type(value) is int and value > 0, "measurement_invalid")


def _hash(value):
    require(type(value) is str and HASH_RE.fullmatch(value) is not None,
            "measurement_hash_invalid")


def _latency_summary(value):
    require(type(value) is dict, "measurement_invalid")
    _duration(value.get("p50"))
    _duration(value.get("p95"))


def _expected_series_filenames():
    names = {"provenance.txt", "results/series.json"}
    for count in (10000, 100000):
        for repeat in (1, 2, 3):
            for role in ("candidate", "baseline"):
                stem = f"{count}-{repeat}-{role}"
                names.add(f"results/{stem}.json")
                names.add(f"results/{stem}-samples.json")
    return names


def _validate_series(series_dir, candidate_sha, candidate_version, binary_sha):
    files = safe_tree(series_dir, max_files=32, max_total=MAX_REPORT_TREE,
                      max_file=MAX_FILE)
    by_name = {name: path for name, path, _ in files}
    require(set(by_name) == _expected_series_filenames(), "series_files_incomplete")
    provenance = by_name["provenance.txt"].read_bytes()
    require(len(provenance) <= MAX_PROVENANCE, "provenance_too_large")
    text = provenance.decode("utf-8", "strict")
    require(f"candidate_sha={candidate_sha}" in text and
            f"baseline_sha={BASELINE_SHA}" in text and
            "packager=production scripts/package.py; target=linux-amd64" in text,
            "series_provenance_invalid")
    series, _ = load_json(by_name["results/series.json"])
    require((series.get("schema"), series.get("kind"), series.get("status"),
             series.get("qualification")) ==
            (1, "performance_series", "passed", "reference_only"),
            "series_status_invalid")
    identity = series.get("identity", {})
    candidate = identity.get("candidate", {})
    baseline = identity.get("baseline", {})
    candidate_identity = (candidate.get("sha256"), candidate.get("revision"),
                          candidate.get("version"), candidate.get("schema"))
    baseline_identity = (baseline.get("sha256"), baseline.get("revision"), baseline.get("version"))
    require(candidate_identity == (binary_sha, candidate_sha, candidate_version, SCHEMA),
            "series_candidate_identity_mismatch")
    _hash(baseline.get("sha256"))
    require(baseline_identity == (baseline.get("sha256"), BASELINE_SHA, "0.1.0-alpha.5") and
            baseline["sha256"] != binary_sha, "series_baseline_identity_invalid")
    environment = series.get("environment", {})
    require(environment.get("os") == "Linux" and
            environment.get("arch") in ("x86_64", "amd64"), "series_environment_invalid")
    measurement = series.get("measurement", {})
    require(measurement.get("order") == "AB,BA,AB" and
            measurement.get("wall_deadline_seconds") == 2700 and
            measurement.get("child_concurrency_limit") == 1 and
            measurement.get("output_budget_bytes") == resource_series.MAX_OUTPUT,
            "series_method_invalid")
    runs = series.get("runs")
    require(type(runs) is list and len(runs) == 12 and
            series.get("observed_runs") == 12 and series.get("expected_runs") == 12 and
            series.get("errors") == [], "series_incomplete")
    seen = set()
    corpora = {10000: set(), 100000: set()}
    measured_samples = {}
    binaries = {"candidate": binary_sha, "baseline": baseline["sha256"]}
    for run in runs:
        require(type(run) is dict, "series_run_invalid")
        count, repeat, role = run.get("size"), run.get("repeat"), run.get("role")
        key = (count, repeat, role)
        require(type(count) is int and count in (10000, 100000) and
                type(repeat) is int and repeat in (1, 2, 3) and
                role in binaries and key not in seen and run.get("status") == "success" and
                run.get("reason") is None, "series_run_invalid")
        seen.add(key)
        report_name = f"results/{count}-{repeat}-{role}.json"
        sample_name = f"results/{count}-{repeat}-{role}-samples.json"
        require((run.get("report"), run.get("samples")) ==
                (pathlib.PurePosixPath(report_name).name,
                 pathlib.PurePosixPath(sample_name).name), "series_file_reference_invalid")
        report_path, sample_path = by_name[report_name], by_name[sample_name]
        require(run.get("report_sha256") == file_digest(report_path) and
                run.get("samples_sha256") == file_digest(sample_path),
                "series_file_digest_mismatch")
        report, _ = load_json(report_path, MAX_SCALE_JSON)
        sidecar, _ = load_json(sample_path, MAX_SCALE_JSON)
        raw_binary = report.get("binary", {})
        expected_revision = candidate_sha if role == "candidate" else BASELINE_SHA
        expected_version = candidate_version if role == "candidate" else "0.1.0-alpha.5"
        require((report.get("schema"), report.get("status"), report.get("errors"),
                 raw_binary.get("sha256"), raw_binary.get("revision"),
                 raw_binary.get("version"), raw_binary.get("schema")) ==
                (1, "success", [], binaries[role], expected_revision, expected_version, SCHEMA),
                "series_raw_report_invalid")
        fixture, workload = report.get("fixture", {}), report.get("workload", {})
        require((fixture.get("version"), fixture.get("seed"), fixture.get("documents_requested"),
                 workload.get("indexed_documents"), workload.get("skipped_documents")) ==
                (2, 42, count, count, 0), "series_fixture_invalid")
        corpus = fixture.get("corpus_sha256")
        _hash(corpus)
        require(run.get("corpus_sha256") == corpus, "series_fixture_invalid")
        corpora[count].add(corpus)
        try:
            samples = resource_series.validate_samples(sidecar, report, binaries[role],
                                                       100, count, 42)
        except (ValueError, KeyError, TypeError, AttributeError):
            raise EvidenceError("series_samples_invalid") from None
        require(all(len(values) == 100 for values in samples.values()) and
                run.get("observed_samples") == {"common": 100, "rare": 100},
                "series_samples_incomplete")
        measured_samples[key] = samples
    require(seen == {(count, repeat, role) for count in (10000, 100000)
                     for repeat in (1, 2, 3) for role in ("candidate", "baseline")},
            "series_incomplete")
    require(all(len(values) == 1 for values in corpora.values()), "series_corpus_mismatch")
    summaries = series.get("summary")
    require(type(summaries) is list and len(summaries) == 8, "series_summary_invalid")
    summary_keys = set()
    for item in summaries:
        require(type(item) is dict, "series_summary_invalid")
        key = (item.get("size"), item.get("role"), item.get("family"))
        require(key not in summary_keys and key[0] in (10000, 100000) and
                key[1] in binaries and key[2] in ("common", "rare") and
                (item.get("completed_runs"), item.get("expected_runs"), item.get("pooled_count")) ==
                (3, 3, 300), "series_summary_invalid")
        summary_keys.add(key)
    require(len(summary_keys) == 8, "series_summary_invalid")
    expected_summary = {}
    for count in (10000, 100000):
        for role in binaries:
            for family in ("common", "rare"):
                per_run = [resource_series.summary(measured_samples[(count, repeat, role)][family])["p95"]
                           for repeat in (1, 2, 3)]
                pooled = [value for repeat in (1, 2, 3)
                          for value in measured_samples[(count, repeat, role)][family]]
                expected_summary[(count, role, family)] = {
                    "completed_runs": 3, "expected_runs": 3,
                    "median_run_p95": round(resource_series.nearest(per_run, .5), 3),
                    "min_run_p95": min(per_run), "max_run_p95": max(per_run),
                    "pooled_count": len(pooled),
                    "pooled_p95": round(resource_series.nearest(pooled, .95), 3),
                }
    actual_summary = {(item["size"], item["role"], item["family"]): item
                      for item in summaries}
    for key, expected in expected_summary.items():
        actual = actual_summary[key]
        require(all(actual.get(field) == value for field, value in expected.items()),
                "series_summary_mismatch")
    ratios = series.get("paired_ratios")
    require(type(ratios) is list and len(ratios) == 12, "series_pairs_incomplete")
    expected_ratios = {}
    for count in (10000, 100000):
        for repeat in (1, 2, 3):
            for family in ("common", "rare"):
                candidate_p95 = resource_series.summary(
                    measured_samples[(count, repeat, "candidate")][family])["p95"]
                baseline_p95 = resource_series.summary(
                    measured_samples[(count, repeat, "baseline")][family])["p95"]
                require(baseline_p95 > 0, "series_pairs_invalid")
                expected_ratios[(count, repeat, family)] = round(candidate_p95 / baseline_p95, 6)
    observed_ratios = {}
    for ratio in ratios:
        require(type(ratio) is dict and type(ratio.get("size")) is int and
                type(ratio.get("repeat")) is int and
                type(ratio.get("candidate_to_baseline_p95")) in (int, float) and
                math.isfinite(ratio["candidate_to_baseline_p95"]), "series_pairs_invalid")
        key = (ratio.get("size"), ratio.get("repeat"), ratio.get("family"))
        require(key in expected_ratios and key not in observed_ratios and
                ratio["candidate_to_baseline_p95"] == expected_ratios[key],
                "series_pairs_invalid")
        observed_ratios[key] = ratio["candidate_to_baseline_p95"]
    require(set(observed_ratios) == set(expected_ratios), "series_pairs_incomplete")
    return series, environment


def _validate_soak(soak_path, candidate_sha, candidate_version, binary_sha):
    soak_dir = pathlib.Path(soak_path).parent
    files = safe_tree(soak_dir, max_files=4, max_total=4 << 20,
                      max_file=MAX_PROVENANCE)
    require({name for name, _, _ in files} == {"report.json", "provenance.txt"},
            "soak_files_incomplete")
    provenance = (soak_dir / "provenance.txt").read_bytes()
    text = provenance.decode("utf-8", "strict")
    require(len(provenance) <= MAX_PROVENANCE and
            f"candidate_sha={candidate_sha}" in text and
            "packager=production scripts/package.py; target=linux-amd64" in text,
            "soak_provenance_invalid")
    report, _ = load_json(soak_path, 2 << 20)
    binary = report.get("binary", {})
    workload, limits, counters = (report.get(key, {}) for key in
                                  ("workload", "limits", "counters"))
    require((report.get("schema"), report.get("kind"), report.get("status"),
             report.get("profile"), report.get("source_sha"), report.get("source_dirty"),
             binary.get("sha256"), binary.get("revision"), binary.get("version")) ==
            (1, "mixed_load_soak", "passed", "long", candidate_sha, False,
             binary_sha, candidate_sha, candidate_version), "soak_identity_invalid")
    require(report.get("clean_exit") is True and report.get("failure_codes") == [] and
            type(counters.get("unexpected_errors")) is int and counters["unexpected_errors"] == 0 and
            type(counters.get("completed_cycles")) is int and counters["completed_cycles"] >= 12 and
            type(counters.get("restarts")) is int and counters["restarts"] >= 12,
            "soak_incomplete")
    _duration(report.get("elapsed_seconds"))
    require(report["elapsed_seconds"] >= 1800 and
            limits.get("target_duration_seconds", 0) >= 1800 and
            limits.get("duration_seconds", 0) >= 1800 and
            (workload.get("seed"), workload.get("documents"), workload.get("target_cycles")) ==
            (42, 100, 12), "soak_duration_invalid")
    cases = report.get("cases")
    required_cases = {"paging", "refresh_cancel", "collection_restart_provenance", "cycle"}
    require(type(cases) is list and len(cases) >= 12 * len(required_cases) and
            all(type(case) is dict and case.get("status") == "passed" and
                case.get("case") in required_cases for case in cases) and
            all(sum(1 for case in cases if case["case"] == name) >= 12
                for name in required_cases), "soak_cases_invalid")
    environment = report.get("environment", {})
    require(environment.get("os") == "Linux" and
            environment.get("arch") in ("x86_64", "amd64"), "soak_environment_invalid")
    return report, environment


def _manual_record(path, expected_kind, candidate, native):
    raw_file = _regular_file(path, MAX_MANUAL_JSON)
    report, raw = manual_evidence.parse(raw_file)
    require(len(raw) <= MAX_MANUAL_JSON, "manual_report_too_large")
    require(report.get("kind") == expected_kind, "manual_report_kind_mismatch")
    try:
        outcome = manual_evidence.validate(report, candidate, native)
    except (ValueError, KeyError, TypeError, AttributeError, RecursionError):
        raise EvidenceError("manual_report_invalid_or_mismatched") from None
    if outcome == "failed":
        raise EvidenceError("manual_report_failed")
    require(outcome in ("complete", "incomplete"), "manual_report_invalid")
    return report, outcome


def _safe_windows_report(report, outcome, candidate, native):
    target = "windows-amd64"
    result = {"kind": "windows_first_run", "evidence_type": "manual",
              "status": "passed" if outcome == "complete" else "incomplete",
              "target": target, "candidate_sha": candidate["candidate_sha"],
              "candidate_version": candidate["candidate_version"],
              "archive_sha256": native[target]["archive"]["sha256"],
              "reference": report["reference"],
              "cases": manual_evidence.safe_manual_cases(report)}
    if outcome == "complete":
        result["status"] = "passed"
    return result


def _safe_linux_observations(report, outcome, candidate, native):
    return {"format_version": 1, "kind": "linux_first_run",
            "candidate_sha": candidate["candidate_sha"],
            "candidate_version": candidate["candidate_version"],
            "observations": [{"status": "complete" if outcome == "complete" else "incomplete",
                              "target": "linux-amd64",
                              "archive_sha256": native["linux-amd64"]["archive"]["sha256"],
                              "reference": report["reference"],
                              "cases": manual_evidence.safe_manual_cases(report)}]}


def _prepare_manual_inputs(windows_path, linux_path, candidate, native):
    records, refs = {}, set()
    for field, path, kind in (("windows", windows_path, "windows_first_run"),
                              ("linux", linux_path, "linux_first_run")):
        if path is None:
            continue
        report, outcome = _manual_record(path, kind, candidate, native)
        reference = report.get("reference", "")
        if reference:
            require(reference not in refs, "manual_reference_duplicate")
            refs.add(reference)
        if field == "windows":
            records[field] = _safe_windows_report(report, outcome, candidate, native)
        else:
            records[field] = _safe_linux_observations(report, outcome, candidate, native)
        records[field + "_outcome"] = outcome
    return records


def _publication_notes(source_path, manual):
    source = _regular_file(source_path, 1 << 20).read_bytes()
    try:
        text = source.decode("utf-8")
    except UnicodeDecodeError:
        raise EvidenceError("publication_notes_invalid") from None
    if not manual:
        return source
    if manual.get("windows_outcome") == "complete":
        old = ("The planned owner Windows VM first-run is not yet a result; manual status remains pending "
               "until a matching report is reviewed. A passed report will describe only its recorded VM, "
               "Windows build, and completed scenarios.")
        new = ("A matching Windows amd64 first-run report for this exact candidate and archive was supplied "
               "and passed the existing offline manual-evidence validation. The handoff retains its reference "
               "and eight recorded scenario statuses/timings; environment details and free-form observations "
               "were not independently verified or retained. This is not a release decision.")
        require(old in text, "publication_notes_template_changed")
        text = text.replace(old, new, 1)
        paragraph = ("This base text does not assert matching beta.1 manual or voluntary-use outcomes. "
                     "Automated package, browser, or installer checks are not human first-run or "
                     "voluntary-use reports. Matching observations must be tied to the exact candidate "
                     "and archive; if they are accepted after this source text is frozen, the "
                     "publication-notes copy and hash must be updated and reviewed against that "
                     "evidence before use.")
        updated = ("The matching Windows first-run report is included as sanitized, candidate-bound "
                   "evidence in this handoff. Apple Silicon first-run and voluntary-use evidence remain "
                   "pending. The static base above is retained except for the superseded Windows status; "
                   "this supplement does not authorize publication.")
        require(paragraph in text, "publication_notes_template_changed")
        text = text.replace(paragraph, updated, 1)
    additions = []
    if manual.get("windows_outcome") == "incomplete":
        additions.append("- Windows amd64 first-run: a matching report was received but remains incomplete; readiness is pending.")
    if manual.get("linux_outcome") == "complete":
        additions.append("- Linux amd64 manual observations: recorded separately under the TASK 32 intake; not a new readiness gate.")
    elif manual.get("linux_outcome") == "incomplete":
        additions.append("- Linux amd64 manual observations: an incomplete TASK 32 intake is recorded separately; not a readiness result.")
    if additions:
        text += "\n\n## Candidate-bound handoff supplement\n\n" + "\n".join(additions) + "\n"
    return text.encode("utf-8")


def _stage_copy(source, destination, maximum=MAX_FILE):
    source = _regular_file(source, maximum)
    destination.parent.mkdir(parents=True, exist_ok=True)
    with source.open("rb") as src, destination.open("xb") as dst:
        shutil.copyfileobj(src, dst, 1 << 20)


def _copy_tree(source, destination, *, max_total, max_file=MAX_FILE):
    files = safe_tree(source, max_files=64, max_total=max_total, max_file=max_file)
    for relative, path, _ in files:
        _stage_copy(path, destination / pathlib.PurePosixPath(relative), max_file)


def _expected_readiness(readiness_report, sha, version, windows_passed):
    require(readiness_report.get("candidate_sha") == sha and
            readiness_report.get("candidate_version") == version and
            readiness_report.get("software_status") == "passed" and
            readiness_report.get("final_release") is False and
            readiness_report.get("release_readiness") == "pending",
            "readiness_not_pending")
    evidence = readiness_report.get("evidence", {})
    technical = ("strict_candidate", "freshness_browser", "installer_native", "crash_recovery",
                 "scale_10k", "scale_100k")
    require(all(evidence.get(key) == "passed" for key in technical),
            "technical_readiness_incomplete")
    expected_windows = "passed" if windows_passed else "pending"
    require(evidence.get("windows_first_run") == expected_windows and
            evidence.get("apple_silicon_first_run") == "pending" and
            evidence.get("voluntary_usage") == "pending", "human_readiness_invalid")
    gaps = set(readiness_report.get("gaps", []))
    expected_gaps = {"apple_silicon_first_run_pending", "voluntary_usage_pending"}
    if not windows_passed:
        expected_gaps.add("windows_first_run_pending")
    require(gaps == expected_gaps, "readiness_gaps_invalid")


def _manifest_entries(root):
    files = safe_tree(root, max_files=MAX_INPUT_FILES, max_total=MAX_HANDOFF_TREE,
                      max_file=MAX_FILE)
    return [{"path": name, "size": size, "sha256": file_digest(path)}
            for name, path, size in files if name != "PACKAGE-MANIFEST.json"]


def _zip_reports(root, destination):
    files = safe_tree(root, max_files=MAX_INPUT_FILES, max_total=MAX_HANDOFF_TREE,
                      max_file=MAX_FILE)
    destination.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(destination, "x", compression=zipfile.ZIP_DEFLATED,
                         compresslevel=6) as archive:
        for relative, path, _size in files:
            info = zipfile.ZipInfo(relative, (2020, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = 0o100644 << 16
            with path.open("rb") as stream:
                archive.writestr(info, stream.read(MAX_FILE + 1))
    require(destination.stat().st_size <= MAX_REPORTS_ZIP, "reports_zip_too_large")
    _zip_members(destination, total_limit=MAX_HANDOFF_TREE,
                 member_limit=MAX_FILE, max_members=MAX_INPUT_FILES)


def _readiness_cli(candidate_path, sha, version, recovery_path, evidence_paths, output_path):
    command = [sys.executable, "-B", str(pathlib.Path(readiness.__file__).resolve()),
               "--candidate", str(candidate_path), "--candidate-sha", sha,
               "--candidate-version", version, "--recovery", str(recovery_path),
               "--output", str(output_path)]
    for evidence_path in evidence_paths:
        command.extend(("--evidence", str(evidence_path)))
    try:
        result = subprocess.run(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=30, check=False)
    except (OSError, subprocess.TimeoutExpired):
        raise EvidenceError("readiness_execution_failed") from None
    require(result.returncode == 0, "readiness_execution_failed")
    report, _ = load_json(output_path, 1 << 20)
    return report


def _build_payload(stage, args, candidate, native, manifest, run_metadata,
                   summary_raw, recovery_raw, scale_data, series_data, soak_data,
                   manual):
    sha, version = args.candidate_sha, args.candidate_version
    summary_path = stage / "main-verification/verification-summary.json"
    summary_path.parent.mkdir(parents=True, exist_ok=True)
    summary_path.write_bytes(summary_raw)
    recovery_path = stage / "linux-package/recovery-candidate.json"
    recovery_path.parent.mkdir(parents=True, exist_ok=True)
    recovery_path.write_bytes(recovery_raw)
    evidence_dir = stage / "evidence"
    evidence_dir.mkdir(parents=True, exist_ok=True)
    write_json(evidence_dir / "recovery.json", {"kind": "crash_recovery",
                                                  "measurement": _safe_json_bytes(recovery_raw)})
    for kind, (record, raw) in scale_data.items():
        suffix = "10k" if kind == "scale_10k" else "100k"
        raw_path = stage / f"scale-{suffix}.json"
        raw_path.write_bytes(raw)
        write_json(evidence_dir / f"scale-{suffix}.json", {"kind": kind, "measurement": record})
    _copy_tree(args.performance_series_dir, stage / "performance-series",
               max_total=MAX_REPORT_TREE)
    _copy_tree(pathlib.Path(args.performance_soak).parent, stage / "performance-soak",
               max_total=4 << 20, max_file=MAX_PROVENANCE)
    _copy_tree(args.kits_dir, stage / "kits", max_total=24 << 20,
               max_file=8 << 20)
    for target in ("linux-amd64", "windows-amd64", "darwin-arm64"):
        kit = stage / "kits" / f"{target}.zip"
        require(kit.is_file(), "acceptance_kit_missing")

    if "windows" in manual:
        if manual["windows_outcome"] == "complete":
            write_json(evidence_dir / "windows-first-run.json", manual["windows"])
        else:
            write_json(stage / "manual-evidence/windows-first-run.json", manual["windows"])
    if "linux" in manual:
        write_json(stage / "manual-evidence/linux-observations.json", manual["linux"])
    if manual:
        summary = {"candidate_sha": sha, "candidate_version": version,
                   "windows_first_run": manual.get("windows_outcome", "not_supplied"),
                   "linux_first_run": manual.get("linux_outcome", "not_supplied"),
                   "apple_silicon_first_run": "pending", "voluntary_usage": "pending"}
        write_json(stage / "manual-evidence/intake-summary.json", summary)

    notes = _publication_notes(pathlib.Path(__file__).resolve().parents[1] /
                               "docs/release-notes-beta1.md", manual)
    (stage / "publication-notes.md").write_bytes(notes)
    notes_sha = sha256_bytes(notes)

    readiness_evidence = [evidence_dir / "scale-10k.json", evidence_dir / "scale-100k.json"]
    windows_passed = "windows" in manual and manual["windows_outcome"] == "complete"
    if windows_passed:
        readiness_evidence.append(evidence_dir / "windows-first-run.json")
    readiness_path = stage / "readiness-after-scale.json"
    readiness_result = _readiness_cli(summary_path, sha, version, recovery_path,
                                      readiness_evidence, readiness_path)
    _expected_readiness(readiness_result, sha, version, windows_passed)

    assets = []
    for item in manifest["assets"]:
        assets.append({"basename": item["basename"], "size": item["size"],
                       "sha256": item["sha256"]})
    candidate_run = run_metadata["candidate_run"]
    performance_run = run_metadata["performance_run"]
    candidate_artifacts = [{key: item[key] for key in
                            ("name", "id", "digest", "size_in_bytes", "expires_at", "url")}
                            for item in candidate_run["artifacts"]
                            if item["name"] in {f"release-assets-{sha}",
                                                f"release-verification-{sha}",
                                                f"release-package-{sha}-linux-amd64"}]
    expected_performance_artifacts = {
        f"performance-series-{sha}-{performance_run['run_id']}",
        f"performance-soak-{sha}-{performance_run['run_id']}",
    }
    performance_artifacts = [{key: item[key] for key in
                              ("name", "id", "digest", "size_in_bytes", "expires_at", "url")}
                             for item in performance_run["artifacts"]
                             if item["name"] in expected_performance_artifacts]
    series_report, series_environment = series_data
    soak_report, soak_environment = soak_data
    output_hashes = {
        "scale_10k": {"path": "scale-10k.json", "sha256": sha256_bytes(scale_data["scale_10k"][1])},
        "scale_100k": {"path": "scale-100k.json", "sha256": sha256_bytes(scale_data["scale_100k"][1])},
        "performance_series": {"status": series_report["status"],
                               "qualification": series_report["qualification"],
                               "files": len(_expected_series_filenames())},
        "performance_soak": {"status": soak_report["status"],
                             "elapsed_seconds": soak_report["elapsed_seconds"],
                             "clean_exit": soak_report["clean_exit"]},
    }
    evidence_index = {
        "index_version": 1,
        "repository": REPOSITORY,
        "candidate": {"sha": sha, "version": version, "schema_version": SCHEMA,
                      "client_api_version": CLIENT_API, "vsix_version": VSIX_VERSION,
                      "linux_binary_sha256": native["linux-amd64"]["binary_sha256"],
                      "candidate_delivery_url": run_metadata["candidate_delivery_url"],
                      "assets": assets,
                      "manifest_sha256": file_digest(pathlib.Path(args.assets_dir) / "release-manifest.json"),
                      "checksums_sha256": file_digest(pathlib.Path(args.assets_dir) / "SHA256SUMS.txt")},
        "source_runs": {
            "candidate": {key: candidate_run[key] for key in
                          ("run_id", "run_attempt", "workflow_id", "workflow_path", "event",
                           "head_branch", "head_sha", "conclusion", "url")},
            "performance": {key: performance_run[key] for key in
                            ("run_id", "run_attempt", "workflow_id", "workflow_path", "event",
                             "head_branch", "head_sha", "conclusion", "url")},
            "candidate_artifacts": candidate_artifacts,
            "performance_artifacts": performance_artifacts,
        },
        "technical_outcomes": {"strict_candidate": "passed", "recovery": "passed",
                               "scale_10k": "passed", "scale_100k": "passed",
                               "performance_series": "passed", "performance_soak": "passed"},
        "measurements": output_hashes,
        "performance_environment": {"series": series_environment,
                                    "soak": soak_environment,
                                    "qualification": series_report["qualification"],
                                    "baseline": {"source_sha": BASELINE_SHA,
                                                 "version": "0.1.0-alpha.5",
                                                 "binary_sha256": series_report["identity"]["baseline"]["sha256"]}},
        "readiness": {"software_status": readiness_result["software_status"],
                      "release_readiness": readiness_result["release_readiness"],
                      "final_release": False,
                      "human_pending": [key for key in
                                        ("windows_first_run", "apple_silicon_first_run", "voluntary_usage")
                                        if readiness_result["evidence"][key] == "pending"],
                      "evidence": readiness_result["evidence"]},
        "platform_summary": {
            "windows-amd64": {"native_ci": "passed", "manual_first_run":
                              "passed" if windows_passed else "pending",
                              "scope": "operator-supplied matching manual report" if windows_passed else "not supplied"},
            "linux-amd64": {"native_ci": "passed", "manual_first_run":
                            ("recorded" if manual.get("linux_outcome") == "complete" else
                             "incomplete" if manual.get("linux_outcome") == "incomplete" else "not supplied"),
                            "readiness_gate": False},
            "macos": {"native_ci": "passed", "manual_first_run": "pending",
                      "coverage": "macOS CI-only; Apple Silicon and Intel manual outcomes not inferred"},
            "linux-arm64": {"native_ci": "passed", "manual_first_run": "experimental/pending"},
        },
        "publication_notes_sha256": notes_sha,
        "final_release": False,
    }
    write_json(stage / "evidence-index.json", evidence_index)
    manifest_entries = _manifest_entries(stage)
    write_json(stage / "PACKAGE-MANIFEST.json", {"manifest_version": 1,
                                                  "files": manifest_entries})
    readme = ("# Beta.1 evidence handoff\n\n"
              f"Candidate: `{sha}` / `{version}`. This is a reports-only evidence package; "
              "the canonical eight release assets remain in their original candidate artifact.\n\n"
              f"Candidate asset delivery: {run_metadata['candidate_delivery_url']}\n\n"
              f"Candidate run: {candidate_run['url']} (run `{candidate_run['run_id']}`, "
              f"attempt `{candidate_run['run_attempt']}`).\n\n"
              f"Performance run: {performance_run['url']} (run `{performance_run['run_id']}`, "
              f"attempt `{performance_run['run_attempt']}`).\n\n"
              f"Linux CLI SHA-256: `{native['linux-amd64']['binary_sha256']}`.\n\n"
              "Technical evidence passed. Human gaps are listed in `readiness-after-scale.json`; "
              "`final_release` remains `false`. Preserve the downloaded reports ZIP and its SHA-256 "
              "from the workflow job summary before the 30-day artifact retention expires.\n")
    (stage / "README.md").write_text(readme, encoding="utf-8", newline="\n")
    # README is itself part of the payload manifest; write it before the final
    # manifest and regenerate the manifest after it exists.
    manifest_entries = _manifest_entries(stage)
    write_json(stage / "PACKAGE-MANIFEST.json", {"manifest_version": 1,
                                                  "files": manifest_entries})
    return evidence_index, readiness_result


def _require_disjoint(output_dir, reports_zip, inputs):
    output_dir = pathlib.Path(os.path.abspath(output_dir))
    reports_zip = pathlib.Path(os.path.abspath(reports_zip))
    require(output_dir != reports_zip and output_dir not in reports_zip.parents and
            reports_zip not in output_dir.parents, "output_path_overlap")
    for item in inputs:
        path = pathlib.Path(os.path.abspath(item))
        require(path != output_dir and path not in output_dir.parents and
                output_dir not in path.parents and path != reports_zip and
                path not in reports_zip.parents and reports_zip not in path.parents,
                "output_path_overlap")
    try:
        require(pathlib.Path(os.path.commonpath((str(output_dir), str(reports_zip)))) != output_dir,
                "output_path_overlap")
    except ValueError:
        raise EvidenceError("output_path_overlap") from None


def _require_output_separate(output_dir, inputs):
    output_dir = pathlib.Path(os.path.abspath(output_dir))
    for item in inputs:
        path = pathlib.Path(os.path.abspath(item))
        require(path != output_dir and path not in output_dir.parents and
                output_dir not in path.parents, "output_path_overlap")


def assemble(args):
    require(args.candidate_version == VERSION and SHA_RE.fullmatch(args.candidate_sha) is not None,
            "candidate_identity_mismatch")
    output_dir = pathlib.Path(args.output_dir)
    reports_zip = pathlib.Path(args.reports_zip)
    inputs = [args.assets_dir, args.verification_summary, args.recovery,
              args.performance_series_dir, args.performance_soak, args.scale_10k,
              args.scale_100k, args.run_metadata, args.kits_dir]
    inputs.extend(path for path in (args.windows_report, args.linux_report) if path)
    _require_disjoint(output_dir, reports_zip, inputs)
    _check_components(output_dir, allow_missing=True)
    _check_components(reports_zip, allow_missing=True)
    require(not output_dir.exists() and not reports_zip.exists(), "output_exists")
    output_dir.parent.mkdir(parents=True, exist_ok=True)
    reports_zip.parent.mkdir(parents=True, exist_ok=True)

    metadata, _ = load_json(args.run_metadata, 1 << 20)
    validate_trusted_metadata(metadata, args.candidate_sha, args.candidate_version)
    run = metadata["candidate_run"]
    summary, summary_raw = load_json(args.verification_summary)
    native, manifest = _validate_summary(summary, args.assets_dir,
                                         args.candidate_sha, args.candidate_version)
    require(summary.get("run_url") == run["url"] and
            str(summary.get("run_attempt")) == str(run["run_attempt"]),
            "summary_run_provenance_mismatch")
    recovery, recovery_raw = load_json(args.recovery)
    try:
        readiness.check_recovery({"kind": "crash_recovery", "measurement": recovery},
                                 summary, native)
    except (ValueError, KeyError, TypeError, AttributeError):
        raise EvidenceError("recovery_invalid") from None
    scale_data = {
        "scale_10k": _read_scale(args.scale_10k, "scale_10k", summary, native),
        "scale_100k": _read_scale(args.scale_100k, "scale_100k", summary, native),
    }
    binary_sha = native["linux-amd64"]["binary_sha256"]
    series_data = _validate_series(args.performance_series_dir, args.candidate_sha,
                                   args.candidate_version, binary_sha)
    soak_data = _validate_soak(args.performance_soak, args.candidate_sha,
                               args.candidate_version, binary_sha)
    manual = _prepare_manual_inputs(args.windows_report, args.linux_report, summary, native)
    # The generated kits must be tied to the verified asset inventory and exact
    # artifact URL. Their blank human reports never enter readiness.py.
    for target in ("linux-amd64", "windows-amd64", "darwin-arm64"):
        verify_kit(pathlib.Path(args.kits_dir) / f"{target}.zip", args.assets_dir, manifest,
                   args.candidate_sha, args.candidate_version, target,
                   metadata["candidate_delivery_url"],
                   file_digest(pathlib.Path(args.assets_dir) / "release-manifest.json"),
                   file_digest(pathlib.Path(args.assets_dir) / "SHA256SUMS.txt"))

    temporary = pathlib.Path(tempfile.mkdtemp(prefix=".beta-evidence-", dir=output_dir.parent))
    zip_temp = None
    try:
        evidence_index, readiness_result = _build_payload(
            temporary, args, summary, native, manifest, metadata,
            summary_raw, recovery_raw, scale_data, series_data, soak_data, manual)
        _verify_handoff_directory(temporary, args.assets_dir, args.candidate_sha,
                                  args.candidate_version, expected_manual=manual)
        zip_temp = reports_zip.parent / ("." + reports_zip.name + ".tmp-" + str(os.getpid()))
        zip_temp.unlink(missing_ok=True)
        _zip_reports(temporary, zip_temp)
        _verify_reports_zip(temporary, zip_temp)
        zip_sha = file_digest(zip_temp, MAX_REPORTS_ZIP)
        os.replace(temporary, output_dir)
        try:
            os.replace(zip_temp, reports_zip)
        except OSError:
            shutil.rmtree(output_dir, ignore_errors=True)
            raise
        _write_diagnostics(args.diagnostics, "passed", "none", zip_sha,
                           args.candidate_sha, args.candidate_version)
        return {"result_version": 1, "status": "passed",
                "candidate_sha": args.candidate_sha,
                "candidate_version": args.candidate_version,
                "report_zip_sha256": zip_sha}
    except BaseException:
        shutil.rmtree(temporary, ignore_errors=True)
        if zip_temp:
            zip_temp.unlink(missing_ok=True)
        if output_dir.exists():
            # Only our newly assembled directory can be present here.
            shutil.rmtree(output_dir, ignore_errors=True)
        raise


def _verify_handoff_directory(root, assets_dir, sha, version, expected_manual=None):
    files = safe_tree(root, max_files=MAX_INPUT_FILES, max_total=MAX_HANDOFF_TREE,
                      max_file=MAX_FILE)
    paths = {name: path for name, path, _ in files}
    required_names = {
        "README.md", "evidence-index.json", "PACKAGE-MANIFEST.json", "publication-notes.md",
        "main-verification/verification-summary.json", "linux-package/recovery-candidate.json",
        "evidence/recovery.json", "performance-series/provenance.txt",
        "performance-series/results/series.json", "performance-soak/provenance.txt",
        "performance-soak/report.json", "scale-10k.json", "evidence/scale-10k.json",
        "scale-100k.json", "evidence/scale-100k.json", "readiness-after-scale.json",
        "kits/linux-amd64.zip", "kits/windows-amd64.zip", "kits/darwin-arm64.zip",
    }
    optional_names = {
        "evidence/windows-first-run.json", "manual-evidence/windows-first-run.json",
        "manual-evidence/linux-observations.json", "manual-evidence/intake-summary.json",
    }
    expected_names = required_names | (set(paths) & optional_names)
    expected_names.update(
        f"performance-series/results/{count}-{repeat}-{role}{suffix}"
        for count in (10000, 100000) for repeat in (1, 2, 3)
        for role in ("candidate", "baseline")
        for suffix in (".json", "-samples.json"))
    require(set(paths) == expected_names, "handoff_layout_invalid")
    require(not ({"evidence/windows-first-run.json", "manual-evidence/windows-first-run.json"}
                 <= set(paths)), "windows_evidence_ambiguous")
    manual_paths = set(paths) & optional_names
    require(("manual-evidence/intake-summary.json" in manual_paths) ==
            bool(manual_paths - {"manual-evidence/intake-summary.json"}),
            "manual_summary_mismatch")
    require("PACKAGE-MANIFEST.json" in paths and "evidence-index.json" in paths,
            "handoff_layout_invalid")
    manifest, _ = load_json(paths["PACKAGE-MANIFEST.json"], 4 << 20)
    require(manifest.get("manifest_version") == 1 and type(manifest.get("files")) is list,
            "package_manifest_invalid")
    listed = manifest["files"]
    require(all(type(item) is dict and set(item) == {"path", "size", "sha256"}
                for item in listed), "package_manifest_invalid")
    names = [item["path"] for item in listed]
    require(names == sorted(names) and len(names) == len(set(names)) and
            "PACKAGE-MANIFEST.json" not in names, "package_manifest_invalid")
    actual_names = set(paths) - {"PACKAGE-MANIFEST.json"}
    require(set(names) == actual_names, "package_manifest_mismatch")
    for item in listed:
        _safe_relative(item["path"])
        path = paths[item["path"]]
        require(item["size"] == path.stat().st_size and
                item["sha256"] == file_digest(path), "package_manifest_mismatch")
    index, _ = load_json(paths["evidence-index.json"], 4 << 20)
    require(index.get("repository") == REPOSITORY and
            index.get("candidate", {}).get("sha") == sha and
            index.get("candidate", {}).get("version") == version and
            index.get("final_release") is False, "evidence_index_invalid")
    summary, _ = load_json(paths["main-verification/verification-summary.json"])
    native, asset_manifest = _validate_summary(summary, assets_dir, sha, version)
    binary_sha = native["linux-amd64"]["binary_sha256"]
    require(index["candidate"].get("linux_binary_sha256") == binary_sha,
            "evidence_index_binary_mismatch")
    recovery, recovery_raw = load_json(paths["linux-package/recovery-candidate.json"])
    try:
        readiness.check_recovery({"kind": "crash_recovery", "measurement": recovery},
                                 summary, native)
    except (ValueError, KeyError, TypeError, AttributeError):
        raise EvidenceError("recovery_invalid") from None
    recovery_wrapper, _ = load_json(paths["evidence/recovery.json"])
    require(recovery_wrapper == {"kind": "crash_recovery", "measurement": recovery},
            "recovery_wrapper_invalid")
    scale_records = {}
    for kind, suffix in (("scale_10k", "10k"), ("scale_100k", "100k")):
        record, raw = load_json(paths[f"scale-{suffix}.json"], MAX_SCALE_JSON)
        checked, _ = _read_scale(paths[f"scale-{suffix}.json"], kind, summary, native)
        wrapper, _ = load_json(paths[f"evidence/scale-{suffix}.json"])
        require(wrapper == {"kind": kind, "measurement": checked}, "scale_wrapper_invalid")
        scale_records[kind] = (record, raw)
    series_info = _validate_series(root / "performance-series", sha, version, binary_sha)
    soak_info = _validate_soak(root / "performance-soak/report.json", sha, version, binary_sha)
    windows_path = paths.get("evidence/windows-first-run.json")
    windows_passed = windows_path is not None
    if windows_path:
        window, _ = load_json(windows_path)
        try:
            outcome = manual_evidence.validate(window, summary, native)
        except (ValueError, KeyError, TypeError, AttributeError):
            raise EvidenceError("windows_supplement_invalid") from None
        require(outcome == "complete" and window.get("status") == "passed",
                "windows_supplement_invalid")
    elif "manual-evidence/windows-first-run.json" in paths:
        window, _ = load_json(paths["manual-evidence/windows-first-run.json"], MAX_MANUAL_JSON)
        try:
            outcome = manual_evidence.validate(window, summary, native)
        except (ValueError, KeyError, TypeError, AttributeError, RecursionError):
            raise EvidenceError("windows_intake_invalid") from None
        require(outcome == "incomplete" and window.get("status") in ("incomplete", "not_run"),
                "windows_intake_invalid")
    if "manual-evidence/linux-observations.json" in paths:
        observations, _ = load_json(paths["manual-evidence/linux-observations.json"], 64 << 10)
        _validate_linux_observations(observations, summary, native)
    readiness_path = paths.get("readiness-after-scale.json")
    require(readiness_path is not None, "readiness_missing")
    readiness_report, _ = load_json(readiness_path, 1 << 20)
    _expected_readiness(readiness_report, sha, version, windows_passed)
    if expected_manual is not None:
        _verify_manual_payload(paths, expected_manual, summary, native)
    _verify_asset_index(index, asset_manifest, assets_dir, sha, version)
    require(index.get("readiness", {}).get("evidence") == readiness_report["evidence"] and
            index.get("readiness", {}).get("final_release") is False,
            "evidence_index_readiness_mismatch")
    require(index.get("publication_notes_sha256") == file_digest(paths["publication-notes.md"]),
            "publication_notes_hash_mismatch")
    require(series_info[0]["qualification"] == "reference_only" and
            soak_info[0]["status"] == "passed", "performance_evidence_invalid")
    # No application archives belong in the reports handoff.
    require(not any(pathlib.PurePosixPath(name).name.endswith((".tar.gz", ".vsix")) or
                    re.fullmatch(r"findrail_.*\.zip", pathlib.PurePosixPath(name).name)
                    for name in paths), "app_asset_in_reports_handoff")
    return {"files": len(files), "binary_sha256": binary_sha,
            "readiness": readiness_report, "scale": scale_records,
            "performance_series": series_info[0], "performance_soak": soak_info[0],
            "recovery_raw_sha256": sha256_bytes(recovery_raw)}


def _validate_linux_observations(observations, candidate, native):
    require(type(observations) is dict and
            (observations.get("format_version"), observations.get("kind"),
             observations.get("candidate_sha"), observations.get("candidate_version")) ==
            (1, "linux_first_run", candidate["candidate_sha"], candidate["candidate_version"]),
            "linux_observations_invalid")
    records = observations.get("observations")
    require(type(records) is list and 0 < len(records) <= 100, "linux_observations_invalid")
    refs = set()
    expected_archive = native["linux-amd64"]["archive"]["sha256"]
    for record in records:
        require(type(record) is dict and record.get("status") in ("complete", "incomplete") and
                record.get("target") == "linux-amd64" and
                record.get("archive_sha256") == expected_archive and
                set(record) == {"status", "target", "archive_sha256", "reference", "cases"},
                "linux_observations_invalid")
        reference = record.get("reference")
        require(type(reference) is str and
                (not reference and record["status"] == "incomplete" or
                 readiness.REFERENCE.fullmatch(reference) is not None) and
                (not reference or reference not in refs), "linux_observations_invalid")
        if reference:
            refs.add(reference)
        cases = record.get("cases")
        require(type(cases) is list and len(cases) == len(readiness.MANUAL_CASES) and
                [item.get("case") for item in cases] == list(readiness.MANUAL_CASES),
                "linux_observations_invalid")
        for item in cases:
            require(type(item) is dict and item.get("status") in ("passed", "failed", "not_run") and
                    (item.get("elapsed_seconds") is None or
                     (type(item["elapsed_seconds"]) in (int, float) and
                      math.isfinite(item["elapsed_seconds"]) and item["elapsed_seconds"] >= 0)) and
                    set(item) == {"case", "status", "elapsed_seconds"},
                    "linux_observations_invalid")


def _verify_manual_payload(paths, manual, candidate, native):
    if "windows" in manual:
        if manual["windows_outcome"] == "complete":
            require("evidence/windows-first-run.json" in paths, "windows_supplement_missing")
            actual, _ = load_json(paths["evidence/windows-first-run.json"])
            require(actual == manual["windows"], "windows_supplement_mismatch")
        else:
            require("manual-evidence/windows-first-run.json" in paths, "windows_intake_missing")
            actual, _ = load_json(paths["manual-evidence/windows-first-run.json"])
            require(actual == manual["windows"], "windows_intake_mismatch")
    if "linux" in manual:
        require("manual-evidence/linux-observations.json" in paths,
                "linux_observations_missing")
        actual, _ = load_json(paths["manual-evidence/linux-observations.json"])
        require(actual == manual["linux"], "linux_observations_mismatch")
    if manual:
        require("manual-evidence/intake-summary.json" in paths, "manual_summary_missing")
        summary, _ = load_json(paths["manual-evidence/intake-summary.json"])
        require(summary.get("candidate_sha") == candidate["candidate_sha"] and
                summary.get("candidate_version") == candidate["candidate_version"],
                "manual_summary_mismatch")


def _verify_asset_index(index, manifest, assets_dir, sha, version):
    candidate = index.get("candidate", {})
    actual = [{"basename": item["basename"], "size": item["size"], "sha256": item["sha256"]}
              for item in manifest["assets"]]
    require(candidate.get("assets") == actual and
            candidate.get("manifest_sha256") == file_digest(pathlib.Path(assets_dir) / "release-manifest.json") and
            candidate.get("checksums_sha256") == file_digest(pathlib.Path(assets_dir) / "SHA256SUMS.txt") and
            (candidate.get("schema_version"), candidate.get("client_api_version"),
             candidate.get("vsix_version")) == (SCHEMA, CLIENT_API, VSIX_VERSION),
            "evidence_index_assets_invalid")


def _verify_reports_zip(root, zip_path):
    members = _zip_members(zip_path, total_limit=MAX_HANDOFF_TREE,
                           member_limit=MAX_FILE, max_members=MAX_INPUT_FILES)
    files = safe_tree(root, max_files=MAX_INPUT_FILES, max_total=MAX_HANDOFF_TREE,
                      max_file=MAX_FILE)
    expected = {name: path for name, path, _ in files}
    require({name for name, _, _ in members} == set(expected), "reports_zip_layout_invalid")
    try:
        with zipfile.ZipFile(zip_path) as archive:
            for name, _, _ in members:
                content = archive.read(name)
                require(content == expected[name].read_bytes(), "reports_zip_digest_mismatch")
    except EvidenceError:
        raise
    except (OSError, zipfile.BadZipFile, KeyError, RuntimeError):
        raise EvidenceError("reports_zip_invalid") from None


def verify(args):
    require(args.candidate_version == VERSION and SHA_RE.fullmatch(args.candidate_sha) is not None,
            "candidate_identity_mismatch")
    _require_disjoint(args.handoff_dir, args.reports_zip, (args.assets_dir,))
    assets = validate_assets(args.assets_dir, args.candidate_sha, args.candidate_version)
    _verify_handoff_directory(args.handoff_dir, args.assets_dir,
                              args.candidate_sha, args.candidate_version)
    zip_path = _regular_file(args.reports_zip, MAX_REPORTS_ZIP)
    _verify_reports_zip(args.handoff_dir, zip_path)
    return {"result_version": 1, "status": "passed",
            "candidate_sha": args.candidate_sha,
            "candidate_version": args.candidate_version,
            "report_zip_sha256": file_digest(zip_path, MAX_REPORTS_ZIP)}


def _write_diagnostics(path, status, code, zip_sha=None, sha=None, version=None):
    if not path:
        return
    value = {"diagnostic_version": 1, "status": status, "error_code": code}
    if SHA_RE.fullmatch(str(sha or "")):
        value["candidate_sha"] = sha
    if version == VERSION:
        value["candidate_version"] = version
    if zip_sha and HASH_RE.fullmatch(zip_sha):
        value["report_zip_sha256"] = zip_sha
    try:
        write_json(path, value)
    except (OSError, EvidenceError):
        pass


def _write_manual_inputs(args):
    destination = pathlib.Path(args.output_dir)
    _check_components(destination, allow_missing=True)
    destination.mkdir(parents=True, exist_ok=True)
    outputs = {}
    for field, env_name in (("windows", args.windows_env), ("linux", args.linux_env)):
        raw = os.environ.get(env_name, "")
        if raw == "":
            outputs[field] = ""
            continue
        encoded = raw.encode("utf-8")
        require(len(encoded) <= MAX_MANUAL_JSON, "manual_report_too_large")
        _safe_json_bytes(encoded, MAX_MANUAL_JSON)
        path = destination / f"{field}-report.json"
        require(not path.exists(), "manual_input_exists")
        path.write_bytes(encoded)
        outputs[field] = str(path)
    if args.github_output:
        with pathlib.Path(args.github_output).open("a", encoding="utf-8") as out:
            out.write(f"windows_report={outputs['windows']}\n")
            out.write(f"linux_report={outputs['linux']}\n")
    print(json.dumps({"windows_supplied": bool(outputs["windows"]),
                      "linux_supplied": bool(outputs["linux"])}))


def mark_diagnostics(args):
    allowed = {"input_check_failed", "preflight_failed", "artifact_download_failed",
               "candidate_extract_failed", "scale_10k_failed", "scale_100k_failed",
               "kit_build_failed", "manual_input_invalid", "assemble_failed",
               "workflow_failed"}
    require(args.code in allowed, "diagnostic_code_invalid")
    _write_diagnostics(args.output, args.status, args.code,
                       sha=args.candidate_sha, version=args.candidate_version)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    extract = sub.add_parser("extract-candidate")
    extract.add_argument("--candidate-sha", required=True)
    extract.add_argument("--candidate-version", required=True)
    extract.add_argument("--assets-dir", required=True, type=pathlib.Path)
    extract.add_argument("--verification-summary", required=True, type=pathlib.Path)
    extract.add_argument("--output-dir", required=True, type=pathlib.Path)
    extract.add_argument("--diagnostics", type=pathlib.Path)
    kits = sub.add_parser("prepare-kits")
    kits.add_argument("--candidate-sha", required=True)
    kits.add_argument("--candidate-version", required=True)
    kits.add_argument("--assets-dir", required=True, type=pathlib.Path)
    kits.add_argument("--run-metadata", required=True, type=pathlib.Path)
    kits.add_argument("--output-dir", required=True, type=pathlib.Path)
    kits.add_argument("--diagnostics", type=pathlib.Path)
    manual = sub.add_parser("write-manual-inputs")
    manual.add_argument("--windows-env", default="WINDOWS_REPORT_JSON")
    manual.add_argument("--linux-env", default="LINUX_REPORT_JSON")
    manual.add_argument("--output-dir", required=True, type=pathlib.Path)
    manual.add_argument("--github-output", type=pathlib.Path)
    assemble_parser = sub.add_parser("assemble")
    for name in ("candidate-sha", "candidate-version"):
        assemble_parser.add_argument("--" + name, required=True)
    for name in ("assets-dir", "verification-summary", "recovery",
                 "performance-series-dir", "performance-soak", "scale-10k",
                 "scale-100k", "run-metadata", "kits-dir", "output-dir",
                 "reports-zip"):
        assemble_parser.add_argument("--" + name, required=True, type=pathlib.Path)
    assemble_parser.add_argument("--windows-report", type=pathlib.Path)
    assemble_parser.add_argument("--linux-report", type=pathlib.Path)
    assemble_parser.add_argument("--diagnostics", type=pathlib.Path)
    verify_parser = sub.add_parser("verify")
    verify_parser.add_argument("--candidate-sha", required=True)
    verify_parser.add_argument("--candidate-version", required=True)
    verify_parser.add_argument("--assets-dir", required=True, type=pathlib.Path)
    verify_parser.add_argument("--handoff-dir", required=True, type=pathlib.Path)
    verify_parser.add_argument("--reports-zip", required=True, type=pathlib.Path)
    result_parser = sub.add_parser("mark-diagnostics")
    result_parser.add_argument("--output", required=True, type=pathlib.Path)
    result_parser.add_argument("--status", choices=("failed", "incomplete"), default="failed")
    result_parser.add_argument("--code", required=True)
    result_parser.add_argument("--candidate-sha")
    result_parser.add_argument("--candidate-version")
    args = parser.parse_args(argv)
    sha = getattr(args, "candidate_sha", None)
    version = getattr(args, "candidate_version", None)
    diag = getattr(args, "diagnostics", None)
    try:
        if args.command == "extract-candidate":
            root, binary_sha = extract_candidate(args.assets_dir, args.verification_summary,
                                                 args.candidate_sha, args.candidate_version,
                                                 args.output_dir)
            _write_diagnostics(diag, "incomplete", "measurements_not_completed", sha=sha,
                               version=version)
            print(json.dumps({"candidate_binary": str(root / "findrail"),
                              "binary_sha256": binary_sha}))
            return 0
        if args.command == "prepare-kits":
            prepare_kits(args.assets_dir, args.run_metadata, args.candidate_sha,
                         args.candidate_version, args.output_dir)
            _write_diagnostics(diag, "incomplete", "handoff_not_assembled", sha=sha,
                               version=version)
            print(json.dumps({"status": "prepared", "kits": 3}))
            return 0
        if args.command == "write-manual-inputs":
            _write_manual_inputs(args)
            return 0
        if args.command == "assemble":
            result = assemble(args)
            print(json.dumps(result, sort_keys=True, allow_nan=False))
            return 0
        if args.command == "verify":
            result = verify(args)
            print(json.dumps(result, sort_keys=True, allow_nan=False))
            return 0
        mark_diagnostics(args)
        return 0
    except EvidenceError as error:
        code = error.code
    except (OSError, ValueError, KeyError, TypeError, AttributeError, StopIteration,
            zipfile.BadZipFile, RecursionError, subprocess.SubprocessError):
        code = "input_invalid"
    if diag:
        _write_diagnostics(diag, "failed", code, sha=sha, version=version)
    if args.command in ("assemble", "verify"):
        print(json.dumps({"result_version": 1, "status": "failed",
                          "candidate_sha": sha if SHA_RE.fullmatch(str(sha or "")) else None,
                          "candidate_version": version if version == VERSION else VERSION,
                          "report_zip_sha256": None}, sort_keys=True, allow_nan=False))
    else:
        print(json.dumps({"status": "failed", "error_code": code}, sort_keys=True))
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
