#!/usr/bin/env python3
"""Exercise packaged alpha.2, alpha.3, and alpha.4 upgrade/restore paths."""
import argparse
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
urllib.request.install_opener(urllib.request.build_opener(urllib.request.ProxyHandler({})))


def create_schema2(directory, source_root):
    folder_id = 'fs_' + hashlib.sha256(str(source_root).encode()).hexdigest()
    dbpath = directory / 'findrail.db'
    db = sqlite3.connect(dbpath)
    for name in ('001_init.sql', '002_local_alpha.sql'):
        db.executescript((ROOT / 'internal/store/sqlite/migrations' / name).read_text())
    db.execute('INSERT INTO sources(id,kind,name,root,max_text_bytes,max_pdf_bytes) VALUES(?,?,?,?,?,?)',
               (folder_id, 'filesystem', 'Synthetic PDF source', str(source_root), 1048576, 16777216))
    db.execute("""INSERT INTO documents(id,source_id,title,uri,path,content,content_hash,size_bytes,modified_at,scan_token,media_type,page_count)
               VALUES('legacy-pdf-doc',?,'guide.pdf',?,'guide.pdf','intro oldcandidateevidence','synthetic-hash',27,'2026-01-01T00:00:00Z','old-token','application/pdf',2)""",
               (folder_id, (source_root / 'guide.pdf').as_uri()))
    db.executescript("""
      INSERT INTO document_pages(document_id,page_number,content)
        VALUES('legacy-pdf-doc',1,'intro'),('legacy-pdf-doc',2,'oldcandidateevidence retained page two');
    """)
    db.close()
    return dbpath, folder_id


def directory_digest(directory):
    """Hash a stopped data directory including any SQLite WAL/SHM sidecars."""
    digest = hashlib.sha256()
    for path in sorted(p for p in pathlib.Path(directory).rglob('*') if p.is_file()):
        digest.update(path.relative_to(directory).as_posix().encode())
        digest.update(b'\0')
        digest.update(path.read_bytes())
    return digest.hexdigest()


def invoke(binary, args, *, ok=True):
    cwd = pathlib.Path(args[args.index('--data-dir') + 1]).parent
    result = subprocess.run([str(binary), *args], text=True, capture_output=True, timeout=20, check=False, cwd=cwd)
    if (result.returncode == 0) != ok:
        raise RuntimeError(f'command {args!r} exit={result.returncode}: {result.stdout}\n{result.stderr}')
    return result


def free_port():
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', 0))
        return probe.getsockname()[1]


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


