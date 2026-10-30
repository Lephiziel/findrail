#!/usr/bin/env python3
"""Pure release decision helpers; API errors and ambiguous state fail closed."""
import hashlib
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request


ALPHA2_ASSETS = {
    f'findrail_0.1.0-alpha.2_{target}' + ('.zip' if target.startswith('windows-') else '.tar.gz')
    for target in ('linux-amd64', 'linux-arm64', 'darwin-amd64', 'darwin-arm64', 'windows-amd64')
} | {'SHA256SUMS.txt'}
ALPHA3_REVISION = 'd5dc1bec50efcb3c26d09e9b24b14cf3c0b2f4cb'
BETA1_VERSION = '0.1.0-beta.1'
PUBLISHED_VERSIONS = {
    '0.1.0-alpha.2', '0.1.0-alpha.3', '0.1.0-alpha.4',
    '0.1.0-alpha.5', BETA1_VERSION,
}
PROMOTION_FINGERPRINT_KEYS = (
    'expected_handoff_artifact_digest',
    'expected_reports_zip_sha256',
    'expected_package_manifest_sha256',
    'expected_evidence_index_sha256',
    'expected_publication_notes_sha256',
    'expected_manifest_sha256',
    'expected_checksums_sha256',
    'expected_asset_inventory_sha256',
)
PROMOTION_RUN_KEYS = (
    'candidate_run_id', 'candidate_run_attempt',
    'performance_run_id', 'performance_run_attempt',
    'handoff_run_id', 'handoff_run_attempt',
)
HEX256_RE = re.compile(r'[0-9a-f]{64}')
RUN_ID_RE = re.compile(r'[1-9][0-9]{0,19}')
MAX_GITHUB_JSON = 16 << 20


def _read_github_json(response, label):
    raw = response.read(MAX_GITHUB_JSON + 1)
    if len(raw) > MAX_GITHUB_JSON:
        raise ValueError(label + ' exceeds the response bound')
    try:
        return json.loads(raw)
    except (json.JSONDecodeError, UnicodeDecodeError) as error:
        raise ValueError(label + ' is invalid JSON') from error


def validate_upstream(repository, event, branch, source_sha, expected_repository, expected_branch='main'):
    if repository != expected_repository or event != 'push' or branch != expected_branch or not re.fullmatch(r'[0-9a-f]{40}', source_sha or ''):
        raise ValueError('untrusted or malformed upstream event; publication denied')


def validate_publication_version(candidate_version, release_version, requested):
    """An explicit publish request must never silently downgrade to preview."""
    if requested and candidate_version != release_version:
        raise ValueError('publishing requires candidate version to match build/package/VERSION')
    return bool(requested and candidate_version == release_version)


def existing_release_decision(version, current_sha, tag_sha, release, assets, api_error=None):
    if api_error:
        raise ValueError('release API query failed: ' + str(api_error))
    if version not in PUBLISHED_VERSIONS:
        raise ValueError('unknown published release contract')
    if tag_sha and not release:
        raise ValueError('tag exists without a release; refusing publication')
    if not release:
        if tag_sha:
            raise ValueError('tag exists without a release; refusing publication')
        return 'publish'
    if not tag_sha or not re.fullmatch(r'[0-9a-f]{40}', tag_sha):
        raise ValueError('release tag SHA is missing or invalid')
    if release.get('tag_sha') != tag_sha:
        raise ValueError('release metadata contradicts its tag SHA')
    names = {asset['name'] for asset in assets}
    if len(names) != len(assets):
        raise ValueError('existing release contains duplicate assets')
    expected = ALPHA2_ASSETS if version == '0.1.0-alpha.2' else None
    if expected is None:
        schemas = {'0.1.0-alpha.3': 6, '0.1.0-alpha.4': 7,
                   '0.1.0-alpha.5': 8, BETA1_VERSION: 8}
        if version == BETA1_VERSION:
            if release.get('tag_name') != 'v' + BETA1_VERSION:
                raise ValueError('published beta.1 release tag name is contradictory')
            if release.get('prerelease') is not True or release.get('draft') is not False:
                raise ValueError('published beta.1 must be a public prerelease')
        if version == '0.1.0-alpha.3' and tag_sha != ALPHA3_REVISION:
            raise ValueError('published alpha.3 tag does not match its historical revision')
        manifest = release.get('manifest')
        if (not manifest or manifest.get('manifest_version') != 1 or
                manifest.get('source_revision') != tag_sha or manifest.get('app_version') != version or
                type(manifest.get('schema_version')) is not int or
                manifest.get('schema_version') != schemas[version] or manifest.get('client_api_version') != 1 or
                manifest.get('extension_version') != '0.1.0' or not isinstance(manifest.get('assets'), list)):
            raise ValueError('existing release manifest contradicts version or tag revision')
        manifest_names = [item.get('basename') for item in manifest['assets']]
        if any(not isinstance(name, str) or not name or name in ('.', '..') or
               any(char in name for char in '/\\:\n\r') for name in manifest_names):
            raise ValueError('existing release manifest has an unsafe asset name')
        if len(manifest_names) != len(set(manifest_names)):
            raise ValueError('existing release manifest has duplicate or invalid names')
        targets = ('linux-amd64', 'linux-arm64', 'darwin-amd64', 'darwin-arm64', 'windows-amd64')
        contractual = {f'findrail_{version}_{target}' + ('.zip' if target.startswith('windows-') else '.tar.gz') for target in targets}
        contractual.add('findrail-readonly-0.1.0.vsix')
        if set(manifest_names) != contractual:
            raise ValueError('existing release manifest does not have the complete version asset contract')
        expected = set(manifest_names) | {'release-manifest.json', 'SHA256SUMS.txt'}
    if names != expected:
        raise ValueError('existing release is incomplete for its version contract')
    if version == '0.1.0-alpha.2' and release.get('tag_sha') != tag_sha:
        raise ValueError('historical alpha.2 tag metadata mismatch')
    # The release is tied to its immutable historical tag, not today's docs/feature commit.
    return 'noop'


