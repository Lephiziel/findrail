#!/usr/bin/env python3
"""Exercise explicit preset files, lifecycle, privacy and keyboard flow in Chromium."""
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
from selenium.webdriver.common.action_chains import ActionChains
from selenium.webdriver.common.by import By
from selenium.webdriver.common.keys import Keys
from selenium.webdriver.support.ui import Select, WebDriverWait


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def main():
    binary = pathlib.Path(sys.argv[1]).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix='findrail-preset-browser-') as td:
        base = pathlib.Path(td)
        index, root, extra = base / 'index', base / 'Synthetic', base / 'Second'
        root.mkdir(); extra.mkdir()
        (root / 'notes.md').write_text('synthetic preset browser phrase', encoding='utf-8')
        (extra / 'other.md').write_text('a different synthetic source', encoding='utf-8')
        downloads = base / 'downloads'; downloads.mkdir()
        address = f'127.0.0.1:{free_port()}'; url = 'http://' + address
        env = {**os.environ, 'NO_PROXY': '127.0.0.1,localhost', 'no_proxy': '127.0.0.1,localhost'}

        def index_folder(path):
            subprocess.run([str(binary), 'index', '--data-dir', str(index), str(path)],
                           check=True, capture_output=True, text=True, env=env)

        def launch(mode):
            args = [str(binary), mode, '--data-dir', str(index), '--addr', address]
            args += ['--no-open'] if mode == 'start' else ['--no-sync']
            return subprocess.Popen(args, cwd=base, env=env, stdout=subprocess.DEVNULL,
                                    stderr=subprocess.PIPE, text=True)

        def ready(process):
            deadline = time.monotonic() + 20
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise AssertionError('preset server exited: ' + process.stderr.read())
                try:
                    with urllib.request.urlopen(url + '/healthz', timeout=1): return
                except OSError: time.sleep(.05)
            raise AssertionError('server did not start')

        index_folder(root)
        proc = launch('start'); browser = None
        try:
            ready(proc)
            options = webdriver.ChromeOptions()
            options.binary_location = os.environ.get('FINDRAIL_CHROME') or shutil.which('chromium') or shutil.which('google-chrome') or ''
            options.add_argument('--headless=new'); options.add_argument('--no-sandbox'); options.add_argument('--disable-dev-shm-usage')
            options.add_experimental_option('prefs', {'download.default_directory': str(downloads), 'download.prompt_for_download': False})
            from selenium.webdriver.chrome.service import Service
            browser = webdriver.Chrome(service=Service(executable_path=os.environ['FINDRAIL_CHROMEDRIVER']) if os.environ.get('FINDRAIL_CHROMEDRIVER') else Service(), options=options)
            wait = WebDriverWait(browser, 20)
            element = lambda name: browser.find_element(By.ID, name)

            def open_page():
                browser.get(url + '/')
                wait.until(lambda d: len(Select(element('source')).options) >= 2)

            def install_effect_spies():
                browser.execute_script("""
                    window.__presetSearches=[];window.__presetWrites=[];window.__presetMutations=[];
                    const original=window.fetch.bind(window);
                    window.fetch=(input,options)=>{const path=String(input);if(path.includes('/api/v1/search'))window.__presetSearches.push(path);
                        if(options?.method&&!['GET','HEAD'].includes(options.method))window.__presetMutations.push(path);return original(input,options)};
                    const store=Storage.prototype.setItem;Storage.prototype.setItem=function(...args){window.__presetWrites.push('storage');return store.apply(this,args)};
                    const database=indexedDB.open.bind(indexedDB);indexedDB.open=(...args)=>{window.__presetWrites.push('indexedDB');return database(...args)};
                    for(const method of ['pushState','replaceState']){const original=history[method].bind(history);history[method]=(...args)=>{window.__presetWrites.push(method);return original(...args)}};
                """)

            def no_preset_side_effects():
                time.sleep(.15)
                assert browser.execute_script('return window.__presetSearches.length') == 0
                assert browser.execute_script('return window.__presetWrites.length') == 0
                assert browser.execute_script('return window.__presetMutations.length') == 0

            def load(path):
                element('preset-file').send_keys(str(path))
                wait.until(lambda d: not element('preset-preview').get_property('hidden'))

            def run_explicit_search(source):
                element('submit').click()
                wait.until(lambda d: '1 matching' in element('status').text)
                calls = browser.execute_script('return window.__presetSearches')
                assert len(calls) == 1, calls
                from urllib.parse import parse_qs, urlsplit
                params = parse_qs(urlsplit(calls[0]).query)
                assert params['source'] == [source] and params['q'] == ['synthetic phrase'], params
                assert params['mode'] == ['advanced'] and params['format'] == ['text'], params
                assert params['path_prefix'] == ['notes.md'] and params['title_contains'] == ['notes'], params
                assert 'cursor' not in params

            open_page()
            source = json.load(urllib.request.urlopen(url + '/api/v1/sources'))['sources'][0]['id']
            # Real pinned evidence with an unsaved note must survive preset operations.
            element('query').send_keys('synthetic'); element('submit').click()
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, '.result-preview')) == 1)
            browser.find_element(By.CSS_SELECTOR, '.result-preview').click()
            wait.until(lambda d: 'synthetic preset browser phrase' in element('preview-body').text)
            if not element('whole-preview').is_selected(): element('whole-preview').click()
            element('add-evidence').click(); element('close-preview').click(); element('collection-open').click()
            browser.find_element(By.CSS_SELECTOR, '.collection-row textarea').send_keys('unsaved preset safety note')
            evidence = browser.execute_script('return window.localEvidenceCollection.entries')
            element('collection-close').click()
            browser.execute_script("""
                for(const [id,value] of Object.entries({'preset-title':'Synthetic preset',query:'synthetic phrase',mode:'advanced',format:'text','path-prefix':'notes.md','title-contains':'notes'}))document.getElementById(id).value=value;
                document.getElementById('source').value=arguments[0];document.getElementById('query').dispatchEvent(new Event('input'));
            """, source)
            install_effect_spies()
            element('preset-save').click(); path = downloads / 'findrail-search-preset.json'
            wait.until(lambda _: path.exists())
            saved = json.loads(path.read_text(encoding='utf-8'))
            assert saved['query'] == 'synthetic phrase' and saved['source']['id_hint'] == source
            assert element('preset-status').is_displayed() and 'downloaded' in element('preset-status').text
            browser.execute_script("""for(const [id,value] of Object.entries({query:'',mode:'literal',source:'',format:'all','path-prefix':'','title-contains':''}))document.getElementById(id).value=value;document.getElementById('query').dispatchEvent(new Event('input'));""")
            invalid = base / 'invalid.json'; invalid.write_bytes(b'\xff')
            element('preset-file').send_keys(str(invalid))
            wait.until(lambda d: 'UTF-8' in element('preset-status').text)
            assert element('preset-status').is_displayed() and element('preset-preview').get_property('hidden')
            assert element('query').get_attribute('value') == ''
            # Single-line inputs must not silently join words in a multiline file.
            multiline = base / 'multiline.json'; data = dict(saved); data['query'] = 'first\nsecond'; multiline.write_text(json.dumps(data), encoding='utf-8')
            load(multiline); Select(element('preset-source-map')).select_by_value(source); element('preset-apply').click()
            assert element('query').get_attribute('value') == ''
            assert 'single-line' in element('preset-status').text and element('preset-status').is_displayed()
            element('preset-cancel').click()
            load(path)
            assert browser.execute_script('return document.activeElement.id') == 'preset-source-map'
            assert not element('preset-apply').is_enabled()
            element('preset-source-map').send_keys(Keys.ESCAPE)
            wait.until(lambda d: element('preset-preview').get_property('hidden'))
            assert browser.execute_script('return document.activeElement.id') == 'preset-file'
            # A delayed native File read must not reopen a canceled preview.
            delayed = base / 'delayed.json'; delayed.write_bytes(path.read_bytes())
            browser.execute_script("""
                window.__arrayBuffer=File.prototype.arrayBuffer;
                File.prototype.arrayBuffer=function(){if(this.name!=='delayed.json')return window.__arrayBuffer.call(this);return new Promise(resolve=>{window.__finishPresetRead=()=>window.__arrayBuffer.call(this).then(bytes=>{resolve(bytes);window.__presetReadFinished=true})})};
            """)
            element('preset-file').send_keys(str(delayed))
            wait.until(lambda d: d.execute_script("return typeof window.__finishPresetRead==='function'"))
            browser.execute_script("document.getElementById('preset-file').focus()")
            ActionChains(browser).send_keys(Keys.ESCAPE).perform()
            browser.execute_script('window.__finishPresetRead();File.prototype.arrayBuffer=window.__arrayBuffer')
            wait.until(lambda d: d.execute_script('return window.__presetReadFinished===true'))
            assert element('preset-preview').get_property('hidden') and not element('preset-apply').is_enabled()
            # A missing hint never falls back to All or selects a matching name.
            missing = base / 'missing.json'; data = dict(saved); data['source'] = {**saved['source'], 'id_hint': 'missing-source'}
            missing.write_text(json.dumps(data), encoding='utf-8'); load(missing)
            assert 'missing' in element('preset-summary').text
            assert element('preset-source-map').get_attribute('value') == ''
            Select(element('preset-source-map')).select_by_value(source)
            index_folder(extra)
            wait.until(lambda d: len(Select(element('preset-source-map')).options) == 3)
            assert element('preset-source-map').get_attribute('value') == '' and not element('preset-apply').is_enabled()
            Select(element('preset-source-map')).select_by_value(source)
            no_preset_side_effects()
            element('preset-apply').send_keys(Keys.ENTER)
            assert browser.execute_script('return document.activeElement.id') == 'query'
            assert element('query').get_attribute('value') == 'synthetic phrase'
            no_preset_side_effects()
            assert browser.execute_script('return window.localEvidenceCollection.entries') == evidence
            run_explicit_search(source)
            assert browser.execute_script('return window.localEvidenceCollection.entries') == evidence
            # The same file workflow works in read-only serve composition.
            proc.terminate(); proc.communicate(timeout=8); proc = launch('serve'); ready(proc); open_page()
            assert json.load(urllib.request.urlopen(url + '/api/v1/capabilities'))['management'] is False
            assert element('preset-save').is_displayed() and element('preset-file').is_displayed()
            install_effect_spies(); load(path); Select(element('preset-source-map')).select_by_value(source)
            element('preset-apply').send_keys(Keys.ENTER); no_preset_side_effects(); run_explicit_search(source)
            print('Preset browser journey passed: explicit download/load/map/apply/search; visible errors; keyboard cancel/focus; canceled file reads; refreshed inventory; dirty notes; no hidden search/storage/history/mutations; read-only serve.')
        finally:
            if browser: browser.quit()
            if proc.poll() is None:
                proc.terminate()
                try: proc.communicate(timeout=8)
                except subprocess.TimeoutExpired: proc.kill(); proc.communicate(timeout=3)


if __name__ == '__main__': main()
