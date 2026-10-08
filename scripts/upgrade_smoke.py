#!/usr/bin/env python3
"""Exercise a packaged candidate against a synthetic historical schema-3 index."""
import hashlib
import json
import pathlib
import shutil
import socket
import sqlite3
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]


def create_schema3(directory, source_root):
    folder_id = 'fs_' + hashlib.sha256(str(source_root).encode()).hexdigest()
    dbpath = directory / 'findrail.db'
    db = sqlite3.connect(dbpath)
    for name in ('001_init.sql', '002_local_alpha.sql', '003_github_snapshots.sql'):
        db.executescript((ROOT / 'internal/store/sqlite/migrations' / name).read_text())
    db.execute('INSERT INTO sources(id,kind,name,root,max_text_bytes,max_pdf_bytes) VALUES(?,?,?,?,?,?)',
               (folder_id, 'filesystem', 'Synthetic PDF source', str(source_root), 1048576, 16777216))
    db.execute("INSERT INTO sources(id,kind,name,root,max_text_bytes,max_pdf_bytes) VALUES('legacy-gh','github','Synthetic GitHub source','owner/repo',1048576,0)")
    db.execute("""INSERT INTO documents(id,source_id,title,uri,path,content,content_hash,size_bytes,modified_at,scan_token,media_type,page_count)
               VALUES('legacy-pdf-doc',?,'guide.pdf',?,'guide.pdf','intro oldcandidateevidence','synthetic-hash',27,'2026-01-01T00:00:00Z','old-token','application/pdf',2)""",
               (folder_id, (source_root / 'guide.pdf').as_uri()))
    db.executescript("""
      INSERT INTO document_pages(document_id,page_number,content)
        VALUES('legacy-pdf-doc',1,'intro'),('legacy-pdf-doc',2,'oldcandidateevidence retained page two');
      INSERT INTO github_sources(source_id,repository_id,owner,repo,repository_url,ref_mode,ref_value,selected_path,
        max_bytes,policy_version,snapshot_sha,commit_time,registration_token,revision)
        VALUES('legacy-gh',7,'owner','repo','https://github.com/owner/repo','default','','',1048576,1,
        'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','2026-01-01T00:00:00Z','synthetic-token',3);
    """)
    db.close()
    return dbpath, folder_id


def invoke(binary, args):
    cwd = pathlib.Path(args[args.index('--data-dir') + 1]).parent
    return subprocess.run([str(binary), *args], text=True, capture_output=True, timeout=20, check=True, cwd=cwd)


