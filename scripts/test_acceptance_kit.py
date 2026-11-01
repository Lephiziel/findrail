import hashlib
import io
import json
import pathlib
import stat
import sys
import tarfile
import tempfile
import unittest
import zipfile
from unittest import mock

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import acceptance_fixture
import acceptance_kit
import package
import release_assets

PINNED_ALPHA5_MANIFEST_SHA256 = '28ed088d474117f9ce0614626b7d62b47ed27e6646e5670d52affeeeee9031b7'
PINNED_ALPHA5_CHECKSUMS_SHA256 = 'f587ffc579c133f6f5f204b5bddd88fbc174aaa9341430fb701832dd5d781423'


def verified_synthetic_bundle(root, version='0.1.0-beta.1', revision='2' * 40):
    """Create a fully valid eight-file bundle with synthetic native packages."""
    root = pathlib.Path(root)
    root.mkdir(parents=True, exist_ok=True)
    for target in release_assets.TARGETS:
        os_name = target.split('-', 1)[0]
        stem = f'findrail_{version}_{target}'
        binary = 'findrail.exe' if os_name == 'windows' else 'findrail'
        launcher = 'findrail-desktop.exe' if os_name == 'windows' else 'findrail-desktop'
        build_info = {'version': version, 'revision': revision, 'dirty': False, 'target': target,
                      'schema_version': 8, 'go_version': 'go synthetic', 'source_date_epoch': 1700000000}
        entries = {
            'INSTALL.txt': b'Synthetic package instructions.\n', 'LICENSE': b'Synthetic license.\n',
            'NOTICE': b'Synthetic notice.\n', 'BUILD-INFO.json': (json.dumps(build_info, sort_keys=True, indent=2) + '\n').encode(),
            binary: b'synthetic native binary', launcher: b'synthetic desktop launcher',
            'licenses/DEPENDENCIES.txt': b'', 'licenses/Go-LICENSE.txt': b'Synthetic Go license.\n',
        }
        for source, destination in package.RESOURCE_FILES[os_name].items():
            entries[destination] = b'Synthetic packaged resource.\n'
        if os_name == 'darwin':
            entries['Findrail.app/Contents/MacOS/Findrail'] = b'synthetic app launcher'
        archive = root / (stem + ('.zip' if os_name == 'windows' else '.tar.gz'))
        if os_name == 'windows':
            with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as output:
                for name, content in sorted(entries.items()):
                    info = zipfile.ZipInfo(f'{stem}/{name}', (2020, 1, 1, 0, 0, 0))
                    executable = name in (binary, launcher, 'Findrail.app/Contents/MacOS/Findrail')
                    if name in package.EXECUTABLE_RESOURCES:
                        executable = True
                    info.external_attr = (0o100755 if executable else 0o100644) << 16
                    output.writestr(info, content)
        else:
            with tarfile.open(archive, 'w:gz') as output:
                for name, content in sorted(entries.items()):
                    info = tarfile.TarInfo(f'{stem}/{name}')
                    info.size = len(content)
                    executable = name in (binary, launcher, 'Findrail.app/Contents/MacOS/Findrail') or name in package.EXECUTABLE_RESOURCES
                    info.mode = 0o755 if executable else 0o644
                    info.mtime = 1700000000
                    output.addfile(info, io.BytesIO(content))

    vsix = root / release_assets.VSIX
    extension = {'name': 'findrail-readonly', 'publisher': 'findrail-local', 'version': '0.1.0',
                 'main': './dist/extension.js', 'extensionKind': ['ui']}
    with zipfile.ZipFile(vsix, 'w', zipfile.ZIP_DEFLATED) as output:
        for name, content in {
            '[Content_Types].xml': b'<Types/>', 'extension.vsixmanifest': b'<PackageManifest/>',
            'extension/package.json': json.dumps(extension).encode(), 'extension/LICENSE.txt': b'Synthetic license',
            'extension/readme.md': b'Synthetic readme', 'extension/changelog.md': b'Synthetic changelog',
            'extension/dist/extension.js': b'// synthetic',
        }.items():
            output.writestr(name, content)
    manifest = release_assets.build_manifest(root, version, revision)
    (root / 'release-manifest.json').write_text(json.dumps(manifest, sort_keys=True, indent=2) + '\n')
    checksum_names = sorted([item['basename'] for item in manifest['assets']] + ['release-manifest.json'])
    (root / 'SHA256SUMS.txt').write_text(''.join(
        f'{hashlib.sha256((root / name).read_bytes()).hexdigest()}  {name}\n' for name in checksum_names))
    return root


