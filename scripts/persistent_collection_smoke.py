#!/usr/bin/env python3
"""Headless local journey for named collection persistence; uses synthetic notes only."""
import json
import os
import pathlib
import re
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
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support.ui import Select


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def gate_response(driver, path, method):
    """Delay exactly one matching response; unrelated polling remains live."""
    driver.execute_script("""
        const original=window.fetch.bind(window);let matched=false;
        window.__collectionGateHit=false;
        const pending=new Promise(resolve=>window.__collectionGateRelease=resolve);
        window.fetch=(url,options={})=>{
          const hold=!matched&&String(url)===arguments[0]&&(options.method||'GET')===arguments[1];
          if(hold)matched=true;
          return original(url,options).then(async response=>{
            if(hold){window.__collectionGateHit=true;await pending;}
            return response;
          });
        };
        window.__releaseCollectionGate=()=>{window.fetch=original;window.__collectionGateRelease();};
    """, path, method)


def api(driver, path, method="GET", body=None):
    return driver.execute_async_script("""
        const [path,method,body,finish]=arguments;
        window.findrailBootstrap.then(({token})=>fetch(path,{
          method,cache:'no-store',headers:{'X-Findrail-Token':token,'Content-Type':'application/json'},
          body:body===null?undefined:JSON.stringify(body)
        })).then(async r=>({status:r.status,data:r.status===204?null:await r.json()}))
          .then(finish,error=>finish({error:String(error)}));
    """, path, method, body)


