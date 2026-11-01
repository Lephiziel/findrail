#!/usr/bin/env python3
"""Assemble and verify the exact, unsigned Findrail release asset set."""
import argparse
import hashlib
import json
import pathlib
import re
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import package

TARGETS = ('linux-amd64', 'linux-arm64', 'darwin-amd64', 'darwin-arm64', 'windows-amd64')
VSIX = 'findrail-readonly-0.1.0.vsix'
ALPHA3_REVISION = 'd5dc1bec50efcb3c26d09e9b24b14cf3c0b2f4cb'


def contract_schema(version):
    # Published contracts are immutable, even after source migrations advance.
    # A new schema needs a new release/candidate contract.
    if version == '0.1.0-alpha.3':
        return 6
    if version == '0.1.0-alpha.4':
        return 7
    if version == '0.1.0-alpha.5':
        # Explicit release contract; later source migrations do not rewrite it.
        return 8
    if version == '0.1.0-beta.1':
        # Preparation-only same-schema candidate; deliberately not a range.
        return 8
    raise ValueError('unsupported release/candidate version contract')


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def expected_archives(version):
    contract_schema(version)
    if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?', version):
        raise ValueError('invalid application version')
    return {f'findrail_{version}_{target}' + ('.zip' if target.startswith('windows-') else '.tar.gz'): target
            for target in TARGETS}


def verify(directory, version, revision, extension_version='0.1.0'):
    if not re.fullmatch(r'[0-9a-f]{40}', revision):
        raise ValueError('candidate revision must be an exact 40-character commit SHA')
    root = pathlib.Path(directory)
    expected = expected_archives(version)
    expected[VSIX] = None
    expected['release-manifest.json'] = None
    expected['SHA256SUMS.txt'] = None
    entries = list(root.iterdir())
    if any(not p.is_file() or p.is_symlink() for p in entries):
        raise ValueError('release staging may contain regular files only')
    actual = {p.name for p in entries}
    if actual != set(expected):
        raise ValueError(f'asset set mismatch: missing={sorted(set(expected)-actual)}, unknown={sorted(actual-set(expected))}')
    for name in actual:
        if not package.safe_basename(name):
            raise ValueError('unsafe asset basename: ' + name)
    manifest_path = root / 'release-manifest.json'
    manifest = json.loads(manifest_path.read_text(encoding='utf-8'))
    if manifest != build_manifest(root, version, revision, extension_version):
        raise ValueError('manifest does not match assets, version, or source revision')
    sum_names = []
    for line in (root / 'SHA256SUMS.txt').read_text(encoding='utf-8').splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})  ([^\s]+)', line)
        if not match or match[2] in sum_names or not package.safe_basename(match[2]):
            raise ValueError('invalid SHA256SUMS entry')
        name = match[2]
        if name == 'SHA256SUMS.txt' or name not in expected or digest(root / name) != match[1]:
            raise ValueError('checksum mismatch or unexpected checksum entry: ' + name)
        sum_names.append(name)
    if set(sum_names) != set(expected) - {'SHA256SUMS.txt'}:
        raise ValueError('checksum list is incomplete')
    for name, target in expected_archives(version).items():
        package.verify_archive(root / name, target, version, manifest['schema_version'])
        with __import__('tempfile').TemporaryDirectory(prefix='findrail-release-verify-') as temp:
            extracted = package.extract_verified(root / name, pathlib.Path(temp) / 'unpacked')
            info = json.loads((extracted / 'BUILD-INFO.json').read_text(encoding='utf-8'))
            if (info.get('revision') != revision or info.get('dirty') is not False or
                    info.get('version') != version or info.get('target') != target or
                    package.build_info_schema(info) != manifest['schema_version']):
                raise ValueError('archive has unknown, dirty, or wrong-revision provenance: ' + name)
    import zipfile
    with zipfile.ZipFile(root / VSIX) as zf:
        names = zf.namelist()
        if len(names) > 200 or sum(item.file_size for item in zf.infolist()) > 100 * 1024 * 1024:
            raise ValueError('VSIX exceeds the bounded member/size policy')
        if len(names) != len(set(names)) or any(not n or pathlib.PurePosixPath(n).is_absolute() or
                pathlib.PurePosixPath(n).as_posix() != n or any(part in ('', '.', '..') for part in n.split('/')) or
                not package.safe_basename(n.rsplit('/', 1)[-1]) or '..' in pathlib.PurePosixPath(n).parts or
                '\\' in n or ':' in n or (n not in ('[Content_Types].xml', 'extension.vsixmanifest') and not n.startswith('extension/')) for n in names):
            raise ValueError('unsafe or duplicate VSIX member')
        for item in zf.infolist():
            mode = item.external_attr >> 16
            if package.stat.S_IFMT(mode) not in (0, package.stat.S_IFREG, package.stat.S_IFDIR):
                raise ValueError('VSIX contains a non-regular member')
        manifests = [n for n in names if n.endswith('/package.json')]
        if len(manifests) != 1:
            raise ValueError('VSIX does not contain extension package.json')
        if zf.getinfo(manifests[0]).file_size > 1024 * 1024:
            raise ValueError('VSIX package metadata exceeds the size limit')
        extension = json.loads(zf.read(manifests[0]))
        if extension.get('version') != extension_version:
            raise ValueError('VSIX extension version does not match its package contract')
        if (extension.get('name') != 'findrail-readonly' or extension.get('publisher') != 'findrail-local' or
                extension.get('main') != './dist/extension.js' or extension.get('extensionKind') != ['ui']):
            raise ValueError('VSIX identity or entry point does not match the Findrail client contract')
        if manifests[0] != 'extension/package.json':
            raise ValueError('VSIX extension manifest is at an unexpected path')
        required = {'[Content_Types].xml', 'extension.vsixmanifest', 'extension/package.json',
                    'extension/LICENSE.txt', 'extension/readme.md', 'extension/changelog.md',
                    'extension/dist/extension.js'}
        if not required.issubset(names) or any('/node_modules/' in n or '/test/' in n or n.endswith('.map') for n in names):
            raise ValueError('VSIX composition is incomplete or contains development files')
    return manifest


