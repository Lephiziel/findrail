#!/usr/bin/env python3
"""Content-free progress and provenance for production candidate verification."""
import argparse
import hashlib
import json
import os
import pathlib
import platform
import tempfile
import time

import package
import release_assets


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
                root = package.extract_verified(archive, pathlib.Path(temp))
                metadata = json.loads((root / "BUILD-INFO.json").read_text(encoding="utf-8"))
                record.update(source_clean=metadata.get("dirty") is False, build_info=metadata)
                if (metadata.get("revision"), metadata.get("version"), metadata.get("target"), metadata.get("dirty")) != (revision, version, target, False):
                    raise ValueError("native record provenance differs from candidate")
                binary = root / ("findrail.exe" if target.startswith("windows-") else "findrail")
                record["binary_sha256"] = digest(binary)
            if status == "success": record["status"] = "passed"
        except (ValueError, OSError, KeyError) as error:
            record["error_type"] = type(error).__name__
    return record


def summarize(native_directory, assets, browser_report, revision, version):
    manifest = release_assets.verify(assets, version, revision)
    records = [json.loads(path.read_text(encoding="utf-8")) for path in sorted(native_directory.rglob("verification.json"))]
    if sorted(record.get("target", "") for record in records) != sorted(package.TARGETS):
        raise ValueError("exactly one native record per target is required")
    for record in records:
        if (record.get("candidate_sha"), record.get("candidate_version"), record.get("status"), record.get("source_clean")) != (revision, version, "passed", True):
            raise ValueError("native record is incomplete or belongs to another candidate")
        archive = record["archive"]
        actual = assets / archive["basename"]
        if archive["sha256"] != digest(actual) or archive["size"] != actual.stat().st_size:
            raise ValueError("native record archive differs from the aggregated asset")
    browser = json.loads(browser_report.read_text(encoding="utf-8"))
    if (browser.get("candidate_sha"), browser.get("candidate_version"), browser.get("status"), browser.get("browser_skipped")) != (revision, version, "passed", False):
        raise ValueError("browser journey is incomplete or belongs to another candidate")
    if not browser.get("cases") or any(case.get("status") != "passed" for case in browser["cases"]):
        raise ValueError("browser report contains skipped or failed cases")
    linux = next(record for record in records if record["target"] == "linux-amd64")
    if browser.get("binary_sha256") != linux.get("binary_sha256"):
        raise ValueError("browser did not exercise the packaged Linux binary")
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
