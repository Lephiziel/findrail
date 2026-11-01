#!/usr/bin/env python3
"""Create an offline manual acceptance kit from an already verified release set."""
import argparse
import hashlib
import json
import os
import pathlib
import sys
import tempfile
import zipfile

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import acceptance_fixture
import release_assets

RELEASE_VERSION = '0.1.0-alpha.5'
RELEASE_SHA = 'cb53ee9638f69b47b386921e41368f3b66ceddde'
CANDIDATE_VERSION = '0.1.0-beta.1'
PUBLISHED_MANIFEST_SHA256 = '28ed088d474117f9ce0614626b7d62b47ed27e6646e5670d52affeeeee9031b7'
PUBLISHED_CHECKSUMS_SHA256 = 'f587ffc579c133f6f5f204b5bddd88fbc174aaa9341430fb701832dd5d781423'
SCHEMA = 8
API = 1
TARGETS = ('windows-amd64', 'darwin-arm64', 'linux-amd64')
CASES = (
    ('install', 'Install the downloaded original archive using its included instructions.'),
    ('launch', 'Launch Findrail and open its local interface.'),
    ('repeat-launch', 'Quit, then launch Findrail a second time.'),
    ('retrieval', 'Index this kit’s fixture folder and search the words in EXPECTED_RESULTS.md.'),
    ('collection-restart', 'Save the named collection and restart Findrail using the same isolated data directory.'),
    ('refresh', 'Edit the fixture marker, refresh the source, and search for the new marker.'),
    ('quit', 'Use Findrail’s Quit action and confirm the app and local server stop.'),
    ('uninstall-data-preserved', 'Use only the included per-user uninstaller on this test installation/menu entry; confirm the isolated test data and original fixture files remain untouched.'),
)


def sha256(path):
    digest = hashlib.sha256()
    with pathlib.Path(path).open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(block)
    return digest.hexdigest()


