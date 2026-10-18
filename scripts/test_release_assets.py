import io
import copy
import json
import pathlib
import sys
import tarfile
import tempfile
import unittest
import zipfile
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import release_assets as release
import candidate_record


class ReleaseAssetTests(unittest.TestCase):
    def summary_fixture(self, root):
        source, revision = self.candidate(root, version='0.1.0-alpha.5', schema=8)
        assets = root / 'assets'
        release.assemble(source, assets, '0.1.0-alpha.5', revision)
        records = root / 'native'
        for target in release.TARGETS:
            system, machine = target.split('-')
            with patch.object(candidate_record.platform, 'system', return_value={'linux':'Linux','darwin':'Darwin','windows':'Windows'}[system]), \
                 patch.object(candidate_record.platform, 'machine', return_value=machine):
                record = candidate_record.native(source, revision, '0.1.0-alpha.5', target, 'success')
            self.assertEqual(record['status'], 'passed')
            if target == 'linux-amd64':
                measurement = {'status':'success','binary':{'sha256':record['binary_sha256'],'revision':revision},
                               'comparison':{'same_host':True,'fixture_sha256_matches':True,'binary':{'revision':'1d47f9d54ff81ab566fcf3f8c0c6bf9af86a33f7'}}}
                baseline = copy.deepcopy(measurement)
                baseline['comparison']['binary']['revision'] = '6b157fe3a6fe2f3f3808732ce72a8e74a118f763'
                boundaries = copy.deepcopy(measurement)
                boundaries['metrics'] = {'paging_window':{'returned':500,'beyond_237':True},
                    'evidence_bundle_boundary':{'codec_boundary_selected_text_bytes':2<<20,'accepted_selected_text_bytes':1<<20,
                                               'one_byte_over_quota_status':413,'persistent_collections_created':0}}
                record['resources'] = dict(zip(candidate_record.LINUX_RESOURCE_REPORTS,
                    [measurement,baseline,boundaries,{'binary_sha256':record['binary_sha256'],'document_text_bytes':8<<20,'archive_bytes':4096,'elapsed_seconds':0.1}]))
                binary_sha = record['binary_sha256']
            path = records / target / 'verification.json'; path.parent.mkdir(parents=True)
            path.write_text(json.dumps(record))
        passed = lambda names:[{'case':name,'status':'passed','duration_seconds':0.1} for name in names]
        journey = {'candidate_sha':revision,'candidate_version':'0.1.0-alpha.5','status':'passed','browser_skipped':False,
                   'source_clean':True,'target':'linux-amd64','schema_version':8,'binary_sha256':binary_sha,
                   'offline_fixture_sha256':'f'*64,'cases':passed(candidate_record.JOURNEY_CASES),
                   'portable_transfer':{'candidate_sha':revision,'candidate_version':'0.1.0-alpha.5','status':'passed',
                                        'binary_sha256':binary_sha,'cases':passed(candidate_record.TRANSFER_CASES)}}
        common = next(case for case in journey['cases'] if case['case']=='packaged_browser_journey.py')
        common['details'] = {'status':'passed','candidate_sha':revision,'binary_sha256':binary_sha,
                             'chrome':'139.0.7258.66','chromedriver':'139.0.7258.66','cases':passed(candidate_record.COMMON_BROWSER_CASES)}
        browser = root / 'browser.json'; browser.write_text(json.dumps(journey))
        return records, assets, browser, revision, journey

    def test_machine_summary_binds_each_native_archive_and_browser_binary(self):
        with tempfile.TemporaryDirectory() as temp:
            records, assets, browser, revision, journey = self.summary_fixture(pathlib.Path(temp))
            result = candidate_record.summarize(records, assets, browser, revision, '0.1.0-alpha.5')
            self.assertEqual(len(result['native']),5); self.assertEqual(len(result['assets']),8)
            for changes in ({'candidate_sha':'b'*40},{'status':'incomplete'},{'browser_skipped':True},{'binary_sha256':'0'*64},
                            {'cases':[{'case':'synthetic','status':'skipped'}]}):
                with self.subTest(changes=changes):
                    browser.write_text(json.dumps(journey | changes))
                    with self.assertRaises(ValueError):candidate_record.summarize(records,assets,browser,revision,'0.1.0-alpha.5')
            browser.write_text(json.dumps(journey))
            path=records/'linux-amd64'/'verification.json'
            changed=json.loads(path.read_text());changed['archive']['sha256']='0'*64;path.write_text(json.dumps(changed))
            with self.assertRaises(ValueError):candidate_record.summarize(records,assets,browser,revision,'0.1.0-alpha.5')

    def test_summary_rejects_missing_duplicate_or_unmeasured_acceptance_cases(self):
        with tempfile.TemporaryDirectory() as temp:
            records,assets,browser,revision,journey=self.summary_fixture(pathlib.Path(temp))
            variants=[dict(journey,cases=[{'case':'synthetic','status':'passed','duration_seconds':0}])]
            for missing in ['mcp_smoke.py','doctor_integrity_stopped_directory','portable_transfer_browser_smoke.py']:
                variants.append(dict(journey,cases=[case for case in journey['cases'] if case['case']!=missing]))
            variants.append(dict(journey,cases=journey['cases']+[journey['cases'][0]]))
            invalid_time=copy.deepcopy(journey);invalid_time['cases'][0].pop('duration_seconds');variants.append(invalid_time)
            for bad in variants:
                with self.subTest(cases=[c['case'] for c in bad['cases']]):
                    browser.write_text(json.dumps(bad))
                    with self.assertRaises(ValueError):candidate_record.summarize(records,assets,browser,revision,'0.1.0-alpha.5')

    def test_summary_requires_both_complete_child_journeys_on_the_same_binary(self):
        with tempfile.TemporaryDirectory() as temp:
            records,assets,browser,revision,journey=self.summary_fixture(pathlib.Path(temp))
            for child in ['portable_transfer','common']:
                for change in ['missing_case','wrong_binary','failed']:
                    bad=copy.deepcopy(journey)
                    details=bad['portable_transfer'] if child=='portable_transfer' else next(c for c in bad['cases'] if c['case']=='packaged_browser_journey.py')['details']
                    if change=='missing_case':details['cases'].pop()
                    elif change=='wrong_binary':details['binary_sha256']='0'*64
                    else:details['status']='failed'
                    with self.subTest(child=child,change=change):
                        browser.write_text(json.dumps(bad))
                        with self.assertRaises(ValueError):candidate_record.summarize(records,assets,browser,revision,'0.1.0-alpha.5')

    def test_summary_requires_every_linux_resource_report_and_matching_provenance(self):
        with tempfile.TemporaryDirectory() as temp:
            records,assets,browser,revision,journey=self.summary_fixture(pathlib.Path(temp))
            path=records/'linux-amd64'/'verification.json';original=json.loads(path.read_text())
            for missing in candidate_record.LINUX_RESOURCE_REPORTS:
                bad=copy.deepcopy(original);bad['resources'].pop(missing)
                with self.subTest(missing=missing):
                    path.write_text(json.dumps(bad))
                    with self.assertRaises(ValueError):candidate_record.summarize(records,assets,browser,revision,'0.1.0-alpha.5')
            for change in ['failed','wrong_binary','wrong_baseline','wrong_boundary']:
                bad=copy.deepcopy(original)
                if change=='failed':bad['resources']['alpha5-resource-comparison.json']['status']='failed'
                elif change=='wrong_binary':bad['resources']['alpha5-resource-comparison.json']['binary']['sha256']='0'*64
                elif change=='wrong_baseline':bad['resources']['source-baseline-resource-comparison.json']['comparison']['binary']['revision']='0'*40
                else:bad['resources']['snapshot-resource-boundary.json']['document_text_bytes']=1
                with self.subTest(change=change):
                    path.write_text(json.dumps(bad))
                    with self.assertRaises(ValueError):candidate_record.summarize(records,assets,browser,revision,'0.1.0-alpha.5')

    def test_native_failure_does_not_relabel_clean_source_as_dirty(self):
        with tempfile.TemporaryDirectory() as temp:
            source, revision = self.candidate(pathlib.Path(temp), version='0.1.0-alpha.5', schema=8)
            with patch.object(candidate_record.platform, 'system', return_value='Linux'), \
                 patch.object(candidate_record.platform, 'machine', return_value='x86_64'):
                record = candidate_record.native(source, revision, '0.1.0-alpha.5', 'linux-amd64', 'failure')
                self.assertEqual(record['status'], 'failed'); self.assertTrue(record['source_clean'])
                wrong = candidate_record.native(source, revision, '0.1.0-alpha.5', 'linux-arm64', 'success')
                self.assertEqual(wrong['status'], 'failed')

    def candidate(self, root, revision='a' * 40, dirty=False, version='0.1.0-alpha.4', schema=None,
                  include_schema=True, metadata_version=None, target_override=None):
        source = root / 'input'
        source.mkdir()
        schema = release.contract_schema(version) if schema is None else schema
        for name, target in release.expected_archives(version).items():
            stem = pathlib.Path(name).name.removesuffix('.tar.gz').removesuffix('.zip')
            info = {'version': metadata_version or version, 'revision': revision, 'dirty': dirty,
                    'target': target_override or target, 'schema_version': schema}
            if not include_schema:
                del info['schema_version']
            members = {'BUILD-INFO.json': json.dumps(info).encode(),
                       'INSTALL.txt': b'x', 'LICENSE': b'x', 'NOTICE': b'x',
                       'licenses/DEPENDENCIES.txt': b'example.com/dependency v1.0.0\n',
                       'licenses/Go-LICENSE.txt': b'x',
                       'licenses/example.com_dependency-LICENSE.txt': b'x'}
            historical = version == '0.1.0-alpha.3' and revision == release.ALPHA3_REVISION and dirty is False
            if not historical:
                members['findrail-desktop.exe' if target.startswith('windows-') else 'findrail-desktop'] = b'desktop'
                members.update({destination: b'resource' for destination in release.package.RESOURCE_FILES[target.split('-', 1)[0]].values()})
                if target.startswith('darwin-'):
                    members['Findrail.app/Contents/MacOS/Findrail'] = b'launcher'
            if target.startswith('windows-'):
                members['findrail.exe'] = b'fake'
                with zipfile.ZipFile(source / name, 'w') as zf:
                    for path, data in members.items():
                        item = zipfile.ZipInfo(f'{stem}/{path}')
                        executable = path in {'findrail.exe', 'findrail-desktop.exe'} or path in release.package.EXECUTABLE_RESOURCES
                        item.external_attr = (0o100755 if executable else 0o100644) << 16
                        zf.writestr(item, data)
            else:
                members['findrail'] = b'fake'
                with tarfile.open(source / name, 'w:gz') as tf:
                    for path, data in members.items():
                        item = tarfile.TarInfo(f'{stem}/{path}')
                        item.size = len(data)
                        executable = path in {'findrail', 'findrail-desktop', 'Findrail.app/Contents/MacOS/Findrail'} or path in release.package.EXECUTABLE_RESOURCES
                        item.mode = 0o755 if executable else 0o644
                        tf.addfile(item, io.BytesIO(data))
        with zipfile.ZipFile(source / release.VSIX, 'w') as zf:
            zf.writestr('extension/package.json', json.dumps({'version': '0.1.0', 'name': 'findrail-readonly',
                'publisher': 'findrail-local', 'main': './dist/extension.js', 'extensionKind': ['ui']}))
            for name in ('[Content_Types].xml', 'extension.vsixmanifest', 'extension/LICENSE.txt',
                         'extension/readme.md', 'extension/changelog.md', 'extension/dist/extension.js'):
                zf.writestr(name, 'fixture')
        return source, revision

    def test_candidate_manifest_archive_metadata_and_checksums(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp); source, revision = self.candidate(root); output = root / 'release'
            manifest = release.assemble(source, output, '0.1.0-alpha.4', revision)
            # Alpha.4 remains a pinned schema-7 release after the source moves to schema 8.
            self.assertEqual(manifest['schema_version'], release.contract_schema('0.1.0-alpha.4'))
            self.assertEqual(len(manifest['assets']), 6)
            self.assertEqual(release.verify(output, '0.1.0-alpha.4', revision), manifest)

    def test_published_alpha4_schema_is_frozen_when_source_advances(self):
        with patch.object(release.package, 'schema_version', return_value=8):
            self.assertEqual(release.contract_schema('0.1.0-alpha.4'), 7)
        self.assertEqual(release.contract_schema('0.1.0-alpha.3'), 6)
        with self.assertRaises(ValueError): release.contract_schema('0.1.0-alpha.6')

    def test_alpha4_remains_verifiable_after_a_new_source_migration(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            source, revision = self.candidate(root, schema=7)
            output = root / 'release'
            release.assemble(source, output, '0.1.0-alpha.4', revision)
            with patch.object(release.package, 'schema_version', return_value=8):
                self.assertEqual(release.verify(output, '0.1.0-alpha.4', revision)['schema_version'], 7)

    def test_alpha5_candidate_tracks_trusted_source_schema_only(self):
        for source_schema in (7, 8):
            with self.subTest(source_schema=source_schema), tempfile.TemporaryDirectory() as temp:
                root = pathlib.Path(temp)
                with patch.object(release.package, 'schema_version', return_value=source_schema):
                    self.assertEqual(release.contract_schema('0.1.0-alpha.5'), source_schema)
                    source, revision = self.candidate(root, version='0.1.0-alpha.5', schema=source_schema)
                    output = root / 'release'
                    manifest = release.assemble(source, output, '0.1.0-alpha.5', revision)
                    self.assertEqual(manifest['schema_version'], source_schema)
                    self.assertEqual(release.verify(output, '0.1.0-alpha.5', revision)['schema_version'], source_schema)

    def test_alpha3_and_alpha4_contracts_do_not_follow_alpha5_source_schema(self):
        with patch.object(release.package, 'schema_version', return_value=8):
            self.assertEqual(release.contract_schema('0.1.0-alpha.3'), 6)
            self.assertEqual(release.contract_schema('0.1.0-alpha.4'), 7)

    def test_historical_alpha3_contract_keeps_schema_six(self):
        self.assertEqual(release.contract_schema('0.1.0-alpha.3'), 6)

    def test_historical_alpha3_schema_remains_verifiable_when_source_advances(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            source, revision = self.candidate(root, version='0.1.0-alpha.3', schema=6)
            output = root / 'release'
            with patch.object(release.package, 'schema_version', return_value=7):
                release.assemble(source, output, '0.1.0-alpha.3', revision)
                self.assertEqual(release.verify(output, '0.1.0-alpha.3', revision)['schema_version'], 6)

    def test_exact_assets_provenance_schema_and_checksum_fail_closed(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp); source, revision = self.candidate(root); output = root / 'release'
            release.assemble(source, output, '0.1.0-alpha.4', revision)
            (output / 'unexpected').write_text('no')
            with self.assertRaisesRegex(ValueError, 'asset set'): release.verify(output, '0.1.0-alpha.4', revision)
            (output / 'unexpected').unlink()
            sums = output / 'SHA256SUMS.txt'
            sums.write_text(sums.read_text() + sums.read_text().splitlines()[0] + '\n')
            with self.assertRaises(ValueError): release.verify(output, '0.1.0-alpha.4', revision)

    def test_archive_member_allowlist_rejects_unknown_bundle_files(self):
        for directory in (False, True):
            with tempfile.TemporaryDirectory() as temp:
                root = pathlib.Path(temp)
                source, revision = self.candidate(root)
                name = 'findrail_0.1.0-alpha.4_linux-amd64.tar.gz'
                archive = source / name
                replacement = source / 'rewritten.tar.gz'
                with tarfile.open(archive, 'r:gz') as original, tarfile.open(replacement, 'w:gz') as output:
                    for member in original.getmembers():
                        output.addfile(member, original.extractfile(member) if member.isfile() else None)
                    payload = b'unlisted resource'
                    extra = tarfile.TarInfo(name.removesuffix('.tar.gz') + '/launcher/linux/unreviewed')
                    extra.size = 0 if directory else len(payload); extra.mode = 0o644
                    if directory: extra.type = tarfile.DIRTYPE
                    output.addfile(extra, None if directory else io.BytesIO(payload))
                replacement.replace(archive)
                with self.subTest(directory=directory), self.assertRaisesRegex(ValueError, 'unexpected (member|directory)'):
                    release.assemble(source, root / 'out', '0.1.0-alpha.4', revision)
    def test_fake_schema_dirty_and_wrong_revision_are_rejected(self):
        current = release.package.schema_version()
        for revision, dirty, schema in (('b' * 40, False, current), ('a' * 40, True, current), ('a' * 40, False, current + 1), ('unknown', False, current)):
            with self.subTest(revision=revision, dirty=dirty, schema=schema), tempfile.TemporaryDirectory() as temp:
                root = pathlib.Path(temp); source, _ = self.candidate(root, revision, dirty, schema=schema)
                with self.assertRaises(ValueError): release.assemble(source, root / 'out', '0.1.0-alpha.4', 'a' * 40)

    def test_alpha5_rejects_mismatched_candidate_schema_and_metadata(self):
        version = '0.1.0-alpha.5'
        schema = release.package.schema_version()
        cases = [dict(revision='b' * 40), dict(dirty=True), dict(schema=schema + 1),
                 dict(metadata_version='0.1.0-alpha.4'), dict(target_override='linux-arm64')]
        for changes in cases:
            with self.subTest(changes=changes), tempfile.TemporaryDirectory() as temp:
                root = pathlib.Path(temp)
                candidate_options = {'schema': schema, **changes}
                source, _ = self.candidate(root, version=version, **candidate_options)
                with self.assertRaises(ValueError):
                    release.assemble(source, root / 'out', version, 'a' * 40)

    def test_fake_extension_and_unknown_version_are_rejected(self):
        with self.assertRaises(ValueError): release.contract_schema('0.1.0-alpha.9')
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp); source, revision = self.candidate(root)
            vsix = source / release.VSIX
            with zipfile.ZipFile(vsix) as zf:
                members = {name: zf.read(name) for name in zf.namelist()}
            metadata = json.loads(members['extension/package.json'])
            metadata['publisher'] = 'another-publisher'
            members['extension/package.json'] = json.dumps(metadata).encode()
            with zipfile.ZipFile(vsix, 'w') as zf:
                for name, content in members.items(): zf.writestr(name, content)
            with self.assertRaisesRegex(ValueError, 'VSIX identity'):
                release.assemble(source, root / 'out', '0.1.0-alpha.4', revision)

    def test_original_published_alpha3_without_schema_field_is_verifiable(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            source, revision = self.candidate(root, revision=release.ALPHA3_REVISION,
                version='0.1.0-alpha.3', schema=6, include_schema=False)
            with patch.object(release.package, 'schema_version', return_value=7):
                for name, target in release.expected_archives('0.1.0-alpha.3').items():
                    release.package.verify_archive(source / name, target, '0.1.0-alpha.3')
                release.assemble(source, root / 'out', '0.1.0-alpha.3', revision)
                self.assertEqual(release.verify(root / 'out', '0.1.0-alpha.3', revision)['schema_version'], 6)

    def test_missing_schema_is_rejected_outside_original_published_build(self):
        cases = [('0.1.0-alpha.3', 'a' * 40, False),
                 ('0.1.0-alpha.3', release.ALPHA3_REVISION, True),
                 ('0.1.0-alpha.4', release.ALPHA3_REVISION, False)]
        for version, revision, dirty in cases:
            with self.subTest(version=version, revision=revision, dirty=dirty), tempfile.TemporaryDirectory() as temp:
                root = pathlib.Path(temp)
                source, _ = self.candidate(root, revision=revision, dirty=dirty,
                    version=version, include_schema=False)
                with self.assertRaises(ValueError):
                    release.assemble(source, root / 'out', version, revision)

    def test_missing_target_and_vsix_fail_closed(self):
        for name in (release.VSIX, 'findrail_0.1.0-alpha.4_linux-arm64.tar.gz'):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temp:
                root = pathlib.Path(temp); source, revision = self.candidate(root)
                (source / name).unlink()
                with self.assertRaises(ValueError):
                    release.assemble(source, root / 'out', '0.1.0-alpha.4', revision)

    def test_checksum_mismatch_is_rejected_independently(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp); source, revision = self.candidate(root); output = root / 'out'
            release.assemble(source, output, '0.1.0-alpha.4', revision)
            sums = output / 'SHA256SUMS.txt'
            text = sums.read_text()
            sums.write_text('0' * 64 + text[64:])
            with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                release.verify(output, '0.1.0-alpha.4', revision)

    def test_manifest_duplicates_unsafe_names_and_wrong_revision_are_rejected(self):
        for mutation in ('duplicate', 'unsafe', 'revision'):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temp:
                root = pathlib.Path(temp); source, revision = self.candidate(root); output = root / 'out'
                release.assemble(source, output, '0.1.0-alpha.4', revision)
                path = output / 'release-manifest.json'; manifest = json.loads(path.read_text())
                if mutation == 'duplicate': manifest['assets'].append(manifest['assets'][0])
                elif mutation == 'unsafe': manifest['assets'][0]['basename'] = '../escape'
                else: manifest['source_revision'] = 'b' * 40
                path.write_text(json.dumps(manifest))
                with self.assertRaises(ValueError): release.verify(output, '0.1.0-alpha.4', revision)


if __name__ == '__main__':
    unittest.main()
