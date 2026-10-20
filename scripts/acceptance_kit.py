#!/usr/bin/env python3
"""Create an offline manual acceptance kit from an already verified release set."""
import argparse
import hashlib
import json
import pathlib
import sys
import tempfile
import zipfile

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import acceptance_fixture
import release_assets

RELEASE_VERSION = '0.1.0-alpha.5'
RELEASE_SHA = 'cb53ee9638f69b47b386921e41368f3b66ceddde'
PUBLISHED_MANIFEST_SHA256 = '28ed088d474117f9ce0614626b7d62b47ed27e6646e5670d52affeeeee9031b7'
PUBLISHED_CHECKSUMS_SHA256 = 'f587ffc579c133f6f5f204b5bddd88fbc174aaa9341430fb701832dd5d781423'
SCHEMA = 8
API = 1
TARGETS = ('windows-amd64', 'darwin-arm64')
CASES = (
    ('install', 'Install the downloaded original archive using its included instructions.'),
    ('launch', 'Launch Findrail and open its local interface.'),
    ('repeat-launch', 'Quit, then launch Findrail a second time.'),
    ('retrieval', 'Index this kit’s fixture folder and search the words in EXPECTED_RESULTS.md.'),
    ('collection-restart', 'Save the named collection and restart Findrail using the same isolated data directory.'),
    ('refresh', 'Edit the fixture marker, refresh the source, and search for the new marker.'),
    ('quit', 'Use Findrail’s Quit action and confirm the app and local server stop.'),
    ('uninstall-data-preserved', 'Using only this isolated test installation/data, uninstall the application and confirm the test data and original fixture files remain.'),
)


def sha256(path):
    digest = hashlib.sha256()
    with pathlib.Path(path).open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(block)
    return digest.hexdigest()


