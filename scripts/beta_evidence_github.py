#!/usr/bin/env python3
"""Read-only GitHub preflight and exact-ID artifact intake for beta evidence."""
import argparse
import hashlib
import json
import os
import pathlib
import re
import shutil
import stat
import sys
import urllib.error
import urllib.parse
import urllib.request
import zipfile

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import beta_evidence

REPOSITORY = beta_evidence.REPOSITORY
API_ROOT = "https://api.github.com/repos/" + REPOSITORY
WORKFLOWS = {
    "candidate": ".github/workflows/alpha-release.yml",
    "performance": ".github/workflows/performance-check.yml",
    "handoff": ".github/workflows/beta-evidence.yml",
}
RUN_ID_RE = beta_evidence.RUN_ID_RE
MAX_API_JSON = 16 << 20
MAX_ARTIFACT_ZIP = beta_evidence.MAX_INPUT_TREE
MAX_ARTIFACT_TOTAL = 1 << 30
MAX_ARTIFACT_FILES = 256


class PreflightError(ValueError):
    def __init__(self, code="preflight_failed"):
        super().__init__(code)
        self.code = code


def require(condition, code="preflight_failed"):
    if not condition:
        raise PreflightError(code)


def _json_object(raw, maximum=MAX_API_JSON):
    require(len(raw) <= maximum, "api_response_too_large")

    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result, "duplicate_api_key")
            result[key] = value
        return result

    try:
        value = json.loads(raw.decode("utf-8"), object_pairs_hook=pairs,
                           parse_constant=lambda _value: (_ for _ in ()).throw(
                               PreflightError("invalid_api_json")))
    except PreflightError:
        raise
    except (UnicodeDecodeError, json.JSONDecodeError, RecursionError, ValueError):
        raise PreflightError("invalid_api_json") from None
    require(type(value) is dict, "invalid_api_json")
    return value


class _SafeRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, new_url):
        old_host = urllib.parse.urlsplit(request.full_url).hostname
        new = super().redirect_request(request, response, code, message, headers, new_url)
        if new is not None:
            parsed = urllib.parse.urlsplit(new_url)
            require(parsed.scheme == "https", "unsafe_artifact_redirect")
            if parsed.hostname != old_host:
                for key in list(new.headers):
                    if key.lower() in ("authorization", "proxy-authorization"):
                        del new.headers[key]
                for key in list(new.unredirected_hdrs):
                    if key.lower() in ("authorization", "proxy-authorization"):
                        del new.unredirected_hdrs[key]
        return new


class GitHubAPI:
    def __init__(self, token):
        require(type(token) is str and len(token) >= 20, "github_token_missing")
        self.token = token
        self.opener = urllib.request.build_opener(_SafeRedirect())

    def _request(self, url, *, accept="application/vnd.github+json"):
        headers = {
            "Accept": accept,
            "Authorization": "Bearer " + self.token,
            "X-GitHub-Api-Version": "2022-11-28",
            "User-Agent": "findrail-beta-evidence-read-only",
        }
        request = urllib.request.Request(url, headers=headers, method="GET")
        try:
            return self.opener.open(request, timeout=60)
        except (OSError, urllib.error.URLError, urllib.error.HTTPError):
            raise PreflightError("github_api_request_failed") from None

    def json(self, path):
        require(path.startswith("/") and ".." not in path, "invalid_api_path")
        with self._request(API_ROOT + path) as response:
            raw = response.read(MAX_API_JSON + 1)
        return _json_object(raw)

    def download_artifact(self, artifact_id, destination):
        require(RUN_ID_RE.fullmatch(str(artifact_id)) is not None, "artifact_identity_invalid")
        url = f"{API_ROOT}/actions/artifacts/{artifact_id}/zip"
        digest = hashlib.sha256()
        total = 0
        destination = pathlib.Path(destination)
        destination.parent.mkdir(parents=True, exist_ok=True)
        try:
            with self._request(url, accept="application/vnd.github+json") as response:
                with destination.open("xb") as output:
                    while True:
                        block = response.read(1 << 20)
                        if not block:
                            break
                        total += len(block)
                        require(total <= MAX_ARTIFACT_ZIP, "artifact_download_too_large")
                        digest.update(block)
                        output.write(block)
        except PreflightError:
            destination.unlink(missing_ok=True)
            raise
        except (OSError, urllib.error.URLError, urllib.error.HTTPError):
            destination.unlink(missing_ok=True)
            raise PreflightError("artifact_download_failed") from None
        require(total > 0, "artifact_download_empty")
        return digest.hexdigest(), total