def upgrade_alpha3_schema6(candidate, published_alpha3, root):
    if published_alpha3 is None:
        raise SystemExit('published alpha.3 binary required to verify stopped-backup restore')
    from smoke import pdf_fixture
    data = root / 'alpha3-schema6-data'
    historical_source = root / 'actual-published-alpha3-source'
    historical_source.mkdir()
    (historical_source / 'guide.pdf').write_bytes(pdf_fixture(['intro', 'oldcandidateevidence retained page two']))
    indexed = json.loads(invoke(published_alpha3, ['index', '--data-dir', str(data), '--report', '--json', str(historical_source)]).stdout)
    source_id = indexed['source']['id']
    dbpath = data / 'findrail.db'
    db = sqlite3.connect(dbpath)
    if db.execute('PRAGMA user_version').fetchone()[0] != 6:
        raise SystemExit('actual published alpha.3 did not create schema 6')
    before = {
        'sources': db.execute('SELECT COUNT(*) FROM sources').fetchone()[0],
        'source_provenance': db.execute('SELECT id,kind,name,root FROM sources ORDER BY id').fetchall(),
        'documents': db.execute('SELECT COUNT(*) FROM documents').fetchone()[0],
        'document_provenance': db.execute('SELECT id,source_id,title,uri,path,media_type,page_count FROM documents ORDER BY id').fetchall(),
        'hash': db.execute("SELECT content_hash FROM documents ORDER BY id LIMIT 1").fetchone()[0],
        'search': db.execute("SELECT COUNT(*) FROM documents_fts WHERE documents_fts MATCH 'oldcandidateevidence'").fetchone()[0],
        'pages': db.execute("SELECT page_number,content FROM document_pages ORDER BY document_id,page_number").fetchall(),
        'report': db.execute('SELECT report_id,report_json,indexed_documents FROM indexing_reports WHERE source_id=?', (source_id,)).fetchone(),
    }
    db.close()
    if before['sources'] != 1 or before['documents'] != 1 or before['search'] != 1 or before['pages'][1][0] != 2:
        raise SystemExit('actual alpha.3 schema-6 baseline evidence is invalid')
    # Prove the fixture is readable by the real old executable before migration.
    old_listing = json.loads(invoke(published_alpha3, ['sources', '--data-dir', str(data), '--json']).stdout)
    old_search = json.loads(invoke(published_alpha3, ['search', '--data-dir', str(data), '--json', 'oldcandidateevidence']).stdout)
    if len(old_listing['sources']) != 1 or old_search['total'] != 1 or old_search['results'][0].get('page') != 2:
        raise SystemExit('published alpha.3 cannot read the schema-6 baseline')

    backup = root / 'stopped-alpha3-schema6-backup'
    shutil.copytree(data, backup)
    stopped_digest = directory_digest(backup)
    listing = json.loads(invoke(candidate, ['sources', '--data-dir', str(data), '--json']).stdout)
    if len(listing['sources']) != 1:
        raise SystemExit('schema-6 migration lost source provenance')
    search_result = json.loads(invoke(candidate, ['search', '--data-dir', str(data), '--json', 'oldcandidateevidence']).stdout)
    if search_result['total'] != 1 or search_result['results'][0]['page'] != 2:
        raise SystemExit('schema-6 migration lost search or PDF page provenance')
    report = json.loads(invoke(candidate, ['source-report', '--data-dir', str(data), '--source', source_id, '--json']).stdout)
    if report.get('available') is not True or report['report'].get('indexed_documents') != 1:
        raise SystemExit('schema-6 indexing report did not survive migration')
    upgraded = sqlite3.connect(dbpath)
    schema = upgraded.execute('PRAGMA user_version').fetchone()[0]
    after = {
        'sources': upgraded.execute('SELECT COUNT(*) FROM sources').fetchone()[0],
        'source_provenance': upgraded.execute('SELECT id,kind,name,root FROM sources ORDER BY id').fetchall(),
        'documents': upgraded.execute('SELECT COUNT(*) FROM documents').fetchone()[0],
        'document_provenance': upgraded.execute('SELECT id,source_id,title,uri,path,media_type,page_count FROM documents ORDER BY id').fetchall(),
        'hash': upgraded.execute("SELECT content_hash FROM documents ORDER BY id LIMIT 1").fetchone()[0],
        'search': upgraded.execute("SELECT COUNT(*) FROM documents_fts WHERE documents_fts MATCH 'oldcandidateevidence'").fetchone()[0],
        'pages': upgraded.execute("SELECT page_number,content FROM document_pages ORDER BY document_id,page_number").fetchall(),
        'report': upgraded.execute('SELECT report_id,report_json,indexed_documents FROM indexing_reports WHERE source_id=?', (source_id,)).fetchone(),
    }
    collection_tables = upgraded.execute("SELECT name FROM sqlite_master WHERE type='table' AND name IN ('evidence_collections','evidence_collection_entries') ORDER BY name").fetchall()
    upgraded.close()
    if schema != 8 or before != after or len(collection_tables) != 2:
        raise SystemExit(f'published alpha.3 upgrade was not additive/preserved (schema={schema}, before={before}, after={after})')
    schema8_digest = directory_digest(data)
    rejected = invoke(published_alpha3, ['sources', '--data-dir', str(data), '--json'], ok=False)
    if 'unsupported index schema 8' not in rejected.stderr.lower() or directory_digest(data) != schema8_digest:
        raise SystemExit('published alpha.3 did not reject schema 8 without changing it')

    # Persist a named collection through a real candidate server restart.
    port = free_port(); authority = f'127.0.0.1:{port}'; base = 'http://' + authority
    def start_candidate():
        return subprocess.Popen([str(candidate), 'start', '--data-dir', str(data), '--no-open', '--addr', authority],
                                cwd=root, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
    process = start_candidate()
    try:
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError('schema-8 candidate server exited: ' + process.stderr.read())
            try:
                with urllib.request.urlopen(base + '/api/v1/capabilities', timeout=1) as response:
                    if json.load(response).get('collection_persistence'):
                        break
            except (OSError, urllib.error.URLError):
                time.sleep(.05)
        else:
            raise RuntimeError('schema-8 candidate collections did not become available')
        with urllib.request.urlopen(urllib.request.Request(base + '/api/v1/session', headers={'Origin': base}), timeout=3) as response:
            token = json.load(response)['token']
        headers = {'Origin': base, 'Content-Type': 'application/json', 'Sec-Fetch-Site': 'same-origin', 'X-Findrail-Token': token}
        req = urllib.request.Request(base + '/api/v1/collections', data=json.dumps({'title': 'Alpha.3 upgrade journey'}).encode(), method='POST', headers=headers)
        with urllib.request.urlopen(req, timeout=3) as response:
            collection_id = json.load(response)['id']
    finally:
        process.terminate(); process.communicate(timeout=8)
    process = start_candidate()
    try:
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError('candidate restart exited: ' + process.stderr.read())
            try:
                with urllib.request.urlopen(base + '/api/v1/capabilities', timeout=1):
                    break
            except (OSError, urllib.error.URLError):
                time.sleep(.05)
        else:
            raise RuntimeError('candidate restart did not become ready')
        with urllib.request.urlopen(urllib.request.Request(base + '/api/v1/session', headers={'Origin': base}), timeout=3) as response:
            restored_token = json.load(response)['token']
        request = urllib.request.Request(base + '/api/v1/collections/' + collection_id,
            headers={'Origin': base, 'X-Findrail-Token': restored_token})
        with urllib.request.urlopen(request, timeout=3) as response:
            restored_collection = json.load(response)
        if restored_collection.get('title') != 'Alpha.3 upgrade journey':
            raise SystemExit('schema-8 collection did not restore after candidate process restart')
    finally:
        process.terminate(); process.communicate(timeout=8)
    restored = root / 'restored-alpha3-backup'
    shutil.copytree(backup, restored)
    if directory_digest(restored) != stopped_digest:
        raise SystemExit('stopped schema-6 backup restore changed bytes')
    invoke(published_alpha3, ['sources', '--data-dir', str(restored), '--json'])
    restored_search = json.loads(invoke(published_alpha3, ['search', '--data-dir', str(restored), '--json', 'oldcandidateevidence']).stdout)
    if restored_search['total'] != 1 or restored_search['results'][0].get('page') != 2:
        raise SystemExit('published alpha.3 cannot search its restored schema-6 backup')
    restored_db = sqlite3.connect(restored / 'findrail.db')
    restored_schema = restored_db.execute('PRAGMA user_version').fetchone()[0]
    restored_collection_table = restored_db.execute("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='evidence_collections'").fetchone()[0]
    restored_db.close()
    if restored_schema != 6 or restored_collection_table:
        raise SystemExit('published alpha.3 did not read its restored schema-6 backup unchanged')
    if directory_digest(restored) != stopped_digest:
        raise SystemExit('published alpha.3 changed its restored stopped backup')
    print(f'Published alpha.3-created schema-6 fixture migrated to schema 8 with search, source/document provenance, indexing report, and collection tables; stopped backup restored and opened read-only by the published alpha.3 binary; new collection {collection_id!r} restored after candidate restart. No downgrade attempted.')


def upgrade_alpha4_schema7(candidate, published_alpha4, root):
    """Migrate a real alpha.4-created index and prove its stopped backup is restorable."""
    if published_alpha4 is None:
        raise SystemExit('published alpha.4 binary required to verify schema-7 backup restore')
    import socket
    import urllib.error
    import urllib.request

    data = root / 'alpha4-schema7-data'
    source_root = root / 'published alpha4 synthetic originals'
    source_root.mkdir()
    (source_root / 'first.md').write_text('published alpha4 legacyfirst passage', encoding='utf-8')
    (source_root / 'second.md').write_text('published alpha4 legacysecond passage', encoding='utf-8')
    indexed = json.loads(invoke(published_alpha4, ['index', '--data-dir', str(data), '--report', '--json', str(source_root)]).stdout)
    source_id = indexed['source']['id']
    if indexed.get('report', {}).get('committed') is not True:
        raise SystemExit('published alpha.4 did not commit its synthetic indexing report')
    baseline = sqlite3.connect(data / 'findrail.db')
    if baseline.execute('PRAGMA user_version').fetchone()[0] != 7:
        raise SystemExit('published alpha.4 did not create a schema-7 index')
    report_count = baseline.execute('SELECT COUNT(*) FROM indexing_reports WHERE source_id=?', (source_id,)).fetchone()[0]
    baseline.close()
    if report_count < 1:
        raise SystemExit('published alpha.4 index has no committed diagnostics report')

    # Create and save a real collection through the published alpha.4 API.
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', 0)); port = probe.getsockname()[1]
    authority, base = f'127.0.0.1:{port}', f'http://127.0.0.1:{port}'
    server = subprocess.Popen([str(published_alpha4), 'start', '--data-dir', str(data), '--no-open', '--addr', authority],
                              cwd=root, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)

    def request(path, *, method='GET', payload=None, token=None):
        headers = {'Origin': base}
        body = None
        if payload is not None:
            body = json.dumps(payload).encode()
            headers['Content-Type'] = 'application/json'
        if token:
            headers.update({'X-Findrail-Token': token, 'Sec-Fetch-Site': 'same-origin'})
        req = urllib.request.Request(base + path, data=body, method=method, headers=headers)
        with urllib.request.urlopen(req, timeout=4) as response:
            return json.load(response)

    try:
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if server.poll() is not None:
                raise RuntimeError('published alpha.4 server exited: ' + server.stderr.read())
            try:
                session = request('/api/v1/session')
                break
            except (OSError, urllib.error.URLError):
                time.sleep(.05)
        else:
            raise RuntimeError('published alpha.4 did not start for schema-7 collection fixture')
        token = session['token']
        collection = request('/api/v1/collections', method='POST', payload={'title': 'Published alpha.4 saved evidence'}, token=token)
        collection_id = collection['id']
        entries = []
        for term in ('legacyfirst', 'legacysecond'):
            result = json.loads(invoke(published_alpha4, ['search', '--data-dir', str(data), '--source', source_id, '--json', term]).stdout)
            evidence = result['results'][0]
            excerpt = 'published alpha4 ' + term + ' passage'
            pinned = request(f"/api/v1/collections/{collection_id}/entries", method='POST', token=token,
                payload={'revision': collection['revision'], 'document_id': evidence['id'], 'page': 0,
                         'excerpt': excerpt, 'whole_preview': True, 'original_identity': evidence['original_identity']})
            collection = pinned
            entry = collection['entries'][-1]
            entries.append({'key': entry['key'], 'note': 'saved alpha.4 note for ' + term,
                'document_id': evidence['id'], 'page': 0, 'excerpt': excerpt, 'whole_preview': True,
                'original_identity': evidence['original_identity']})
        saved = request('/api/v1/collections/' + collection_id, method='PUT', token=token,
            payload={'revision': collection['revision'], 'title': collection['title'], 'entries': list(reversed(entries))})
        if [entry['note'] for entry in saved['entries']] != ['saved alpha.4 note for legacysecond', 'saved alpha.4 note for legacyfirst']:
            raise SystemExit('published alpha.4 collection note/order setup failed')
    finally:
        if server.poll() is None:
            server.terminate()
        try:
            server.communicate(timeout=8)
        except subprocess.TimeoutExpired:
            server.kill(); server.communicate(timeout=3)

    backup = root / 'stopped-alpha4-schema7-backup'
    shutil.copytree(data, backup)
    backup_fingerprint = directory_digest(backup)
    def stored_state(db):
        return {
            'sources': db.execute('SELECT id,kind,name,root FROM sources ORDER BY id').fetchall(),
            'documents': db.execute('SELECT id,source_id,title,uri,path,content_hash FROM documents ORDER BY id').fetchall(),
            'report': db.execute('SELECT report_id,report_json,indexed_documents FROM indexing_reports WHERE source_id=?', (source_id,)).fetchall(),
            'collection': db.execute('SELECT c.id,c.title,c.revision,e.entry_id,e.ordinal,e.source_id,e.document_id,e.snapshot_json FROM evidence_collections c JOIN evidence_collection_entries e ON e.collection_id=c.id WHERE c.id=? ORDER BY e.ordinal', (collection_id,)).fetchall(),
        }

    baseline = sqlite3.connect(backup / 'findrail.db')
    before = stored_state(baseline)
    baseline.close()
    if len(before['documents']) != 2 or len(before['collection']) != 2:
        raise SystemExit('schema-7 fixture is missing real documents or saved collection entries')

    # The candidate migrates the original. The separate stopped copy remains for
    # a real alpha.4 open/search fingerprint check after migration.
    listing = json.loads(invoke(candidate, ['sources', '--data-dir', str(data), '--json']).stdout)
    if len(listing['sources']) != 1 or listing['sources'][0]['id'] != source_id:
        raise SystemExit('schema-7 migration lost source provenance')
    for term in ('legacyfirst', 'legacysecond'):
        result = json.loads(invoke(candidate, ['search', '--data-dir', str(data), '--source', source_id, '--json', term]).stdout)
        if result['total'] != 1:
            raise SystemExit('schema-7 migration lost searchable content: ' + term)
    report = json.loads(invoke(candidate, ['source-report', '--data-dir', str(data), '--source', source_id, '--json']).stdout)
    if report.get('available') is not True or report.get('report', {}).get('indexed_documents') != 2:
        raise SystemExit('schema-7 migration lost its committed indexing report')
    upgraded = sqlite3.connect(data / 'findrail.db')
    if upgraded.execute('PRAGMA user_version').fetchone()[0] != 8:
        raise SystemExit('schema-7 index did not migrate to schema 8')
    start_values = upgraded.execute('SELECT preview_start_char FROM evidence_collection_entries WHERE collection_id=? ORDER BY ordinal', (collection_id,)).fetchall()
    after = stored_state(upgraded)
    upgraded.close()
    if start_values != [(0,), (0,)]:
        raise SystemExit('schema-7 collection did not migrate with legacy start=0')
    changed = [key for key in before if after[key] != before[key]]
    if changed:
        raise SystemExit('schema-7 migration changed saved state: ' + ', '.join(changed))

    # A schema-8 candidate must refuse the actual published alpha.4 binary; no
    # downgrade is attempted and rejection must not mutate the candidate index.
    schema8_fingerprint = directory_digest(data)
    rejected = invoke(published_alpha4, ['sources', '--data-dir', str(data), '--json'], ok=False)
    if 'unsupported index schema 8' not in rejected.stderr.lower():
        raise SystemExit('published alpha.4 failed for a reason other than unsupported schema 8: ' + rejected.stderr)
    if directory_digest(data) != schema8_fingerprint:
        raise SystemExit('published alpha.4 changed the schema-8 candidate data directory while rejecting it')

    restored = root / 'restored-alpha4-schema7-backup'
    shutil.copytree(backup, restored)
    if directory_digest(restored) != backup_fingerprint:
        raise SystemExit('stopped alpha.4 backup restore changed bytes')
    restored_list = json.loads(invoke(published_alpha4, ['sources', '--data-dir', str(restored), '--json']).stdout)
    restored_search = json.loads(invoke(published_alpha4, ['search', '--data-dir', str(restored), '--source', source_id, '--json', 'legacysecond']).stdout)
    if len(restored_list['sources']) != 1 or restored_search['total'] != 1 or directory_digest(restored) != backup_fingerprint:
        raise SystemExit('published alpha.4 did not read its unchanged restored schema-7 backup')

    # Force migration 008 to fail after an otherwise valid schema-7 fixture.
    failed = root / 'failed-alpha4-schema7-migration'
    shutil.copytree(backup, failed)
    failed_db = sqlite3.connect(failed / 'findrail.db')
    failed_db.execute('ALTER TABLE evidence_collection_entries ADD COLUMN preview_start_char INTEGER NOT NULL DEFAULT 0')
    failed_db.commit(); failed_db.close()
    failure_before = directory_digest(failed)
    rejected = invoke(candidate, ['sources', '--data-dir', str(failed), '--json'], ok=False)
    if 'duplicate column' not in rejected.stderr.lower() or 'preview_start_char' not in rejected.stderr:
        raise SystemExit('schema-8 migration failed for an unexpected reason: ' + rejected.stderr)
    if directory_digest(failed) != failure_before:
        raise SystemExit('failed schema-8 migration was not an atomic rollback')
    print(f'Published alpha.4 schema-7 index migrated to candidate schema 8: source/search/report and two saved ordered notes preserved with legacy start=0; full stopped backup restored and read by real alpha.4; real alpha.4 rejected schema 8 without mutation; induced migration failure rolled back atomically. Collection {collection_id!r}.')


def published_alpha5_schema8_same_schema(candidate, published_alpha5, root):
    """Prove beta opens and preserves an actual published alpha.5 schema-8 index."""
    if published_alpha5 is None:
        raise SystemExit('published alpha.5 binary required for beta same-schema compatibility')
    import socket, urllib.error, urllib.request
    data, originals = root / 'alpha5-schema8-data', root / 'alpha5 synthetic originals'
    originals.mkdir()
    (originals / 'alpha5-compat.md').write_text('published alpha5compatibility provenance marker', encoding='utf-8')
    indexed = json.loads(invoke(published_alpha5, ['index', '--data-dir', str(data), '--report', '--json', str(originals)]).stdout)
    source_id = indexed['source']['id']
    if indexed.get('report', {}).get('committed') is not True:
        raise SystemExit('published alpha.5 did not commit its source index')
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', 0)); port = probe.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    server = subprocess.Popen([str(published_alpha5), 'start', '--data-dir', str(data), '--no-open', '--addr', f'127.0.0.1:{port}'],
                              cwd=root, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
    def request(path, method='GET', payload=None, token=None):
        headers = {'Origin': base}; body = None
        if payload is not None:
            body = json.dumps(payload).encode(); headers['Content-Type'] = 'application/json'
        if token: headers.update({'X-Findrail-Token': token, 'Sec-Fetch-Site': 'same-origin'})
        req = urllib.request.Request(base + path, data=body, method=method, headers=headers)
        with urllib.request.urlopen(req, timeout=4) as response: return json.load(response)
    try:
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if server.poll() is not None: raise RuntimeError('published alpha.5 server exited')
            try: session = request('/api/v1/session'); break
            except (OSError, urllib.error.URLError): time.sleep(.05)
        else: raise RuntimeError('published alpha.5 did not start')
        result = json.loads(invoke(published_alpha5, ['search', '--data-dir', str(data), '--source', source_id, '--json', 'alpha5compatibility']).stdout)
        evidence = result['results'][0]
        collection = request('/api/v1/collections', 'POST', {'title': 'Alpha5 same-schema fixture'}, session['token'])
        collection = request(f"/api/v1/collections/{collection['id']}/entries", 'POST', {
            'revision': collection['revision'], 'document_id': evidence['id'], 'page': 0,
            'excerpt': evidence['excerpt'], 'whole_preview': True, 'original_identity': evidence['original_identity']}, session['token'])
        collection_id = collection['id']
    finally:
        if server.poll() is None: server.terminate()
        try: server.communicate(timeout=8)
        except subprocess.TimeoutExpired: server.kill(); server.communicate(timeout=3)
    db = sqlite3.connect(data / 'findrail.db')
    before = {'schema': db.execute('PRAGMA user_version').fetchone()[0],
              'sources': db.execute('SELECT id,kind,name,root FROM sources ORDER BY id').fetchall(),
              'documents': db.execute('SELECT id,source_id,title,uri,path,content_hash FROM documents ORDER BY id').fetchall(),
              'collections': db.execute('SELECT id,title,revision FROM evidence_collections ORDER BY id').fetchall(),
              'entries': db.execute('SELECT collection_id,ordinal,source_id,document_id,snapshot_json FROM evidence_collection_entries ORDER BY collection_id,ordinal').fetchall()}
    db.close()
    if before['schema'] != 8 or not before['entries']:
        raise SystemExit('published alpha.5 fixture lacks schema-8 collection content')
    if json.loads(invoke(candidate, ['sources', '--data-dir', str(data), '--json']).stdout)['sources'][0]['id'] != source_id:
        raise SystemExit('beta candidate lost alpha.5 source provenance')
    if json.loads(invoke(candidate, ['search', '--data-dir', str(data), '--source', source_id, '--json', 'alpha5compatibility']).stdout)['total'] != 1:
        raise SystemExit('beta candidate lost alpha.5 indexed content')
    db = sqlite3.connect(data / 'findrail.db')
    after = {'schema': db.execute('PRAGMA user_version').fetchone()[0],
             'sources': db.execute('SELECT id,kind,name,root FROM sources ORDER BY id').fetchall(),
             'documents': db.execute('SELECT id,source_id,title,uri,path,content_hash FROM documents ORDER BY id').fetchall(),
             'collections': db.execute('SELECT id,title,revision FROM evidence_collections ORDER BY id').fetchall(),
             'entries': db.execute('SELECT collection_id,ordinal,source_id,document_id,snapshot_json FROM evidence_collection_entries ORDER BY collection_id,ordinal').fetchall()}
    db.close()
    if before != after: raise SystemExit('beta candidate migrated or changed same-schema alpha.5 data')
    stopped_backup = root / 'stopped-alpha5-schema8-backup'; shutil.copytree(data, stopped_backup)
    invoke(candidate, ['sources', '--data-dir', str(data), '--json'])
    restored = root / 'restored-alpha5-schema8-backup'; shutil.copytree(stopped_backup, restored)
    if json.loads(invoke(candidate, ['search', '--data-dir', str(restored), '--source', source_id, '--json', 'alpha5compatibility']).stdout)['total'] != 1:
        raise SystemExit('beta candidate could not reopen stopped alpha.5 backup')
    print(f'Published alpha.5 schema-8 index opened by beta without migration; content, source provenance and collection {collection_id!r} preserved across reopen and stopped-backup restore. Same-schema compatibility only.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=pathlib.Path)
    parser.add_argument('--alpha3-binary', type=pathlib.Path)
    parser.add_argument('--alpha4-binary', type=pathlib.Path)
    parser.add_argument('--alpha5-binary', type=pathlib.Path)
    parser.add_argument('--alpha5-only', action='store_true', help='run only the beta same-schema alpha.5 compatibility case')
    args = parser.parse_args()
    binary = args.binary.resolve()
    with tempfile.TemporaryDirectory(prefix='findrail-upgrade-') as temp:
        root = pathlib.Path(temp); data = root / 'legacy data'; data.mkdir()
        if args.alpha5_only:
            published_alpha5_schema8_same_schema(binary, args.alpha5_binary.resolve() if args.alpha5_binary else None, root)
            return
        source_root = root / 'synthetic originals'; source_root.mkdir()
        from smoke import docx_fixture
        (source_root / 'legacy.docx').write_bytes(docx_fixture('legacydocxconfiguredphrase preserved original'))
        dbpath, folder_id = create_schema2(data, source_root)
        historical = sqlite3.connect(dbpath)
        before_state = {
            'sources': historical.execute('SELECT COUNT(*) FROM sources').fetchone()[0],
            'documents': historical.execute('SELECT COUNT(*) FROM documents').fetchone()[0],
            'hash': historical.execute("SELECT content_hash FROM documents WHERE id='legacy-pdf-doc'").fetchone()[0],
            'search': historical.execute("SELECT COUNT(*) FROM documents_fts WHERE documents_fts MATCH 'oldcandidateevidence'").fetchone()[0],
            'pages': historical.execute("SELECT page_number,content FROM document_pages WHERE document_id='legacy-pdf-doc' ORDER BY page_number").fetchall(),
        }
        historical.close()
        if before_state['sources'] != 1 or before_state['documents'] != 1 or before_state['search'] != 1 or before_state['pages'][1][0] != 2:
            raise SystemExit('synthetic alpha.2 baseline inventory/search/PDF evidence invalid')
        before = directory_digest(data)
        backup = root / 'stopped full backup'
        shutil.copytree(data, backup)
        backup_hash = directory_digest(backup)
        if before != backup_hash: raise SystemExit('backup hash differs')
        listing = json.loads(invoke(binary, ['sources', '--data-dir', str(data), '--json']).stdout)
        if len(listing['sources']) != 1: raise SystemExit('migration lost alpha.2 source')
        result = json.loads(invoke(binary, ['search', '--data-dir', str(data), '--json', 'oldcandidateevidence']).stdout)
        if result['total'] != 1 or result['results'][0]['page'] != 2: raise SystemExit('migration lost PDF FTS/page evidence')
        updated = sqlite3.connect(dbpath)
        schema = updated.execute('PRAGMA user_version').fetchone()[0]
        after_state = {
            'sources': updated.execute('SELECT COUNT(*) FROM sources').fetchone()[0],
            'documents': updated.execute('SELECT COUNT(*) FROM documents').fetchone()[0],
            'hash': updated.execute("SELECT content_hash FROM documents WHERE id='legacy-pdf-doc'").fetchone()[0],
            'search': updated.execute("SELECT COUNT(*) FROM documents_fts WHERE documents_fts MATCH 'oldcandidateevidence'").fetchone()[0],
            'pages': updated.execute("SELECT page_number,content FROM document_pages WHERE document_id='legacy-pdf-doc' ORDER BY page_number").fetchall(),
        }
        policies = updated.execute('SELECT max_docx_bytes FROM sources WHERE id=?', (folder_id,)).fetchone()
        updated.close()
        from package import schema_version
        expected_schema = schema_version()
        if schema != expected_schema or policies != (0,): raise SystemExit(f'schema-{expected_schema} migration state mismatch')
        if before_state != after_state: raise SystemExit('migration changed the recorded inventory, content hash, FTS or PDF pages')
        report = json.loads(invoke(binary, ['source-report', '--data-dir', str(data), '--source', folder_id, '--json']).stdout)
        if report.get('available') is not False or report.get('availability') != 'not_yet_available':
            raise SystemExit('schema migration fabricated a source report')
        configure_legacy_docx(binary, data, root, folder_id)
        sources = json.loads(invoke(binary, ['sources', '--data-dir', str(data), '--json']).stdout)['sources']
        legacy = next(source for source in sources if source['id'] == folder_id)
        if legacy['max_docx_bytes'] != 8388608: raise SystemExit('DOCX policy was not persisted across restart')
        result = json.loads(invoke(binary, ['search', '--data-dir', str(data), '--json', 'legacydocxconfiguredphrase']).stdout)
        if result['total'] != 1: raise SystemExit('explicitly configured legacy DOCX was not indexed')
        restored = root / 'rollback restore'
        shutil.copytree(backup, restored)
        if directory_digest(restored) != before:
            raise SystemExit('whole-directory rollback restore differed')
        restored_db = sqlite3.connect(restored / 'findrail.db')
        restored_schema = restored_db.execute('PRAGMA user_version').fetchone()[0]
        restored_state = restored_db.execute("SELECT id,content_hash FROM documents WHERE id='legacy-pdf-doc'").fetchone()
        restored_db.close()
        if restored_schema != 2 or restored_state != ('legacy-pdf-doc', 'synthetic-hash'):
            raise SystemExit('rollback did not preserve the original schema-2 fixture')
        print(f'Synthetic schema-2 upgrade passed: source inventory, FTS/PDF page, schema-{expected_schema} disabled DOCX default, unavailable pre-scan diagnostics, Configure/index/restart, stopped full-directory rollback including sidecars; restored schema remains 2. This is not an actual alpha.2 binary restore. No reverse migration was attempted.')
        upgrade_alpha3_schema6(binary, args.alpha3_binary.resolve() if args.alpha3_binary else None, root)
        upgrade_alpha4_schema7(binary, args.alpha4_binary.resolve() if args.alpha4_binary else None, root)
        if args.alpha5_binary:
            published_alpha5_schema8_same_schema(binary, args.alpha5_binary.resolve(), root)


if __name__ == '__main__': main()
