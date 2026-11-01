#!/usr/bin/env python3
"""Run the compiled portable-snapshot journey using only synthetic documents."""
import argparse
import json
import os
import pathlib
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile

sys.dont_write_bytecode = True
from smoke import docx_fixture, pdf_fixture, eventually


def call(binary, *args, ok=True, timeout=120):
    result = subprocess.run([str(binary), *map(str, args)], capture_output=True,
                            text=True, encoding='utf-8', timeout=timeout)
    if ok and result.returncode:
        raise AssertionError((args, result.stdout, result.stderr))
    if not ok and not result.returncode:
        raise AssertionError(('unexpected success', args, result.stdout))
    return result


def json_call(binary, *args, ok=True):
    result = call(binary, *args, ok=ok)
    return json.loads(result.stdout) if result.returncode == 0 else result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('binary')
    args = parser.parse_args()
    binary = pathlib.Path(args.binary).resolve()
    with tempfile.TemporaryDirectory(prefix='findrail-portable-smoke-') as temporary:
        base = pathlib.Path(temporary)
        root = base / 'notes – синтетика'
        sub = root / '資料'
        sub.mkdir(parents=True)
        (root / 'retry notes_%.md').write_text('retry budget phrase survives offline export\n', encoding='utf-8')
        (sub / 'runbook report.pdf').write_bytes(pdf_fixture([
            'positive evidence on page one',
            'retrieval phrase page two forbidden marker',
        ]))
        (root / 'second.pdf').write_bytes(pdf_fixture(['secondpdfproof marker']))
        (root / 'réunion-notes.docx').write_bytes(docx_fixture('docxindexedphrase no page claim'))

        index_a, index_b = base / 'index A', base / 'index B'
        call(binary, 'index', '--data-dir', index_a, '--max-docx-bytes', 1 << 20, root)
        source_entry = json_call(binary, 'sources', '--data-dir', index_a, '--json')['sources'][0]
        source = source_entry['id']
        archive_path = base / 'notes – snapshot.findrail.zip'
        exported = json_call(binary, 'export-source', '--data-dir', index_a, '--source', source,
                             '--output', archive_path, '--json')
        assert exported['documents'] == 4 and exported['pages'] == 3
        assert 'retry budget phrase' not in json.dumps(exported)
        inspected = json_call(binary, 'inspect-export', '--json', archive_path)
        assert inspected['integrity'] == 'valid' and inspected['documents'] == 4
        assert 'origin_location' not in inspected
        shown = json_call(binary, 'inspect-export', '--json', '--show-paths', archive_path)
        assert shown['origin_location'] == source_entry['root']
        inspect_dir = base / 'inspect-must-not-create'
        json_call(binary, 'inspect-export', '--data-dir', inspect_dir, '--json', archive_path)
        assert not inspect_dir.exists()

        # Refused exports must not create inside the index or watched root.
        call(binary, 'export-source', '--data-dir', index_a, '--source', source,
             '--output', root / 'self.findrail.zip', ok=False)
        call(binary, 'export-source', '--data-dir', index_a, '--source', source,
             '--output', index_a / 'inside.findrail.zip', ok=False)
        missing_index = base / 'missing source index'
        call(binary, 'export-source', '--data-dir', missing_index, '--source', 'misspelled',
             '--output', base / 'missing.findrail.zip', ok=False)
        assert not missing_index.exists() and not (base / 'missing.findrail.zip').exists()

        original_archive = archive_path.read_bytes()
        refused = call(binary, 'export-source', '--data-dir', index_a, '--source', source,
                       '--output', archive_path, '--json', ok=False)
        assert archive_path.read_bytes() == original_archive
        if os.name != 'nt':
            link = base / 'snapshot-link.zip'
            link.symlink_to(archive_path)
            call(binary, 'export-source', '--data-dir', index_a, '--source', source,
                 '--output', link, ok=False)
            assert link.is_symlink() and link.read_bytes() == original_archive

        # Invalid import must not create or migrate the destination index.
        broken = base / 'broken.zip'; broken.write_bytes(b'not a zip')
        missing = base / 'missing destination'
        call(binary, 'import-source', '--data-dir', missing, '--name', 'bad', broken, ok=False)
        assert not (missing / 'findrail.db').exists()
        if hasattr(os, 'mkfifo'):
            fifo = base / 'archive.pipe'
            os.mkfifo(fifo)
            refused = call(binary, 'inspect-export', '--timeout', '1s', fifo,
                           ok=False, timeout=3)
            assert 'regular file' in refused.stderr

        imported = json_call(binary, 'import-source', '--data-dir', index_b, '--name', 'Moved notes',
                              '--json', archive_path)
        imported_id = imported['source_id']
        assert imported['status'] == 'imported' and imported_id != source
        duplicate = json_call(binary, 'import-source', '--data-dir', index_b, '--name', 'ignored rename',
                              '--json', archive_path)
        assert duplicate['status'] == 'already_imported' and duplicate['source_id'] == imported_id

        # Add a live source with the same relative paths. Archive identity stays isolated.
        live = base / 'live copy'; live.mkdir()
        (live / 'retry notes_%.md').write_text('retry budget phrase survives offline export\n', encoding='utf-8')
        (live / 'réunion-notes.docx').write_bytes(docx_fixture('docxindexedphrase no page claim'))
        (live / '資料').mkdir()
        (live / '資料' / 'runbook report.pdf').write_bytes((sub / 'runbook report.pdf').read_bytes())
        (live / 'second.pdf').write_bytes((root / 'second.pdf').read_bytes())
        call(binary, 'index', '--data-dir', index_b, '--max-docx-bytes', 1 << 20, live)
        sources = json_call(binary, 'sources', '--data-dir', index_b, '--json')['sources']
        live_id = next(s['id'] for s in sources if s['kind'] == 'filesystem')
        archive_status = next(s for s in sources if s['kind'] == 'archive')
        assert archive_status['archive']['fingerprint'] == exported['fingerprint']
        assert live_id != imported_id
        assert json_call(binary, 'search', '--data-dir', index_b, '--source', imported_id,
                         '--format', 'pdf', '--json', 'secondpdfproof')['total'] == 1

        search = json_call(binary, 'search', '--data-dir', index_b, '--source', imported_id,
                           '--mode', 'advanced', '--format', 'pdf', '--path-prefix', '資料',
                           '--title-contains', 'runbook', '--json', '"retrieval phrase"')
        assert search['total'] == 1 and search['results'][0]['page'] == 2
        assert search['results'][0]['uri'].endswith('#page=2')
        assert json_call(binary, 'search', '--data-dir', index_b, '--source', imported_id,
                         '--mode', 'advanced', '--json', 'positive -forbidden')['total'] == 0
        assert json_call(binary, 'search', '--data-dir', index_b, '--source', imported_id,
                         '--mode', 'advanced', '--json', '"page one retrieval"')['total'] == 0
        assert json_call(binary, 'search', '--data-dir', index_b, '--source', imported_id,
                         '--mode', 'advanced', '--json', 'retry OR docxindexedphrase')['total'] == 2
        assert json_call(binary, 'search', '--data-dir', index_b, '--source', imported_id,
                         '--mode', 'advanced', '--json', 'retry budg*')['total'] == 1
        assert json_call(binary, 'search', '--data-dir', index_b, '--source', imported_id,
                         '--mode', 'advanced', '--format', 'docx', '--title-contains', 'réunion',
                         '--json', 'docxindexedphrase')['total'] == 1
        title_only = json_call(binary, 'search', '--data-dir', index_b, '--source', imported_id,
                               '--mode', 'advanced', '--format', 'pdf', '--json', 'runbook')['results'][0]
        assert title_only.get('page', 0) == 0 and '#page=' not in title_only['uri']
        docx_result = json_call(binary, 'search', '--data-dir', index_b, '--source', imported_id,
                                '--json', 'docxindexedphrase')['results'][0]
        assert docx_result['media_type'].endswith('wordprocessingml.document') and docx_result.get('page', 0) == 0 and '#page=' not in docx_result['uri']

        # Re-export keeps provenance/content identity while import name/time/IDs differ.
        reexport = base / 'reexport.zip'
        reexport_info = json_call(binary, 'export-source', '--data-dir', index_b, '--source', imported_id,
                                  '--output', reexport, '--json')
        assert reexport_info['fingerprint'] == exported['fingerprint']

        # Drop the input/original files: imported search and HTTP evidence remain offline.
        archive_path.unlink()
        for child in root.rglob('*'):
            if child.is_file(): child.unlink()
        (root / '資料').rmdir(); root.rmdir()
        assert json_call(binary, 'search', '--data-dir', index_b, '--source', imported_id,
                         '--json', 'retry budget')['total'] == 1

        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0)); port = probe.getsockname()[1]
        process = subprocess.Popen([str(binary), 'start', '--data-dir', str(index_b), '--no-open',
                                    '--addr', f'127.0.0.1:{port}'], cwd=base,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, encoding='utf-8')
        base_url = f'http://127.0.0.1:{port}'
        def get(path):
            with urllib.request.urlopen(base_url + path, timeout=3) as response:
                return json.load(response)
        try:
            def ready():
                if process.poll() is not None:
                    raise RuntimeError('start exited before HTTP readiness: ' + process.stderr.read())
                try:
                    return get('/healthz') == {'status': 'ok'}
                except (urllib.error.URLError, TimeoutError):
                    return False
            eventually(ready)
            inventory = get('/api/v1/sources')['sources']
            assert any(s['id'] == imported_id and s['kind'] == 'archive' for s in inventory)
            http_search = get('/api/v1/search?' + urllib.parse.urlencode({'q': 'retrieval phrase', 'source': imported_id}))
            result = http_search['results'][0]
            evidence = get('/api/v1/documents/' + result['id'] + '?page=2')
            assert evidence['page_count'] == 2 and 'retrieval phrase' in evidence['text']
            call(binary, 'forget', '--data-dir', index_b, imported_id)
            eventually(lambda: all(s['id'] != imported_id for s in get('/api/v1/sources')['sources']))
            assert json_call(binary, 'search', '--data-dir', index_b, '--source', live_id,
                             '--json', 'retry budget')['total'] == 1
        finally:
            if process.poll() is None: process.terminate()
            try: process.communicate(timeout=8)
            except subprocess.TimeoutExpired: process.kill(); process.communicate(timeout=2)
        if os.name != 'nt':
            assert process.returncode == 0, process.stderr.read()

        # Removal permits fresh registration state and another complete import.
        retry = json_call(binary, 'import-source', '--data-dir', index_b, '--name', 'Fresh state',
                          '--json', reexport)
        assert retry['status'] == 'imported'
        print(f"portable snapshot smoke passed: {exported['documents']} documents, {exported['pages']} pages, {exported['archive_bytes']} archive bytes")


if __name__ == '__main__':
    main()
