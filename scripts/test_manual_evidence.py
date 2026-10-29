import copy
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import manual_evidence
import readiness
import test_readiness


class ManualEvidenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        test_readiness.ReadinessTests.setUpClass()
        cls.candidate = test_readiness.ReadinessTests.summary
        cls.native = readiness.strict_candidate(cls.candidate)

    def setUp(self):
        self.sha = self.candidate['candidate_sha']
        self.version = self.candidate['candidate_version']
        self.ref = 'sha256:' + 'a' * 64

    def manual(self):
        return {'kind': 'windows_first_run', 'reference': self.ref, 'candidate_sha': self.sha,
                'candidate_version': self.version, 'target': 'windows-amd64', 'archive_sha256':
                self.native['windows-amd64']['archive']['sha256'], 'evidence_type': 'manual', 'status': 'passed',
                'cases': [{'case': name, 'status': 'passed', 'elapsed_seconds': 0.0} for name in readiness.MANUAL_CASES]}

    def usage(self, **changes):
        record = {'kind': 'voluntary_usage', 'reference': self.ref, 'candidate_sha': self.sha,
                  'candidate_version': self.version, 'evidence_type': 'manual', 'return_use': True,
                  'retrieval_one': 'not_found', 'retrieval_two': 'found', 'collection_restart': 'not_restored'}
        record.update(changes)
        return record

    def linux_first_run(self, **changes):
        report = {'kind': 'linux_first_run', 'reference': '', 'candidate_sha': self.sha,
                  'candidate_version': self.version, 'target': 'linux-amd64',
                  'archive_sha256': self.native['linux-amd64']['archive']['sha256'],
                  'evidence_type': 'manual', 'status': 'not_run', 'schema_version': self.candidate['schema_version'],
                  'client_api_version': self.candidate['client_api_version'], 'cases': [
                      {'id': name, 'status': 'not_run', 'elapsed_seconds': None, 'observation': ''}
                      for name in readiness.MANUAL_CASES]}
        report.update(changes)
        return report

    def test_full_readiness_records_validate_unchanged(self):
        manual = self.manual()
        complete = manual_evidence.validate(manual, self.candidate, self.native)
        exported = {'kind': manual['kind'], **{k: manual[k] for k in ('evidence_type', 'target', 'archive_sha256', 'reference')},
                    'status': 'passed', 'candidate_sha': self.sha, 'candidate_version': self.version,
                    'cases': [{'case': c['case'], 'status': c['status'], 'elapsed_seconds': c['elapsed_seconds']} for c in manual['cases']]}
        readiness.check_manual(exported, self.candidate, self.native)
        self.assertEqual(complete, 'complete')
        usage = self.usage()
        self.assertEqual(manual_evidence.validate(usage, self.candidate, self.native), 'complete')
        readiness.check_usage({'kind': 'voluntary_usage', 'candidate_sha': self.sha, 'candidate_version': self.version,
                               'reports': [{k: usage[k] for k in ('reference', 'evidence_type', 'return_use', 'retrieval_one', 'retrieval_two', 'collection_restart')}]}, self.candidate)

    def test_identity_target_archive_and_null_zero(self):
        for key, value in (('candidate_sha', '0'*40), ('candidate_version', 'wrong'),
                           ('target', 'darwin-arm64'), ('archive_sha256', '0'*64)):
            report = self.manual()
            report[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                manual_evidence.validate(report, self.candidate, self.native)
        report = self.manual()
        report['cases'][0]['elapsed_seconds'] = None
        self.assertEqual(manual_evidence.validate(report, self.candidate, self.native), 'incomplete')
        report['cases'][0]['elapsed_seconds'] = 0
        self.assertEqual(manual_evidence.validate(report, self.candidate, self.native), 'complete')
        for status in ('failed', 'not_run'):
            report = self.manual()
            report['cases'][0]['status'] = status
            self.assertEqual(manual_evidence.validate(report, self.candidate, self.native), 'failed' if status == 'failed' else 'incomplete')

    def test_usage_keeps_misses_failures_and_requires_return(self):
        report = self.usage()
        self.assertEqual(manual_evidence.validate(report, self.candidate, self.native), 'complete')
        self.assertEqual(manual_evidence.validate(self.usage(return_use=False), self.candidate, self.native), 'incomplete')
        self.assertEqual(manual_evidence.validate(self.usage(retrieval_two='not_tried'), self.candidate, self.native), 'incomplete')

    def test_linux_first_run_blank_is_incomplete_and_never_voluntary_return(self):
        blank = self.linux_first_run()
        self.assertEqual(manual_evidence.validate(blank, self.candidate, self.native), 'incomplete')
        self.assertEqual(blank['kind'], 'linux_first_run')
        self.assertFalse(self.usage(return_use=False)['return_use'])
        self.assertEqual(manual_evidence.validate(self.usage(return_use=False), self.candidate, self.native), 'incomplete')
        completed = self.linux_first_run(reference=self.ref, status='passed', cases=[
            {'id': name, 'status': 'passed', 'elapsed_seconds': 0.0, 'observation': ''}
            for name in readiness.MANUAL_CASES])
        self.assertEqual(manual_evidence.validate(completed, self.candidate, self.native), 'complete')
        with self.assertRaises(ValueError):
            manual_evidence.validate({**completed, 'target': 'darwin-arm64'}, self.candidate, self.native)
        self.assertNotIn('linux_first_run', readiness.KINDS)

    def test_linux_intake_writes_separate_observations_and_clears_stale_success(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            candidate, report_path, out = (root / name for name in ('candidate.json', 'linux.json', 'out'))
            candidate.write_text(json.dumps(self.candidate))
            report_path.write_text(json.dumps(self.linux_first_run()))
            command = [sys.executable, str(pathlib.Path(manual_evidence.__file__)), '--candidate', str(candidate),
                       '--report', str(report_path), '--output', str(out)]
            result = subprocess.run(command, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 1)
            summary = json.loads((out / 'intake.json').read_text())
            self.assertEqual(summary['status'], 'incomplete')
            self.assertEqual(summary['counts']['incomplete'], 1)
            self.assertEqual(summary['outputs'], ['linux-observations.json'])
            self.assertFalse((out / 'supplemental.json').exists())
            observations = json.loads((out / 'linux-observations.json').read_text())
            self.assertEqual(observations['format_version'], 1)
            self.assertEqual(observations['kind'], 'linux_first_run')
            self.assertEqual(observations['observations'][0]['status'], 'incomplete')
            self.assertNotIn('"observation":', (out / 'linux-observations.json').read_text())

            complete = self.linux_first_run(reference=self.ref, status='passed', cases=[
                {'id': name, 'status': 'passed', 'elapsed_seconds': 0.0, 'observation': 'private detail'}
                for name in readiness.MANUAL_CASES])
            report_path.write_text(json.dumps(complete))
            result = subprocess.run(command, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            summary = json.loads((out / 'intake.json').read_text())
            self.assertEqual(summary['status'], 'recorded')
            self.assertEqual(summary['counts']['complete'], 1)
            self.assertEqual(summary['outputs'], ['linux-observations.json'])
            self.assertFalse((out / 'supplemental.json').exists())
            self.assertNotIn('private detail', (out / 'linux-observations.json').read_text())

            failed = self.linux_first_run(reference=self.ref, status='failed', cases=[
                {'id': name, 'status': 'passed', 'elapsed_seconds': 0.0, 'observation': 'private failure detail'}
                for name in readiness.MANUAL_CASES])
            failed['cases'][3]['status'] = 'failed'
            report_path.write_text(json.dumps(failed))
            result = subprocess.run(command, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 1)
            summary = json.loads((out / 'intake.json').read_text())
            self.assertEqual(summary['status'], 'blocked')
            self.assertEqual(summary['counts']['failed'], 1)
            self.assertEqual(summary['outputs'], ['linux-observations.json'])
            observations = json.loads((out / 'linux-observations.json').read_text())
            self.assertEqual(observations['observations'][0]['status'], 'failed')
            self.assertEqual(observations['observations'][0]['cases'][3]['status'], 'failed')
            self.assertNotIn('private failure detail', (out / 'linux-observations.json').read_text())
            self.assertFalse((out / 'supplemental.json').exists())

            complete['archive_sha256'] = '0' * 64
            report_path.write_text(json.dumps(complete))
            result = subprocess.run(command, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 1)
            summary = json.loads((out / 'intake.json').read_text())
            self.assertEqual(summary['counts']['mismatch'], 1)
            self.assertFalse((out / 'linux-observations.json').exists())
            self.assertFalse((out / 'supplemental.json').exists())

    def test_incomplete_usage_intake_retains_only_outcome_facts(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            candidate, report, out = (root / name for name in ('candidate.json', 'usage.json', 'out'))
            candidate.write_text(json.dumps(self.candidate))
            report.write_text(json.dumps(self.usage(return_use=False, retrieval_one='not_found',
                                                     retrieval_two='not_tried', collection_restart='restored',
                                                     status='incomplete', obstacle='PRIVATE prose')))
            result = subprocess.run([sys.executable, str(pathlib.Path(manual_evidence.__file__)),
                                     '--candidate', str(candidate), '--report', str(report), '--output', str(out)],
                                    capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 1)
            summary = json.loads((out / 'intake.json').read_text())
            self.assertEqual(summary['counts']['incomplete'], 1)
            self.assertEqual(summary['outputs'], ['usage-observations.json'])
            observations = json.loads((out / 'usage-observations.json').read_text())['observations'][0]
            self.assertEqual(observations['retrieval_one'], 'not_found')
            self.assertEqual(observations['retrieval_two'], 'not_tried')
            self.assertEqual(observations['collection_restart'], 'restored')
            self.assertFalse(observations['return_use'])
            self.assertNotIn('PRIVATE', (out / 'usage-observations.json').read_text())
            self.assertFalse((out / 'supplemental.json').exists())

            failed = self.usage(return_use=False, retrieval_one='not_found', retrieval_two='found',
                                 collection_restart='not_restored', status='failed', obstacle='PRIVATE failure')
            report.write_text(json.dumps(failed))
            result = subprocess.run([sys.executable, str(pathlib.Path(manual_evidence.__file__)),
                                     '--candidate', str(candidate), '--report', str(report), '--output', str(out)],
                                    capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 1)
            summary = json.loads((out / 'intake.json').read_text())
            self.assertEqual(summary['status'], 'blocked')
            self.assertEqual(summary['counts']['failed'], 1)
            self.assertEqual(summary['outputs'], ['usage-observations.json'])
            observations = json.loads((out / 'usage-observations.json').read_text())['observations'][0]
            self.assertEqual(observations['status'], 'failed')
            self.assertEqual(observations['retrieval_one'], 'not_found')
            self.assertEqual(observations['retrieval_two'], 'found')
            self.assertEqual(observations['collection_restart'], 'not_restored')
            self.assertNotIn('PRIVATE', (out / 'usage-observations.json').read_text())
            self.assertFalse((out / 'supplemental.json').exists())

    def test_task24_acceptance_kit_report_shape_and_aggregate_status(self):
        report = self.manual()
        report.update(status='passed', observer='Private Name', date='today', notes='private notes',
                      schema_version=self.candidate['schema_version'],
                      client_api_version=self.candidate['client_api_version'])
        report['cases'] = [{'id': case['case'], 'status': 'passed', 'elapsed_seconds': 0,
                            'observation': 'private observation'} for case in report['cases']]
        self.assertEqual(manual_evidence.validate(report, self.candidate, self.native), 'complete')
        report['status'] = 'failed'
        self.assertEqual(manual_evidence.validate(report, self.candidate, self.native), 'failed')
        report['status'] = 'not_run'
        self.assertEqual(manual_evidence.validate(report, self.candidate, self.native), 'incomplete')
        report['status'] = 'passed'
        report['manifest_sha256'] = '0' * 64
        with self.assertRaises(ValueError):
            manual_evidence.validate(report, self.candidate, self.native)

    def test_task24_overall_failed_cannot_export_as_passed(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            candidate, report_path, out = (root / n for n in ('candidate.json', 'report.json', 'out'))
            candidate.write_text(json.dumps(self.candidate))
            report = self.manual()
            report.update(status='failed', observer='private observer', notes='private blocker')
            report['cases'] = [{'id': case['case'], 'status': 'passed', 'elapsed_seconds': 0,
                                'observation': 'private observation'} for case in report['cases']]
            report_path.write_text(json.dumps(report))
            result = subprocess.run([sys.executable, str(pathlib.Path(manual_evidence.__file__)),
                                     '--candidate', str(candidate), '--report', str(report_path),
                                     '--output', str(out)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 1)
            summary = json.loads((out/'intake.json').read_text())
            self.assertEqual(summary['counts']['failed'], 1)
            self.assertFalse((out/'supplemental.json').exists())
            self.assertNotIn('private', result.stdout + result.stderr + (out/'intake.json').read_text())

    def test_duplicate_keys_nonfinite_and_unknown_fields_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            p = pathlib.Path(temp) / 'input.json'
            for text in ('{"x":1,"x":2}', '{"x":NaN}', '{"x":1e999}', ' '*(1<<20)+'{}'):
                p.write_text(text)
                with self.assertRaises((ValueError, json.JSONDecodeError)):
                    manual_evidence.parse(p)
        with self.assertRaises(ValueError):
            manual_evidence.validate({**self.manual(), 'private_path': '/secret'}, self.candidate, self.native)

    def test_cli_sanitizes_output_deduplicates_and_removes_stale_output(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            c, r1, r2, out = (root / name for name in ('candidate.json', 'one.json', 'two.json', 'out'))
            c.write_text(json.dumps(self.candidate))
            report = self.usage(obstacle='PRIVATE FREE TEXT')
            r1.write_text(json.dumps(report))
            r2.write_text(json.dumps(report))
            cmd = [sys.executable, str(pathlib.Path(manual_evidence.__file__)), '--candidate', str(c), '--report', str(r1), '--report', str(r2), '--output', str(out)]
            result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 1)
            self.assertNotIn('PRIVATE FREE TEXT', result.stdout + result.stderr + (out/'intake.json').read_text())
            self.assertFalse((out/'supplemental.json').exists())
            report = self.usage()
            r2.write_text(json.dumps(report))
            result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 1)
            self.assertFalse((out/'supplemental.json').exists())

    def test_cli_exports_misses_and_failures_without_prose_and_rejects_mixed_kinds(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            candidate, usage, manual, out = (root / n for n in ('candidate.json', 'usage.json', 'manual.json', 'out'))
            candidate.write_text(json.dumps(self.candidate))
            usage.write_text(json.dumps(self.usage(obstacle='PRIVATE NOTES')))
            command = [sys.executable, str(pathlib.Path(manual_evidence.__file__)), '--candidate', str(candidate),
                       '--report', str(usage), '--output', str(out)]
            result = subprocess.run(command, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            exported = json.loads((out/'supplemental.json').read_text())
            self.assertEqual(exported['reports'][0]['retrieval_one'], 'not_found')
            self.assertEqual(exported['reports'][0]['collection_restart'], 'not_restored')
            self.assertNotIn('PRIVATE NOTES', (out/'supplemental.json').read_text())
            manual.write_text(json.dumps(self.manual()))
            result = subprocess.run(command + ['--report', str(manual)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 1)
            self.assertFalse((out/'supplemental.json').exists())

    def test_mixed_versions_and_private_file_prose_never_exported(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            candidate, one, two, out = (root / n for n in ('candidate.json', '1.json', '2.json', 'out'))
            candidate.write_text(json.dumps(self.candidate))
            one.write_text(json.dumps(self.usage()))
            two.write_text(json.dumps(self.usage(reference='sha256:'+'b'*64, candidate_version='0.1.0-alpha.4', obstacle='PRIVATE ERROR')))
            result = subprocess.run([sys.executable, str(pathlib.Path(manual_evidence.__file__)), '--candidate', str(candidate), '--report', str(one), '--report', str(two), '--output', str(out)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 1)
            all_output = result.stdout + result.stderr + (out/'intake.json').read_text()
            self.assertNotIn(str(root), all_output)
            self.assertNotIn('PRIVATE ERROR', all_output)
            self.assertFalse((out/'supplemental.json').exists())


if __name__ == '__main__':
    unittest.main()