def safe_json(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + '\n'


def _validate_paths(assets, output):
    assets = assets.resolve(strict=True)
    output = output.resolve()
    if assets == output or assets in output.parents or output in assets.parents:
        raise ValueError('output and assets directory must be separate, non-nested paths')
    if output.exists() and not output.is_file():
        raise ValueError('output must be a file path')
    if output.exists() and output.is_symlink():
        raise ValueError('output must not be a symlink')
    if output.exists():
        raise ValueError('refusing to overwrite an existing output')
    return assets, output


def build(assets_dir, target, output, delivery_method=None):
    assets_dir, output = _validate_paths(pathlib.Path(assets_dir), pathlib.Path(output))
    if target not in TARGETS:
        raise ValueError('target must be windows-amd64 or darwin-arm64')
    try:
        input_manifest = json.loads((assets_dir / 'release-manifest.json').read_text(encoding='utf-8'))
    except (OSError, json.JSONDecodeError) as error:
        raise ValueError('cannot read verified release manifest') from error
    source_sha = input_manifest.get('source_revision')
    manifest = release_assets.verify(assets_dir, RELEASE_VERSION, source_sha)
    if manifest.get('schema_version') != SCHEMA or manifest.get('client_api_version') != API:
        raise ValueError('verified release contract has unexpected schema or Client API')
    archive_name = next((entry['basename'] for entry in manifest['assets']
                         if entry.get('target') == target), None)
    expected_name = f'findrail_{RELEASE_VERSION}_{target}' + ('.zip' if target.startswith('windows-') else '.tar.gz')
    if archive_name != expected_name:
        raise ValueError('verified manifest has no exact target archive')
    archive = assets_dir / archive_name
    published_bytes = (
        manifest['source_revision'] == RELEASE_SHA and
        manifest['app_version'] == RELEASE_VERSION and
        sha256(assets_dir / 'release-manifest.json') == PUBLISHED_MANIFEST_SHA256 and
        sha256(assets_dir / 'SHA256SUMS.txt') == PUBLISHED_CHECKSUMS_SHA256
    )
    if delivery_method is None:
        if published_bytes:
            delivery_method = 'GitHub release browser download'
        else:
            raise ValueError('candidate kits require an explicit --delivery-method')
    if not isinstance(delivery_method, str) or not delivery_method.strip():
        raise ValueError('delivery method must be explicit')
    fixture = acceptance_fixture.files()
    fixture_hash = hashlib.sha256()
    for name, content in sorted(fixture.items()):
        fixture_hash.update(name.encode('utf-8') + b'\0')
        fixture_hash.update(content if isinstance(content, bytes) else content.encode('utf-8'))
        fixture_hash.update(b'\0')
    kind = 'published_release' if published_bytes else 'source_candidate'
    release_url = (f'https://github.com/Lephiziel/findrail/releases/download/v{RELEASE_VERSION}/'
                   if published_bytes else None)
    metadata = {
        'kind': kind, 'candidate_sha': manifest['source_revision'],
        'candidate_version': manifest['app_version'], 'schema_version': manifest['schema_version'],
        'client_api_version': manifest['client_api_version'], 'target': target,
        'archive': {'basename': archive_name, 'size_bytes': archive.stat().st_size, 'sha256': sha256(archive)},
        'manifest_sha256': sha256(assets_dir / 'release-manifest.json'),
        'checksums_sha256': sha256(assets_dir / 'SHA256SUMS.txt'),
        'fixture_sha256': fixture_hash.hexdigest(), 'delivery_method': delivery_method,
        'release_url': release_url,
    }
    report = {
        'kind': 'windows_first_run' if target.startswith('windows') else 'apple_silicon_first_run',
        'evidence_type': 'manual', 'status': 'not_run', 'candidate_sha': metadata['candidate_sha'],
        'candidate_version': metadata['candidate_version'], 'target': target,
        'archive_sha256': metadata['archive']['sha256'], 'reference': '', 'observer': '', 'date': '',
        'notes': '', 'cases': [{'id': case_id, 'status': 'not_run', 'elapsed_seconds': None,
                                'observation': ''} for case_id, _ in CASES],
    }
    start = f'''# Findrail manual acceptance — {target}\n\nThis kit is a guide and synthetic fixture, not an installer or official application package.\n\n## Before you begin\n\n1. Follow `START_HERE.md` and download the **original archive** from its stated source in your browser. Do not install the copy inside this kit.\n2. Verify the downloaded archive against the displayed SHA-256 using the platform command shown below. Keep it in a folder you own.\n3. Extract it using Explorer / Finder and read the archive’s `INSTALL.txt`. Use only a separate test data directory. Do not point this procedure at your normal index.\n4. Complete all eight cases in `REPORT.md`. No case is pre-passed. Missing platform/manual observations remain pending.\n\nOptional: run `findrail-readonly-0.1.0.vsix` only if separately testing the VS Code extension; it is not part of these eight cases.\n'''
    if release_url:
        download = f'''Official browser download: {release_url}{archive_name}\nRelease page: https://github.com/Lephiziel/findrail/releases/tag/v0.1.0-alpha.5'''
    else:
        download = f'''Candidate delivery: {delivery_method}\nNo published release URL is claimed. Obtain the candidate archive and checksum/manifest bundle from that delivery channel.'''
    start_here = f'''# START HERE — {target}\n\n## What this is\n\nA manual, offline-friendly acceptance kit for Findrail `{metadata['candidate_version']}` on `{target}`. It contains fictional documents and a blank report. It does not include or rebuild the application.\n\n## Download and verify the original application\n\n{download}\n\nExpected archive: `{archive_name}`  \nExpected SHA-256: `{metadata['archive']['sha256']}`\n\nDownload the original asset in your browser. This matters: repackaging can change Windows download marking and macOS quarantine/Gatekeeper behavior.\n\nOn Windows PowerShell:\n\n```powershell\n(Get-FileHash .\\{archive_name} -Algorithm SHA256).Hash.ToLower()\n```\n\nOn macOS Terminal:\n\n```sh\nshasum -a 256 {archive_name}\n```\n\nCompare all 64 hexadecimal characters with the expected SHA-256 above. Stop if it differs. Do not disable SmartScreen, Gatekeeper, quarantine, or other protections globally. If the OS blocks or warns, record the exact observed result and follow only the application-specific guidance in the official installation instructions and your organization’s policy.\n\n## Install and test\n\n- **Windows:** use Explorer to extract the downloaded ZIP, then follow that extracted folder’s `INSTALL.txt` and normal per-user installer flow. Record SmartScreen / Defender behavior; do not treat command-line or CI installation as Explorer experience.\n- **Apple Silicon:** use Finder to extract the downloaded archive and follow `INSTALL.txt`. Record whether the download carried quarantine metadata and the actual Gatekeeper prompt/allowance outcome. Do not remove quarantine or lower global security settings to force a pass.\n- Read `EXPECTED_RESULTS.md`, then complete the eight ordered cases in `REPORT.md`. Use the three files in `fixture/`; do not select personal folders. For uninstall, use only the isolated test installation and test data.\n- Native opening of originals/folder reveal is optional and separate. If attempted, name the viewer; no viewer means not run, not passed.\n\nOptional own-documents mode: use only if you independently choose to. Do not send document contents, private paths, search terms, indexes, logs, or screenshots. A coarse found/not-found outcome and an anonymized obstacle are sufficient; keep private material local.\n\n## Preserve and optionally share your report\n\nSave a private copy of `REPORT.md` and `report.json` with your observations. Share only if you choose, through a channel agreed with the project. Remove personal paths, document text, queries, account names and other identifying information first. There is no automatic upload, email, telemetry, API token, or credential collection in this kit.\n'''
    expected = '''# Expected synthetic results\n\nUse literal searches in the app’s search box after adding this kit’s `fixture/` directory as a source. Search behavior is demonstrated, not ranked or evaluated.\n\n| Search word | Expected document | Expected detail |\n|---|---|---|\n| `thimble` | `Juniper-Notes.md` | Fictional amber thimble note. |\n| `observatory` | `Juniper-Notes.md` | Juniper Observatory. |\n| `juniper` | `Élodie-Parcel.docx` | Fictional parcel in the DOCX text. |\n| `kestrel` | `Copper-Lantern.pdf` | **Page 2 of 2**; preview/location should identify page 2. |\n| `violet` | `Copper-Lantern.pdf` | **Page 2 of 2**. |\n\nFor the saved-collection path, search `thimble`, preview `Juniper-Notes.md`, add the matching passage to a collection named `Acceptance notebook`, add the note `synthetic restart check`, save, choose **Quit Findrail**, and relaunch with the exact same isolated data directory. Reopen the collection and confirm its citation and note are present.\n\nFor refresh, edit `fixture/Juniper-Notes.md`: replace `amber` with the unique word `tealmarker`. Use the app’s **Refresh** action (or wait for automatic refresh), then search `tealmarker`. The Markdown document should appear. Restore the original fixture after the test or discard only the copied kit fixture.\n\nThe DOCX is a passive, minimal document. Opening Office is not needed: search and preview use extracted text. A standard PDF viewer is also not needed for indexed preview.\n'''
    lines = ['# Manual acceptance report', '', 'Fill outcomes from what you actually observe. Initially every outcome is `not_run`; leave elapsed time blank/unknown if you did not measure it. Do not infer native GUI, quarantine, or security behavior from CI.', '', '## Candidate identity', '', f"- Kind: `{kind}`", f"- Source SHA: `{metadata['candidate_sha']}`", f"- Version / schema / Client API: `{metadata['candidate_version']}` / `{metadata['schema_version']}` / `{metadata['client_api_version']}`", f"- Target: `{target}`", f"- Archive: `{archive_name}` ({metadata['archive']['size_bytes']} bytes)", f"- Archive SHA-256: `{metadata['archive']['sha256']}`", f"- Delivery: {delivery_method}", f"- Manifest SHA-256: `{metadata['manifest_sha256']}`", f"- SHA256SUMS SHA-256: `{metadata['checksums_sha256']}`", '', '## Observer and environment', '', '- Observer (optional):', '- Date/time and timezone:', '- OS version/build:', '- Browser used to download:', '- CPU / architecture:', '- Download source and extraction method:', '- Actual SmartScreen / Defender / quarantine / Gatekeeper outcome:', '- Brief sanitized notes:', '', '## Eight manual cases', '']
    for case_id, instruction in CASES:
        lines.extend([f'### `{case_id}`', '', instruction, '', '- Outcome: `not_run` (choose only `passed`, `failed`, or `not_run`)', '- Elapsed seconds: unknown', '- Observation:', ''])
    lines.extend(['## Optional original-file actions', '', 'Record separately; do not count these as one of the eight cases.', '', '- Action attempted (open original / reveal folder):', '- Viewer / file manager available:', '- Outcome / obstacle:', '', '## Safe handoff', '', 'Keep this report local unless you choose to share it. Do not send documents, queries, private paths, indexes, credentials, raw logs, or unredacted screenshots. A sanitized outcome and anonymized blocker are enough.'])
    report_md = '\n'.join(lines) + '\n'
    files = {
        'START_HERE.md': start_here, 'EXPECTED_RESULTS.md': expected,
        'candidate.json': safe_json(metadata), 'REPORT.md': report_md,
        'report.json': safe_json(report), **fixture,
    }
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(prefix='.acceptance-kit-', suffix='.zip', dir=output.parent, delete=False) as temporary:
        temp_path = pathlib.Path(temporary.name)
    try:
        with zipfile.ZipFile(temp_path, 'w', zipfile.ZIP_DEFLATED, compresslevel=9) as kit:
            for name, content in sorted(files.items()):
                info = zipfile.ZipInfo(name, (2020, 1, 1, 0, 0, 0))
                info.external_attr = 0o100644 << 16
                info.compress_type = zipfile.ZIP_DEFLATED
                kit.writestr(info, content if isinstance(content, bytes) else content.encode('utf-8'))
        # Validate the finished layout before atomically publishing it.
        with zipfile.ZipFile(temp_path) as kit:
            if kit.testzip() is not None or set(kit.namelist()) != set(files):
                raise ValueError('generated kit ZIP failed layout/integrity validation')
            if any(info.is_dir() or info.external_attr >> 16 & 0o170000 != 0o100000 for info in kit.infolist()):
                raise ValueError('generated kit contains a non-regular member')
        temp_path.replace(output)
    finally:
        temp_path.unlink(missing_ok=True)
    return metadata


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--assets-dir', required=True, type=pathlib.Path)
    parser.add_argument('--target', required=True, choices=TARGETS)
    parser.add_argument('--output', required=True, type=pathlib.Path)
    parser.add_argument('--delivery-method', help='required for source candidates; e.g. Actions artifact')
    args = parser.parse_args()
    try:
        print(safe_json(build(args.assets_dir, args.target, args.output, args.delivery_method)), end='')
    except (OSError, ValueError, KeyError, json.JSONDecodeError, zipfile.BadZipFile) as error:
        parser.error(str(error))


if __name__ == '__main__':
    main()
