import hashlib
import json
import pathlib
import stat
import sys
import tempfile
import unittest
import zipfile
from unittest import mock

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import acceptance_fixture
import acceptance_kit
import package


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
            'assets': [{'basename': self.archive_name, 'target': self.target}],
        }
        self.verify = mock.patch.object(acceptance_kit.release_assets, 'verify', return_value=self.manifest)
        self.verify_mock = self.verify.start()
        self.addCleanup(self.verify.stop)

    def tearDown(self):
        self.temp.cleanup()

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
            'assets': [{'basename': archive_name, 'target': self.target}],
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