def verify_remote_checksums(assets, checksum_text):
    checksums = {}
    for line in checksum_text.splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  ([^\s]+)', line)
        if not match or match[2] in checksums:
            raise ValueError('invalid existing checksum manifest')
        checksums[match[2]] = match[1]
    if set(checksums) != {asset['name'] for asset in assets if asset['name'] != 'SHA256SUMS.txt'}:
        raise ValueError('existing checksum manifest asset set is incomplete')
    for asset in assets:
        name = asset['name']
        if name == 'SHA256SUMS.txt':
            continue
        data = asset.get('data')
        if data is None or hashlib.sha256(data).hexdigest() != checksums[name]:
            raise ValueError('existing release asset checksum mismatch: ' + name)


def github_release_state(repository, version, current_sha, token):
    """Query immutable release identity and every byte; only paired 404s are absent."""
    if version not in PUBLISHED_VERSIONS:
        raise ValueError('unknown published release contract')
    if not re.fullmatch(r'[0-9a-f]{40}', current_sha):
        raise ValueError('current source revision is not a full commit SHA')
    base = 'https://api.github.com/repos/' + repository

    def get(url, allow_missing=False):
        request = urllib.request.Request(url, headers={'Accept': 'application/vnd.github+json',
            'Authorization': 'Bearer ' + token, 'X-GitHub-Api-Version': '2022-11-28'})
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                return _read_github_json(response, 'GitHub API response')
        except urllib.error.HTTPError as error:
            if allow_missing and error.code == 404:
                return None
            raise ValueError(f'GitHub API request failed ({error.code}): {url}') from error
        except (OSError, json.JSONDecodeError) as error:
            raise ValueError('GitHub API query failed: ' + str(error)) from error

    tag = 'v' + version
    release = get(f'{base}/releases/tags/{tag}', allow_missing=True)
    ref = get(f'{base}/git/ref/tags/{tag}', allow_missing=True)
    if not release and not ref:
        # A draft can be hidden from /releases/tags while still occupying this
        # immutable tag/version. Never create a second draft to repair it.
        for page_number in range(1, 21):
            page = get(f'{base}/releases?per_page=100&page={page_number}')
            if type(page) is not list:
                raise ValueError('GitHub release list response is invalid')
            if any(type(item) is dict and item.get('tag_name') == tag for item in page):
                raise ValueError('a draft or incomplete release already occupies beta.1; refusing repair')
            if len(page) < 100:
                break
        else:
            raise ValueError('GitHub release list exceeds the bounded draft lookup')
        return {'decision': 'publish', 'published_sha': None}
    if ref is None:
        raise ValueError('release exists without its Git tag')
    obj = ref['object']
    if obj.get('type') == 'tag':
        obj = get(f"{base}/git/tags/{obj['sha']}")['object']
    tag_sha = obj.get('sha') if obj.get('type') == 'commit' else None
    if release is None:
        raise ValueError('tag exists without a release; refusing publication')
    remote_assets = release.get('assets')
    if type(remote_assets) is not list:
        raise ValueError('existing release asset list is invalid')
    assets = []
    total_downloaded = 0
    expected_download_prefix = f'https://github.com/{repository}/releases/download/'
    for asset in remote_assets:
        if (type(asset) is not dict or type(asset.get('name')) is not str or
                type(asset.get('browser_download_url')) is not str or
                not asset['browser_download_url'].startswith(expected_download_prefix)):
            raise ValueError('existing release asset metadata is invalid')
        request = urllib.request.Request(asset['browser_download_url'], headers={'Accept': 'application/octet-stream'})
        try:
            with urllib.request.urlopen(request, timeout=60) as response:
                data = response.read((512 << 20) + 1)
        except (OSError, urllib.error.HTTPError) as error:
            raise ValueError(f"could not download existing release asset {asset.get('name')}: {error}") from error
        if len(data) > (512 << 20):
            raise ValueError('existing release asset exceeds the verification bound')
        total_downloaded += len(data)
        if total_downloaded > (1 << 30):
            raise ValueError('existing release assets exceed the aggregate verification bound')
        if type(asset.get('size')) is not int or asset['size'] != len(data):
            raise ValueError('existing release asset size mismatch: ' + asset['name'])
        assets.append({'name': asset['name'], 'data': data})
    checksum = next((asset['data'].decode('utf-8') for asset in assets if asset['name'] == 'SHA256SUMS.txt'), None)
    manifest = None
    manifest_asset = next((asset['data'] for asset in assets if asset['name'] == 'release-manifest.json'), None)
    if manifest_asset:
        try:
            manifest = json.loads(manifest_asset)
        except (ValueError, UnicodeDecodeError) as error:
            raise ValueError('existing release manifest is invalid') from error
    release_data = {'tag_sha': tag_sha, 'manifest': manifest,
                    'tag_name': release.get('tag_name'),
                    'prerelease': release.get('prerelease'),
                    'draft': release.get('draft')}
    decision = existing_release_decision(version, current_sha, tag_sha, release_data, assets)
    if checksum is None:
        raise ValueError('existing release has no checksum asset')
    verify_remote_checksums(assets, checksum)
    if manifest is not None:
        by_name = {asset['name']: asset for asset in assets}
        if set(by_name) != {item['basename'] for item in manifest.get('assets', [])} | {'release-manifest.json', 'SHA256SUMS.txt'}:
            raise ValueError('existing manifest and release asset inventory disagree')
        for item in manifest['assets']:
            target = next((target for name, target in {
                f'findrail_{version}_{target}' + ('.zip' if target.startswith('windows-') else '.tar.gz'): target
                for target in ('linux-amd64', 'linux-arm64', 'darwin-amd64', 'darwin-arm64', 'windows-amd64')
            }.items() if name == item['basename']), None)
            is_vsix = item['basename'] == 'findrail-readonly-0.1.0.vsix'
            if (item.get('kind') != ('archive' if target else 'vsix') or
                    (target and item.get('target') != target) or (is_vsix and 'target' in item) or
                    not isinstance(item.get('size'), int) or item['size'] < 0 or
                    not re.fullmatch(r'[0-9a-f]{64}', item.get('sha256', ''))):
                raise ValueError('existing release manifest has invalid asset metadata: ' + item['basename'])
            asset = by_name[item['basename']]
            if item.get('size') != len(asset['data']) or item.get('sha256') != hashlib.sha256(asset['data']).hexdigest():
                raise ValueError('existing release manifest digest/size mismatch: ' + item['basename'])
        # Matching manifest/checksum bytes alone cannot establish the embedded source identity.
        import pathlib
        import tempfile
        import release_assets
        with tempfile.TemporaryDirectory(prefix='findrail-existing-release-') as temporary:
            directory = pathlib.Path(temporary)
            for asset in assets:
                (directory / asset['name']).write_bytes(asset['data'])
            release_assets.verify(directory, version, tag_sha)
    return {'decision': decision, 'published_sha': tag_sha if decision == 'noop' else None}


