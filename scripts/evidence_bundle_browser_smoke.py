#!/usr/bin/env python3
"""Exercise production bundle export/import in real Chromium against start."""
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
from selenium.webdriver.common.by import By
from selenium.webdriver.common.keys import Keys
from selenium.webdriver.support.ui import Select, WebDriverWait

sys.dont_write_bytecode = True
from smoke import docx_fixture, pdf_fixture


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def main():
    binary = pathlib.Path(sys.argv[1]).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="findrail-bundle-browser-") as td:
        base = pathlib.Path(td)
        index, source_a, source_b, similar_source = base / "index", base / "A" / "Notes", base / "B" / "Notes", base / "C" / "Notes"
        source_a.mkdir(parents=True); source_b.mkdir(parents=True); similar_source.mkdir(parents=True)
        content = "First synthetic selected quote. Another exact selected passage."
        for root in (source_a, source_b):
            (root / "notes.md").write_text(content, encoding="utf-8")
            (root / "manual.pdf").write_bytes(pdf_fixture(["irrelevant page one", "page two synthetic evidence"] ))
            (root / "late.docx").write_bytes(docx_fixture("late DOCX synthetic selected quote"))
        (similar_source / "notes.md").write_text("Different contents in a similarly named source.", encoding="utf-8")
        downloads = base / "downloads"; downloads.mkdir()
        address = f"127.0.0.1:{free_port()}"; url = "http://" + address
        env = {**os.environ, "NO_PROXY": "127.0.0.1,localhost", "no_proxy": "127.0.0.1,localhost"}

        def launch():
            return subprocess.Popen([str(binary), "start", "--data-dir", str(index), "--no-open", "--addr", address],
                                    cwd=base, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)

        def ready(proc):
            deadline = time.monotonic() + 20
            while time.monotonic() < deadline:
                if proc.poll() is not None:
                    raise AssertionError("start exited: " + proc.stderr.read())
                try:
                    with urllib.request.urlopen(url + "/healthz", timeout=1): return
                except OSError: time.sleep(.05)
            raise AssertionError("start did not become ready")

        subprocess.run([str(binary), "index", "--data-dir", str(index), str(source_a)], cwd=base, env=env,
                       capture_output=True, text=True, check=True)
        server = launch(); browser = None
        try:
            ready(server)
            options = webdriver.ChromeOptions()
            options.binary_location = os.environ.get("FINDRAIL_CHROME") or shutil.which("chromium") or shutil.which("google-chrome") or ""
            options.add_argument("--headless=new"); options.add_argument("--no-sandbox"); options.add_argument("--disable-dev-shm-usage")
            options.add_experimental_option("prefs", {"download.default_directory": str(downloads), "download.prompt_for_download": False})
            driver_path = os.environ.get("FINDRAIL_CHROMEDRIVER")
            from selenium.webdriver.chrome.service import Service
            browser = webdriver.Chrome(service=Service(executable_path=driver_path) if driver_path else Service(), options=options)
            wait = WebDriverWait(browser, 20); browser.get(url + "/")
            token = browser.execute_async_script("const done=arguments[0];window.findrailBootstrap.then(x=>done(x.token));")

            def add_selected(query, selected, page=None):
                field = browser.find_element(By.ID, "query"); field.clear(); field.send_keys(query); browser.find_element(By.ID, "search-form").submit()
                wait.until(lambda d: "1 matching" in d.find_element(By.ID, "status").text)
                browser.find_element(By.CSS_SELECTOR, ".result button").click()
                wait.until(lambda d: selected.split()[0] in d.find_element(By.ID, "preview-body").text)
                if page:
                    wait.until(lambda d: f"Page {page} /" in d.find_element(By.ID, "page-label").text)
                chosen = browser.execute_script("""
                    const body=document.getElementById('preview-body'), wanted=arguments[0],nodes=[];
                    const visit=n=>{if(n.nodeType===3&&n.textContent.length)nodes.push(n);else for(const child of n.childNodes)visit(child)};visit(body);
                    const joined=nodes.map(n=>n.textContent).join(''),start=joined.indexOf(wanted);if(start<0)return null;
                    let left=start,right=start+wanted.length,a=null,b=null;
                    for(const n of nodes){if(a===null&&left<=n.textContent.length)a=[n,left];else if(a===null)left-=n.textContent.length;if(b===null&&right<=n.textContent.length)b=[n,right];else if(b===null)right-=n.textContent.length}
                    const r=document.createRange();r.setStart(a[0],a[1]);r.setEnd(b[0],b[1]);getSelection().removeAllRanges();getSelection().addRange(r);return getSelection().toString();
                """, selected)
                assert chosen == selected, (chosen, selected, browser.find_element(By.ID, "preview-body").text)
                browser.find_element(By.ID, "add-evidence").click()
                browser.find_element(By.ID, "close-preview").click()

            add_selected("First", "First synthetic selected quote.")
            add_selected("page two", "page two synthetic evidence", 2)
            add_selected("late DOCX", "late DOCX synthetic selected quote")
            browser.find_element(By.ID, "collection-open").click()
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".collection-row")) == 3)
            browser.find_element(By.ID, "collection-title").clear(); browser.find_element(By.ID, "collection-title").send_keys("Source A collection")
            browser.find_element(By.ID, "collection-save").click()
            wait.until(lambda d: "Collection saved" in d.find_element(By.ID, "collection-status").text)
            notes = browser.find_elements(By.CSS_SELECTOR, ".collection-row textarea")
            notes[0].send_keys("saved note one"); notes[1].send_keys("saved note two"); notes[2].send_keys("saved note three")
            # Reorder by moving the second citation first through the production move control.
            browser.find_element(By.XPATH, "(//section[contains(@class,'collection-row')])[2]//button[.='Move up']").click()
            browser.find_element(By.ID, "collection-title").clear(); browser.find_element(By.ID, "collection-title").send_keys("Source A collection edited")
            browser.find_element(By.ID, "collection-export-bundle").click()
            try: wait.until(lambda d: "Save collection edits" in d.find_element(By.ID, "collection-status").text)
            except Exception as error: raise AssertionError((browser.find_element(By.ID, "collection-status").text,
                browser.execute_script("return [window.localEvidenceCollection.generation,document.getElementById('collection-export-bundle').hidden,document.getElementById('collection-export-bundle').disabled]"))) from error
            assert not (downloads / "findrail-evidence-bundle.json").exists(), "dirty notes/order/title must not export as the saved snapshot"
            browser.find_element(By.ID, "collection-save").click()
            wait.until(lambda d: "Collection saved" in d.find_element(By.ID, "collection-status").text)
            headers = {"Origin": url, "X-Findrail-Token": token}
            request = urllib.request.Request(url + "/api/v1/collections", headers=headers)
            with urllib.request.urlopen(request) as response: original_collection_id = json.load(response)["collections"][0]["id"]
            request = urllib.request.Request(url + "/api/v1/collections/" + original_collection_id, headers=headers)
            with urllib.request.urlopen(request) as response: original_snapshot = json.load(response)
            browser.find_element(By.ID, "collection-export-bundle").click()
            bundle_path = downloads / "findrail-evidence-bundle.json"
            wait.until(lambda _: bundle_path.exists())
            original = json.loads(bundle_path.read_text(encoding="utf-8"))
            assert [x["text"] for x in original["entries"]] == ["page two synthetic evidence", "First synthetic selected quote.", "late DOCX synthetic selected quote"]
            assert [x["note"] for x in original["entries"]] == ["saved note two", "saved note one", "saved note three"]
            with urllib.request.urlopen(url + "/api/v1/sources") as r: source_id = json.load(r)["sources"][0]["id"]
            browser.quit(); browser = None; server.terminate(); server.communicate(timeout=8)

            subprocess.run([str(binary), "forget", "--data-dir", str(index), source_id], cwd=base, env=env, check=True, capture_output=True)
            subprocess.run([str(binary), "index", "--data-dir", str(index), str(source_b)], cwd=base, env=env, check=True, capture_output=True)
            subprocess.run([str(binary), "index", "--data-dir", str(index), str(similar_source)], cwd=base, env=env, check=True, capture_output=True)
            server = launch(); ready(server)
            browser = webdriver.Chrome(service=Service(executable_path=driver_path) if driver_path else Service(), options=options)
            browser.get(url + "/"); wait = WebDriverWait(browser, 20)
            token = browser.execute_async_script("const done=arguments[0];window.findrailBootstrap.then(x=>done(x.token));")
            headers = {"Origin": url, "X-Findrail-Token": token}
            browser.find_element(By.ID, "collection-open").click(); browser.find_element(By.ID, "collection-import-bundle").click()
            file_input = browser.find_element(By.ID, "bundle-file"); wait.until(lambda _: not file_input.get_property("disabled")); file_input.send_keys(str(bundle_path))
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, "#bundle-mappings select")) == 1)
            with urllib.request.urlopen(url + "/api/v1/sources") as r: destinations = json.load(r)["sources"]
            assert sum(source["name"] == "Notes" for source in destinations) >= 2, destinations
            destination = next(source["id"] for source in destinations if source["root"] == str(source_b.resolve()))
            oversized = base / "oversized.json"; oversized.write_bytes(b" " * (4 * 1024 * 1024 + 1))
            invalid_utf8 = base / "invalid-utf8.json"; invalid_utf8.write_bytes(b"\xff\xfe")
            wrong_version = base / "wrong-version.json"; wrong_version.write_text('{"format":"findrail-evidence-bundle","version":9,"sources":[],"entries":[]}', encoding="utf-8")
            for path, expected in ((oversized, "4 MiB"), (invalid_utf8, "UTF-8"), (wrong_version, "version")):
                file_input.send_keys(str(path))
                wait.until(lambda d: expected.lower() in d.find_element(By.ID, "bundle-status").text.lower())
                assert not browser.find_element(By.ID, "bundle-commit").is_enabled()
            # The file's JSON must reach strict server parsing untouched.
            raw_bundle = bundle_path.read_text(encoding="utf-8")
            duplicate = base / "duplicate.json"
            duplicate.write_text(raw_bundle.replace('"title":"Source A collection"', '"title":"shadow","title":"Source A collection"'), encoding="utf-8")
            file_input.send_keys(str(duplicate))
            Select(browser.find_element(By.CSS_SELECTOR, "#bundle-mappings select")).select_by_value(destination)
            browser.find_element(By.ID, "bundle-preview").click()
            wait.until(lambda d: d.find_element(By.ID, "bundle-status").text and "Choose a destination" not in d.find_element(By.ID, "bundle-status").text)
            assert not browser.find_element(By.ID, "bundle-commit").is_enabled()
            bad_hash = json.loads(raw_bundle); bad_hash["entries"][0]["content_hash"] = "0" * 64
            bad_hash_path = base / "changed-hash.json"; bad_hash_path.write_text(json.dumps(bad_hash), encoding="utf-8")
            file_input.send_keys(str(bad_hash_path)); Select(browser.find_element(By.CSS_SELECTOR, "#bundle-mappings select")).select_by_value(destination)
            browser.find_element(By.ID, "bundle-preview").click()
            wait.until(lambda d: "changed_hash" in d.find_element(By.ID, "bundle-results").text)
            assert not browser.find_element(By.ID, "bundle-commit").is_enabled()
            wrong_page = json.loads(raw_bundle); wrong_page["entries"][0]["page"] = 1
            wrong_page_path = base / "wrong-page.json"; wrong_page_path.write_text(json.dumps(wrong_page), encoding="utf-8")
            file_input.send_keys(str(wrong_page_path)); Select(browser.find_element(By.CSS_SELECTOR, "#bundle-mappings select")).select_by_value(destination)
            browser.find_element(By.ID, "bundle-preview").click()
            wait.until(lambda d: any(code in d.find_element(By.ID, "bundle-results").text for code in ("quote_mismatch", "invalid_page")))
            assert not browser.find_element(By.ID, "bundle-commit").is_enabled()
            browser.switch_to.active_element.send_keys(Keys.ESCAPE)
            wait.until(lambda d: not d.find_element(By.ID, "bundle-import").get_property("open"))
            assert browser.find_element(By.ID, "bundle-file").get_property("value") == ""
            browser.find_element(By.ID, "collection-import-bundle").send_keys(Keys.ENTER)
            wait.until(lambda d: not d.find_element(By.ID, "bundle-file").get_property("disabled"))
            file_input = browser.find_element(By.ID, "bundle-file")
            file_input.send_keys(str(bundle_path))
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, "#bundle-mappings select")) == 1)
            mapping = browser.find_element(By.CSS_SELECTOR, "#bundle-mappings select")
            target_index = next(i for i, option in enumerate(Select(mapping).options) if option.get_attribute("value") == destination)
            mapping.send_keys(*([Keys.ARROW_DOWN] * target_index), Keys.ENTER)
            browser.execute_script("""
                window.__originalFetch=window.fetch.bind(window);
                window.fetch=(input,options)=>String(input).includes('/collections/import/preview')
                  ?new Promise(resolve=>{window.__resolveLateBundlePreview=()=>resolve(new Response(JSON.stringify({entries:[{index:0,status:'match'},{index:1,status:'match'}],claim:'late'}),{status:200}))})
                  :window.__originalFetch(input,options);
            """)
            browser.find_element(By.ID, "bundle-preview").click()
            wait.until(lambda d: d.execute_script("return typeof window.__resolveLateBundlePreview==='function'"))
            browser.switch_to.active_element.send_keys(Keys.ESCAPE)
            wait.until(lambda d: not d.find_element(By.ID, "bundle-import").get_property("open"))
            browser.execute_script("window.__resolveLateBundlePreview()")
            browser.execute_script("window.fetch=window.__originalFetch")
            time.sleep(.1)
            assert browser.find_element(By.ID, "bundle-commit").get_attribute("disabled") is not None
            browser.find_element(By.ID, "collection-import-bundle").click()
            wait.until(lambda d: not d.find_element(By.ID, "bundle-file").get_property("disabled"))
            file_input = browser.find_element(By.ID, "bundle-file"); file_input.send_keys(str(bundle_path))
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, "#bundle-mappings select")) == 1)
            mapping = browser.find_element(By.CSS_SELECTOR, "#bundle-mappings select")
            target_index = next(i for i, option in enumerate(Select(mapping).options) if option.get_attribute("value") == destination)
            mapping.send_keys(*([Keys.ARROW_DOWN] * target_index), Keys.ENTER)
            status_before_offline = browser.find_element(By.ID, "bundle-status").text
            browser.set_network_conditions(offline=True, latency=0, download_throughput=0, upload_throughput=0)
            browser.find_element(By.ID, "bundle-preview").send_keys(Keys.ENTER)
            wait.until(lambda d: d.find_element(By.ID, "bundle-status").text != status_before_offline)
            assert not browser.find_element(By.ID, "bundle-commit").is_enabled()
            browser.set_network_conditions(offline=False, latency=0, download_throughput=5_000_000, upload_throughput=5_000_000)
            browser.find_element(By.ID, "bundle-preview").send_keys(Keys.ENTER)
            wait.until(lambda d: "3 matches; 0 conflicts" in d.find_element(By.ID, "bundle-status").text)
            browser.execute_script("window.dispatchEvent(new CustomEvent('findrail-inventory',{detail:arguments[0]}))", [
                {"id": destination, "name": "Notes", "kind": "filesystem", "documents": 99, "last_indexed_at": "changed-during-preview"},
                {"id": next(source["id"] for source in destinations if source["id"] != destination), "name": "Notes", "kind": "filesystem", "documents": 1, "last_indexed_at": "unchanged"},
            ])
            assert not browser.find_element(By.ID, "bundle-commit").is_enabled()
            assert "inventory changed" in browser.find_element(By.ID, "bundle-status").text.lower()
            browser.find_element(By.ID, "bundle-preview").send_keys(Keys.ENTER)
            wait.until(lambda d: "3 matches; 0 conflicts" in d.find_element(By.ID, "bundle-status").text)
            # A new preview requires its own explicit confirmation.
            browser.find_element(By.ID, "bundle-confirm").send_keys(Keys.SPACE)
            browser.find_element(By.ID, "bundle-preview").send_keys(Keys.ENTER)
            wait.until(lambda d: "3 matches; 0 conflicts" in d.find_element(By.ID, "bundle-status").text)
            assert not browser.find_element(By.ID, "bundle-confirm").is_selected()
            assert not browser.find_element(By.ID, "bundle-commit").is_enabled()
            browser.find_element(By.ID, "bundle-title").clear(); browser.find_element(By.ID, "bundle-title").send_keys("Imported Source B")
            # Hold the production request to exercise the pending-commit controls.
            browser.execute_script("""
                window.__originalFetch=window.fetch.bind(window);window.__bundleImportRequests=0;
                window.fetch=(input,options)=>String(input).endsWith('/collections/import')
                  ?new Promise((resolve,reject)=>{window.__bundleImportRequests++;window.__finishBundleImport=()=>window.__originalFetch(input,options).then(resolve,reject)})
                  :window.__originalFetch(input,options);
            """)
            browser.find_element(By.ID, "bundle-confirm").send_keys(Keys.SPACE); browser.find_element(By.ID, "bundle-commit").send_keys(Keys.ENTER)
            wait.until(lambda d: d.execute_script("return window.__bundleImportRequests===1"))
            assert all(not browser.find_element(By.ID, control).is_enabled() for control in
                       ("bundle-file", "bundle-preview", "bundle-title", "bundle-confirm", "bundle-commit"))
            browser.execute_script("""
                document.getElementById('bundle-title').dispatchEvent(new Event('input'));
                document.getElementById('bundle-confirm').dispatchEvent(new Event('change'));
                document.getElementById('bundle-commit').click();
            """)
            assert not browser.find_element(By.ID, "bundle-commit").is_enabled()
            assert browser.execute_script("return window.__bundleImportRequests") == 1
            browser.execute_script("window.__finishBundleImport();window.fetch=window.__originalFetch")
            wait.until(lambda d: d.find_element(By.ID, "collection-title").get_attribute("value") == "Imported Source B")
            assert browser.execute_script("return document.activeElement.id") == "collection-title"
            assert [e.get_attribute("value") for e in browser.find_elements(By.CSS_SELECTOR, ".collection-row textarea")] == ["saved note two", "saved note one", "saved note three"]
            with urllib.request.urlopen(urllib.request.Request(url + "/api/v1/collections", headers=headers)) as response:
                collection_list = json.load(response)["collections"]
            imported = next(item for item in collection_list if item["title"] == "Imported Source B")
            assert imported["id"] != original_collection_id and imported["entry_count"] == 3
            request = urllib.request.Request(url + "/api/v1/collections/" + imported["id"], headers=headers)
            with urllib.request.urlopen(request) as response: imported_snapshot = json.load(response)
            old_evidence = [item.get("evidence", item) for item in original_snapshot["entries"]]
            new_evidence = [item.get("evidence", item) for item in imported_snapshot["entries"]]
            assert all(old.get("id") != new.get("id") for old, new in zip(old_evidence, new_evidence))
            assert all(old.get("collected_at") != new.get("collected_at") for old, new in zip(original_snapshot["entries"], imported_snapshot["entries"]))
            bundle_path.unlink()
            browser.find_element(By.ID, "collection-export-bundle").click()
            wait.until(lambda _: bundle_path.exists())
            imported_bundle = json.loads(bundle_path.read_text(encoding="utf-8"))
            assert [item["text"] for item in imported_bundle["entries"]] == [item["text"] for item in original["entries"]]
            assert [item["note"] for item in imported_bundle["entries"]] == [item["note"] for item in original["entries"]]
            browser.quit(); browser = None; server.terminate(); server.communicate(timeout=8)
            server = launch(); ready(server)
            browser = webdriver.Chrome(service=Service(executable_path=driver_path) if driver_path else Service(), options=options)
            browser.get(url + "/"); wait = WebDriverWait(browser, 20)
            token = browser.execute_async_script("const done=arguments[0];window.findrailBootstrap.then(x=>done(x.token));")
            headers = {"Origin": url, "X-Findrail-Token": token}
            picker = browser.find_element(By.ID, "collection-picker")
            try:
                wait.until(lambda d: "Imported Source B (3)" in d.execute_script("return Array.from(arguments[0].options).map(o=>o.textContent).join('|')", d.find_element(By.ID, "collection-picker")))
            except Exception as error:
                with urllib.request.urlopen(urllib.request.Request(url + "/api/v1/collections", headers=headers)) as response:
                    body = response.read().decode()
                raise AssertionError(("imported collection missing after restart", body, browser.find_element(By.ID, "collection-status").text,
                                     browser.execute_script("return [document.getElementById('collection-picker').hidden,Array.from(document.getElementById('collection-picker').options).map(x=>x.text)]"))) from error
            Select(picker).select_by_visible_text("Imported Source B (3)")
            wait.until(lambda d: d.find_element(By.ID, "collection-title").get_attribute("value") == "Imported Source B")
            assert [e.get_attribute("value") for e in browser.find_elements(By.CSS_SELECTOR, ".collection-row textarea")] == ["saved note two", "saved note one", "saved note three"]
            bundle_path.unlink()
            browser.find_element(By.ID, "collection-open").click()
            browser.find_element(By.ID, "collection-export-bundle").click()
            wait.until(lambda _: bundle_path.exists())
            restarted_bundle = json.loads(bundle_path.read_text(encoding="utf-8"))
            assert [item["text"] for item in restarted_bundle["entries"]] == [item["text"] for item in imported_bundle["entries"]]
            assert [item["note"] for item in restarted_bundle["entries"]] == [item["note"] for item in imported_bundle["entries"]]
            browser.quit(); browser = None; server.terminate(); server.communicate(timeout=8)
            server = subprocess.Popen([str(binary), "serve", "--data-dir", str(index), "--addr", address, "--no-sync"],
                                      cwd=base, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
            ready(server)
            browser = webdriver.Chrome(service=Service(executable_path=driver_path) if driver_path else Service(), options=options)
            browser.get(url + "/"); wait = WebDriverWait(browser, 20)
            with urllib.request.urlopen(url + "/api/v1/capabilities") as response:
                assert json.load(response).get("evidence_bundle") is not True
            browser.find_element(By.ID, "collection-open").click()
            assert browser.find_element(By.ID, "collection-export-bundle").get_attribute("hidden") is not None
            assert browser.find_element(By.ID, "collection-import-bundle").get_attribute("hidden") is not None
            print("Evidence bundle browser workflow passed: strict file validation, duplicate-key rejection, explicit same-name source mapping, changed-hash conflict, keyboard cancel/preview/confirm, ordered notes, production import/export, restart and read-only capability gating.")
        finally:
            if browser: browser.quit()
            if server.poll() is None:
                server.terminate()
                try: server.communicate(timeout=8)
                except subprocess.TimeoutExpired: server.kill(); server.communicate(timeout=3)


if __name__ == "__main__": main()