def build_manifest(root, version, revision, extension_version='0.1.0'):
    schema = contract_schema(version)
    entries = []
    for name, target in sorted({**expected_archives(version), VSIX: None}.items()):
        path = pathlib.Path(root) / name
        if not path.is_file():
            raise ValueError('missing asset: ' + name)
        entries.append({'basename': name, 'kind': 'archive' if target else 'vsix',
                        **({'target': target} if target else {}), 'size': path.stat().st_size, 'sha256': digest(path)})
    return {'manifest_version': 1, 'app_version': version, 'source_revision': revision,
            'schema_version': schema, 'client_api_version': 1, 'extension_version': extension_version,
            'assets': entries}


def assemble(directory, output, version, revision, extension_version='0.1.0'):
    source, destination = pathlib.Path(directory), pathlib.Path(output)
    destination.mkdir(parents=True, exist_ok=True)
    expected = {**expected_archives(version), VSIX: None}
    source_entries = list(source.iterdir())
    if any(not p.is_file() or p.is_symlink() for p in source_entries) or {p.name for p in source_entries} != set(expected):
        raise ValueError('input staging must contain exactly five archives and the contracted VSIX')
    for name in expected:
        (destination / name).write_bytes((source / name).read_bytes())
    manifest = build_manifest(destination, version, revision, extension_version)
    (destination / 'release-manifest.json').write_text(json.dumps(manifest, sort_keys=True, indent=2) + '\n', encoding='utf-8')
    checksum_names = sorted(set(expected) | {'release-manifest.json'})
    (destination / 'SHA256SUMS.txt').write_text(''.join(f'{digest(destination / name)}  {name}\n' for name in checksum_names), encoding='utf-8')
    verify(destination, version, revision, extension_version)
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    build = sub.add_parser('assemble')
    build.add_argument('--input', required=True); build.add_argument('--output', required=True)
    build.add_argument('--version', required=True); build.add_argument('--revision', required=True)
    check = sub.add_parser('verify')
    check.add_argument('--directory', required=True); check.add_argument('--version', required=True)
    check.add_argument('--revision', required=True)
    args = parser.parse_args()
    try:
        result = assemble(args.input, args.output, args.version, args.revision) if args.command == 'assemble' else verify(args.directory, args.version, args.revision)
        print(json.dumps(result, sort_keys=True, indent=2))
    except (OSError, ValueError, KeyError) as error:
        parser.error(str(error))


if __name__ == '__main__':
    main()