def github_release_decision(repository, version, current_sha, token):
    """Compatibility wrapper returning only publish/noop for existing callers."""
    return github_release_state(repository, version, current_sha, token)['decision']


def validate_promotion_request(request):
    """Validate explicit owner dispatch context and all handoff fingerprints."""
    required = {
        'repository', 'event', 'ref', 'actor', 'workflow_sha', 'candidate_sha',
        'candidate_version', 'release_version', 'scope', 'publish',
        *PROMOTION_RUN_KEYS, *PROMOTION_FINGERPRINT_KEYS,
    }
    if type(request) is not dict or set(request) != required:
        raise ValueError('promotion request is incomplete or has unknown fields')
    if (request['repository'] != 'Lephiziel/findrail' or
            request['event'] != 'workflow_dispatch' or
            request['ref'] != 'refs/heads/main'):
        raise ValueError('promotion requires canonical main workflow_dispatch')
    if request['publish'] not in (True, False) or type(request['publish']) is not bool:
        raise ValueError('publish must be an explicit boolean')
    if request['publish'] and request['actor'] != 'Lephiziel':
        raise ValueError('only the repository owner may request publication')
    if request['scope'] != 'limited-desktop-beta':
        raise ValueError('unsupported or missing promotion scope')
    if request['candidate_version'] != BETA1_VERSION or request['release_version'] != BETA1_VERSION:
        raise ValueError('promotion requires the immutable beta.1 VERSION contract')
    for key in ('workflow_sha', 'candidate_sha'):
        if not isinstance(request[key], str) or not re.fullmatch(r'[0-9a-f]{40}', request[key]):
            raise ValueError(key.replace('_', ' ') + ' must be a full commit SHA')
    if request['candidate_sha'] != request['workflow_sha']:
        raise ValueError('candidate SHA must be the exact workflow-dispatch main SHA')
    for key in PROMOTION_RUN_KEYS:
        if not isinstance(request[key], str) or not RUN_ID_RE.fullmatch(request[key]):
            raise ValueError(key.replace('_', ' ') + ' must be an exact positive decimal identity')
    for key in PROMOTION_FINGERPRINT_KEYS:
        value = request[key]
        pattern = r'sha256:[0-9a-f]{64}' if key == 'expected_handoff_artifact_digest' else r'[0-9a-f]{64}'
        if not isinstance(value, str) or not re.fullmatch(pattern, value):
            raise ValueError(key.replace('_', ' ') + ' is missing or malformed')
    return request