def _workflow(api, path):
    encoded = urllib.parse.quote(pathlib.PurePosixPath(path).name, safe="")
    record = api.json(f"/actions/workflows/{encoded}")
    require(record.get("path") == path and type(record.get("id")) is int and
            record["id"] > 0 and record.get("state") == "active", "workflow_identity_invalid")
    return record["id"]


def _jobs(api, run_id):
    page = api.json(f"/actions/runs/{run_id}/jobs?filter=latest&per_page=100")
    require(type(page.get("total_count")) is int and type(page.get("jobs")) is list and
            page["total_count"] == len(page["jobs"]) and len(page["jobs"]) < 100,
            "jobs_incomplete")
    normalized = []
    for job in page["jobs"]:
        require(type(job) is dict and type(job.get("name")) is str and
                type(job.get("run_attempt")) is int and job["run_attempt"] > 0,
                "jobs_invalid")
        normalized.append({"name": job["name"], "run_attempt": job["run_attempt"],
                           "status": job.get("status"), "conclusion": job.get("conclusion")})
    return normalized


def _artifacts(api, run_id, run, candidate_sha, kind):
    page = api.json(f"/actions/runs/{run_id}/artifacts?per_page=100")
    require(type(page.get("total_count")) is int and type(page.get("artifacts")) is list and
            page["total_count"] == len(page["artifacts"]) and len(page["artifacts"]) < 100,
            "artifacts_incomplete")
    normalized = []
    repository_id = run.get("repository", {}).get("id")
    require(type(repository_id) is int and repository_id > 0, "repository_identity_invalid")
    for artifact in page["artifacts"]:
        require(type(artifact) is dict and type(artifact.get("workflow_run")) is dict,
                "artifact_identity_invalid")
        provenance = artifact["workflow_run"]
        expected_head_sha = run["head_sha"] if kind == "handoff" else candidate_sha
        require(provenance.get("id") == int(run_id) and
                provenance.get("head_sha") == expected_head_sha and
                provenance.get("head_branch") == "main" and
                provenance.get("repository_id") == repository_id and
                provenance.get("head_repository_id") == repository_id,
                "artifact_run_mismatch")
        artifact_id = artifact.get("id")
        require(type(artifact_id) is int and artifact_id > 0 and
                type(artifact.get("name")) is str, "artifact_identity_invalid")
        normalized.append({
            "id": str(artifact_id),
            "name": artifact["name"],
            "digest": artifact.get("digest"),
            "size_in_bytes": artifact.get("size_in_bytes"),
            "expired": artifact.get("expired"),
            "expires_at": artifact.get("expires_at"),
            "url": f"https://github.com/{REPOSITORY}/actions/runs/{run_id}/artifacts/{artifact_id}",
        })
    return normalized


def _normalize_run(api, run_id, kind, candidate_sha):
    require(RUN_ID_RE.fullmatch(str(run_id)) is not None, "run_id_invalid")
    workflow_path = WORKFLOWS[kind]
    workflow_id = _workflow(api, workflow_path)
    run = api.json(f"/actions/runs/{run_id}")
    expected_url = f"https://github.com/{REPOSITORY}/actions/runs/{run_id}"
    expected_head = run.get("head_sha") if kind == "handoff" else candidate_sha
    require(str(run.get("id", "")) == str(run_id) and
            run.get("workflow_id") == workflow_id and
            run.get("repository", {}).get("full_name") == REPOSITORY and
            run.get("head_repository", {}).get("full_name") == REPOSITORY and
            run.get("head_sha") == expected_head and
            beta_evidence.SHA_RE.fullmatch(str(expected_head)) is not None and
            run.get("head_branch") == "main" and
            run.get("status") == "completed" and run.get("conclusion") == "success" and
            run.get("event") == ("push" if kind == "candidate" else "workflow_dispatch") and
            run.get("html_url") == expected_url and
            type(run.get("run_attempt")) is int and run["run_attempt"] > 0,
            f"{kind}_run_identity_invalid")
    run_path = run.get("path")
    if run_path is not None:
        require(type(run_path) is str and run_path.split("@", 1)[0] == workflow_path,
                f"{kind}_workflow_path_invalid")
    jobs = _jobs(api, str(run_id))
    artifacts = _artifacts(api, str(run_id), run, candidate_sha, kind)
    return {
        "run_id": str(run_id), "run_attempt": run["run_attempt"],
        "workflow_id": str(workflow_id), "workflow_path": workflow_path,
        "event": run["event"], "head_branch": run["head_branch"],
        "head_sha": run["head_sha"], "status": run["status"],
        "conclusion": run["conclusion"], "url": expected_url,
        "jobs": jobs, "artifacts": artifacts,
    }


