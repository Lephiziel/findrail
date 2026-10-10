#!/usr/bin/env python3
"""Exercise the compiled alpha using synthetic documents and a temporary index."""
import argparse
import json
import os
import pathlib
import shutil
import select
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile

# The smoke journeys only contact loopback Findrail servers. Bypass ambient
# system/environment proxies so a hosted runner cannot route those test calls
# outside the machine or reset them before the local server receives them.
urllib.request.install_opener(urllib.request.build_opener(urllib.request.ProxyHandler({})))


def pdf_fixture(pages):
    objects = [b'<< /Type /Catalog /Pages 2 0 R >>', b'',
               b'<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>']
    kids = []
    for body in pages:
        number = len(objects) + 1
        kids.append(f'{number} 0 R')
        stream = f'BT /F1 12 Tf 72 720 Td ({body}) Tj ET'.encode('ascii')
        objects.extend([
            (f'<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] '
             f'/Resources << /Font << /F1 3 0 R >> >> /Contents {number+1} 0 R >>').encode(),
            f'<< /Length {len(stream)} >>\nstream\n'.encode() + stream + b'\nendstream'])
    objects[1] = f'<< /Type /Pages /Count {len(pages)} /Kids [{" ".join(kids)}] >>'.encode()
    data = bytearray(b'%PDF-1.4\n')
    offsets = []
    for number, obj in enumerate(objects, 1):
        offsets.append(len(data))
        data.extend(f'{number} 0 obj\n'.encode() + obj + b'\nendobj\n')
    xref = len(data)
    data.extend(f'xref\n0 {len(objects)+1}\n0000000000 65535 f \n'.encode())
    for offset in offsets:
        data.extend(f'{offset:010d} 00000 n \n'.encode())
    data.extend(f'trailer\n<< /Size {len(objects)+1} /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF\n'.encode())
    return bytes(data)


def docx_fixture(text):
    parts = {
        '[Content_Types].xml': '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>',
        '_rels/.rels': '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="r1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>',
        'word/document.xml': '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>' + text + '</w:t></w:r></w:p></w:body></w:document>',
    }
    from xml.sax.saxutils import escape
    parts['word/document.xml'] = parts['word/document.xml'].replace(text, escape(text))
    import io
    output = io.BytesIO()
    with zipfile.ZipFile(output, 'w', zipfile.ZIP_DEFLATED) as archive:
        for name, value in parts.items():
            archive.writestr(name, value)
    return output.getvalue()


def startup_error_smoke(binary):
    """Check occupied-port failure and a missing opener without launching a browser."""
    if sys.platform != 'linux':
        return
    with tempfile.TemporaryDirectory(prefix='findrail-start-errors-') as temporary:
        base = pathlib.Path(temporary)
        with socket.socket() as occupied:
            occupied.bind(('127.0.0.1', 0)); port = occupied.getsockname()[1]
            occupied.listen()
            failed = subprocess.run([str(binary), 'start', '--no-open', '--data-dir', str(base / 'busy-index'),
                                     '--addr', f'127.0.0.1:{port}'], cwd=base, capture_output=True,
                                    text=True, encoding='utf-8', timeout=10)
        assert failed.returncode != 0 and '127.0.0.1' in failed.stderr, failed

        # A private empty PATH makes xdg-open unresolvable; no real browser is run.
        empty_path = base / 'empty-path'; empty_path.mkdir()
        probe = socket.socket(); probe.bind(('127.0.0.1', 0)); port = probe.getsockname()[1]; probe.close()
        process = subprocess.Popen([str(binary), 'start', '--data-dir', str(base / 'browser-index'),
                                    '--addr', f'127.0.0.1:{port}'], cwd=base,
                                   env=dict(os.environ, PATH=str(empty_path)), stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, text=True, encoding='utf-8')
        try:
            ready, _, _ = select.select([process.stdout], [], [], 10)
            assert ready, 'server did not print its local URL'
            line = process.stdout.readline()
            assert line.startswith('Findrail local UI: http://127.0.0.1:'), line
        finally:
            if process.poll() is None: process.terminate()
        stdout, stderr = process.communicate(timeout=5)
        assert process.returncode == 0, (line, stdout, stderr)
        assert 'Could not start the browser opener' in stderr and 'Open http://127.0.0.1:' in stderr, stderr


