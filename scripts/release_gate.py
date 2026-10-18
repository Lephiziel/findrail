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


def validate_upstream(repository, event, branch, source_sha, expected_repository, expected_branch='main'):
    if repository != expected_repository or event != 'push' or branch != expected_branch or not re.fullmatch(r'[0-9a-f]{40}', source_sha or ''):
        raise ValueError('untrusted or malformed upstream event; publication denied')


def existing_release_decision(version, current_sha, tag_sha, release, assets, api_error=None):
    if api_error:
        raise ValueError('release API query failed: ' + str(api_error))
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
        schemas = {'0.1.0-alpha.3': 6, '0.1.0-alpha.4': 7, '0.1.0-alpha.5': 8}
        if version not in schemas:
            raise ValueError('unknown published release contract')
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


def github_release_decision(repository, version, current_sha, token):
    """Query tag/release and all bytes; 404 is the only absent-state signal."""
    if not re.fullmatch(r'[0-9a-f]{40}', current_sha):
        raise ValueError('current source revision is not a full commit SHA')
    base = 'https://api.github.com/repos/' + repository

    def get(url, allow_missing=False):
        request = urllib.request.Request(url, headers={'Accept': 'application/vnd.github+json',
            'Authorization': 'Bearer ' + token, 'X-GitHub-Api-Version': '2022-11-28'})
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                return json.load(response)
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
        return 'publish'
    if ref is None:
        raise ValueError('release exists without its Git tag')
    obj = ref['object']
    if obj.get('type') == 'tag':
        obj = get(f"{base}/git/tags/{obj['sha']}")['object']
    tag_sha = obj.get('sha') if obj.get('type') == 'commit' else None
    if release is None:
        raise ValueError('tag exists without a release; refusing publication')
    assets = []
    for asset in release.get('assets', []):
        request = urllib.request.Request(asset['browser_download_url'], headers={'Accept': 'application/octet-stream'})
        try:
            with urllib.request.urlopen(request, timeout=60) as response:
                data = response.read()
        except (OSError, urllib.error.HTTPError) as error:
            raise ValueError(f"could not download existing release asset {asset.get('name')}: {error}") from error
        assets.append({'name': asset['name'], 'data': data})
    checksum = next((asset['data'].decode('utf-8') for asset in assets if asset['name'] == 'SHA256SUMS.txt'), None)
    manifest = None
    manifest_asset = next((asset['data'] for asset in assets if asset['name'] == 'release-manifest.json'), None)
    if manifest_asset:
        try:
            manifest = json.loads(manifest_asset)
        except (ValueError, UnicodeDecodeError) as error:
            raise ValueError('existing release manifest is invalid') from error
    release_data = {'tag_sha': tag_sha, 'manifest': manifest}
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
    return decision


if __name__ == '__main__':
    import argparse
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repository', required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--current-sha', required=True)
    args = parser.parse_args()
    try:
        print(github_release_decision(args.repository, args.version, args.current_sha, os.environ['GH_TOKEN']))
    except (ValueError, KeyError) as error:
        parser.error(str(error))