def _extract_zip(archive_path, destination):
    members = beta_evidence._zip_members(
        archive_path, total_limit=MAX_ARTIFACT_ZIP,
        member_limit=beta_evidence.MAX_ARCHIVE_MEMBER, max_members=MAX_ARTIFACT_FILES)
    root = pathlib.Path(destination)
    require(not root.exists(), "extraction_destination_exists")
    root.mkdir(parents=True)
    seen_files = set()
    seen_directories = {}

    def register_directory(name):
        key = name.casefold()
        require(key not in seen_files, "unsafe_artifact_zip")
        previous = seen_directories.get(key)
        require(previous is None or previous == name, "unsafe_artifact_zip")
        seen_directories[key] = name

    uncompressed_total = 0
    for name, _size, info in members:
        uncompressed_total += _size
        require(uncompressed_total <= MAX_ARTIFACT_TOTAL, "artifact_contents_too_large")
        target = root.joinpath(*pathlib.PurePosixPath(name).parts)
        parts = pathlib.PurePosixPath(name).parts
        directory_parts = parts if info.is_dir() else parts[:-1]
        for index in range(1, len(directory_parts) + 1):
            register_directory("/".join(directory_parts[:index]))
        if info.is_dir():
            target.mkdir(parents=True, exist_ok=True)
            continue
        key = name.casefold()
        require(key not in seen_files and key not in seen_directories,
                "unsafe_artifact_zip")
        target.parent.mkdir(parents=True, exist_ok=True)
        try:
            with zipfile.ZipFile(archive_path) as zipped, zipped.open(info) as source:
                with target.open("xb") as output:
                    remaining = info.file_size
                    while remaining:
                        block = source.read(min(1 << 20, remaining))
                        require(block, "unsafe_artifact_zip")
                        output.write(block)
                        remaining -= len(block)
                    require(source.read(1) == b"", "unsafe_artifact_zip")
        except beta_evidence.EvidenceError:
            raise
        except (OSError, zipfile.BadZipFile, RuntimeError, EOFError):
            raise PreflightError("unsafe_artifact_zip") from None
        seen_files.add(key)
    return root, uncompressed_total


def _main_sha(api):
    ref = api.json('/git/ref/heads/main')
    obj = ref.get('object', {})
    sha = obj.get('sha') if type(obj) is dict else None
    require(obj.get('type') == 'commit' and beta_evidence.SHA_RE.fullmatch(str(sha or '')) is not None,
            'main_ref_invalid')
    return sha


def promotion_metadata(candidate_sha, candidate_version, candidate_run_id,
                       candidate_run_attempt, performance_run_id,
                       performance_run_attempt, handoff_run_id,
                       handoff_run_attempt, expected_handoff_artifact_digest,
                       token=None, require_main_head=True):
    """Re-read exact run/attempt/artifact identities for a beta promotion."""
    require(candidate_version == beta_evidence.VERSION and
            beta_evidence.SHA_RE.fullmatch(str(candidate_sha)) is not None,
            'candidate_identity_mismatch')
    run_values = (candidate_run_id, performance_run_id, handoff_run_id)
    attempt_values = (candidate_run_attempt, performance_run_attempt, handoff_run_attempt)
    require(all(RUN_ID_RE.fullmatch(str(value)) is not None for value in run_values) and
            all(type(value) is int and value > 0 for value in attempt_values),
            'run_attempt_invalid')
    require(beta_evidence.ARTIFACT_DIGEST_RE.fullmatch(
        str(expected_handoff_artifact_digest or '')) is not None,
        'handoff_artifact_digest_invalid')
    api = GitHubAPI(token if token is not None else os.environ.get('GITHUB_TOKEN', ''))
    if require_main_head:
        require(_main_sha(api) == candidate_sha, 'main_moved_after_candidate_freeze')
    candidate = _normalize_run(api, str(candidate_run_id), 'candidate', candidate_sha)
    performance = _normalize_run(api, str(performance_run_id), 'performance', candidate_sha)
    handoff = _normalize_run(api, str(handoff_run_id), 'handoff', candidate_sha)
    require(candidate['run_attempt'] == candidate_run_attempt and
            performance['run_attempt'] == performance_run_attempt and
            handoff['run_attempt'] == handoff_run_attempt,
            'run_attempt_mismatch')
    require(handoff['head_sha'] == candidate_sha,
            'handoff_candidate_sha_mismatch')
    assets_artifact = beta_evidence._artifact_by_name(
        candidate, f'release-assets-{candidate_sha}')
    handoff_name = f'beta-evidence-handoff-{handoff_run_id}-{handoff_run_attempt}'
    handoff_artifact = beta_evidence._artifact_by_name(handoff, handoff_name)
    require(handoff_artifact.get('digest') == expected_handoff_artifact_digest,
            'handoff_artifact_digest_mismatch')
    metadata = {
        'metadata_version': 1, 'repository': REPOSITORY,
        'candidate_sha': candidate_sha, 'candidate_version': candidate_version,
        'candidate_run': candidate, 'performance_run': performance,
        'candidate_delivery_url': assets_artifact['url'],
        'promotion_handoff_run': handoff,
        'promotion_handoff_artifact': handoff_artifact,
    }
    try:
        beta_evidence.validate_trusted_metadata(metadata, candidate_sha, candidate_version)
    except beta_evidence.EvidenceError as error:
        raise PreflightError(error.code) from None
    return metadata