def eventually(condition, timeout=12):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if condition():
            return
        time.sleep(0.05)
    raise AssertionError('Reconciliation did not complete')


def demo_smoke(binary):
    with tempfile.TemporaryDirectory(prefix='findrail-demo-smoke-') as temporary:
        parent = pathlib.Path(temporary)
        env = dict(os.environ, TMPDIR=temporary, TMP=temporary, TEMP=temporary)
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            port = probe.getsockname()[1]
        process = subprocess.Popen([str(binary), 'demo', '--no-open', '--sync-interval', '1s',
                                    '--addr', f'127.0.0.1:{port}'], env=env,
                                   cwd=parent, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, encoding='utf-8')
        base = f'http://127.0.0.1:{port}'

        def get(path):
            with urllib.request.urlopen(base + path, timeout=3) as response:
                return json.load(response)

        try:
            def ready():
                if process.poll() is not None:
                    raise RuntimeError('Demo exited: ' + process.stderr.read())
                try:
                    return get('/healthz') == {'status': 'ok'}
                except (urllib.error.URLError, TimeoutError):
                    return False
            eventually(ready)
            assert get('/api/v1/capabilities')['management'] is False
            try:
                request = urllib.request.Request(base + '/api/v1/sources', data=b'{}', method='POST',
                                                 headers={'Origin': base, 'Content-Type': 'application/json'})
                urllib.request.urlopen(request, timeout=3)
                raise AssertionError('demo unexpectedly enabled source mutations')
            except urllib.error.HTTPError as error:
                assert error.code != 202
            try:
                request = urllib.request.Request(base + '/api/v1/sources/example/configure', data=b'{"max_docx_bytes":0}', method='POST',
                                                 headers={'Origin': base, 'Content-Type': 'application/json'})
                urllib.request.urlopen(request, timeout=3)
                raise AssertionError('demo unexpectedly exposed Configure')
            except urllib.error.HTTPError as error:
                assert error.code == 404
            response = get('/api/v1/search?q=idempotency')
            assert response['total'] == 3, response
            pdf = next(r for r in response['results'] if r['media_type'] == 'application/pdf')
            assert pdf['page'] == 2 and pdf['uri'].endswith('#page=2'), pdf
            evidence = get('/api/v1/documents/' + pdf['id'] + '?page=2')
            assert evidence['page_count'] == 2 and 'Idempotency' in evidence['text'], evidence
            sources = get('/api/v1/sources')['sources']
            assert len(sources) == 1 and sources[0]['documents'] == 3, sources
            documents = pathlib.Path(sources[0]['root'])
            workspace = documents.parent
            assert workspace.parent.resolve() == parent.resolve() and documents.name == 'documents', documents
            note = documents / 'retry-notes.md'
            note.write_text(note.read_text(encoding='utf-8').replace('amber', 'cobalt'), encoding='utf-8')
            eventually(lambda: get('/api/v1/search?q=cobalt')['total'] == 1)
            assert get('/api/v1/search?q=amber')['total'] == 0
        finally:
            if process.poll() is None:
                process.terminate()
            try:
                process.communicate(timeout=7)
            except subprocess.TimeoutExpired:
                process.kill()
                process.communicate(timeout=2)
        if sys.platform != 'win32':
            assert process.returncode == 0, process.returncode
            assert not workspace.exists(), workspace


