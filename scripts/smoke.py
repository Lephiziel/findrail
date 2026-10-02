#!/usr/bin/env python3
"""Exercise the compiled alpha using synthetic documents and a temporary index."""
import argparse
import json
import pathlib
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request


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


def eventually(condition, timeout=12):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if condition():
            return
        time.sleep(0.05)
    raise AssertionError('Reconciliation did not complete')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=pathlib.Path)
    binary = parser.parse_args().binary.resolve(strict=True)
    repo = pathlib.Path(__file__).resolve().parents[1]
    with tempfile.TemporaryDirectory(prefix='findrail-smoke-') as temporary:
        base_dir = pathlib.Path(temporary)
        docs, data_dir = base_dir / 'notes', base_dir / 'index'
        shutil.copytree(repo / 'examples' / 'notes', docs)
        pdf_path = docs / 'space.pdf'
        good_pdf = pdf_fixture(['intro page', 'constellation orbit evidence'])
        pdf_path.write_bytes(good_pdf)
        (docs / 'textless.pdf').write_bytes(pdf_fixture(['']))

        def run(command, *arguments):
            result = subprocess.run([str(binary), command, '--data-dir', str(data_dir), *arguments],
                                    check=True, capture_output=True, text=True, timeout=20)
            return json.loads(result.stdout)

        indexed = run('index', '--json', str(docs))
        assert indexed['seen'] == 4 and indexed['skipped_pdf'] == 1, indexed
        assert run('search', '--json', 'webhook')['total'] == 1
        assert run('search', '--json', 'поиск')['total'] == 1
        found = run('search', '--json', 'constellation')['results'][0]
        assert found['page'] == 2 and found['uri'].endswith('#page=2'), found

        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            port = probe.getsockname()[1]
        process = subprocess.Popen([str(binary), 'serve', '--data-dir', str(data_dir),
                                    '--sync-interval', '1s', '--addr', f'127.0.0.1:{port}'],
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
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
            evidence = get('/api/v1/documents/' + found['id'] + '?page=2')
            assert evidence['page_count'] == 2 and 'constellation' in evidence['text']
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
    print('Findrail alpha smoke passed: CLI, PDF worker, preview, auto-refresh, rollback and HTTP.')


if __name__ == '__main__':
    main()