def preflight(candidate_sha, candidate_version, candidate_run_id,
              performance_run_id, download_dir, metadata_path, token=None,
              previous_handoff_run_id=None, candidate_run_attempt=None,
              performance_run_attempt=None, handoff_run_id=None,
              handoff_run_attempt=None, expected_handoff_artifact_digest=None,
              require_main_head=False):
    require(candidate_version == beta_evidence.VERSION and
            beta_evidence.SHA_RE.fullmatch(candidate_sha) is not None,
            "candidate_identity_mismatch")
    require(RUN_ID_RE.fullmatch(str(candidate_run_id)) is not None and
            RUN_ID_RE.fullmatch(str(performance_run_id)) is not None,
            "run_id_invalid")
    promotion = handoff_run_id is not None
    require((handoff_run_attempt is not None and expected_handoff_artifact_digest is not None) == promotion,
            'promotion_identity_incomplete')
    require(not (promotion and previous_handoff_run_id is not None),
            'promotion_handoff_mode_ambiguous')
    if promotion:
        require(type(candidate_run_attempt) is int and type(performance_run_attempt) is int,
                'run_attempt_invalid')
        metadata = promotion_metadata(
            candidate_sha, candidate_version, candidate_run_id, candidate_run_attempt,
            performance_run_id, performance_run_attempt, handoff_run_id,
            handoff_run_attempt, expected_handoff_artifact_digest, token,
            require_main_head=require_main_head)
        candidate = metadata['candidate_run']
        performance = metadata['performance_run']
        handoff = metadata['promotion_handoff_run']
        handoff_artifact = metadata['promotion_handoff_artifact']
        api = GitHubAPI(token if token is not None else os.environ.get('GITHUB_TOKEN', ''))
    else:
        api = GitHubAPI(token if token is not None else os.environ.get("GITHUB_TOKEN", ""))
        candidate = _normalize_run(api, str(candidate_run_id), "candidate", candidate_sha)
        performance = _normalize_run(api, str(performance_run_id), "performance", candidate_sha)
        if candidate_run_attempt is not None:
            require(candidate['run_attempt'] == candidate_run_attempt, 'candidate_run_attempt_mismatch')
        if performance_run_attempt is not None:
            require(performance['run_attempt'] == performance_run_attempt, 'performance_run_attempt_mismatch')
    previous_handoff = previous_artifact = None
    if previous_handoff_run_id is not None:
        require(RUN_ID_RE.fullmatch(str(previous_handoff_run_id)) is not None,
                "previous_handoff_run_id_invalid")
        previous_handoff = _normalize_run(api, str(previous_handoff_run_id),
                                          "handoff", candidate_sha)
        previous_name = (f"beta-evidence-handoff-{previous_handoff['run_id']}-"
                         f"{previous_handoff['run_attempt']}")
        previous_artifact = beta_evidence._artifact_by_name(previous_handoff, previous_name)
    assets_artifact = beta_evidence._artifact_by_name(
        candidate, f"release-assets-{candidate_sha}")
    metadata = {
        "metadata_version": 1, "repository": REPOSITORY,
        "candidate_sha": candidate_sha, "candidate_version": candidate_version,
        "candidate_run": candidate, "performance_run": performance,
        "candidate_delivery_url": assets_artifact["url"],
    }
    if promotion:
        metadata["promotion_handoff_run"] = handoff
        metadata["promotion_handoff_artifact"] = handoff_artifact
    elif previous_handoff is not None:
        metadata["previous_handoff_run"] = previous_handoff
        metadata["previous_handoff_artifact"] = previous_artifact
    try:
        beta_evidence.validate_trusted_metadata(metadata, candidate_sha, candidate_version)
    except beta_evidence.EvidenceError as error:
        raise PreflightError(error.code) from None

    download_dir = pathlib.Path(download_dir)
    metadata_path = pathlib.Path(metadata_path)
    beta_evidence._check_components(download_dir, allow_missing=True)
    beta_evidence._check_components(metadata_path, allow_missing=True)
    require(not download_dir.exists() and not metadata_path.exists(), "output_exists")
    download_dir.mkdir(parents=True)
    selected = (
        (candidate, f"release-assets-{candidate_sha}", "assets"),
        (candidate, f"release-verification-{candidate_sha}", "verification"),
        (candidate, f"release-package-{candidate_sha}-linux-amd64", "linux-package"),
        (performance, f"performance-series-{candidate_sha}-{performance_run_id}", "performance-series"),
        (performance, f"performance-soak-{candidate_sha}-{performance_run_id}", "performance-soak"),
    )
    if previous_handoff is not None:
        selected += ((previous_handoff, previous_artifact["name"], "previous-handoff"),)
    if promotion:
        selected += ((handoff, handoff_artifact['name'], 'handoff-bundle'),)
    downloaded_total = 0
    extracted_total = 0
    try:
        for run, artifact_name, folder in selected:
            artifact = beta_evidence._artifact_by_name(run, artifact_name)
            archive = download_dir / ("artifact-" + artifact["id"] + ".zip")
            actual_digest, actual_size = api.download_artifact(artifact["id"], archive)
            downloaded_total += actual_size
            require(downloaded_total <= MAX_ARTIFACT_TOTAL, "artifact_download_total_too_large")
            require(actual_size == artifact["size_in_bytes"], "artifact_size_mismatch")
            expected_digest = artifact["digest"].removeprefix("sha256:")
            require(actual_digest == expected_digest, "artifact_digest_mismatch")
            _root, uncompressed_size = _extract_zip(archive, download_dir / folder)
            extracted_total += uncompressed_size
            require(extracted_total <= MAX_ARTIFACT_TOTAL, "artifact_contents_too_large")
            archive.unlink()
        metadata_path.parent.mkdir(parents=True, exist_ok=True)
        temporary = metadata_path.with_name("." + metadata_path.name + ".tmp")
        beta_evidence.write_json(temporary, metadata)
        os.replace(temporary, metadata_path)
    except BaseException:
        shutil.rmtree(download_dir, ignore_errors=True)
        metadata_path.unlink(missing_ok=True)
        raise
    return {"status": "preflight_passed", "candidate_run_id": str(candidate_run_id),
            "performance_run_id": str(performance_run_id),
            **({'handoff_run_id': str(handoff_run_id),
                'handoff_artifact_id': handoff_artifact['id']} if promotion else {}),
            "artifacts_downloaded": len(selected)}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("preflight",))
    parser.add_argument("--candidate-sha", required=True)
    parser.add_argument("--candidate-version", required=True)
    parser.add_argument("--candidate-run-id", required=True)
    parser.add_argument("--candidate-run-attempt", type=int)
    parser.add_argument("--performance-run-id", required=True)
    parser.add_argument("--performance-run-attempt", type=int)
    parser.add_argument("--previous-handoff-run-id")
    parser.add_argument("--handoff-run-id")
    parser.add_argument("--handoff-run-attempt", type=int)
    parser.add_argument("--expected-handoff-artifact-digest")
    parser.add_argument("--require-main-head", action="store_true")
    parser.add_argument("--download-dir", type=pathlib.Path)
    parser.add_argument("--metadata", required=True, type=pathlib.Path)
    args = parser.parse_args(argv)
    try:
        if args.download_dir is None:
            raise PreflightError('download_dir_missing')
        result = preflight(args.candidate_sha, args.candidate_version,
                           args.candidate_run_id, args.performance_run_id,
                           args.download_dir, args.metadata,
                           previous_handoff_run_id=args.previous_handoff_run_id,
                           candidate_run_attempt=args.candidate_run_attempt,
                           performance_run_attempt=args.performance_run_attempt,
                           handoff_run_id=args.handoff_run_id,
                           handoff_run_attempt=args.handoff_run_attempt,
                           expected_handoff_artifact_digest=args.expected_handoff_artifact_digest,
                           require_main_head=args.require_main_head)
        print(json.dumps(result, sort_keys=True))
        return 0
    except PreflightError as error:
        code = error.code
    except beta_evidence.EvidenceError as error:
        code = error.code
    except (OSError, ValueError, KeyError, TypeError, AttributeError,
            RecursionError, zipfile.BadZipFile):
        code = "preflight_failed"
    print(json.dumps({"status": "failed", "error_code": code}, sort_keys=True))
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
