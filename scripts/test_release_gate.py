import pathlib
import sys
import unittest
import io
import json
import tempfile
import hashlib
import urllib.error
import zipfile
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import release_gate
import release_assets
import test_release_assets


class ReleaseGateTests(unittest.TestCase):
    def test_publish_version_mismatch_is_rejected_not_downgraded(self):
        self.assertFalse(release_gate.validate_publication_version('0.1.0-beta.1', '0.1.0-alpha.5', False))
        with self.assertRaisesRegex(ValueError, 'match'):
            release_gate.validate_publication_version('0.1.0-beta.1', '0.1.0-alpha.5', True)
        self.assertTrue(release_gate.validate_publication_version('0.1.0-alpha.5', '0.1.0-alpha.5', True))
        with self.assertRaisesRegex(ValueError, 'unknown published'):
            release_gate.existing_release_decision('0.1.0-beta.2', 'a' * 40, None, None, [])

    def test_only_expected_push_context_is_trusted(self):
        sha = 'a' * 40
        release_gate.validate_upstream('Lephiziel/findrail', 'push', 'main', sha, 'Lephiziel/findrail')
        for args in (('fork/findrail', 'push', 'main', sha), ('Lephiziel/findrail', 'pull_request', 'main', sha),
                     ('Lephiziel/findrail', 'push', 'feature', sha), ('Lephiziel/findrail', 'push', 'main', 'unknown')):
            with self.subTest(args=args), self.assertRaises(ValueError):
                release_gate.validate_upstream(*args, 'Lephiziel/findrail')

    def test_published_complete_alpha2_is_noop_after_main_moves(self):
        tag_sha = 'b' * 40
        names = sorted(release_gate.ALPHA2_ASSETS)
        assets = [{'name': name} for name in names]
        release = {'tag_sha': tag_sha}
        self.assertEqual(release_gate.existing_release_decision('0.1.0-alpha.2', 'c' * 40, tag_sha, release, assets), 'noop')

    def test_incomplete_or_ambiguous_release_is_never_noop(self):
        sha = 'd' * 40
        release = {'tag_sha': sha}
        assets = [{'name': name} for name in release_gate.ALPHA2_ASSETS if name != 'SHA256SUMS.txt']
        with self.assertRaises(ValueError): release_gate.existing_release_decision('0.1.0-alpha.2', 'e' * 40, sha, release, assets)
        with self.assertRaises(ValueError): release_gate.existing_release_decision('0.1.0-alpha.2', 'e' * 40, sha, None, assets)
        with self.assertRaises(ValueError): release_gate.existing_release_decision('0.1.0-alpha.2', 'e' * 40, None, release, assets, 'HTTP 500')

    def test_new_contract_rejects_manifest_sha_or_version_conflicts(self):
        sha = 'f' * 40
        release = {'tag_sha': sha, 'manifest': {'source_revision': '0' * 40, 'app_version': '0.1.0-alpha.3', 'assets': []}}
        with self.assertRaises(ValueError): release_gate.existing_release_decision('0.1.0-alpha.3', '1' * 40, sha, release, [])
        release['manifest'] = {'source_revision': sha, 'app_version': '0.1.0-alpha.2', 'assets': []}
        with self.assertRaises(ValueError): release_gate.existing_release_decision('0.1.0-alpha.3', '1' * 40, sha, release, [])

    def test_remote_checksum_mismatch_and_duplicate_are_rejected(self):
        import hashlib
        content = b'asset'
        good = hashlib.sha256(content).hexdigest()
        release_gate.verify_remote_checksums([{'name': 'a.zip', 'data': content}, {'name': 'SHA256SUMS.txt'}], f'{good}  a.zip\n')
        with self.assertRaises(ValueError): release_gate.verify_remote_checksums([{'name': 'a.zip', 'data': b'bad'}, {'name': 'SHA256SUMS.txt'}], f'{good}  a.zip\n')
        with self.assertRaises(ValueError): release_gate.verify_remote_checksums([{'name': 'a.zip', 'data': content}, {'name': 'SHA256SUMS.txt'}], f'{good}  a.zip\n{good}  a.zip\n')

    def test_github_json_responses_are_bounded(self):
        with patch.object(release_gate, 'MAX_GITHUB_JSON', 4):
            with self.assertRaisesRegex(ValueError, 'response bound'):
                release_gate._read_github_json(io.BytesIO(b'12345'), 'synthetic response')

    def test_alpha3_published_sha_is_immutable_and_unknown_contract_fails_closed(self):
        sha = release_gate.ALPHA3_REVISION
        names = {f'findrail_0.1.0-alpha.3_{target}' + ('.zip' if target.startswith('windows-') else '.tar.gz')
                 for target in ('linux-amd64', 'linux-arm64', 'darwin-amd64', 'darwin-arm64', 'windows-amd64')}
        names.add('findrail-readonly-0.1.0.vsix')
        manifest = {'manifest_version': 1, 'source_revision': sha, 'app_version': '0.1.0-alpha.3',
                    'schema_version': 6, 'client_api_version': 1, 'extension_version': '0.1.0',
                    'assets': [{'basename': name} for name in names]}
        release = {'tag_sha': sha, 'manifest': manifest}
        names |= {'release-manifest.json', 'SHA256SUMS.txt'}
        self.assertEqual(release_gate.existing_release_decision('0.1.0-alpha.3', '1' * 40, sha, release,
                         [{'name': name} for name in names]), 'noop')
        with self.assertRaisesRegex(ValueError, 'historical revision'):
            release_gate.existing_release_decision('0.1.0-alpha.3', '1' * 40, 'a' * 40,
                {'tag_sha': 'a' * 40, 'manifest': {**manifest, 'source_revision': 'a' * 40}},
                [{'name': name} for name in names])
        with self.assertRaisesRegex(ValueError, 'unknown published'):
            release_gate.existing_release_decision('0.1.0-alpha.6', '1' * 40, sha, release, [])

    def alpha4_release(self):
        sha = '1d47f9d54ff81ab566fcf3f8c0c6bf9af86a33f7'
        names = {f'findrail_0.1.0-alpha.4_{target}' + ('.zip' if target.startswith('windows-') else '.tar.gz')
                 for target in release_assets.TARGETS}
        names.add(release_assets.VSIX)
        manifest = {'manifest_version': 1, 'source_revision': sha, 'app_version': '0.1.0-alpha.4',
                    'schema_version': 7, 'client_api_version': 1, 'extension_version': '0.1.0',
                    'assets': [{'basename': name} for name in sorted(names)]}
        return sha, {'tag_sha': sha, 'manifest': manifest}, [
            {'name': name} for name in sorted(names | {'release-manifest.json', 'SHA256SUMS.txt'})]

    def test_alpha4_complete_release_is_noop_after_main_moves(self):
        sha, release, assets = self.alpha4_release()
        self.assertEqual(release_gate.existing_release_decision(
            '0.1.0-alpha.4', 'db1c088470db1e5fd189d1d17f060fc265eb1976', sha, release, assets), 'noop')

    def test_alpha5_complete_release_is_noop_after_main_moves(self):
        sha = '2' * 40
        names = {f'findrail_0.1.0-alpha.5_{target}' + ('.zip' if target.startswith('windows-') else '.tar.gz') for target in release_assets.TARGETS}
        names.add(release_assets.VSIX)
        manifest = {'manifest_version': 1, 'source_revision': sha, 'app_version': '0.1.0-alpha.5',
                    'schema_version': 8, 'client_api_version': 1, 'extension_version': '0.1.0',
                    'assets': [{'basename': name} for name in sorted(names)]}
        release = {'tag_sha': sha, 'manifest': manifest}
        assets = [{'name': name} for name in sorted(names | {'release-manifest.json', 'SHA256SUMS.txt'})]
        self.assertEqual(release_gate.existing_release_decision('0.1.0-alpha.5', 'c' * 40, sha, release, assets), 'noop')
        for key, value in [('schema_version', 7), ('schema_version', 9),
                           ('schema_version', 8.0), ('schema_version', True),
                           ('source_revision', '3' * 40), ('app_version', '0.1.0-alpha.4'),
                           ('client_api_version', 2), ('extension_version', '0.2.0'),
                           ('manifest_version', 2)]:
            bad = {**release, 'manifest': {**manifest, key: value}}
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                release_gate.existing_release_decision('0.1.0-alpha.5', 'c' * 40, sha, bad, assets)
        for changed in (assets[:-1], assets + [assets[0]], assets + [{'name': 'unexpected.zip'}]):
            with self.subTest(assets=changed), self.assertRaises(ValueError):
                release_gate.existing_release_decision('0.1.0-alpha.5', 'c' * 40, sha, release, changed)

    def alpha5_remote_fixture(self, root):
        sha = '2' * 40
        source, _ = test_release_assets.ReleaseAssetTests().candidate(
            root, revision=sha, version='0.1.0-alpha.5', schema=8)
        output = root / 'release'
        release_assets.assemble(source, output, '0.1.0-alpha.5', sha)
        urls = {path.name: 'https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.5/' + path.name
                for path in output.iterdir()}
        base = 'https://api.github.com/repos/Lephiziel/findrail'
        responses = {urls[path.name]: path.read_bytes() for path in output.iterdir()}
        responses[base + '/releases/tags/v0.1.0-alpha.5'] = json.dumps({
            'assets': [{'name': name, 'browser_download_url': url,
                        'size': len(responses[url])} for name, url in urls.items()]}).encode()
        responses[base + '/git/ref/tags/v0.1.0-alpha.5'] = json.dumps({
            'object': {'type': 'commit', 'sha': sha}}).encode()
        return sha, urls, responses

    def check_remote_alpha5(self, responses):
        def response(request, **kwargs):
            value = responses[request.full_url]
            if isinstance(value, Exception):
                raise value
            return io.BytesIO(value)

        with patch.object(release_gate.urllib.request, 'urlopen', side_effect=response), \
                patch.object(release_assets.package, 'schema_version', return_value=9):
            return release_gate.github_release_decision(
                'Lephiziel/findrail', '0.1.0-alpha.5', 'c' * 40, 'test-token')

    def refresh_remote_hashes(self, responses, urls):
        # Deliberately keep the outer hashes coherent to reach embedded verification.
        manifest_url = urls['release-manifest.json']
        manifest = json.loads(responses[manifest_url])
        for item in manifest['assets']:
            data = responses[urls[item['basename']]]
            item['size'] = len(data)
            item['sha256'] = hashlib.sha256(data).hexdigest()
        responses[manifest_url] = json.dumps(manifest).encode()
        responses[urls['SHA256SUMS.txt']] = ''.join(
            hashlib.sha256(responses[urls[name]]).hexdigest() + '  ' + name + '\n'
            for name in sorted(urls) if name != 'SHA256SUMS.txt').encode()
        for url, body in tuple(responses.items()):
            if '/releases/tags/v0.1.0-' not in url:
                continue
            release = json.loads(body)
            for item in release.get('assets', []):
                item['size'] = len(responses[item['browser_download_url']])
            responses[url] = json.dumps(release).encode()
        release_gate.verify_remote_checksums(
            [{'name': name, 'data': responses[url]} for name, url in urls.items()],
            responses[urls['SHA256SUMS.txt']].decode())

    def beta1_remote_fixture(self, root):
        sha = '7' * 40
        source, _ = test_release_assets.ReleaseAssetTests().candidate(
            root, revision=sha, version=release_gate.BETA1_VERSION, schema=8)
        output = root / 'beta-release'
        release_assets.assemble(source, output, release_gate.BETA1_VERSION, sha)
        urls = {path.name: 'https://github.com/Lephiziel/findrail/releases/download/v0.1.0-beta.1/' + path.name
                for path in output.iterdir()}
        base = 'https://api.github.com/repos/Lephiziel/findrail'
        responses = {urls[path.name]: path.read_bytes() for path in output.iterdir()}
        responses[base + '/releases/tags/v0.1.0-beta.1'] = json.dumps({
            'tag_name': 'v0.1.0-beta.1', 'draft': False, 'prerelease': True,
            'assets': [{'name': name, 'browser_download_url': url, 'size': len(responses[url])}
                       for name, url in urls.items()]}).encode()
        responses[base + '/git/ref/tags/v0.1.0-beta.1'] = json.dumps({
            'object': {'type': 'commit', 'sha': sha}}).encode()
        return sha, urls, responses

    def check_remote_beta1(self, responses):
        base = 'https://api.github.com/repos/Lephiziel/findrail'

        def response(request, **kwargs):
            value = responses[request.full_url]
            if isinstance(value, Exception):
                raise value
            return io.BytesIO(value)

        with patch.object(release_gate.urllib.request, 'urlopen', side_effect=response), \
                patch.object(release_assets.package, 'schema_version', return_value=9):
            return release_gate.github_release_state(
                'Lephiziel/findrail', release_gate.BETA1_VERSION, '8' * 40, 'test-token')

    def test_complete_beta1_is_historical_noop_with_immutable_schema_and_exact_eight_assets(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            sha, urls, responses = self.beta1_remote_fixture(root)
            self.assertEqual(self.check_remote_beta1(responses),
                             {'decision': 'noop', 'published_sha': sha})
            release_response = json.loads(responses[
                'https://api.github.com/repos/Lephiziel/findrail/releases/tags/v0.1.0-beta.1'])
            assets = [{'name': item['name']} for item in release_response['assets']]
            manifest = json.loads(responses[urls['release-manifest.json']])
            self.assertEqual(release_gate.existing_release_decision(
                release_gate.BETA1_VERSION, '8' * 40, sha,
                {'tag_sha': sha, 'tag_name': 'v0.1.0-beta.1', 'manifest': manifest,
                 'prerelease': True, 'draft': False},
                assets), 'noop')
            for tag_name in (None, 'v0.1.0-beta.2'):
                invalid_release = {'tag_sha': sha, 'manifest': manifest,
                                   'prerelease': True, 'draft': False}
                if tag_name is not None:
                    invalid_release['tag_name'] = tag_name
                with self.subTest(tag_name=tag_name), self.assertRaisesRegex(ValueError, 'tag name'):
                    release_gate.existing_release_decision(
                        release_gate.BETA1_VERSION, '8' * 40, sha,
                        invalid_release, assets)
            with self.assertRaisesRegex(ValueError, 'prerelease'):
                release_gate.existing_release_decision(
                    release_gate.BETA1_VERSION, '8' * 40, sha,
                    {'tag_sha': sha, 'tag_name': 'v0.1.0-beta.1', 'manifest': manifest,
                     'prerelease': False, 'draft': False},
                    assets)
            with self.assertRaisesRegex(ValueError, 'prerelease'):
                release_gate.existing_release_decision(
                    release_gate.BETA1_VERSION, '8' * 40, sha,
                    {'tag_sha': sha, 'tag_name': 'v0.1.0-beta.1', 'manifest': manifest,
                     'prerelease': True, 'draft': True},
                    assets)

    def test_beta1_remote_rejects_embedded_sha_schema_target_and_vsix_mismatches(self):
        changes = (dict(schema=7), dict(revision='6' * 40), dict(target_override='linux-arm64'))
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            sha, urls, original = self.beta1_remote_fixture(root)
            archive_name = 'findrail_0.1.0-beta.1_linux-amd64.tar.gz'
            for index, change in enumerate(changes):
                with self.subTest(change=change):
                    source_root = root / f'bad-{index}'
                    source_root.mkdir()
                    options = {'revision': sha, 'version': release_gate.BETA1_VERSION,
                               'schema': 8, **change}
                    malformed, _ = test_release_assets.ReleaseAssetTests().candidate(
                        source_root, **options)
                    responses = dict(original)
                    responses[urls[archive_name]] = (malformed / archive_name).read_bytes()
                    self.refresh_remote_hashes(responses, urls)
                    with self.assertRaises(ValueError):
                        self.check_remote_beta1(responses)

            vsix_case_root = root / 'vsix-case'
            vsix_case_root.mkdir()
            _, urls, responses = self.beta1_remote_fixture(vsix_case_root)
            vsix_url = urls[release_assets.VSIX]
            changed = io.BytesIO()
            with zipfile.ZipFile(io.BytesIO(responses[vsix_url])) as original_zip, \
                    zipfile.ZipFile(changed, 'w') as replacement:
                for item in original_zip.infolist():
                    data = original_zip.read(item)
                    if item.filename == 'extension/package.json':
                        metadata = json.loads(data)
                        metadata['publisher'] = 'untrusted-publisher'
                        data = json.dumps(metadata).encode()
                    replacement.writestr(item, data)
            responses[vsix_url] = changed.getvalue()
            self.refresh_remote_hashes(responses, urls)
            with self.assertRaisesRegex(ValueError, 'VSIX identity'):
                self.check_remote_beta1(responses)

    def test_beta1_remote_rejects_partial_tag_release_checksum_collision_and_api_error(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            _, urls, original = self.beta1_remote_fixture(root)
            base = 'https://api.github.com/repos/Lephiziel/findrail'
            release_url = base + '/releases/tags/v0.1.0-beta.1'
            ref_url = base + '/git/ref/tags/v0.1.0-beta.1'
            asset_url = urls['findrail_0.1.0-beta.1_windows-amd64.zip']
            for url, code in ((release_url, 500), (release_url, 404),
                              (ref_url, 404), (asset_url, 403)):
                with self.subTest(url=url, code=code):
                    responses = dict(original)
                    responses[url] = urllib.error.HTTPError(url, code, 'synthetic failure', {}, None)
                    with self.assertRaises(ValueError):
                        self.check_remote_beta1(responses)

            changed = dict(original)
            original_bytes = changed[asset_url]
            changed[asset_url] = original_bytes[:-1] + bytes([original_bytes[-1] ^ 1])
            with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                self.check_remote_beta1(changed)

            duplicate_asset = json.loads(original[release_url])
            duplicate_asset['assets'].append(duplicate_asset['assets'][0])
            collision = dict(original)
            collision[release_url] = json.dumps(duplicate_asset).encode()
            with self.assertRaisesRegex(ValueError, 'duplicate assets'):
                self.check_remote_beta1(collision)

    def test_beta1_absent_release_requires_two_404s_and_no_existing_draft(self):
        base = 'https://api.github.com/repos/Lephiziel/findrail'
        release_url = base + '/releases/tags/v0.1.0-beta.1'
        ref_url = base + '/git/ref/tags/v0.1.0-beta.1'
        list_url = base + '/releases?per_page=100&page=1'

        def response(request, **kwargs):
            if request.full_url == list_url:
                return io.BytesIO(b'[]')
            if request.full_url in (release_url, ref_url):
                raise urllib.error.HTTPError(request.full_url, 404, 'not found', {}, None)
            raise AssertionError(request.full_url)

        with patch.object(release_gate.urllib.request, 'urlopen', side_effect=response):
            self.assertEqual(release_gate.github_release_state(
                'Lephiziel/findrail', release_gate.BETA1_VERSION, 'a' * 40,
                'test-token'), {'decision': 'publish', 'published_sha': None})

        def draft_response(request, **kwargs):
            if request.full_url == list_url:
                return io.BytesIO(json.dumps([{'tag_name': 'v0.1.0-beta.1', 'draft': True}]).encode())
            raise urllib.error.HTTPError(request.full_url, 404, 'not found', {}, None)

        with patch.object(release_gate.urllib.request, 'urlopen', side_effect=draft_response):
            with self.assertRaisesRegex(ValueError, 'draft or incomplete'):
                release_gate.github_release_state(
                    'Lephiziel/findrail', release_gate.BETA1_VERSION, 'a' * 40,
                    'test-token')

    def test_unknown_versions_fail_before_absent_remote_state_can_become_publish(self):
        with patch.object(release_gate.urllib.request, 'urlopen') as opener:
            for version in ('0.1.0-beta.2', '0.1.0', '1.0.0'):
                with self.subTest(version=version), self.assertRaisesRegex(ValueError, 'unknown published'):
                    release_gate.github_release_state('Lephiziel/findrail', version, 'a' * 40, 'test-token')
            opener.assert_not_called()

    def promotion_request(self, **changes):
        request = {
            'repository': 'Lephiziel/findrail', 'event': 'workflow_dispatch',
            'ref': 'refs/heads/main', 'actor': 'Lephiziel',
            'workflow_sha': 'a' * 40, 'candidate_sha': 'a' * 40,
            'candidate_version': release_gate.BETA1_VERSION,
            'release_version': release_gate.BETA1_VERSION,
            'scope': 'limited-desktop-beta', 'publish': True,
            'candidate_run_id': '101', 'candidate_run_attempt': '2',
            'performance_run_id': '202', 'performance_run_attempt': '3',
            'handoff_run_id': '303', 'handoff_run_attempt': '4',
            'expected_handoff_artifact_digest': 'sha256:' + 'b' * 64,
            'expected_reports_zip_sha256': 'c' * 64,
            'expected_package_manifest_sha256': 'd' * 64,
            'expected_evidence_index_sha256': 'e' * 64,
            'expected_publication_notes_sha256': 'f' * 64,
            'expected_manifest_sha256': '1' * 64,
            'expected_checksums_sha256': '2' * 64,
            'expected_asset_inventory_sha256': '3' * 64,
        }
        request.update(changes)
        return request

    def test_promotion_write_sink_is_unreachable_for_untrusted_context_or_evidence(self):
        sink = unittest.mock.Mock()
        valid = self.promotion_request()
        invalid_requests = (
            {'event': 'push'}, {'repository': 'fork/findrail'}, {'ref': 'refs/heads/topic'},
            {'actor': 'contributor'}, {'scope': 'stable'},
            {'candidate_sha': 'a' * 12}, {'candidate_sha': 'b' * 40},
            {'candidate_version': '0.1.0-beta.2'}, {'release_version': '0.1.0-alpha.5'},
            {'expected_evidence_index_sha256': ''}, {'handoff_run_attempt': '0'},
        )
        for change in invalid_requests:
            with self.subTest(change=change), self.assertRaises(ValueError):
                release_gate.guarded_promotion_write(
                    self.promotion_request(**change), True, 'publish', 'a' * 40, sink)
        with self.assertRaisesRegex(ValueError, 'verification'):
            release_gate.guarded_promotion_write(valid, False, 'publish', 'a' * 40, sink)
        with self.assertRaisesRegex(ValueError, 'ambiguous'):
            release_gate.guarded_promotion_write(valid, True, 'error', 'a' * 40, sink)
        with self.assertRaisesRegex(ValueError, 'main moved'):
            release_gate.guarded_promotion_write(valid, True, 'publish', 'b' * 40, sink)
        dry_run = self.promotion_request(publish=False)
        self.assertEqual(release_gate.guarded_promotion_write(
            dry_run, True, 'publish', 'a' * 40, sink),
            {'decision': 'dry-run', 'published_sha': None})
        self.assertEqual(release_gate.guarded_promotion_write(
            valid, True, 'noop', 'b' * 40, sink, published_sha='9' * 40),
            {'decision': 'noop', 'published_sha': '9' * 40})
        sink.assert_not_called()

    def test_only_complete_explicit_owner_dispatch_reaches_the_mocked_write_sink(self):
        sink = unittest.mock.Mock()
        result = release_gate.guarded_promotion_write(
            self.promotion_request(), True, 'publish', 'a' * 40, sink)
        self.assertEqual(result['decision'], 'write_started')
        sink.assert_called_once_with()

    def test_draft_bytes_are_checked_twice_before_public_transition(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            sha, urls, _responses = self.beta1_remote_fixture(root)
            assets_dir = root / 'beta-release'
            names = release_gate._release_assets(release_gate.BETA1_VERSION)
            assets = [{'id': index + 1, 'name': name,
                       'size': (assets_dir / name).stat().st_size,
                       'url': f'https://api.github.com/repos/Lephiziel/findrail/releases/assets/{index + 1}'}
                      for index, name in enumerate(names)]
            data = {item['name']: (assets_dir / item['name']).read_bytes() for item in assets}
            draft = {'id': 77, 'tag_name': 'v0.1.0-beta.1', 'draft': True,
                     'prerelease': True, 'target_commitish': sha,
                     'body': 'Exact reviewed beta.1 notes',
                     'upload_url': 'https://uploads.github.com/repos/Lephiziel/findrail/releases/77/assets{?name,label}'}
            events = []

            def api_json(repository, path, token, method='GET', payload=None, accept='application/vnd.github+json'):
                events.append(('api', method, path))
                if method == 'POST' and path == '/releases':
                    self.assertTrue(payload['draft'])
                    self.assertTrue(payload['prerelease'])
                    self.assertEqual(payload['make_latest'], 'false')
                    self.assertEqual(payload['body'], 'Exact reviewed beta.1 notes')
                    return dict(draft)
                if method == 'GET' and path.endswith('/assets?per_page=100'):
                    return assets
                if method == 'GET' and path == '/releases/77':
                    return draft
                if method == 'PATCH' and path == '/releases/77':
                    self.assertEqual(payload, {'draft': False, 'prerelease': True,
                                               'make_latest': 'false'})
                    return {'draft': False, 'prerelease': True}
                raise AssertionError((method, path))

            def upload(upload_url, name, path, token):
                events.append(('upload', name))
                return {'name': name, 'size': pathlib.Path(path).stat().st_size}

            def download(asset, token, maximum=512 << 20):
                events.append(('download', asset['name']))
                return data[asset['name']]

            with patch.object(release_gate, '_api_json', side_effect=api_json), \
                    patch.object(release_gate, '_api_upload', side_effect=upload), \
                    patch.object(release_gate, '_download_api_asset', side_effect=download):
                release_gate._publish_draft_then_transition(
                    assets_dir, b'Exact reviewed beta.1 notes',
                    self.promotion_request(workflow_sha=sha, candidate_sha=sha), 'test-token')
            self.assertEqual([event[1] for event in events if event[0] == 'upload'], names)
            self.assertEqual(len([event for event in events if event[0] == 'download']), 16)
            patch_index = next(i for i, event in enumerate(events)
                               if event[:2] == ('api', 'PATCH'))
            self.assertEqual(patch_index, len(events) - 1)

            events.clear()
            changed = data[names[0]]
            data[names[0]] = changed[:-1] + bytes([changed[-1] ^ 1])
            with patch.object(release_gate, '_api_json', side_effect=api_json), \
                    patch.object(release_gate, '_api_upload', side_effect=upload), \
                    patch.object(release_gate, '_download_api_asset', side_effect=download):
                with self.assertRaises(ValueError):
                    release_gate._publish_draft_then_transition(
                        assets_dir, b'Exact reviewed beta.1 notes',
                        self.promotion_request(workflow_sha=sha, candidate_sha=sha), 'test-token')
            self.assertFalse(any(event[:2] == ('api', 'PATCH') for event in events))

    def test_remote_alpha5_verifies_tag_and_all_bytes_after_source_advances(self):
        with tempfile.TemporaryDirectory() as temp:
            sha, urls, responses = self.alpha5_remote_fixture(pathlib.Path(temp))
            self.assertEqual(self.check_remote_alpha5(responses), 'noop')
            base = 'https://api.github.com/repos/Lephiziel/findrail'
            tag_object = 'd' * 40
            responses[base + '/git/ref/tags/v0.1.0-alpha.5'] = json.dumps({
                'object': {'type': 'tag', 'sha': tag_object}}).encode()
            responses[base + '/git/tags/' + tag_object] = json.dumps({
                'object': {'type': 'commit', 'sha': sha}}).encode()
            self.assertEqual(self.check_remote_alpha5(responses), 'noop')
            archive_url = urls['findrail_0.1.0-alpha.5_linux-amd64.tar.gz']
            original_bytes = responses[archive_url]
            responses[archive_url] = original_bytes[:-1] + bytes([original_bytes[-1] ^ 1])
            with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                self.check_remote_alpha5(responses)

    def test_remote_alpha5_rejects_wrong_embedded_archive_despite_matching_hashes(self):
        changes = (dict(schema=7), dict(schema=9), dict(revision='f' * 40),
                   dict(dirty=True), dict(metadata_version='0.1.0-alpha.4'),
                   dict(target_override='linux-arm64'))
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            sha, urls, original = self.alpha5_remote_fixture(root)
            name = 'findrail_0.1.0-alpha.5_linux-amd64.tar.gz'
            for i, change in enumerate(changes):
                with self.subTest(change=change):
                    fixture_root = root / str(i)
                    fixture_root.mkdir()
                    options = {'revision': sha, 'schema': 8, **change}
                    malformed, _ = test_release_assets.ReleaseAssetTests().candidate(
                        fixture_root, version='0.1.0-alpha.5', **options)
                    responses = dict(original)
                    responses[urls[name]] = (malformed / name).read_bytes()
                    self.refresh_remote_hashes(responses, urls)
                    with self.assertRaises(ValueError):
                        self.check_remote_alpha5(responses)

    def test_remote_alpha5_rejects_wrong_vsix_despite_matching_hashes(self):
        with tempfile.TemporaryDirectory() as temp:
            _, urls, responses = self.alpha5_remote_fixture(pathlib.Path(temp))
            url = urls[release_assets.VSIX]
            changed = io.BytesIO()
            with zipfile.ZipFile(io.BytesIO(responses[url])) as original, \
                    zipfile.ZipFile(changed, 'w') as replacement:
                for item in original.infolist():
                    data = original.read(item)
                    if item.filename == 'extension/package.json':
                        metadata = json.loads(data)
                        metadata['publisher'] = 'another-publisher'
                        data = json.dumps(metadata).encode()
                    replacement.writestr(item, data)
            responses[url] = changed.getvalue()
            self.refresh_remote_hashes(responses, urls)
            with self.assertRaisesRegex(ValueError, 'VSIX identity'):
                self.check_remote_alpha5(responses)

    def test_remote_alpha5_api_error_or_ambiguous_state_is_not_noop(self):
        with tempfile.TemporaryDirectory() as temp:
            _, urls, original = self.alpha5_remote_fixture(pathlib.Path(temp))
            base = 'https://api.github.com/repos/Lephiziel/findrail'
            release_url = base + '/releases/tags/v0.1.0-alpha.5'
            ref_url = base + '/git/ref/tags/v0.1.0-alpha.5'
            archive_url = urls['findrail_0.1.0-alpha.5_windows-amd64.zip']
            for url, code in ((release_url, 500), (release_url, 404),
                              (ref_url, 404), (archive_url, 403)):
                with self.subTest(url=url, code=code):
                    responses = dict(original)
                    responses[url] = urllib.error.HTTPError(url, code, 'synthetic failure', {}, None)
                    with self.assertRaises(ValueError):
                        self.check_remote_alpha5(responses)

    def test_alpha4_rejects_wrong_schema_revision_and_incomplete_assets(self):
        sha, release, assets = self.alpha4_release()
        for key, value in [('schema_version', 6), ('schema_version', 8),
                           ('schema_version', 7.0), ('source_revision', 'd' * 40),
                           ('app_version', '0.1.0-alpha.3')]:
            changed = {**release, 'manifest': {**release['manifest'], key: value}}
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                release_gate.existing_release_decision('0.1.0-alpha.4', 'c' * 40, sha, changed, assets)
        for changed_assets in (assets[:-1], assets + [assets[0]]):
            with self.assertRaises(ValueError):
                release_gate.existing_release_decision('0.1.0-alpha.4', 'c' * 40, sha, release, changed_assets)

    def test_remote_alpha4_checks_all_archive_bytes_when_main_and_schema_advance(self):
        sha = 'b' * 40
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            fixture = test_release_assets.ReleaseAssetTests()
            source, _ = fixture.candidate(root, revision=sha, schema=7)
            output = root / 'release'
            release_assets.assemble(source, output, '0.1.0-alpha.4', sha)
            base = 'https://api.github.com/repos/Lephiziel/findrail'
            assets = [{'name': path.name, 'browser_download_url': 'https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.4/' + path.name,
                       'size': path.stat().st_size}
                      for path in output.iterdir()]
            responses = {
                base + '/releases/tags/v0.1.0-alpha.4': json.dumps({'assets': assets}).encode(),
                base + '/git/ref/tags/v0.1.0-alpha.4': json.dumps({'object': {'type': 'commit', 'sha': sha}}).encode(),
                **{'https://github.com/Lephiziel/findrail/releases/download/v0.1.0-alpha.4/' + path.name:
                   path.read_bytes() for path in output.iterdir()}}

            def response(request, **kwargs):
                return io.BytesIO(responses[request.full_url])

            with patch.object(release_gate.urllib.request, 'urlopen', side_effect=response), \
                    patch.object(release_assets.package, 'schema_version', return_value=8):
                self.assertEqual(release_gate.github_release_decision(
                    'Lephiziel/findrail', '0.1.0-alpha.4', 'c' * 40, 'test-token'), 'noop')
                archive = next(asset['browser_download_url'] for asset in assets
                               if asset['name'].endswith('.tar.gz'))
                original_bytes = responses[archive]
                responses[archive] = original_bytes[:-1] + bytes([original_bytes[-1] ^ 1])
                with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                    release_gate.github_release_decision(
                        'Lephiziel/findrail', '0.1.0-alpha.4', 'c' * 40, 'test-token')


if __name__ == '__main__':
    unittest.main()