def asset_inventory_fingerprint(inventory):
    """Hash the exact, sorted eight-file inventory recorded in the handoff."""
    import release_assets
    expected = (set(release_assets.expected_archives(BETA1_VERSION)) |
                {release_assets.VSIX, 'release-manifest.json', 'SHA256SUMS.txt'})
    if type(inventory) is not list or len(inventory) != 8:
        raise ValueError('beta.1 asset inventory must contain exactly eight files')
    normalized = []
    for item in inventory:
        if (type(item) is not dict or set(item) != {'basename', 'size', 'sha256'} or
                type(item.get('basename')) is not str or type(item.get('size')) is not int or
                item['size'] <= 0 or not isinstance(item.get('sha256'), str) or
                not HEX256_RE.fullmatch(item['sha256'])):
            raise ValueError('beta.1 asset inventory entry is invalid')
        normalized.append({'basename': item['basename'], 'size': item['size'],
                           'sha256': item['sha256']})
    if {item['basename'] for item in normalized} != expected:
        raise ValueError('beta.1 asset inventory has missing or unknown files')
    normalized.sort(key=lambda item: item['basename'])
    payload = json.dumps(normalized, sort_keys=True, separators=(',', ':')).encode('ascii')
    return hashlib.sha256(payload).hexdigest()


def promotion_request_from_environment():
    """Read scalar workflow inputs without putting user text in shell source."""
    values = {
        'repository': os.environ.get('GITHUB_REPOSITORY', ''),
        'event': os.environ.get('GITHUB_EVENT_NAME', ''),
        'ref': os.environ.get('GITHUB_REF', ''),
        'actor': os.environ.get('GITHUB_ACTOR', ''),
        'workflow_sha': os.environ.get('GITHUB_SHA', ''),
        'candidate_sha': os.environ.get('CANDIDATE_SHA', ''),
        'candidate_version': os.environ.get('CANDIDATE_VERSION', ''),
        'release_version': os.environ.get('RELEASE_VERSION', ''),
        'scope': os.environ.get('PROMOTION_SCOPE', ''),
        'publish': os.environ.get('PUBLISH', '') == 'true',
    }
    values.update({key: os.environ.get(key.upper(), '') for key in
                   (*PROMOTION_RUN_KEYS, *PROMOTION_FINGERPRINT_KEYS)})
    return validate_promotion_request(values)


def verify_promotion_bundle(request, assets_dir, handoff_artifact_dir, run_metadata_path):
    """Re-run strict asset/evidence verification and compare every reviewed digest."""
    import argparse
    import beta_evidence

    request = validate_promotion_request(request)
    bundle = pathlib.Path(handoff_artifact_dir)
    verify_args = argparse.Namespace(
        candidate_sha=request['candidate_sha'],
        candidate_version=request['candidate_version'],
        assets_dir=pathlib.Path(assets_dir),
        handoff_dir=bundle / 'handoff',
        reports_zip=bundle / 'beta-evidence-reports.zip',
        trusted_run_metadata=pathlib.Path(run_metadata_path),
        handoff_artifact_dir=bundle,
    )
    evidence_result = beta_evidence.verify(verify_args)
    metadata, _ = beta_evidence.load_json(run_metadata_path, 16 << 20)
    handoff_run = metadata.get('promotion_handoff_run', {})
    if (metadata.get('candidate_run', {}).get('run_id') != request['candidate_run_id'] or
            metadata.get('candidate_run', {}).get('run_attempt') != int(request['candidate_run_attempt']) or
            metadata.get('performance_run', {}).get('run_id') != request['performance_run_id'] or
            metadata.get('performance_run', {}).get('run_attempt') != int(request['performance_run_attempt']) or
            handoff_run.get('run_id') != request['handoff_run_id'] or
            handoff_run.get('run_attempt') != int(request['handoff_run_attempt'])):
        raise ValueError('GitHub run ID or attempt differs from the accepted handoff')

    index, _ = beta_evidence.load_json(bundle / 'handoff/evidence-index.json', 4 << 20)
    result, _ = beta_evidence.load_json(bundle / 'result.json', 1 << 20)
    actual = {
        'expected_handoff_artifact_digest': metadata['promotion_handoff_artifact']['digest'],
        'expected_reports_zip_sha256': evidence_result['report_zip_sha256'],
        'expected_package_manifest_sha256': beta_evidence.file_digest(
            bundle / 'handoff/PACKAGE-MANIFEST.json'),
        'expected_evidence_index_sha256': beta_evidence.file_digest(
            bundle / 'handoff/evidence-index.json'),
        'expected_publication_notes_sha256': beta_evidence.file_digest(
            bundle / 'handoff/publication-notes.md'),
        'expected_manifest_sha256': beta_evidence.file_digest(
            pathlib.Path(assets_dir) / 'release-manifest.json'),
        'expected_checksums_sha256': beta_evidence.file_digest(
            pathlib.Path(assets_dir) / 'SHA256SUMS.txt'),
        'expected_asset_inventory_sha256': asset_inventory_fingerprint(
            index.get('candidate', {}).get('assets')),
    }
    if result.get('report_zip_sha256') != actual['expected_reports_zip_sha256']:
        raise ValueError('handoff result fingerprint contradicts reports ZIP bytes')
    for key, value in actual.items():
        if request[key] != value:
            raise ValueError('accepted handoff fingerprint mismatch: ' + key)
    return {'status': 'verified', 'candidate_sha': request['candidate_sha'],
            'candidate_version': request['candidate_version'],
            'fingerprints': actual, 'readiness': index['readiness']}


