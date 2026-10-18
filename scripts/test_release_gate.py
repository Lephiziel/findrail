import pathlib
import sys
import unittest
import io
import json
import tempfile
from unittest.mock import patch

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import release_gate
import release_assets
import test_release_assets


class ReleaseGateTests(unittest.TestCase):
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
        for key, value in [('schema_version', 7), ('source_revision', '3' * 40), ('app_version', '0.1.0-alpha.4')]:
            bad = {**release, 'manifest': {**manifest, key: value}}
            with self.subTest(key=key), self.assertRaises(ValueError):
                release_gate.existing_release_decision('0.1.0-alpha.5', 'c' * 40, sha, bad, assets)
        with self.assertRaises(ValueError):
            release_gate.existing_release_decision('0.1.0-alpha.5', 'c' * 40, sha, release, assets[:-1])

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
            assets = [{'name': path.name, 'browser_download_url': 'https://example.invalid/' + path.name}
                      for path in output.iterdir()]
            responses = {
                base + '/releases/tags/v0.1.0-alpha.4': json.dumps({'assets': assets}).encode(),
                base + '/git/ref/tags/v0.1.0-alpha.4': json.dumps({'object': {'type': 'commit', 'sha': sha}}).encode(),
                **{'https://example.invalid/' + path.name: path.read_bytes() for path in output.iterdir()}}

            def response(request, **kwargs):
                return io.BytesIO(responses[request.full_url])

            with patch.object(release_gate.urllib.request, 'urlopen', side_effect=response), \
                    patch.object(release_assets.package, 'schema_version', return_value=8):
                self.assertEqual(release_gate.github_release_decision(
                    'Lephiziel/findrail', '0.1.0-alpha.4', 'c' * 40, 'test-token'), 'noop')
                archive = next(asset['browser_download_url'] for asset in assets
                               if asset['name'].endswith('.tar.gz'))
                responses[archive] += b'corrupt transfer'
                with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                    release_gate.github_release_decision(
                        'Lephiziel/findrail', '0.1.0-alpha.4', 'c' * 40, 'test-token')


if __name__ == '__main__':
    unittest.main()
