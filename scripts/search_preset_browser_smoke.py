#!/usr/bin/env python3
"""Exercise explicit preset save/load/apply in real Chromium against start."""
import os, pathlib, shutil, socket, subprocess, sys, tempfile, time, urllib.request, json
from selenium import webdriver
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait

def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0)); return sock.getsockname()[1]

def main():
    binary=pathlib.Path(sys.argv[1]).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix='findrail-preset-browser-') as td:
        base=pathlib.Path(td); index=base/'index'; root=base/'Synthetic'; root.mkdir(); (root/'notes.md').write_text('synthetic preset browser phrase',encoding='utf-8')
        downloads=base/'downloads'; downloads.mkdir(); address=f'127.0.0.1:{free_port()}'; url='http://'+address
        env={**os.environ,'NO_PROXY':'127.0.0.1,localhost','no_proxy':'127.0.0.1,localhost'}
        subprocess.run([str(binary),'index','--data-dir',str(index),str(root)],check=True,capture_output=True,text=True,env=env)
        proc=subprocess.Popen([str(binary),'start','--data-dir',str(index),'--no-open','--addr',address],cwd=base,env=env,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE,text=True)
        browser=None
        try:
            deadline=time.monotonic()+20
            while time.monotonic()<deadline:
                try:
                    with urllib.request.urlopen(url+'/healthz',timeout=1): break
                except OSError: time.sleep(.05)
            else: raise AssertionError('server did not start')
            options=webdriver.ChromeOptions(); options.binary_location=os.environ.get('FINDRAIL_CHROME') or shutil.which('chromium') or shutil.which('google-chrome') or ''
            options.add_argument('--headless=new'); options.add_argument('--no-sandbox'); options.add_argument('--disable-dev-shm-usage')
            options.add_experimental_option('prefs',{'download.default_directory':str(downloads),'download.prompt_for_download':False})
            from selenium.webdriver.chrome.service import Service
            browser=webdriver.Chrome(service=Service(executable_path=os.environ.get('FINDRAIL_CHROMEDRIVER')) if os.environ.get('FINDRAIL_CHROMEDRIVER') else Service(),options=options)
            wait=WebDriverWait(browser,20); browser.get(url+'/'); wait.until(lambda d:d.execute_script('return !!window.findrailBootstrap'))
            source=json.load(urllib.request.urlopen(url+'/api/v1/sources'))['sources'][0]['id']
            browser.execute_script("""document.getElementById('preset-title').value='Synthetic preset';document.getElementById('query').value='synthetic phrase';document.getElementById('mode').value='advanced';document.getElementById('source').value=arguments[0];document.getElementById('format').value='text';document.getElementById('path-prefix').value='notes';document.getElementById('title-contains').value='Notes';""",source)
            browser.find_element(By.ID,'preset-save').click(); path=downloads/'findrail-search-preset.json'; wait.until(lambda _:path.exists())
            saved=json.loads(path.read_text(encoding='utf-8')); assert saved['query']=='synthetic phrase' and saved['source']['scope']=='source' and saved['source']['id_hint']==source
            browser.execute_script("""for(const [id,value] of Object.entries({query:'',mode:'literal',source:'',format:'all','path-prefix':'','title-contains':''}))document.getElementById(id).value=value;""")
            browser.find_element(By.ID,'preset-file').send_keys(str(path)); wait.until(lambda d:not d.find_element(By.ID,'preset-preview').get_attribute('hidden'))
            time.sleep(.2); searches=browser.execute_script("return performance.getEntriesByType('resource').map(x=>x.name).filter(x=>x.includes('/api/v1/search'))")
            assert not searches, searches
            assert not browser.find_element(By.ID,'preset-apply').is_enabled(), 'source mapping must be explicit'
            from selenium.webdriver.support.ui import Select
            Select(browser.find_element(By.ID,'preset-source-map')).select_by_value(source)
            browser.find_element(By.ID,'preset-apply').click()
            assert browser.find_element(By.ID,'query').get_attribute('value')=='synthetic phrase'
            time.sleep(.2); assert not browser.execute_script("return performance.getEntriesByType('resource').map(x=>x.name).filter(x=>x.includes('/api/v1/search'))")
            assert 'Press Search or Enter' in browser.find_element(By.ID,'status').text
            browser.find_element(By.ID,'submit').click(); wait.until(lambda d:any('/api/v1/search' in x for x in d.execute_script("return performance.getEntriesByType('resource').map(x=>x.name)")))
            request=next(x for x in browser.execute_script("return performance.getEntriesByType('resource').map(x=>x.name)") if '/api/v1/search' in x)
            assert 'source='+source in request and 'q=synthetic' in request, request
        finally:
            if browser: browser.quit()
            proc.terminate()
            try: proc.communicate(timeout=8)
            except subprocess.TimeoutExpired: proc.kill(); proc.communicate()

if __name__=='__main__': main()