def handoff_fingerprints(candidate_sha, assets_dir, handoff_dir, reports_zip,
                         handoff_artifact_digest):
    """Print the exact dispatch fingerprint values from a verified offline handoff."""
    import argparse
    import beta_evidence
    if beta_evidence.ARTIFACT_DIGEST_RE.fullmatch(str(handoff_artifact_digest or '')) is None:
        raise ValueError('handoff artifact digest must be sha256:<64 lowercase hex>')
    result = beta_evidence.verify(argparse.Namespace(
        candidate_sha=candidate_sha, candidate_version=BETA1_VERSION,
        assets_dir=pathlib.Path(assets_dir), handoff_dir=pathlib.Path(handoff_dir),
        reports_zip=pathlib.Path(reports_zip)))
    index, _ = beta_evidence.load_json(pathlib.Path(handoff_dir) / 'evidence-index.json', 4 << 20)
    return {
        'expected_handoff_artifact_digest': handoff_artifact_digest,
        'expected_reports_zip_sha256': result['report_zip_sha256'],
        'expected_package_manifest_sha256': beta_evidence.file_digest(
            pathlib.Path(handoff_dir) / 'PACKAGE-MANIFEST.json'),
        'expected_evidence_index_sha256': beta_evidence.file_digest(
            pathlib.Path(handoff_dir) / 'evidence-index.json'),
        'expected_publication_notes_sha256': beta_evidence.file_digest(
            pathlib.Path(handoff_dir) / 'publication-notes.md'),
        'expected_manifest_sha256': beta_evidence.file_digest(
            pathlib.Path(assets_dir) / 'release-manifest.json'),
        'expected_checksums_sha256': beta_evidence.file_digest(
            pathlib.Path(assets_dir) / 'SHA256SUMS.txt'),
        'expected_asset_inventory_sha256': asset_inventory_fingerprint(
            index.get('candidate', {}).get('assets')),
    }


def guarded_promotion_write(request, verified, release_state, live_main_sha, write_release,
                            published_sha=None):
    """Invoke the single write sink only after every explicit guard is satisfied."""
    request = validate_promotion_request(request)
    if not verified:
        raise ValueError('candidate and evidence verification did not pass')
    if release_state == 'noop':
        if not isinstance(published_sha, str) or not re.fullmatch(r'[0-9a-f]{40}', published_sha):
            raise ValueError('published beta.1 SHA is unavailable for the no-op')
        return {'decision': 'noop', 'published_sha': published_sha}
    if release_state != 'publish':
        raise ValueError('release/tag state is ambiguous; refusing to write')
    if live_main_sha != request['candidate_sha']:
        raise ValueError('main moved after candidate freeze; new review is required')
    if not request['publish']:
        return {'decision': 'dry-run', 'published_sha': None}
    write_release()
    return {'decision': 'write_started', 'published_sha': None}


def github_main_sha(repository, token):
    if repository != 'Lephiziel/findrail':
        raise ValueError('main SHA lookup requires the canonical repository')
    url = 'https://api.github.com/repos/' + repository + '/git/ref/heads/main'
    request = urllib.request.Request(url, headers={
        'Accept': 'application/vnd.github+json',
        'Authorization': 'Bearer ' + token,
        'X-GitHub-Api-Version': '2022-11-28',
    })
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            value = _read_github_json(response, 'GitHub main ref response')
    except (OSError, urllib.error.HTTPError, json.JSONDecodeError) as error:
        raise ValueError('could not recheck canonical main SHA: ' + str(error)) from error
    obj = value.get('object', {}) if type(value) is dict else {}
    sha = obj.get('sha') if type(obj) is dict else None
    if obj.get('type') != 'commit' or not isinstance(sha, str) or not re.fullmatch(r'[0-9a-f]{40}', sha):
        raise ValueError('canonical main ref response is invalid')
    return sha


def _api_json(repository, path, token, method='GET', payload=None, accept='application/vnd.github+json'):
    url = 'https://api.github.com/repos/' + repository + path
    data = None if payload is None else json.dumps(payload, separators=(',', ':')).encode('utf-8')
    headers = {'Accept': accept, 'Authorization': 'Bearer ' + token,
               'X-GitHub-Api-Version': '2022-11-28', 'User-Agent': 'findrail-explicit-release-gate'}
    if data is not None:
        headers['Content-Type'] = 'application/json'
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=120) as response:
            value = _read_github_json(response, 'GitHub release API response')
    except urllib.error.HTTPError as error:
        raise ValueError(f'GitHub release API request failed ({error.code})') from error
    except (OSError, urllib.error.URLError) as error:
        raise ValueError('GitHub release API request failed: ' + str(error)) from error
    if type(value) is not dict and type(value) is not list:
        raise ValueError('GitHub release API returned an unexpected response')
    return value


