#!/usr/bin/env python3
"""Pure release decision helpers; API errors and ambiguous state fail closed."""
import hashlib
import json
import os
import re
import urllib.error
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


if __name__ == '__main__':
    import argparse

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repository', required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--current-sha', required=True)
    parser.add_argument('--json', action='store_true',
                        help='print the decision and immutable published SHA as JSON')
    args = parser.parse_args()
    try:
        state = github_release_state(args.repository, args.version, args.current_sha,
                                     os.environ['GH_TOKEN'])
        print(json.dumps(state, sort_keys=True) if args.json else state['decision'])
    except (ValueError, KeyError, UnicodeDecodeError) as error:
        parser.error(str(error))