class AcceptanceFixtureTests(unittest.TestCase):
    def test_fixture_bytes_are_reproducible_and_passive(self):
        first, second = acceptance_fixture.files(), acceptance_fixture.files()
        self.assertEqual(first, second)
        self.assertEqual(set(first), {
            'fixture/Juniper-Notes.md', 'fixture/Élodie-Parcel.docx', 'fixture/Copper-Lantern.pdf'})
        self.assertEqual(first['fixture/Juniper-Notes.md'],
                         'Fictional field note: amber thimble, stored by the Juniper Observatory.\n')
        with zipfile.ZipFile(__import__('io').BytesIO(first['fixture/Élodie-Parcel.docx'])) as docx:
            self.assertEqual(set(docx.namelist()), {'[Content_Types].xml', '_rels/.rels', 'word/document.xml'})
            self.assertIn(b'juniper observatory parcel', docx.read('word/document.xml').lower())
        pdf = first['fixture/Copper-Lantern.pdf']
        self.assertTrue(pdf.startswith(b'%PDF-1.4'))
        self.assertIn(b'/Count 2', pdf)
        self.assertIn(b'violet kestrel waypoint', pdf)
        for value in first.values():
            self.assertNotIn(b'http://', value if isinstance(value, bytes) else value.encode())


class AcceptanceKitTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.temp.name)
        self.real_verify = acceptance_kit.release_assets.verify
        self.published_manifest_pin = acceptance_kit.PUBLISHED_MANIFEST_SHA256
        self.published_checksums_pin = acceptance_kit.PUBLISHED_CHECKSUMS_SHA256
        self.assets = self.root / 'assets'
        self.assets.mkdir()
        self.target = 'windows-amd64'
        self.archive_name = f'findrail_{acceptance_kit.RELEASE_VERSION}_{self.target}.zip'
        (self.assets / self.archive_name).write_bytes(b'synthetic archive placeholder')
        (self.assets / 'release-manifest.json').write_text(json.dumps({
            'source_revision': acceptance_kit.RELEASE_SHA,
            'app_version': acceptance_kit.RELEASE_VERSION,
        }))
        (self.assets / 'SHA256SUMS.txt').write_text('')
        pin_patches = (
            mock.patch.object(acceptance_kit, 'PUBLISHED_MANIFEST_SHA256',
                              hashlib.sha256((self.assets / 'release-manifest.json').read_bytes()).hexdigest()),
            mock.patch.object(acceptance_kit, 'PUBLISHED_CHECKSUMS_SHA256',
                              hashlib.sha256((self.assets / 'SHA256SUMS.txt').read_bytes()).hexdigest()),
        )
        for patcher in pin_patches:
            patcher.start()
            self.addCleanup(patcher.stop)
        self.output = self.root / 'kit.zip'
        self.manifest = {
            'source_revision': acceptance_kit.RELEASE_SHA,
            'app_version': acceptance_kit.RELEASE_VERSION,
            'schema_version': acceptance_kit.SCHEMA,
            'client_api_version': acceptance_kit.API,
            'assets': [{'basename': self.archive_name, 'target': self.target,
                        'size': (self.assets / self.archive_name).stat().st_size,
                        'sha256': hashlib.sha256((self.assets / self.archive_name).read_bytes()).hexdigest()}],
        }
        self.verify = mock.patch.object(acceptance_kit.release_assets, 'verify', return_value=self.manifest)
        self.verify_mock = self.verify.start()
        self.addCleanup(self.verify.stop)

    def tearDown(self):
        self.temp.cleanup()

    def configure_target(self, target, version='0.1.0-beta.1', source_sha='2' * 40):
        for path in self.assets.iterdir():
            if path.name not in ('release-manifest.json', 'SHA256SUMS.txt'):
                path.unlink()
        self.target = target
        suffix = '.zip' if target.startswith('windows-') else '.tar.gz'
        self.archive_name = f'findrail_{version}_{target}{suffix}'
        (self.assets / self.archive_name).write_bytes(b'synthetic archive placeholder')
        (self.assets / 'release-manifest.json').write_text(json.dumps({
            'source_revision': source_sha, 'app_version': version,
        }))
        self.manifest = {
            'source_revision': source_sha, 'app_version': version,
            'schema_version': acceptance_kit.SCHEMA, 'client_api_version': acceptance_kit.API,
            'assets': [{'basename': self.archive_name, 'target': target,
                        'size': (self.assets / self.archive_name).stat().st_size,
                        'sha256': hashlib.sha256((self.assets / self.archive_name).read_bytes()).hexdigest()}],
        }
        self.verify_mock.return_value = self.manifest

    def test_build_produces_safe_zip_and_blank_manual_outcomes(self):
        metadata = acceptance_kit.build(self.assets, self.target, self.output)
        self.verify_mock.assert_called_once_with(self.assets, acceptance_kit.RELEASE_VERSION,
                                                 acceptance_kit.RELEASE_SHA)
        self.assertEqual(metadata['kind'], 'published_release')
        self.assertEqual(metadata['archive']['basename'], self.archive_name)
        with zipfile.ZipFile(self.output) as kit:
            self.assertIsNone(kit.testzip())
            names = set(kit.namelist())
            self.assertEqual(names, {
                'START_HERE.md', 'EXPECTED_RESULTS.md', 'candidate.json', 'REPORT.md', 'report.json',
                'VOLUNTARY_USAGE_TEMPLATE.json',
                *acceptance_fixture.files().keys()})
            report = json.loads(kit.read('report.json'))
            self.assertEqual(report['status'], 'not_run')
            self.assertEqual([case['id'] for case in report['cases']],
                             [case[0] for case in acceptance_kit.CASES])
            for case in report['cases']:
                self.assertEqual(case['status'], 'not_run')
                self.assertIsNone(case['elapsed_seconds'])
            for item in kit.infolist():
                mode = item.external_attr >> 16
                self.assertEqual(stat.S_IFMT(mode), stat.S_IFREG)
                self.assertFalse(item.filename.lower().endswith(('.exe', '.dll', '.sh', '.ps1', '.py')))
            self.assertNotIn(b'/home/', kit.read('REPORT.md').lower())
            self.assertIn(b'official', kit.read('START_HERE.md').lower())
            self.assertIn(b'Windows VM details', kit.read('REPORT.md'))
            voluntary = json.loads(kit.read('VOLUNTARY_USAGE_TEMPLATE.json'))
            self.assertEqual(voluntary['return_use'], False)
            self.assertEqual(voluntary['status'], 'incomplete')
            self.assertEqual(voluntary['retrieval_one'], 'not_tried')

    def test_linux_kit_has_native_identity_and_independent_blank_report(self):
        self.configure_target('linux-amd64')
        metadata = acceptance_kit.build(self.assets, self.target, self.output, 'verified synthetic fixture')
        self.verify_mock.assert_called_once_with(self.assets, '0.1.0-beta.1', '2' * 40)
        self.assertEqual(metadata['kind'], 'source_candidate')
        self.assertEqual(metadata['target'], 'linux-amd64')
        self.assertEqual(metadata['archive']['basename'], self.archive_name)
        self.assertEqual(metadata['archive']['sha256'], hashlib.sha256((self.assets / self.archive_name).read_bytes()).hexdigest())
        self.assertEqual(metadata['release_url'], None)
        with zipfile.ZipFile(self.output) as kit:
            report = json.loads(kit.read('report.json'))
            self.assertEqual(report['kind'], 'linux_first_run')
            self.assertEqual(report['target'], 'linux-amd64')
            self.assertEqual(report['candidate_sha'], '2' * 40)
            self.assertEqual(report['candidate_version'], '0.1.0-beta.1')
            self.assertEqual(report['schema_version'], 8)
            self.assertEqual(report['client_api_version'], 1)
            self.assertEqual([case['id'] for case in report['cases']], [case[0] for case in acceptance_kit.CASES])
            self.assertTrue(all(case['status'] == 'not_run' and case['elapsed_seconds'] is None and
                                case['observation'] == '' for case in report['cases']))
            template = json.loads(kit.read('VOLUNTARY_USAGE_TEMPLATE.json'))
            self.assertEqual(template['kind'], 'voluntary_usage')
            self.assertFalse(template['return_use'], 'a first run is not a return visit')
            start = kit.read('START_HERE.md').decode()
            self.assertIn('sha256sum', start)
            self.assertIn('launcher/linux/install-user.sh', start)
            self.assertIn('Findrail acceptance test data', start)
            self.assertIn('does not preserve collections across restart', start)
            self.assertIn('No published release URL is claimed', start)
            self.assertNotIn('Windows amd64 — VM procedure', start)
            self.assertIn('Page 2 of 2', kit.read('EXPECTED_RESULTS.md').decode())
            self.assertIn('delivery_method', json.loads(kit.read('candidate.json')))

    def test_complete_verified_synthetic_bundle_builds_linux_kit_and_rejects_tampering(self):
        self.verify_mock.side_effect = self.real_verify
        assets = verified_synthetic_bundle(self.root / 'verified-assets')
        revision = '2' * 40
        verified = release_assets.verify(assets, '0.1.0-beta.1', revision)
        linux = next(item for item in verified['assets'] if item.get('target') == 'linux-amd64')
        metadata = acceptance_kit.build(assets, 'linux-amd64', self.output, 'synthetic verified asset fixture')
        self.assertEqual(metadata['candidate_sha'], revision)
        self.assertEqual(metadata['archive']['basename'], linux['basename'])
        self.assertEqual(metadata['archive']['size_bytes'], linux['size'])
        self.assertEqual(metadata['archive']['sha256'], linux['sha256'])
        with self.assertRaisesRegex(ValueError, 'manifest does not match assets'):
            release_assets.verify(assets, '0.1.0-beta.1', '3' * 40)
        (assets / release_assets.VSIX).unlink()
        with self.assertRaisesRegex(ValueError, 'asset set mismatch'):
            release_assets.verify(assets, '0.1.0-beta.1', revision)
        # Restore the verified file before testing its archive digest.
        verified_synthetic_bundle(assets)
        verified = release_assets.verify(assets, '0.1.0-beta.1', revision)
        linux = next(item for item in verified['assets'] if item.get('target') == 'linux-amd64')
        manifest_path = assets / 'release-manifest.json'
        bad_manifest = json.loads(manifest_path.read_text())
        next(item for item in bad_manifest['assets'] if item.get('target') == 'linux-amd64')['sha256'] = '0' * 64
        manifest_path.write_text(json.dumps(bad_manifest, sort_keys=True, indent=2) + '\n')
        with self.assertRaisesRegex(ValueError, 'manifest does not match assets'):
            release_assets.verify(assets, '0.1.0-beta.1', revision)
        verified_synthetic_bundle(assets)
        # Regenerated gzip archives may have a different header timestamp.
        verified = release_assets.verify(assets, '0.1.0-beta.1', revision)
        linux = next(item for item in verified['assets'] if item.get('target') == 'linux-amd64')
        sums_path = assets / 'SHA256SUMS.txt'
        sums = sums_path.read_text()
        sums_path.write_text(sums.replace(linux['sha256'], '0' * 64, 1))
        with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
            release_assets.verify(assets, '0.1.0-beta.1', revision)
        verified_synthetic_bundle(assets)
        verified = release_assets.verify(assets, '0.1.0-beta.1', revision)
        linux = next(item for item in verified['assets'] if item.get('target') == 'linux-amd64')
        (assets / linux['basename']).write_bytes(b'tampered archive bytes')
        with self.assertRaisesRegex(ValueError, 'manifest does not match assets'):
            release_assets.verify(assets, '0.1.0-beta.1', revision)

    def test_windows_vm_steps_are_specific_and_leave_manual_facts_blank(self):
        metadata = acceptance_kit.build(self.assets, self.target, self.output)
        self.assertEqual(metadata['kind'], 'published_release')
        with zipfile.ZipFile(self.output) as kit:
            start = kit.read('START_HERE.md').decode()
            report = kit.read('REPORT.md').decode()
            for phrase in ('browser **inside the guest**', 'Extract the ZIP with Explorer',
                           'findrail-desktop.exe --data-dir', 'copied from the host',
                           'Do not bypass local execution policy or disable SmartScreen or Defender'):
                self.assertIn(phrase, start)
            for phrase in ('Guest Windows edition/version/build:', 'VM: yes/no; hypervisor:',
                           'Browser used inside guest:', 'Actual SmartScreen result',
                           'Actual Defender result'):
                self.assertIn(phrase, report)
            blank = json.loads(kit.read('report.json'))
            self.assertTrue(all(case['status'] == 'not_run' for case in blank['cases']))

    def test_apple_silicon_report_kind_and_layout_remain_compatible(self):
        self.configure_target('darwin-arm64', acceptance_kit.RELEASE_VERSION, acceptance_kit.RELEASE_SHA)
        metadata = acceptance_kit.build(self.assets, self.target, self.output)
        self.assertEqual(metadata['kind'], 'published_release')
        self.assertTrue(metadata['archive']['basename'].endswith('_darwin-arm64.tar.gz'))
        with zipfile.ZipFile(self.output) as kit:
            report = json.loads(kit.read('report.json'))
            self.assertEqual(report['kind'], 'apple_silicon_first_run')
            self.assertEqual(report['target'], 'darwin-arm64')
            self.assertEqual([case['id'] for case in report['cases']], [case[0] for case in acceptance_kit.CASES])
            self.assertTrue(all(case['status'] == 'not_run' and case['elapsed_seconds'] is None
                                for case in report['cases']))
            start = kit.read('START_HERE.md').decode()
            self.assertIn('Apple Silicon', start)
            self.assertIn('Gatekeeper/quarantine', start)
            self.assertNotIn('linux-amd64', start)

    def test_published_alpha5_identity_fingerprints_remain_pinned(self):
        self.assertEqual(acceptance_kit.RELEASE_VERSION, '0.1.0-alpha.5')
        self.assertEqual(acceptance_kit.RELEASE_SHA, 'cb53ee9638f69b47b386921e41368f3b66ceddde')
        self.assertEqual(self.published_manifest_pin, PINNED_ALPHA5_MANIFEST_SHA256)
        self.assertEqual(self.published_checksums_pin, PINNED_ALPHA5_CHECKSUMS_SHA256)

    def test_candidate_requires_delivery_and_never_claims_release_url(self):
        self.manifest = {**self.manifest, 'source_revision': '1' * 40}
        self.verify_mock.return_value = self.manifest
        (self.assets / 'release-manifest.json').write_text(json.dumps({
            'source_revision': '1' * 40, 'app_version': acceptance_kit.RELEASE_VERSION,
        }))
        with self.assertRaisesRegex(ValueError, 'explicit --delivery-method'):
            acceptance_kit.build(self.assets, self.target, self.output)
        metadata = acceptance_kit.build(self.assets, self.target, self.output, 'private Actions artifact')
        self.assertEqual(metadata['kind'], 'source_candidate')
        self.assertIsNone(metadata['release_url'])

    def test_beta_candidate_kit_uses_verified_beta_artifact_identity(self):
        version = acceptance_kit.CANDIDATE_VERSION
        source_sha = '2' * 40
        archive_name = f'findrail_{version}_{self.target}.zip'
        (self.assets / self.archive_name).unlink()
        (self.assets / archive_name).write_bytes(b'beta candidate archive bytes')
        (self.assets / 'release-manifest.json').write_text(json.dumps({
            'source_revision': source_sha, 'app_version': version,
        }))
        self.manifest = {
            'source_revision': source_sha,
            'app_version': version,
            'schema_version': acceptance_kit.SCHEMA,
            'client_api_version': acceptance_kit.API,
            'assets': [{'basename': archive_name, 'target': self.target,
                        'size': (self.assets / archive_name).stat().st_size,
                        'sha256': hashlib.sha256((self.assets / archive_name).read_bytes()).hexdigest()}],
        }
        self.verify_mock.return_value = self.manifest

        metadata = acceptance_kit.build(self.assets, self.target, self.output, 'Actions artifact 39646624201')

        self.verify_mock.assert_called_once_with(self.assets, version, source_sha)
        self.assertEqual(metadata['kind'], 'source_candidate')
        self.assertEqual(metadata['candidate_sha'], source_sha)
        self.assertEqual(metadata['candidate_version'], version)
        self.assertEqual(metadata['archive']['basename'], archive_name)
        self.assertIsNone(metadata['release_url'])
        with zipfile.ZipFile(self.output) as kit:
            report = json.loads(kit.read('report.json'))
            self.assertEqual(report['candidate_sha'], source_sha)
            self.assertEqual(report['candidate_version'], version)
            self.assertEqual(report['archive_sha256'], metadata['archive']['sha256'])
            self.assertEqual(report['status'], 'not_run')
            self.assertTrue(all(case['status'] == 'not_run' for case in report['cases']))

    def test_acceptance_kit_rejects_unknown_candidate_version(self):
        (self.assets / 'release-manifest.json').write_text(json.dumps({
            'source_revision': acceptance_kit.RELEASE_SHA,
            'app_version': '0.1.0-beta.2',
        }))
        with self.assertRaisesRegex(ValueError, 'only published alpha.5 or preparation beta.1'):
            acceptance_kit.build(self.assets, self.target, self.output, 'Actions artifact')
        self.verify_mock.assert_not_called()

    def test_repacked_archive_with_same_source_sha_is_not_published_release(self):
        original_archive_hash = hashlib.sha256((self.assets / self.archive_name).read_bytes()).hexdigest()
        (self.assets / self.archive_name).write_bytes(b'repacked archive from the same source SHA')
        (self.assets / 'release-manifest.json').write_text(json.dumps({
            'source_revision': acceptance_kit.RELEASE_SHA,
            'app_version': acceptance_kit.RELEASE_VERSION,
            'archive_sha256': hashlib.sha256((self.assets / self.archive_name).read_bytes()).hexdigest(),
        }))
        self.manifest['assets'][0].update(
            size=(self.assets / self.archive_name).stat().st_size,
            sha256=hashlib.sha256((self.assets / self.archive_name).read_bytes()).hexdigest())
        (self.assets / 'SHA256SUMS.txt').write_text('updated checksum for repacked archive\n')

        with self.assertRaisesRegex(ValueError, 'explicit --delivery-method'):
            acceptance_kit.build(self.assets, self.target, self.output)
        metadata = acceptance_kit.build(
            self.assets, self.target, self.output, 'candidate CI artifact')

        self.assertEqual(metadata['candidate_sha'], acceptance_kit.RELEASE_SHA)
        self.assertEqual(metadata['kind'], 'source_candidate')
        self.assertIsNone(metadata['release_url'])
        self.assertNotEqual(metadata['archive']['sha256'], original_archive_hash)

    def test_bad_checksum_or_source_identity_is_rejected_by_existing_validator(self):
        self.verify_mock.side_effect = ValueError('checksum mismatch')
        with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
            acceptance_kit.build(self.assets, self.target, self.output)
        self.assertFalse(self.output.exists())

    def test_malformed_candidate_sha_is_rejected_by_existing_validator(self):
        (self.assets / 'release-manifest.json').write_text(json.dumps({
            'source_revision': 'not-a-sha', 'app_version': acceptance_kit.RELEASE_VERSION,
        }))
        self.verify_mock.side_effect = ValueError('candidate revision must be an exact 40-character commit SHA')
        with self.assertRaisesRegex(ValueError, 'exact 40-character commit SHA'):
            acceptance_kit.build(self.assets, self.target, self.output)

    def test_wrong_schema_or_api_is_rejected(self):
        for field, value in [('schema_version', 7), ('client_api_version', 2)]:
            self.manifest[field] = value
            with self.subTest(field=field), self.assertRaisesRegex(ValueError, 'unexpected schema'):
                acceptance_kit.build(self.assets, self.target, self.output)

    def test_wrong_target_archive_identity_and_unsafe_output_paths_rejected(self):
        self.manifest['assets'][0]['basename'] = 'other.zip'
        with self.assertRaisesRegex(ValueError, 'no exact target archive'):
            acceptance_kit.build(self.assets, self.target, self.output)
        with self.assertRaisesRegex(ValueError, 'separate, non-nested'):
            acceptance_kit.build(self.assets, self.target, self.assets / 'inside.zip')
        with self.assertRaisesRegex(ValueError, 'separate, non-nested'):
            acceptance_kit.build(self.assets, self.target, self.root)
        self.verify_mock.reset_mock()
        with self.assertRaisesRegex(ValueError, 'target must be'):
            acceptance_kit.build(self.assets, 'linux-arm64', self.output)
        self.verify_mock.assert_not_called()

    def test_symlink_output_and_symlink_parent_are_rejected(self):
        victim = self.root / 'victim.txt'
        victim.write_text('preserve')
        symlink = self.root / 'output-link.zip'
        parent_link = self.root / 'linked-dir'
        try:
            symlink.symlink_to(victim)
            parent_link.symlink_to(self.root, target_is_directory=True)
        except OSError as error:
            self.skipTest(f'symlink creation unavailable: {error}')
        with self.assertRaisesRegex(ValueError, 'must not contain symlinks'):
            acceptance_kit.build(self.assets, self.target, symlink)
        with self.assertRaisesRegex(ValueError, 'must not contain symlinks'):
            acceptance_kit.build(self.assets, self.target, parent_link / 'kit.zip')
        self.assertEqual(victim.read_text(), 'preserve')

    def test_existing_output_is_not_overwritten(self):
        self.output.write_text('preserve this')
        with self.assertRaisesRegex(ValueError, 'refusing to overwrite'):
            acceptance_kit.build(self.assets, self.target, self.output)
        self.assertEqual(self.output.read_text(), 'preserve this')

    def test_existing_archive_validator_rejects_traversal_and_symlink(self):
        traversal = self.root / 'traversal.zip'
        with zipfile.ZipFile(traversal, 'w') as archive:
            archive.writestr('../escape', 'no')
        with self.assertRaisesRegex(ValueError, 'unsafe or duplicate'):
            package.extract_verified(traversal, self.root / 'traversal-out')
        symlink = self.root / 'symlink.zip'
        info = zipfile.ZipInfo('pkg/link')
        info.external_attr = (stat.S_IFLNK | 0o777) << 16
        with zipfile.ZipFile(symlink, 'w') as archive:
            archive.writestr(info, 'target')
        with self.assertRaisesRegex(ValueError, 'unsafe archive member type'):
            package.extract_verified(symlink, self.root / 'symlink-out')


if __name__ == '__main__':
    unittest.main()