def _api_upload(upload_url, name, path, token):
    parsed = urllib.parse.urlsplit(upload_url.replace('{?name,label}', ''))
    if parsed.scheme != 'https' or parsed.hostname != 'uploads.github.com':
        raise ValueError('release upload URL is outside the GitHub upload service')
    url = upload_url.replace('{?name,label}', '') + '?' + urllib.parse.urlencode({'name': name})
    path = pathlib.Path(path)
    if path.stat().st_size > 512 << 20:
        raise ValueError('release asset exceeds the upload verification bound: ' + name)
    data = path.read_bytes()
    request = urllib.request.Request(url, data=data, headers={
        'Accept': 'application/vnd.github+json', 'Authorization': 'Bearer ' + token,
        'X-GitHub-Api-Version': '2022-11-28', 'Content-Type': 'application/octet-stream',
    }, method='POST')
    try:
        with urllib.request.build_opener(_ReleaseRedirect()).open(request, timeout=300) as response:
            result = _read_github_json(response, 'release asset upload response')
    except urllib.error.HTTPError as error:
        raise ValueError(f'release asset upload failed ({error.code}): {name}') from error
    except (OSError, urllib.error.URLError, json.JSONDecodeError) as error:
        raise ValueError('release asset upload failed: ' + name) from error
    if (type(result) is not dict or result.get('name') != name or
            result.get('size') != len(data)):
        raise ValueError('release asset upload response mismatch: ' + name)
    return result


class _ReleaseRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, new_url):
        result = super().redirect_request(request, response, code, message, headers, new_url)
        if result is not None and urllib.parse.urlsplit(new_url).hostname != urllib.parse.urlsplit(request.full_url).hostname:
            for key in list(result.headers):
                if key.lower() in ('authorization', 'proxy-authorization'):
                    del result.headers[key]
            for key in list(result.unredirected_hdrs):
                if key.lower() in ('authorization', 'proxy-authorization'):
                    del result.unredirected_hdrs[key]
        return result


def _download_api_asset(asset, token, maximum=512 << 20):
    url = asset.get('url')
    if not isinstance(url, str) or not url.startswith('https://api.github.com/repos/Lephiziel/findrail/releases/assets/'):
        raise ValueError('draft release asset API URL is invalid')
    request = urllib.request.Request(url, headers={
        'Accept': 'application/octet-stream', 'Authorization': 'Bearer ' + token,
        'X-GitHub-Api-Version': '2022-11-28',
    })
    try:
        with urllib.request.build_opener(_ReleaseRedirect()).open(request, timeout=120) as response:
            data = response.read(maximum + 1)
    except (OSError, urllib.error.URLError, urllib.error.HTTPError) as error:
        raise ValueError('could not read uploaded draft asset: ' + str(asset.get('name'))) from error
    if len(data) > maximum:
        raise ValueError('uploaded draft asset exceeds verification bound')
    return data


def _release_assets(version):
    import release_assets
    return sorted(set(release_assets.expected_archives(version)) |
                  {release_assets.VSIX, 'release-manifest.json', 'SHA256SUMS.txt'})


def create_verified_beta_release(request, assets_dir, handoff_artifact_dir, token,
                                 expected, write_audit, write_sink=None,
                                 main_sha_lookup=None, release_state_lookup=None):
    """Verify, stage as a draft, compare remote bytes, then make one prerelease public."""
    import release_assets
    request = validate_promotion_request(request)
    root = pathlib.Path(assets_dir)
    notes_path = pathlib.Path(handoff_artifact_dir) / 'handoff/publication-notes.md'
    inventory = release_assets.verify(root, BETA1_VERSION, request['candidate_sha'])
    notes = notes_path.read_bytes()
    if hashlib.sha256(notes).hexdigest() != expected['expected_publication_notes_sha256']:
        raise ValueError('publication-notes body does not match the reviewed fingerprint')
    for name in _release_assets(BETA1_VERSION):
        if not (root / name).is_file() or (root / name).is_symlink():
            raise ValueError('verified app asset is missing or unsafe: ' + name)
    audit_base = {'candidate_sha': request['candidate_sha'],
                 'candidate_version': BETA1_VERSION, 'scope': request['scope'],
                 'candidate_run_id': request['candidate_run_id'],
                 'candidate_run_attempt': request['candidate_run_attempt'],
                 'performance_run_id': request['performance_run_id'],
                 'performance_run_attempt': request['performance_run_attempt'],
                 'handoff_run_id': request['handoff_run_id'],
                 'handoff_run_attempt': request['handoff_run_attempt'],
                 'fingerprints': expected}
    write_audit({**audit_base, 'status': 'promotion_verified'})
    state = (release_state_lookup or (lambda: github_release_state(
        request['repository'], BETA1_VERSION, request['workflow_sha'], token)))()
    decision = state['decision']
    if decision == 'noop':
        write_audit({**audit_base, 'status': 'noop',
                     'published_sha': state['published_sha']})
        return {'decision': 'noop', 'published_sha': state['published_sha']}
    if decision != 'publish':
        raise ValueError('release/tag state is ambiguous; refusing publication')
    # This is deliberately the final read before the one write sink.
    live_main_sha = (main_sha_lookup or (lambda: github_main_sha(request['repository'], token)))()

    def sink():
        write_audit({**audit_base, 'status': 'write_started',
                     'external_operation_may_be_partial': True})
        if write_sink is not None:
            return write_sink(root, notes_path, request['candidate_sha'])
        return _publish_draft_then_transition(root, notes, request, token)

    result = guarded_promotion_write(request, True, decision, live_main_sha, sink)
    post = (release_state_lookup or (lambda: github_release_state(
        request['repository'], BETA1_VERSION, request['workflow_sha'], token)))()
    if post['decision'] != 'noop' or post['published_sha'] != request['candidate_sha']:
        raise ValueError('published release failed the post-write immutable contract')
    write_audit({**audit_base, 'status': 'published',
                 'published_sha': post['published_sha']})
    return {'decision': 'published', 'published_sha': post['published_sha'],
            'inventory': inventory}