def source_management_smoke(binary):
    with tempfile.TemporaryDirectory(prefix='findrail-management-smoke-') as temporary:
        parent = pathlib.Path(temporary)
        notes = parent / 'Notes Ω'
        notes.mkdir()
        original = notes / 'synthetic.md'
        original.write_text('management marker citation fixture', encoding='utf-8')
        pdf_original = notes / 'fixture.pdf'
        pdf_original.write_bytes(pdf_fixture(['first page only', 'managementpdfphrase page evidence']))
        docx_original = notes / 'réunion-черновик.docx'
        docx_original.write_bytes(docx_fixture('managementdocxphrase original snapshot'))
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            port = probe.getsockname()[1]
        process = subprocess.Popen([str(binary), 'start', '--no-open', '--data-dir', str(parent / 'index'),
                                    '--addr', f'127.0.0.1:{port}'], stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, text=True, encoding='utf-8', cwd=parent)
        base = f'http://127.0.0.1:{port}'

        def get(path):
            with urllib.request.urlopen(base + path, timeout=3) as response:
                return json.load(response)

        def mutate(path, method, body=None, origin=base, token=None):
            headers = {'Origin': origin, 'Content-Type': 'application/json', 'Sec-Fetch-Site': 'same-origin'}
            if token:
                headers['X-Findrail-Token'] = token
            data = None if body is None else json.dumps(body).encode()
            request = urllib.request.Request(base + path, data=data, method=method, headers=headers)
            try:
                with urllib.request.urlopen(request, timeout=4) as response:
                    return response.status, json.load(response) if response.status != 204 else None
            except urllib.error.HTTPError as error:
                return error.code, json.load(error)

        def wait_job(job_id):
            result = {}
            def terminal():
                nonlocal result
                result = get('/api/v1/jobs/' + job_id)
                return result['status'] in ('succeeded', 'failed', 'canceled')
            eventually(terminal, timeout=15)
            assert result['status'] == 'succeeded', result
            return result

        try:
            def ready():
                if process.poll() is not None:
                    raise RuntimeError('Empty start exited: ' + process.stderr.read())
                try:
                    return get('/api/v1/capabilities').get('management') is True
                except (urllib.error.URLError, TimeoutError):
                    return False
            eventually(ready)
            assert get('/api/v1/sources')['sources'] == []
            session = get('/api/v1/session')
            token = session['token']
            assert token and len(token) >= 32
            assert mutate('/api/v1/sources', 'POST', {'type': 'folder', 'path': str(notes)},
                          origin='http://attacker.invalid', token=token)[0] == 403
            assert mutate('/api/v1/sources/not-a-source/configure', 'POST', {'max_docx_bytes': 0},
                          origin='http://attacker.invalid', token=token)[0] == 403
            assert mutate('/api/v1/sources/not-a-source/configure', 'POST', {'max_docx_bytes': 0, 'extra': 1},
                          token=token)[0] == 400
            assert mutate('/api/v1/sources/not-a-source/configure', 'POST', {}, token=token)[0] == 400
            assert get('/api/v1/jobs')['jobs'] == []
            status, accepted = mutate('/api/v1/sources', 'POST', {'type': 'folder', 'path': str(notes), 'max_docx_bytes': 1 << 20}, token=token)
            assert status == 202, (status, accepted)
            wait_job(accepted['job']['id'])
            sources = get('/api/v1/sources')['sources']
            assert len(sources) == 1 and sources[0]['documents'] == 3 and sources[0]['max_docx_bytes'] == 1 << 20
            source_id = sources[0]['id']
            matches = get('/api/v1/search?q=management+marker')['results']
            assert len(matches) == 1
            assert 'citation fixture' in get('/api/v1/documents/' + matches[0]['id'])['text']
            pdf_match = get('/api/v1/search?q=managementpdfphrase')['results'][0]
            assert pdf_match['page'] == 2 and pdf_match['uri'].endswith('#page=2'), pdf_match
            pdf_preview = get('/api/v1/documents/' + pdf_match['id'] + '?page=2')
            assert pdf_preview['page_count'] == 2 and 'page evidence' in pdf_preview['text']
            docx_match = get('/api/v1/search?q=managementdocxphrase')['results'][0]
            docx_preview = get('/api/v1/documents/' + docx_match['id'])
            assert docx_preview.get('page_count', 0) == 0 and '#page=' not in docx_preview['uri'] and 'managementdocxphrase' in docx_preview['text']
            assert docx_preview['uri'].startswith('file:') and 'черновик' not in docx_preview['uri']
            original.write_text('management marker citation fixture watcherrefresh', encoding='utf-8')
            eventually(lambda: get('/api/v1/search?q=watcherrefresh')['total'] == 1, timeout=12)
            staged = notes / 'word-save.tmp.docx'
            staged.write_bytes(docx_fixture('managementdocxphrase renamedsnapshot'))
            staged.replace(docx_original)
            eventually(lambda: get('/api/v1/search?q=renamedsnapshot')['total'] == 1)
            status, accepted = mutate('/api/v1/sources/' + source_id + '/refresh', 'POST', token=token)
            assert status == 202, (status, accepted)
            wait_job(accepted['job']['id'])
            status, accepted = mutate('/api/v1/sources/' + source_id + '/configure', 'POST',
                                      {'max_docx_bytes': 0}, token=token)
            assert status == 202, (status, accepted)
            configured_off = wait_job(accepted['job']['id'])
            assert '1 DOCX files skipped' in configured_off.get('result', ''), configured_off
            assert get('/api/v1/search?q=managementdocxphrase')['total'] == 0
            assert get('/api/v1/search?q=management+marker')['total'] == 1
            assert get('/api/v1/search?q=managementpdfphrase')['total'] == 1
            sources = get('/api/v1/sources')['sources']
            assert sources[0].get('max_docx_bytes', 0) == 0
            eventually(lambda: any(s.get('skipped_docx') == 1 for s in get('/api/v1/sync')['sources']))
            status, accepted = mutate('/api/v1/sources/' + source_id + '/configure', 'POST',
                                      {'max_docx_bytes': 1 << 20}, token=token)
            assert status == 202, (status, accepted)
            wait_job(accepted['job']['id'])
            assert get('/api/v1/search?q=renamedsnapshot')['total'] == 1
            status, accepted = mutate('/api/v1/sources/' + source_id, 'DELETE', token=token)
            assert status == 202, (status, accepted)
            wait_job(accepted['job']['id'])
            assert get('/api/v1/sources')['sources'] == []
            assert get('/api/v1/search?q=management+marker')['total'] == 0
            try:
                get('/api/v1/documents/' + matches[0]['id'])
                raise AssertionError('removed preview remained available')
            except urllib.error.HTTPError as error:
                assert error.code == 404
            assert original.read_text(encoding='utf-8') == 'management marker citation fixture watcherrefresh'
            assert pdf_original.exists()
            assert docx_original.exists()
            with zipfile.ZipFile(docx_original) as archive:
                assert 'renamedsnapshot' in archive.read('word/document.xml').decode('utf-8')
        finally:
            if process.poll() is None:
                process.terminate()
            try:
                process.communicate(timeout=8)
            except subprocess.TimeoutExpired:
                process.kill()
                process.communicate(timeout=2)
        if sys.platform != 'win32':
            assert process.returncode == 0, process.returncode


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=pathlib.Path)
    parser.add_argument('--expected-version')
    arguments = parser.parse_args()
    binary = arguments.binary.resolve(strict=True)
    repo = pathlib.Path(__file__).resolve().parents[1]
    with tempfile.TemporaryDirectory(prefix='findrail-smoke-') as temporary:
        base_dir = pathlib.Path(temporary)
        version = subprocess.run([str(binary), 'version'], cwd=base_dir, capture_output=True, text=True, encoding='utf-8', timeout=5, check=True)
        version_fields = version.stdout.strip().split()
        assert len(version_fields) == 2 and version_fields[0] == 'findrail', version.stdout
        if arguments.expected_version:
            assert version_fields[1] == arguments.expected_version, version.stdout
        help_result = subprocess.run([str(binary), '--help'], cwd=base_dir, capture_output=True, text=True, encoding='utf-8', timeout=5, check=True)
        assert 'Usage:' in help_result.stdout, help_result.stdout
        docs, data_dir = base_dir / 'notes', base_dir / 'index'
        shutil.copytree(repo / 'examples' / 'notes', docs)
        pdf_path = docs / 'space.pdf'
        good_pdf = pdf_fixture(['intro page', 'constellation orbit evidence'])
        pdf_path.write_bytes(good_pdf)
        (docs / 'textless.pdf').write_bytes(pdf_fixture(['']))
        docx_path = docs / 'fictional-meeting.docx'
        docx_path.write_bytes(docx_fixture('docxsearchphrase planning snapshot'))

        def run(command, *arguments):
            result = subprocess.run([str(binary), command, '--data-dir', str(data_dir), *arguments],
                                    check=True, capture_output=True, text=True, encoding="utf-8", timeout=20, cwd=base_dir)
            return json.loads(result.stdout)

        indexed = run('index', '--max-docx-bytes', str(8 << 20), '--json', str(docs))
        assert indexed['seen'] == 5 and indexed['skipped_pdf'] == 1, indexed
        stored = run('sources', '--json')['sources']
        assert len(stored) == 1 and stored[0]['max_docx_bytes'] == 8 << 20, stored
        assert run('search', '--json', 'webhook')['total'] == 1
        assert run('search', '--json', 'поиск')['total'] == 1
        found = run('search', '--json', 'constellation')['results'][0]
        assert found['page'] == 2 and found['uri'].endswith('#page=2'), found
        docx_found = run('search', '--json', 'docxsearchphrase')['results'][0]
        assert docx_found['media_type'].endswith('wordprocessingml.document') and docx_found.get('page', 0) == 0, docx_found
        docx_evidence = run('search', '--json', 'docxsearchphrase')['results'][0]
        assert 'docxsearchphrase' in docx_evidence['snippet'] and '#page=' not in docx_evidence['uri']

        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            port = probe.getsockname()[1]
        # start must populate a fresh index itself, rather than relying on the
        # separate index command exercised above. CI is intentionally headless.
        process = subprocess.Popen([str(binary), 'start', '--no-open', '--data-dir', str(base_dir / 'start-index'),
                                    '--sync-interval', '1s', '--addr', f'127.0.0.1:{port}', str(docs)],
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, encoding="utf-8", cwd=base_dir)
        base = f'http://127.0.0.1:{port}'

        def get(path):
            with urllib.request.urlopen(base + path, timeout=3) as response:
                return json.load(response)

        def count(word):
            return get('/api/v1/search?' + urllib.parse.urlencode({'q': word}))['total']

        try:
            def ready():
                if process.poll() is not None:
                    raise RuntimeError('Server exited: ' + process.stderr.read())
                try:
                    return get('/healthz') == {'status': 'ok'}
                except (urllib.error.URLError, TimeoutError):
                    return False
            eventually(ready)
            assert get('/api/v1/sync')['enabled']
            start_sources = get('/api/v1/sources')['sources']
            assert len(start_sources) == 1 and start_sources[0]['max_docx_bytes'] == 8 << 20
            evidence = get('/api/v1/documents/' + found['id'] + '?page=2')
            assert evidence['page_count'] == 2 and 'constellation' in evidence['text']
            docx_result = get('/api/v1/search?q=docxsearchphrase')['results'][0]
            docx_preview = get('/api/v1/documents/' + docx_result['id'])
            assert docx_preview.get('page_count', 0) == 0 and 'docxsearchphrase' in docx_preview['text'] and '#page=' not in docx_preview['uri']
            replacement = docs / 'temp-word-save.docx'
            replacement.write_bytes(docx_fixture('renamedwordsave snapshot'))
            replacement.replace(docx_path)
            eventually(lambda: count('renamedwordsave') == 1 and count('docxsearchphrase') == 0)
            note = docs / 'live.md'
            note.write_text('automaticrefresh example', encoding='utf-8')
            eventually(lambda: count('automaticrefresh') == 1)
            note.write_text('changedpassage example', encoding='utf-8')
            eventually(lambda: count('changedpassage') == 1 and count('automaticrefresh') == 0)
            note.unlink()
            eventually(lambda: count('changedpassage') == 0)
            # Invalid replacement rolls back the entire scan, preserving PDF evidence.
            pdf_path.write_bytes(b'%PDF-invalid')
            eventually(lambda: any(s['state'] == 'error' for s in get('/api/v1/sync')['sources']))
            assert count('constellation') == 1
            pdf_path.write_bytes(good_pdf)
            eventually(lambda: all(s['state'] == 'idle' for s in get('/api/v1/sync')['sources']))
            assert len(get('/api/v1/sources')['sources']) == 1
            with urllib.request.urlopen(base, timeout=3) as response:
                html = response.read().decode('utf-8')
                assert 'Findrail' in html and 'Preview' in html
        finally:
            if process.poll() is None:
                process.terminate()
            try:
                process.communicate(timeout=7)
            except subprocess.TimeoutExpired:
                process.kill()
                process.communicate(timeout=2)
        # Windows terminate() is an OS kill, not a graceful Unix SIGTERM.
        if sys.platform != 'win32':
            assert process.returncode == 0, process.returncode
    startup_error_smoke(binary)
    source_management_smoke(binary)
    demo_smoke(binary)
    print('Findrail smoke passed: empty start, source management APIs/jobs, demo read-only boundary, CLI, PDF and DOCX extraction, previews, rename refresh, rollback and HTTP.')


if __name__ == '__main__':
    main()