def main():
    binary = pathlib.Path(sys.argv[1]).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="findrail-collections-") as tmp:
        root = pathlib.Path(tmp)
        notes, data = root / "notes", root / "data"
        notes.mkdir()
        (notes / "first.md").write_text("First persistent citation passage.", encoding="utf-8")
        (notes / "second.md").write_text("Second persistent citation passage.", encoding="utf-8")
        indexed = subprocess.run([str(binary), "index", "--json", "--data-dir", str(data), str(notes)], check=True, capture_output=True, text=True, timeout=30)
        assert json.loads(indexed.stdout)["source"]["id"]
        port = free_port()
        server = subprocess.Popen([str(binary), "start", "--data-dir", str(data), "--sync-interval", "1s", "--no-open", "--enable-originals", "--addr", f"127.0.0.1:{port}"], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
        browser = None
        try:
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                if server.poll() is not None:
                    raise RuntimeError(server.stderr.read())
                try:
                    urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=1).close()
                    break
                except Exception:
                    time.sleep(.1)
            options = webdriver.ChromeOptions()
            options.binary_location = os.environ.get("FINDRAIL_CHROME") or shutil.which("chromium") or shutil.which("google-chrome") or ""
            options.add_argument("--headless=new")
            options.add_argument("--no-sandbox")
            options.add_argument("--disable-dev-shm-usage")
            driver = webdriver.Chrome(service=Service(os.environ.get("FINDRAIL_CHROMEDRIVER") or shutil.which("chromedriver")), options=options)
            browser = driver
            wait = WebDriverWait(driver, 12)
            driver.get(f"http://127.0.0.1:{port}/")
            wait.until(lambda d: d.execute_script("return !document.getElementById('collection-save').hidden"))
            driver.find_element(By.ID, "collection-open").click()
            driver.find_element(By.ID, "collection-new").click()
            driver.find_element(By.ID, "collection-title").clear()
            driver.find_element(By.ID, "collection-title").send_keys("Research one")
            driver.find_element(By.ID, "collection-close").click()

            for query in ("First", "Second"):
                driver.execute_script("document.getElementById('query').value=arguments[0];document.getElementById('search-form').requestSubmit()", query)
                # Activate a matching result in one browser turn: live refresh
                # can replace search cards between separate WebDriver calls.
                wait.until(lambda d: d.execute_script("""
                    const row=[...document.querySelectorAll('.result')]
                      .find(x=>x.querySelector('.snippet')?.textContent.includes(arguments[0]));
                    const button=row?.querySelector('.location button');
                    if(!button||button.textContent!=='Preview')return false;
                    button.click();return true;
                """, query))
                wait.until(lambda d: d.find_element(By.ID, "preview-body").text.startswith(query))
                assert not driver.find_element(By.ID, "open-original").get_attribute("hidden")
                driver.execute_script("document.getElementById('whole-preview').checked=true")
                driver.find_element(By.ID, "add-evidence").click()
                driver.find_element(By.ID, "close-preview").click()

            driver.find_element(By.ID, "collection-open").click()
            wait.until(lambda d: d.find_element(By.ID, "collection-count").text == "2")
            driver.execute_script("const n=document.querySelector('.collection-row textarea');n.value='Saved note';n.dispatchEvent(new Event('input',{bubbles:true}));document.querySelectorAll('.collection-row')[1].querySelectorAll('button')[1].click()")
            gate_response(driver, "/api/v1/collections", "POST")
            driver.find_element(By.ID, "collection-save").click()
            wait.until(lambda d: d.execute_script("return window.__collectionGateHit"))
            assert driver.execute_script("return ['collection-picker','collection-new','collection-save','collection-delete','collection-title','collection-clear'].every(id=>document.getElementById(id).disabled)")
            driver.execute_script("document.getElementById('collection-picker').value='';document.getElementById('collection-picker').dispatchEvent(new Event('change'));window.__releaseCollectionGate()")
            wait.until(lambda d: not d.find_element(By.ID, "collection-save").get_attribute("disabled") and "saved" in d.find_element(By.ID, "collection-status").text.lower())
            assert driver.find_element(By.ID, "collection-count").text == "2"
            driver.find_element(By.ID, "collection-title").clear()
            driver.find_element(By.ID, "collection-title").send_keys("Research one renamed")
            driver.find_element(By.ID, "collection-save").click()
            wait.until(lambda d: "saved" in d.find_element(By.ID, "collection-status").text.lower())
            driver.find_element(By.ID, "collection-new").click()
            assert driver.find_element(By.ID, "collection-count").text == "0"
            assert not driver.find_elements(By.CSS_SELECTOR, ".collection-row")
            driver.find_element(By.ID, "collection-title").clear()
            driver.find_element(By.ID, "collection-title").send_keys("Research two")
            driver.find_element(By.ID, "collection-save").click()
            wait.until(lambda d: "saved" in d.find_element(By.ID, "collection-status").text.lower())
            driver.find_element(By.ID, "collection-close").click()
            driver.quit()
            browser = None
            server.terminate()
            server.wait(timeout=7)
            server = subprocess.Popen([str(binary), "start", "--data-dir", str(data), "--sync-interval", "1s", "--no-open", "--enable-originals", "--addr", f"127.0.0.1:{port}"], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                if server.poll() is not None:
                    raise RuntimeError(server.stderr.read())
                try:
                    urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=1).close()
                    break
                except Exception:
                    time.sleep(.1)
            driver = webdriver.Chrome(service=Service(os.environ.get("FINDRAIL_CHROMEDRIVER") or shutil.which("chromedriver")), options=options)
            browser = driver
            wait = WebDriverWait(driver, 12)
            driver.get(f"http://127.0.0.1:{port}/")
            wait.until(lambda d: d.execute_script("return !document.getElementById('collection-picker').hidden"))
            wait.until(lambda d: d.execute_script("return window.localEvidenceCollection.title==='Research two'"))
            driver.find_element(By.ID, "collection-open").click()
            picker = driver.find_element(By.ID, "collection-picker")
            first_id = next(o.get_attribute("value") for o in Select(picker).options if o.text == "Research one renamed (2)")
            gate_response(driver, "/api/v1/collections/" + first_id, "GET")
            Select(picker).select_by_value(first_id)
            wait.until(lambda d: d.execute_script("return window.__collectionGateHit"))
            driver.execute_script("const title=document.getElementById('collection-title');title.value='Local edit during load';title.dispatchEvent(new Event('input'));window.__releaseCollectionGate()")
            wait.until(lambda d: "Local edits changed during load" in d.find_element(By.ID, "collection-status").text)
            assert driver.find_element(By.ID, "collection-title").get_attribute("value") == "Local edit during load"
            assert driver.find_element(By.ID, "collection-count").text == "0"
            Select(picker).select_by_value(first_id)
            wait.until(lambda d: d.find_element(By.ID, "collection-count").text == "2")
            assert driver.execute_script("return document.querySelectorAll('.collection-row textarea')[1]?.value") == "Saved note"
            export = driver.execute_script("return window.localEvidenceCollection.export()")
            assert "First persistent citation passage" in export and "Second persistent citation passage" in export
            assert export.index("Second persistent citation passage") < export.index("First persistent citation passage")
            assert "Saved note" in export
            driver.execute_script("Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async()=>{throw Error('synthetic clipboard denial')}}})")
            driver.find_element(By.ID, "collection-copy").click()
            wait.until(lambda d: not d.find_element(By.ID, "collection-fallback").get_attribute("hidden"))
            fallback = driver.find_element(By.ID, "collection-output").get_attribute("value")
            comparable = lambda value: re.sub(r"(?m)^Exported \d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z", "Exported <time>", value, count=1)
            assert comparable(fallback) == comparable(export), (fallback, export)
            (notes / "first.md").unlink()
            wait.until(lambda d: len(api(d, "/api/v1/collections/" + first_id).get("data", {}).get("entries", [])) == 1)
            driver.find_element(By.ID, "collection-copy").click()
            wait.until(lambda d: "Saved citations were removed" in d.find_element(By.ID, "collection-status").text)
            assert driver.find_element(By.ID, "collection-count").text == "1"
            assert driver.find_element(By.ID, "collection-output").get_attribute("value") == ""
            assert "First persistent citation passage" not in driver.execute_script("return window.localEvidenceCollection.export()")
            print(f"Chromium {driver.capabilities.get('browserVersion')} on {sys.platform}: named collections restored with notes/order, delayed save/load guarded, deleted citation withheld from export")
        finally:
            if browser:
                browser.quit()
            server.terminate()
            try:
                server.wait(timeout=7)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait(timeout=5)


if __name__ == "__main__":
    main()