def _publish_draft_then_transition(assets_dir, notes, request, token):
    import release_assets
    version = BETA1_VERSION
    tag = 'v' + version
    payload = {
        'tag_name': tag,
        'target_commitish': request['candidate_sha'],
        'name': 'Findrail ' + tag,
        'body': notes.decode('utf-8', errors='strict'),
        'draft': True,
        'prerelease': True,
        'make_latest': 'false',
    }
    draft = _api_json(request['repository'], '/releases', token, 'POST', payload)
    release_id = draft.get('id')
    upload_url = draft.get('upload_url')
    if (type(release_id) is not int or release_id <= 0 or draft.get('tag_name') != tag or
            draft.get('draft') is not True or draft.get('prerelease') is not True or
            draft.get('body') != payload['body'] or draft.get('target_commitish') != request['candidate_sha'] or
            not isinstance(upload_url, str)):
        raise ValueError('GitHub did not create the exact beta.1 draft contract')
    names = _release_assets(version)
    for name in names:
        _api_upload(upload_url, name, pathlib.Path(assets_dir) / name, token)

    listed = _api_json(request['repository'], f'/releases/{release_id}/assets?per_page=100', token)
    if type(listed) is not list or len(listed) != len(names):
        raise ValueError('draft release asset set is incomplete before publication')
    by_name = {}
    for asset in listed:
        if type(asset) is not dict or type(asset.get('name')) is not str or asset['name'] in by_name:
            raise ValueError('draft release has duplicate or malformed assets')
        by_name[asset['name']] = asset
    if set(by_name) != set(names):
        raise ValueError('draft release asset names differ from the approved eight-file set')
    with tempfile.TemporaryDirectory(prefix='findrail-beta-draft-') as temporary:
        downloaded = pathlib.Path(temporary)
        for name in names:
            asset = by_name[name]
            data = _download_api_asset(asset, token)
            if asset.get('size') != len(data) or hashlib.sha256(data).hexdigest() != hashlib.sha256(
                    (pathlib.Path(assets_dir) / name).read_bytes()).hexdigest():
                raise ValueError('uploaded draft asset differs from reviewed bytes: ' + name)
            (downloaded / name).write_bytes(data)
        release_assets.verify(downloaded, version, request['candidate_sha'])

    current = _api_json(request['repository'], f'/releases/{release_id}', token)
    if (current.get('draft') is not True or current.get('prerelease') is not True or
            current.get('tag_name') != tag or current.get('body') != payload['body'] or
            current.get('target_commitish') != request['candidate_sha']):
        raise ValueError('draft release changed before public transition')
    final_assets = _api_json(request['repository'], f'/releases/{release_id}/assets?per_page=100', token)
    if type(final_assets) is not list or len(final_assets) != len(names):
        raise ValueError('draft release asset set changed before public transition')
    final_by_name = {}
    for asset in final_assets:
        if type(asset) is not dict or type(asset.get('name')) is not str or asset['name'] in final_by_name:
            raise ValueError('draft release assets changed before public transition')
        final_by_name[asset['name']] = asset
    if set(final_by_name) != set(names):
        raise ValueError('draft release names changed before public transition')
    with tempfile.TemporaryDirectory(prefix='findrail-beta-final-draft-') as temporary:
        final_root = pathlib.Path(temporary)
        for name in names:
            data = _download_api_asset(final_by_name[name], token)
            local = pathlib.Path(assets_dir) / name
            if (final_by_name[name].get('size') != len(data) or
                    hashlib.sha256(data).digest() != hashlib.sha256(local.read_bytes()).digest()):
                raise ValueError('draft asset changed before public transition: ' + name)
            (final_root / name).write_bytes(data)
        release_assets.verify(final_root, version, request['candidate_sha'])
    _api_json(request['repository'], f'/releases/{release_id}', token, 'PATCH', {
        'draft': False, 'prerelease': True, 'make_latest': 'false'})


def stage_promotion_bundle(assets_dir, handoff_artifact_dir, destination):
    destination = pathlib.Path(destination)
    if destination.exists() or destination.is_symlink():
        raise ValueError('promotion staging destination already exists')
    destination.mkdir(parents=True)
    try:
        shutil.copytree(assets_dir, destination / 'release-assets', symlinks=False)
        shutil.copytree(handoff_artifact_dir, destination / 'handoff-artifact', symlinks=False)
    except BaseException:
        shutil.rmtree(destination, ignore_errors=True)
        raise


