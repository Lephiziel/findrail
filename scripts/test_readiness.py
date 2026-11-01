import copy
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import candidate_record
import readiness
import test_release_assets


class ReadinessTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.scratch = tempfile.TemporaryDirectory()
        cls.addClassCleanup(cls.scratch.cleanup)
        records, assets, browser, sha, journey = test_release_assets.ReleaseAssetTests().summary_fixture(pathlib.Path(cls.scratch.name))
        common = next(c for c in journey['cases'] if c['case'] == 'packaged_browser_journey.py')['details']
        common['cases'] += [{'case': name, 'status': 'passed', 'elapsed_seconds': 0.1} for name in readiness.FRESHNESS_CASES]
        browser.write_text(json.dumps(journey))
        cls.summary = candidate_record.summarize(records, assets, browser, sha, '0.1.0-alpha.5')

    def setUp(self):
        self.candidate = copy.deepcopy(self.summary)
        self.sha = self.candidate['candidate_sha']
        self.native = {r['target']: r for r in self.candidate['native']}

    def recovery(self, **overrides):
        record = {'case': 'candidate_refresh_sigkill', 'status': 'passed', 'revision': self.sha,
                  'schema': 8, 'platform': 'linux', 'phase': 'verified_old_or_new_commit_unknown',
                  'binary_sha256': self.native['linux-amd64']['binary_sha256'], 'duration_ms': 30}
        record.update(overrides)
        return {'kind': 'crash_recovery', 'measurement': record}

    def scale(self, kind):
        count, _ = readiness.SCALE_FIXTURES[kind]
        # Historical measurements are only parser fixtures here, never evidence
        # about this synthetic archive's performance.
        path = pathlib.Path(__file__).parents[1] / 'docs/resource-reports' / f'2026-10-18-task17-{count}.json'
        record = json.loads(path.read_text())
        record['binary'].update(revision=self.sha, version=self.candidate['candidate_version'],
                                sha256=self.native['linux-amd64']['binary_sha256'])
        return {'kind': kind, 'measurement': record, 'baseline_role': 'reference'}

    def manual(self, kind):
        target = 'windows-amd64' if kind == 'windows_first_run' else 'darwin-arm64'
        return {'kind': kind, 'evidence_type': 'manual', 'status': 'passed', 'target': target,
                'candidate_sha': self.sha, 'candidate_version': self.candidate['candidate_version'],
                'archive_sha256': self.native[target]['archive']['sha256'],
                'reference': 'https://github.com/Lephiziel/findrail/issues/100',
                'cases': [{'case': case, 'status': 'passed', 'elapsed_seconds': 1} for case in readiness.MANUAL_CASES]}

    def usage(self):
        return {'kind': 'voluntary_usage', 'candidate_sha': self.sha,
                'candidate_version': self.candidate['candidate_version'], 'reports': [
                    {'reference': 'https://github.com/Lephiziel/findrail/issues/101', 'evidence_type': 'manual',
                     'return_use': True, 'retrieval_one': 'found', 'retrieval_two': 'not_found',
                     'collection_restart': 'restored'}]}

    def all_records(self):
        return [self.recovery(), self.scale('scale_10k'), self.scale('scale_100k'),
                self.manual('windows_first_run'), self.manual('apple_silicon_first_run'), self.usage()]

    def test_actual_strict_summary_and_recovery_pass_software_but_leave_release_pending(self):
        result = readiness.classify(self.candidate, [self.recovery()])
        self.assertEqual((result['software_status'], result['release_readiness']), ('passed', 'pending'))
        self.assertFalse(result['final_release'])
        self.assertEqual(result['evidence']['installer_native'], 'passed')
        self.assertEqual(result['evidence']['freshness_browser'], 'passed')
        self.assertIn('scale_100k_pending', result['gaps'])
        self.assertEqual(readiness.classify(self.candidate)['software_status'], 'pending')

    def test_bare_passed_summary_and_self_selected_cases_never_certify(self):
        bare = {k: self.candidate[k] for k in ('candidate_sha', 'candidate_version', 'status', 'source_clean')}
        result = readiness.classify(bare, self.all_records())
        self.assertEqual(result['release_readiness'], 'blocked')
        self.assertEqual(result['software_status'], 'failed')
        self.assertIsNone(result['candidate_sha'])

    def test_complete_evidence_is_only_eligible_for_owner_decision(self):
        result = readiness.classify(self.candidate, self.all_records())
        self.assertEqual(result['gaps'], [])
        self.assertEqual(result['release_readiness'], 'eligible-for-owner-decision')
        self.assertFalse(result['final_release'])

    def test_expected_head_and_version_are_not_inferred_from_the_supplied_file(self):
        for kwargs in ({'candidate_sha': '0'*40}, {'candidate_version': '0.1.0-alpha.4'}):
            with self.subTest(kwargs=kwargs):
                self.assertEqual(readiness.classify(self.candidate, [self.recovery()], **kwargs)['software_status'], 'failed')

    def test_missing_duplicate_skipped_and_unmeasured_existing_coverage_blocks(self):
        for mutate in (lambda c: c['journey']['cases'].pop(),
                       lambda c: c['journey']['cases'].append(copy.deepcopy(c['journey']['cases'][0])),
                       lambda c: c['journey']['cases'][0].update(status='skipped'),
                       lambda c: c['journey']['cases'][0].update(duration_seconds=float('nan')),
                       lambda c: c['native'].pop(),
                       lambda c: next(a for a in c['assets'] if a['basename'].endswith('linux-amd64.tar.gz')).update(sha256='0'*64),
                       lambda c: c['native'][0].update(observed_target='other'),
                       lambda c: c['native'][0]['installer_lifecycle']['cases'].pop(),
                       lambda c: next(n for n in c['native'] if n['target'] == 'linux-amd64').update(resources={})):
            candidate = copy.deepcopy(self.candidate)
            mutate(candidate)
            with self.subTest(candidate=candidate.get('candidate_sha')):
                self.assertEqual(readiness.classify(candidate)['release_readiness'], 'blocked')

    def test_fixed_freshness_cases_cannot_be_replaced_by_report_required_cases(self):
        common = next(c for c in self.candidate['journey']['cases'] if c['case'] == 'packaged_browser_journey.py')['details']
        common['cases'] = common['cases'][:-2]
        common['required_cases'] = []
        self.assertEqual(readiness.classify(self.candidate)['software_status'], 'failed')

    def test_recovery_requires_exact_candidate_binary_phase_and_measured_completion(self):
        for changes in ({'binary_sha256': '0'*64}, {'revision': '0'*40}, {'phase': 'reopened'},
                        {'schema': 7}, {'status': 'interrupted'}, {'duration_ms': -1},
                        {'duration_ms': float('inf')}, {'duration_ms': True}, {'platform': 'darwin'}):
            with self.subTest(changes=changes):
                result = readiness.classify(self.candidate, [self.recovery(**changes)])
                self.assertEqual((result['software_status'], result['release_readiness']), ('failed', 'blocked'))

    def test_duplicate_unknown_and_malformed_supplemental_records_block(self):
        for records in ([self.recovery(), self.recovery()], [{'kind': 'untrusted private text'}],
                        [{'kind': []}], [None], {'kind': 'crash_recovery'}):
            with self.subTest(records=records):
                result = readiness.classify(self.candidate, records)
                self.assertEqual(result['release_readiness'], 'blocked')
                self.assertNotIn('untrusted private text', json.dumps(result))

    def test_scale_must_be_a_complete_final_binary_run_on_the_fixed_corpus(self):
        mutations = (lambda r: r['binary'].update(sha256='0'*64),
                     lambda r: r['binary'].update(revision='0'*40),
                     lambda r: r['binary'].update(version='dev'),
                     lambda r: r['fixture'].update(corpus_sha256='0'*64),
                     lambda r: r['fixture'].update(documents_requested=100),
                     lambda r: r['workload'].update(indexed_documents=100),
                     lambda r: r['metrics']['warm_http_request_latency_ms'].update(p95=float('nan')),
                     lambda r: r['metrics']['refresh_cycle'].update(canceled_scan_preserved_previous_content=False),
                     lambda r: r['metrics']['paging_window']['page_sizes'].pop('50'),
                     lambda r: r.update(status='failed'))
        for mutate in mutations:
            item = self.scale('scale_10k')
            mutate(item['measurement'])
            result = readiness.classify(self.candidate, [self.recovery(), item])
            self.assertEqual(result['release_readiness'], 'blocked')
            self.assertEqual(result['evidence']['scale_10k'], 'invalid')
            self.assertEqual(result['software_status'], 'passed')

    def test_a_different_baseline_binary_requires_an_explicit_reference_role(self):
        item = self.scale('scale_10k')
        self.assertNotEqual(item['measurement']['comparison']['binary']['sha256'], self.native['linux-amd64']['binary_sha256'])
        self.assertEqual(readiness.classify(self.candidate, [item])['evidence']['scale_10k'], 'passed')
        item.pop('baseline_role')
        self.assertEqual(readiness.classify(self.candidate, [item])['release_readiness'], 'blocked')

    def test_manual_reports_require_correct_archive_cases_and_manual_reference(self):
        for mutate in (lambda r: r.update(evidence_type='automated'), lambda r: r.update(archive_sha256='0'*64),
                       lambda r: r.update(target='linux-amd64'), lambda r: r['cases'].pop(),
                       lambda r: r.update(reference='file:///private/report')):
            item = self.manual('windows_first_run')
            mutate(item)
            self.assertEqual(readiness.classify(self.candidate, [item])['release_readiness'], 'blocked')

    def test_usage_requires_actual_manual_task_and_return_use_fields(self):
        for mutate in (lambda r: r.update(reports=['placeholder']),
                       lambda r: r['reports'][0].update(evidence_type='automated'),
                       lambda r: r['reports'][0].update(return_use=False),
                       lambda r: r['reports'][0].update(retrieval_one='not_tried'),
                       lambda r: r['reports'].append(copy.deepcopy(r['reports'][0]))):
            item = self.usage()
            mutate(item)
            self.assertEqual(readiness.classify(self.candidate, [item])['release_readiness'], 'blocked')

    def test_reader_rejects_duplicate_keys_nonfinite_numbers_and_oversized_input(self):
        with tempfile.TemporaryDirectory() as temp:
            path = pathlib.Path(temp) / 'record.json'
            for value in ('{"status":"failed","status":"passed"}', '{"duration":NaN}', '{"duration":1e999}', '[]', ' '*(1<<20)+'{}'):
                path.write_text(value)
                with self.subTest(value=value[:60]), self.assertRaises(ValueError):
                    readiness._json(path)

    def test_cli_exit_code_distinguishes_pending_from_invalid_and_output_is_content_free(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            candidate, recovery, output = (root / name for name in ('candidate.json', 'recovery.json', 'output.json'))
            candidate.write_text(json.dumps(self.candidate))
            recovery.write_text(json.dumps(self.recovery()['measurement']))
            argv = [sys.executable, '-B', str(pathlib.Path(readiness.__file__)), '--candidate', str(candidate),
                    '--candidate-sha', self.sha, '--candidate-version', self.candidate['candidate_version'],
                    '--recovery', str(recovery), '--output', str(output)]
            result = subprocess.run(argv, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads(output.read_text())['software_status'], 'passed')
            candidate.write_text('{"status":"passed","private":"do not retain this"}')
            result = subprocess.run(argv, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertNotIn('do not retain this', output.read_text())
            self.assertEqual(json.loads(output.read_text())['release_readiness'], 'blocked')
            candidate.write_text('{"status":"passed","status":"failed"}')
            result = subprocess.run(argv, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertEqual(json.loads(output.read_text())['gaps'], ['input_invalid'])


if __name__ == '__main__':
    unittest.main()