def configure_legacy_docx(binary, data, cwd, folder_id):
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', 0))
        port = probe.getsockname()[1]
    authority = f'127.0.0.1:{port}'
    base = 'http://' + authority
    process = subprocess.Popen([str(binary), 'start', '--data-dir', str(data), '--no-open', '--addr', authority],
                               cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            try:
                with urllib.request.urlopen(base + '/api/v1/capabilities', timeout=1) as response:
                    if json.load(response).get('management'):
                        break
            except (OSError, urllib.error.URLError):
                if process.poll() is not None:
                    raise RuntimeError('packaged start exited: ' + process.stderr.read())
                time.sleep(.05)
        else:
            raise RuntimeError('packaged start management endpoint did not become ready')

        def get(path):
            request = urllib.request.Request(base + path, headers={'Origin': base})
            with urllib.request.urlopen(request, timeout=3) as response:
                return json.load(response)

        legacy = next(source for source in get('/api/v1/sources')['sources'] if source['id'] == folder_id)
        if legacy.get('max_docx_bytes', 0) != 0:
            raise RuntimeError('legacy DOCX policy unexpectedly enabled')
        token = get('/api/v1/session')['token']
        headers = {'Origin': base, 'Content-Type': 'application/json', 'Sec-Fetch-Site': 'same-origin', 'X-Findrail-Token': token}
        request = urllib.request.Request(base + '/api/v1/sources/' + folder_id + '/configure',
                                         data=json.dumps({'max_docx_bytes': 8388608}).encode(), method='POST', headers=headers)
        with urllib.request.urlopen(request, timeout=3) as response:
            job = json.load(response)['job']
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            job = get('/api/v1/jobs/' + job['id'])
            if job['status'] in ('succeeded', 'failed', 'canceled'):
                break
            time.sleep(.05)
        if job['status'] != 'succeeded':
            db = sqlite3.connect(data / 'findrail.db')
            state = db.execute('SELECT kind,root,max_text_bytes,max_pdf_bytes,max_docx_bytes,length(registration_token),revision FROM sources WHERE id=?', (folder_id,)).fetchone()
            db.close()
            raise RuntimeError('legacy DOCX configure job failed: ' + str(job) + '; persisted source state=' + repr(state))
    finally:
        if process.poll() is None:
            process.terminate()
        try:
            process.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.communicate(timeout=2)


def main():
    if len(sys.argv) != 2: raise SystemExit('usage: upgrade_smoke.py EXTRACTED_FINDRAIL_BINARY')
    binary = pathlib.Path(sys.argv[1]).resolve()
    with tempfile.TemporaryDirectory(prefix='findrail-upgrade-') as temp:
        root = pathlib.Path(temp); data = root / 'legacy data'; data.mkdir()
        source_root = root / 'synthetic originals'; source_root.mkdir()
        from smoke import docx_fixture
        (source_root / 'legacy.docx').write_bytes(docx_fixture('legacydocxconfiguredphrase preserved original'))
        dbpath, folder_id = create_schema3(data, source_root)
        before = hashlib.sha256(dbpath.read_bytes()).hexdigest()
        backup = root / 'stopped full backup'
        shutil.copytree(data, backup)
        backup_hash = hashlib.sha256((backup / 'findrail.db').read_bytes()).hexdigest()
        if before != backup_hash: raise SystemExit('backup hash differs')
        listing = json.loads(invoke(binary, ['sources', '--data-dir', str(data), '--json']).stdout)
        if len(listing['sources']) != 2: raise SystemExit('migration lost sources')
        result = json.loads(invoke(binary, ['search', '--data-dir', str(data), '--json', 'oldcandidateevidence']).stdout)
        if result['total'] != 1 or result['results'][0]['page'] != 2: raise SystemExit('migration lost PDF FTS/page evidence')
        updated = sqlite3.connect(dbpath)
        schema = updated.execute('PRAGMA user_version').fetchone()[0]
        gh = updated.execute('SELECT snapshot_sha,revision FROM github_sources WHERE source_id=?', ('legacy-gh',)).fetchone()
        policies = updated.execute('SELECT max_docx_bytes FROM sources WHERE id=?', (folder_id,)).fetchone()
        updated.close()
        if schema != 4 or gh != ('a' * 40, 3) or policies != (0,): raise SystemExit('schema-4 migration state mismatch')
        configure_legacy_docx(binary, data, root, folder_id)
        sources = json.loads(invoke(binary, ['sources', '--data-dir', str(data), '--json']).stdout)['sources']
        legacy = next(source for source in sources if source['id'] == folder_id)
        if legacy['max_docx_bytes'] != 8388608: raise SystemExit('DOCX policy was not persisted across restart')
        result = json.loads(invoke(binary, ['search', '--data-dir', str(data), '--json', 'legacydocxconfiguredphrase']).stdout)
        if result['total'] != 1: raise SystemExit('explicitly configured legacy DOCX was not indexed')
        restored = root / 'rollback restore'
        shutil.copytree(backup, restored)
        if hashlib.sha256((restored / 'findrail.db').read_bytes()).hexdigest() != before:
            raise SystemExit('whole-directory rollback restore differed')
        print('Packaged schema-3 upgrade passed: sources, GitHub metadata, FTS/PDF page, disabled DOCX default, Configure/index/restart, stopped full-directory rollback copy.')


if __name__ == '__main__': main()