def _write_audit(path, value):
    target = pathlib.Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    temporary = target.with_name('.' + target.name + '.tmp')
    temporary.write_text(json.dumps(value, sort_keys=True, indent=2) + '\n', encoding='utf-8')
    os.replace(temporary, target)


def _promotion_cli(argv):
    import argparse

    parser = argparse.ArgumentParser(description='Verify or explicitly promote the beta.1 evidence bundle.')
    subparsers = parser.add_subparsers(dest='command', required=True)
    for command in ('promotion-verify', 'promotion-publish'):
        sub = subparsers.add_parser(command)
        sub.add_argument('--assets-dir', required=True, type=pathlib.Path)
        sub.add_argument('--handoff-artifact-dir', required=True, type=pathlib.Path)
        sub.add_argument('--run-metadata', required=True, type=pathlib.Path)
        if command == 'promotion-verify':
            sub.add_argument('--stage-directory', type=pathlib.Path)
        else:
            sub.add_argument('--audit-file', required=True, type=pathlib.Path)
    args = parser.parse_args(argv)
    audit = None
    request = None
    try:
        request = promotion_request_from_environment()
        if args.command == 'promotion-verify':
            result = verify_promotion_bundle(
                request, args.assets_dir, args.handoff_artifact_dir, args.run_metadata)
            if args.stage_directory is not None:
                if not request['publish']:
                    raise ValueError('read-only dry-run cannot stage a publisher artifact')
                stage_promotion_bundle(args.assets_dir, args.handoff_artifact_dir,
                                       args.stage_directory)
            print(json.dumps(result, sort_keys=True))
            return 0

        audit = {'status': 'verification_pending', 'candidate_sha': request['candidate_sha'],
                 'candidate_version': request['candidate_version'], 'scope': request['scope'],
                 'candidate_run_id': request['candidate_run_id'],
                 'candidate_run_attempt': request['candidate_run_attempt'],
                 'performance_run_id': request['performance_run_id'],
                 'performance_run_attempt': request['performance_run_attempt'],
                 'handoff_run_id': request['handoff_run_id'],
                 'handoff_run_attempt': request['handoff_run_attempt'],
                 'fingerprints': {key: request[key] for key in PROMOTION_FINGERPRINT_KEYS}}
        _write_audit(args.audit_file, audit)
        try:
            verified = verify_promotion_bundle(
                request, args.assets_dir, args.handoff_artifact_dir, args.run_metadata)
            audit.update({'status': 'verification_passed',
                          'readiness': verified['readiness']})
            _write_audit(args.audit_file, audit)
            token = os.environ.get('GH_TOKEN', '')
            if len(token) < 20:
                raise ValueError('GitHub write token is unavailable')

            def record_audit(update):
                audit.update(update)
                _write_audit(args.audit_file, audit)

            result = create_verified_beta_release(
                request, args.assets_dir, args.handoff_artifact_dir, token,
                verified['fingerprints'],
                record_audit)
            audit.update(result)
            audit['status'] = 'noop' if result['decision'] == 'noop' else 'published'
            _write_audit(args.audit_file, audit)
            print(json.dumps(result, sort_keys=True))
            return 0
        except BaseException:
            status = ('external_operation_failed_may_be_partial'
                      if audit.get('status') == 'write_started' else 'prepublication_failed')
            audit.update({'status': status, 'automatic_repair': False})
            _write_audit(args.audit_file, audit)
            raise
    except (OSError, ValueError, KeyError, TypeError, AttributeError,
            subprocess.SubprocessError, UnicodeError) as error:
        parser.error(str(error))


if __name__ == '__main__':
    argv = sys.argv[1:]
    if argv and argv[0] == 'handoff-fingerprints':
        import argparse
        parser = argparse.ArgumentParser(description='Verify a beta.1 handoff and print dispatch fingerprints.')
        parser.add_argument('command', choices=('handoff-fingerprints',))
        parser.add_argument('--candidate-sha', required=True)
        parser.add_argument('--assets-dir', required=True, type=pathlib.Path)
        parser.add_argument('--handoff-dir', required=True, type=pathlib.Path)
        parser.add_argument('--reports-zip', required=True, type=pathlib.Path)
        parser.add_argument('--handoff-artifact-digest', required=True)
        args = parser.parse_args(argv)
        try:
            values = handoff_fingerprints(args.candidate_sha, args.assets_dir,
                                          args.handoff_dir, args.reports_zip,
                                          args.handoff_artifact_digest)
            print(json.dumps(values, sort_keys=True, indent=2))
        except (OSError, ValueError, KeyError, TypeError, AttributeError) as error:
            parser.error(str(error))
        raise SystemExit(0)
    if argv and argv[0] in ('promotion-verify', 'promotion-publish'):
        raise SystemExit(_promotion_cli(argv))
    import argparse
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repository', required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--current-sha', required=True)
    parser.add_argument('--json', action='store_true',
                        help='print decision and immutable published SHA as JSON')
    args = parser.parse_args()
    try:
        state = github_release_state(args.repository, args.version, args.current_sha,
                                     os.environ['GH_TOKEN'])
        print(json.dumps(state, sort_keys=True) if args.json else state['decision'])
    except (ValueError, KeyError, UnicodeDecodeError) as error:
        parser.error(str(error))