def safe_json(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + '\n'


def target_start_here(base, target, archive_name):
    intro = base.split('\nOn Windows PowerShell:\n', 1)[0]
    if target == 'linux-amd64':
        verify_command = f'''```sh\nsha256sum {archive_name}\n```'''
        platform_instructions = f'''### Linux amd64\n\nDownload the original archive and matching `SHA256SUMS.txt`. Compare all 64 SHA-256 characters with the value above and stop if they differ. Extract with the file manager or `tar -xzf {archive_name}`, enter the extracted folder, and read its actual `INSTALL.txt`.\n\nFor an isolated per-user menu-entry test, use the existing installer with a disposable destination so an existing entry cannot be replaced:\n\n```sh\n./launcher/linux/install-user.sh --destination "$HOME/Findrail acceptance test applications"\n./launcher/linux/uninstall-user.sh --destination "$HOME/Findrail acceptance test applications"\n```\n\nThese scripts only add/remove that test menu entry. For the desktop test, create a separate data directory and launch from the extracted folder:\n\n```sh\nTEST_DATA="$HOME/Findrail acceptance test data"\nmkdir -p "$TEST_DATA"\n./findrail-desktop --data-dir "$TEST_DATA"\n```\n\nIn **Sources → Add folder**, choose this kit’s extracted `fixture/` directory. Use the exact same `TEST_DATA` after **Quit Findrail** when checking the saved collection. Do not substitute `findrail demo`: it uses a temporary read-only index and does not preserve collections across restart.\n'''
    elif target == 'windows-amd64':
        verify_command = f'''```powershell\n(Get-FileHash -LiteralPath .\\{archive_name} -Algorithm SHA256).Hash.ToLower()\n```'''
        platform_instructions = f'''### Windows amd64 — VM procedure\n\nUse a Windows guest VM. Open a browser **inside the guest** and download the original Windows archive and matching `SHA256SUMS.txt` there. Do not use a Linux-host server in place of the Windows executable. If you must copy the archive from the host, record that delivery method; it does not test browser download marking. Compare all 64 SHA-256 characters before continuing. Extract the ZIP with Explorer, enter the extracted folder, and read its actual `INSTALL.txt`.\n\nFor a separate test data directory, launch the archive’s Windows desktop executable in PowerShell:\n\n```powershell\n$testData = Join-Path $env:LOCALAPPDATA 'Findrail acceptance test'\n.\\findrail-desktop.exe --data-dir $testData\n```\n\nFor an isolated per-user shortcut test, use a disposable destination and remove only that test shortcut afterward:\n\n```powershell\n$testMenu = Join-Path $env:TEMP 'Findrail acceptance test menu'\npowershell -File .\\launcher\\windows\\install-user.ps1 -DestinationDirectory $testMenu\npowershell -File .\\launcher\\windows\\uninstall-user.ps1 -DestinationDirectory $testMenu\n```\n\nDo not bypass local execution policy or disable SmartScreen or Defender to make a case pass. If local policy blocks a step, record that result. In `REPORT.md`, record the guest Windows version/build, that this was a VM and its hypervisor, browser and archive source, actual SmartScreen/Defender outcome, and whether delivery was downloaded in-guest or copied from the host. These observations apply only to this guest and these bytes.\n'''
    else:
        verify_command = f'''```sh\nshasum -a 256 {archive_name}\n```'''
        platform_instructions = '''### Apple Silicon\n\nDownload the original archive, verify the full digest, extract with Finder, and read its actual `INSTALL.txt`. Record the real Gatekeeper/quarantine result; do not remove quarantine or lower global protections. For the per-user app test, use the existing `launcher/macos/install-user.sh` with a disposable `--destination` and remove only that test app with the matching `uninstall-user.sh`.\n\nFor the desktop test, create a separate data directory and run `./findrail-desktop --data-dir "$HOME/Findrail acceptance test data"` from the extracted folder. Add only this kit’s `fixture/` directory and use the same data directory after **Quit Findrail**.\n'''
    if target == 'linux-amd64':
        platform_instructions += '''\nFor a separate foreground CLI check, the archive’s `INSTALL.txt` also documents `./findrail start --data-dir "$TEST_DATA" --no-open`. This does not substitute for the desktop-launch cases above.'''
    return (f'''{intro}\n\n{verify_command}\n\nCompare all 64 hexadecimal characters with the expected SHA-256 above. Stop if it differs. Do not disable SmartScreen, Defender, Gatekeeper, quarantine, or other protections globally. Record only the real prompt/result for this archive and environment.\n\n## Install and test\n\n{platform_instructions}\n\nRead `EXPECTED_RESULTS.md`, then complete all eight ordered cases in `REPORT.md`. No outcome is pre-passed. Use only the three synthetic files in `fixture/`; never point this procedure at your normal index. Follow the extracted archive’s `INSTALL.txt` and included per-user installer. Removing the test menu entry does not remove the extracted app, original fixture files, or test data.\n\nClosing a browser tab only hides the interface; **Quit Findrail** stops the desktop server. Use Quit before restart, backup, or uninstall. The app sends nothing automatically.\n\nOptional demo: `findrail demo` is separate and temporary/read-only; it does not preserve collections across restart. Go, Python, Docker, tokens, and an app account are not required.\n''')


def _validate_paths(assets, output):
    assets = assets.resolve(strict=True)
    output = pathlib.Path(os.path.abspath(output))
    for component in (*reversed(output.parents), output):
        if component.is_symlink():
            raise ValueError('output path must not contain symlinks')
    output = output.resolve(strict=False)
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
        raise ValueError('target must be windows-amd64, darwin-arm64, or linux-amd64')
    try:
        input_manifest = json.loads((assets_dir / 'release-manifest.json').read_text(encoding='utf-8'))
    except (OSError, json.JSONDecodeError) as error:
        raise ValueError('cannot read verified release manifest') from error
    source_sha = input_manifest.get('source_revision')
    version = input_manifest.get('app_version')
    if version not in (RELEASE_VERSION, CANDIDATE_VERSION):
        raise ValueError('acceptance kits support only published alpha.5 or preparation beta.1 assets')
    manifest = release_assets.verify(assets_dir, version, source_sha)
    expected_schema = release_assets.contract_schema(version)
    if manifest.get('schema_version') != expected_schema or manifest.get('client_api_version') != API:
        raise ValueError('verified release contract has unexpected schema or Client API')
    archive_entry = next((entry for entry in manifest['assets']
                          if entry.get('target') == target), None)
    archive_name = archive_entry.get('basename') if archive_entry else None
    expected_name = f'findrail_{version}_{target}' + ('.zip' if target.startswith('windows-') else '.tar.gz')
    if archive_name != expected_name:
        raise ValueError('verified manifest has no exact target archive')
    archive = assets_dir / archive_name
    archive_size = archive.stat().st_size
    archive_sha256 = sha256(archive)
    if archive_entry.get('size') != archive_size or archive_entry.get('sha256') != archive_sha256:
        raise ValueError('verified manifest archive size or SHA-256 does not match the asset')
    published_bytes = (
        manifest['source_revision'] == RELEASE_SHA and
        version == RELEASE_VERSION and manifest['app_version'] == RELEASE_VERSION and
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
        'archive': {'basename': archive_name, 'size_bytes': archive_entry['size'], 'sha256': archive_entry['sha256']},
        'manifest_sha256': sha256(assets_dir / 'release-manifest.json'),
        'checksums_sha256': sha256(assets_dir / 'SHA256SUMS.txt'),
        'fixture_sha256': fixture_hash.hexdigest(), 'delivery_method': delivery_method,
        'release_url': release_url,
    }
    report = {
        'kind': {'windows-amd64': 'windows_first_run', 'darwin-arm64': 'apple_silicon_first_run',
                 'linux-amd64': 'linux_first_run'}[target],
        'evidence_type': 'manual', 'status': 'not_run', 'candidate_sha': metadata['candidate_sha'],
        'candidate_version': metadata['candidate_version'], 'target': target,
        'archive_sha256': metadata['archive']['sha256'], 'reference': '', 'observer': '', 'date': '',
        'schema_version': metadata['schema_version'], 'client_api_version': metadata['client_api_version'],
        'manifest_sha256': metadata['manifest_sha256'], 'checksums_sha256': metadata['checksums_sha256'],
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
    start_here = target_start_here(start_here, target, archive_name)
    for case_id, instruction in CASES:
        lines.extend([f'### `{case_id}`', '', instruction, '', '- Outcome: `not_run` (choose only `passed`, `failed`, or `not_run`)', '- Elapsed seconds: unknown', '- Observation:', ''])
    lines.extend(['## Optional original-file actions', '', 'Record separately; do not count these as one of the eight cases.', '', '- Action attempted (open original / reveal folder):', '- Viewer / file manager available:', '- Outcome / obstacle:', '', '## Safe handoff', '', 'Keep this report local unless you choose to share it. Do not send documents, queries, private paths, indexes, credentials, raw logs, or unredacted screenshots. A sanitized outcome and anonymized blocker are enough.'])
    report_md = '\n'.join(lines) + '\n'
    if target == 'windows-amd64':
        vm_details = '''## Windows VM details\n\n- Guest Windows edition/version/build:\n- VM: yes/no; hypervisor:\n- Browser used inside guest:\n- Original archive source:\n- Delivery: downloaded in guest / copied from host (if copied, browser download marking was not tested):\n- Actual SmartScreen result for these bytes:\n- Actual Defender result for these bytes:\n\nDo not disable protections to make a case pass. Report only this guest and this archive.'''
        report_md = report_md.replace('## Eight manual cases\n', vm_details + '\n\n## Eight manual cases\n', 1)
    usage_template = {
        'kind': 'voluntary_usage', 'evidence_type': 'manual',
        'candidate_sha': metadata['candidate_sha'], 'candidate_version': metadata['candidate_version'],
        'reference': '', 'status': 'incomplete', 'return_use': False,
        'retrieval_one': 'not_tried', 'retrieval_two': 'not_tried',
        'collection_restart': 'not_tried',
    }
    files = {
        'START_HERE.md': start_here, 'EXPECTED_RESULTS.md': expected,
        'candidate.json': safe_json(metadata), 'REPORT.md': report_md,
        'report.json': safe_json(report), 'VOLUNTARY_USAGE_TEMPLATE.json': safe_json(usage_template), **fixture,
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
        try:
            os.link(temp_path, output)
        except FileExistsError as error:
            raise ValueError('refusing to overwrite an existing output') from error
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
