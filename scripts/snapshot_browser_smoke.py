#!/usr/bin/env python3
"""Run the Sources portable snapshot workflow in Chromium against the supplied binary."""
import json
import os
import pathlib
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request

from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.common.keys import Keys
from selenium.webdriver.support.ui import Select, WebDriverWait

sys.dont_write_bytecode = True
from smoke import docx_fixture, pdf_fixture


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def main():
    binary = pathlib.Path(sys.argv[1]).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix='Findrail snapshot browser – ') as td:
        base = pathlib.Path(td)
        root, index_a, index_b = base / 'Synthetic notes – 雪', base / 'index A', base / 'index B'
        root.mkdir()
        (root / 'note.md').write_text('synthetic portable snapshot exact quote', encoding='utf-8')
        (root / 'word.docx').write_bytes(docx_fixture('synthetic portable DOCX body'))
        (root / 'manual.pdf').write_bytes(pdf_fixture(['first page', 'synthetic portable PDF page two']))
        archive, changed = base / 'snapshot – 雪.zip', base / 'changed.zip'
        alternate = base / 'alternate.zip'
        env = {**os.environ, 'NO_PROXY': '127.0.0.1,localhost', 'no_proxy': '127.0.0.1,localhost'}
        subprocess.run([str(binary), 'index', '--data-dir', str(index_a), '--max-docx-bytes', str(8 << 20), str(root)],
                       check=True, capture_output=True, text=True, env=env)
        address = f'127.0.0.1:{free_port()}'; url = 'http://' + address
        process = browser = None

        def launch(index, mode='start'):
            nonlocal process
            args = [str(binary), mode, '--data-dir', str(index), '--addr', address]
            args += ['--no-open'] if mode == 'start' else ['--no-sync']
            process = subprocess.Popen(args, cwd=base, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
            deadline = time.monotonic() + 20
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise AssertionError('snapshot server exited: ' + process.stderr.read())
                try:
                    with urllib.request.urlopen(url + '/healthz', timeout=1): return
                except OSError: time.sleep(.05)
            raise AssertionError('snapshot server startup timed out')

        def stop():
            if process and process.poll() is None:
                process.terminate()
                try: process.communicate(timeout=8)
                except subprocess.TimeoutExpired:
                    process.kill(); process.communicate(timeout=3)
                    raise AssertionError('snapshot server failed to drain')

        def inventory():
            with urllib.request.urlopen(url + '/api/v1/sources', timeout=3) as response:
                return json.load(response)['sources']

        try:
            launch(index_a)
            options = webdriver.ChromeOptions()
            options.binary_location = os.environ.get('FINDRAIL_CHROME') or shutil.which('chromium') or shutil.which('google-chrome') or ''
            for flag in ('--headless=new', '--no-sandbox', '--disable-dev-shm-usage', '--no-proxy-server'):
                options.add_argument(flag)
            driver = os.environ.get('FINDRAIL_CHROMEDRIVER')
            browser = webdriver.Chrome(service=Service(executable_path=driver) if driver else Service(), options=options)
            browser.set_page_load_timeout(15)
            wait = WebDriverWait(browser, 25)
            element = lambda name: browser.find_element(By.ID, name)

            def page():
                browser.get(url + '/')
                wait.until(lambda _: element('snapshot-inspect-open').is_displayed())
                browser.execute_script('''
                  window.__snapshotEffects={network:[],writes:[],searches:[],mutations:[]};
                  const original=window.fetch.bind(window);
                  window.fetch=(input,options={})=>{const target=new URL(String(input),location.href);if(target.origin!==location.origin)window.__snapshotEffects.network.push(target.href);
                    if(target.pathname.startsWith('/api/v1/search'))window.__snapshotEffects.searches.push(target.href);
                    if(options.method&&options.method!=='GET')window.__snapshotEffects.mutations.push(target.pathname);
                    return original(input,options)};
                  const write=Storage.prototype.setItem;Storage.prototype.setItem=function(...args){window.__snapshotEffects.writes.push('storage');return write.apply(this,args)};
                  const open=indexedDB.open.bind(indexedDB);indexedDB.open=(...args)=>{window.__snapshotEffects.writes.push('indexedDB');return open(...args)};
                  for(const name of ['pushState','replaceState']){const original=history[name].bind(history);history[name]=(...args)=>{window.__snapshotEffects.writes.push(name);return original(...args)}};
                ''')

            def close():
                element('snapshot-close').click()
                wait.until(lambda _: not element('snapshot-transfer').get_property('open'))

            def open_inspect(path):
                element('snapshot-inspect-open').click()
                wait.until(lambda _: element('snapshot-submit').is_enabled())
                element('snapshot-path').send_keys(str(path)); element('snapshot-submit').click()

            def inspect(path):
                open_inspect(path)
                wait.until(lambda _: element('snapshot-submit').text == 'Import frozen source' and element('snapshot-submit').is_enabled())
                assert browser.execute_script('return document.activeElement.id') == 'snapshot-name'
                assert '3 documents' in element('snapshot-summary').text and '2 pages' in element('snapshot-summary').text
                assert str(root) not in element('snapshot-summary').text and 'exact quote' not in element('snapshot-summary').text

            def search(query, kind='all'):
                element('query').clear(); element('query').send_keys(query)
                Select(element('format')).select_by_value(kind); element('submit').click()
                wait.until(lambda _: '1 matching' in element('status').text and element('submit').is_enabled())
                browser.find_element(By.CSS_SELECTOR, '.result-preview').click()
                wait.until(lambda _: query in element('preview-body').text)

            page()
            # Let the initial folder watcher commit before opening a source-bound export.
            previous = None; stable_since = time.monotonic()
            deadline = time.monotonic() + 20
            while time.monotonic() < deadline:
                rows = inventory(); current = [(s['id'], s['documents'], s['last_indexed_at']) for s in rows]
                if current == previous and rows[0]['documents'] == 3 and time.monotonic() - stable_since > 2: break
                if current != previous: previous = current; stable_since = time.monotonic()
                time.sleep(.1)
            else: raise AssertionError('source did not settle')
            search('exact quote', 'text')
            element('whole-preview').click(); element('add-evidence').click(); element('close-preview').click(); element('collection-open').click()
            browser.find_element(By.CSS_SELECTOR, '.collection-row textarea').send_keys('unsaved snapshot safety note')
            pinned = browser.execute_script('return window.localEvidenceCollection.entries')
            element('collection-close').click()
            # Opening and exporting preserves query, pinned text and dirty notes.
            source_id = inventory()[0]['id']
            browser.find_element(By.CSS_SELECTOR, f'.source-card[data-id="{source_id}"] button[title^="Export indexed"]').click()
            wait.until(lambda _: element('snapshot-submit').is_enabled())
            element('snapshot-path').send_keys(str(archive)); element('snapshot-submit').click()
            wait.until(lambda _: 'Snapshot exported and verified' in element('snapshot-summary').text)
            assert archive.is_file() and 'fingerprint' in element('snapshot-summary').text
            assert browser.execute_script('return document.activeElement.id') == 'snapshot-close'
            assert browser.execute_script('return window.localEvidenceCollection.entries') == pinned
            close()
            assert browser.execute_script('return document.activeElement.textContent') == 'Export snapshot'
            # No accidental overwrite, and no retry of an output request on failure.
            original = archive.read_bytes()
            browser.find_element(By.CSS_SELECTOR, f'.source-card[data-id="{source_id}"] button[title^="Export indexed"]').click()
            wait.until(lambda _: element('snapshot-submit').is_enabled())
            element('snapshot-path').send_keys(str(archive)); element('snapshot-submit').click()
            wait.until(lambda _: element('snapshot-submit').is_enabled())
            assert 'exported and verified' not in element('snapshot-summary').text and archive.read_bytes() == original
            close()
            assert browser.execute_script('return window.__snapshotEffects.network') == []
            assert browser.execute_script('return window.__snapshotEffects.writes') == []
            stop()
            # A second valid archive changes the semantic fingerprint after inspection.
            (root / 'note.md').write_text('different synthetic snapshot content', encoding='utf-8')
            subprocess.run([str(binary), 'index', '--data-dir', str(index_a), '--max-docx-bytes', str(8 << 20), str(root)], check=True, capture_output=True, text=True, env=env)
            subprocess.run([str(binary), 'export-source', '--data-dir', str(index_a), '--source', source_id, '--output', str(alternate)], check=True, capture_output=True, text=True, env=env)
            launch(index_b); page()
            # A real malformed archive must return a visible recoverable error.
            malformed = base / 'malformed.zip'; malformed.write_bytes(b'not a zip')
            open_inspect(malformed)
            wait.until(lambda _: element('snapshot-submit').is_enabled() and 'Inspect again' in element('snapshot-summary').text)
            assert element('snapshot-summary').is_displayed() and inventory() == []
            close()
            # A delayed real inspection response cannot populate a reopened dialog.
            browser.execute_script('''
              const original=window.fetch.bind(window);window.__snapshotFetch=original;
              window.fetch=async(input,options)=>{const response=await original(input,options);
                if(String(input).includes('/snapshots/inspections/')){window.__snapshotDelayed=response;return new Promise(resolve=>{window.__releaseSnapshot=()=>resolve(response)})}return response};
            ''')
            open_inspect(archive)
            wait.until(lambda _: browser.execute_script("return typeof window.__releaseSnapshot==='function'"))
            element('snapshot-close').click(); element('snapshot-inspect-open').click()
            wait.until(lambda _: not element('snapshot-check').get_property('hidden'))
            before = element('snapshot-summary').text
            browser.execute_script('window.__releaseSnapshot();window.fetch=window.__snapshotFetch')
            time.sleep(.2)
            assert element('snapshot-summary').text == before and element('snapshot-name').get_property('hidden')
            element('snapshot-check').click(); wait.until(lambda _: element('snapshot-submit').is_enabled()); close()
            # Changed bytes after inspection fail before creating any source.
            shutil.copyfile(archive, changed); inspect(changed); changed.write_bytes(alternate.read_bytes())
            element('snapshot-name').send_keys('Should not be imported'); element('snapshot-submit').click()
            wait.until(lambda _: element('snapshot-submit').is_enabled() and element('snapshot-submit').text == 'Inspect snapshot')
            assert inventory() == []
            close(); inspect(archive)
            element('snapshot-name').send_keys('Frozen portable notes'); element('snapshot-submit').click()
            wait.until(lambda _: 'Frozen source “Frozen portable notes” imported' in element('snapshot-summary').text)
            assert browser.execute_script('return document.activeElement.id') == 'snapshot-close'
            close(); rows = inventory(); assert len(rows) == 1 and rows[0]['kind'] == 'archive'
            imported = rows[0]['id']; wait.until(lambda _: len(Select(element('source')).options) == 2)
            labels = [button.text for button in browser.find_elements(By.CSS_SELECTOR, f'.source-card[data-id="{imported}"] button')]
            assert 'Refresh' not in labels and 'Configure' not in labels
            assert str(root) not in browser.find_element(By.CSS_SELECTOR, f'.source-card[data-id="{imported}"]').text
            assert element('collection-count').text == '0', 'source ZIP unexpectedly transferred a collection'
            Select(element('source')).select_by_value(imported)
            for query, kind in [('exact quote', 'text'), ('DOCX body', 'docx'), ('page two', 'pdf')]:
                search(query, kind)
                assert 'Frozen imported snapshot' in element('preview-notice').text
                assert element('open-original').get_property('hidden') and element('open-folder').get_property('hidden')
                if kind == 'pdf': assert 'Page 2 / 2' in element('page-label').text
                element('close-preview').click()
            # Clear the query so inventory refresh cannot cause an unrelated background search.
            element('query').clear()
            browser.execute_script("document.getElementById('query').dispatchEvent(new Event('input'))")
            before_searches = browser.execute_script('return window.__snapshotEffects.searches.length')
            inspect(archive); element('snapshot-name').send_keys('Do not rename existing source'); element('snapshot-submit').click()
            wait.until(lambda _: 'already imported as' in element('snapshot-summary').text)
            assert 'Frozen portable notes' in element('snapshot-summary').text and inventory()[0]['name'] == 'Frozen portable notes'
            close()
            assert browser.execute_script('return window.__snapshotEffects.searches.length') == before_searches
            assert browser.execute_script('return window.__snapshotEffects.network') == []
            assert browser.execute_script('return window.__snapshotEffects.writes') == []
            stop(); launch(index_b); page()
            wait.until(lambda _: len(Select(element('source')).options) == 2)
            Select(element('source')).select_by_value(imported); search('page two', 'pdf'); element('close-preview').click()
            browser.find_element(By.CSS_SELECTOR, f'.source-card[data-id="{imported}"] button[title^="Removes only"]').click()
            element('confirm-remove').click(); wait.until(lambda _: inventory() == [])
            wait.until(lambda _: len(Select(element('source')).options) == 1)
            stop(); launch(index_b, 'serve'); browser.get(url + '/')
            wait.until(lambda _: browser.execute_async_script('const done=arguments[0];window.findrailBootstrap.then(x=>done(x.capabilities.management===false))'))
            assert element('management').get_property('hidden') and element('snapshot-inspect-open').get_property('hidden')
            print('Snapshot browser journey passed: native paths, export/verify, overwrite refusal, malformed and changed archive, late response, explicit import, frozen MD/DOCX/PDF page 2, duplicate identity/name, dirty notes, no storage/history/origin requests, restart/Forget, read-only capability.')
        finally:
            if browser: browser.quit()
            stop()


if __name__ == '__main__':
    main()
