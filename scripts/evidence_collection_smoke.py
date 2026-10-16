#!/usr/bin/env python3
"""Compiled Findrail + real Chromium collection journey (synthetic fixture only)."""
import pathlib
import json
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import os

from selenium import webdriver
from selenium.webdriver.common.by import By
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.keys import Keys
from selenium.webdriver.support.ui import WebDriverWait
sys.dont_write_bytecode = True
from smoke import docx_fixture, pdf_fixture


def port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def main():
    binary = pathlib.Path(sys.argv[1]).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="findrail-evidence-") as tmp:
        base = pathlib.Path(tmp)
        notes, index = base / "notes", base / "index"
        notes.mkdir()
        primary = notes / "<img onerror=alert(1)>.md"
        primary.write_text(
            "Synthetic evidence alpha passage. Another sentence. <script>window.__evidencePwned=true</script>", encoding="utf-8")
        (notes / "runbook.pdf").write_bytes(pdf_fixture(["pdfpageone marker", "pdfpagetwo marker"]))
        (notes / "team-notes.docx").write_bytes(docx_fixture("docxunique marker"))
        indexed = subprocess.run([str(binary), "index", "--data-dir", str(index), "--max-docx-bytes", str(1 << 20), "--json", str(notes)], check=True,
                                 timeout=30, capture_output=True, text=True)
        source_id = json.loads(indexed.stdout)["source"]["id"]
        archive_path = base / "synthetic.findrail.zip"
        subprocess.run([str(binary), "export-source", "--data-dir", str(index), "--source", source_id,
                        "--output", str(archive_path), "--json"], check=True, timeout=30, capture_output=True, text=True)
        imported = subprocess.run([str(binary), "import-source", "--data-dir", str(index), "--name", "Frozen synthetic archive",
                                   "--json", str(archive_path)], check=True, timeout=30, capture_output=True, text=True)
        archive_id = json.loads(imported.stdout)["source_id"]
        github = subprocess.run(["go", "run", "scripts/evidence_collection_fixture.go", str(index)], cwd=pathlib.Path(__file__).resolve().parents[1],
                                check=True, timeout=120, capture_output=True, text=True)
        github_id = github.stdout.strip()
        http_port = port()
        server = subprocess.Popen([str(binary), "serve", "--data-dir", str(index), "--no-sync",
                                   "--addr", f"127.0.0.1:{http_port}"], stdout=subprocess.DEVNULL,
                                  stderr=subprocess.PIPE, text=True)
        options = webdriver.ChromeOptions()
        options.binary_location = os.environ.get("FINDRAIL_CHROME") or shutil.which("chromium") or shutil.which("chromium-browser") or shutil.which("google-chrome") or ""
        if not options.binary_location:
            raise RuntimeError("Chromium/Chrome binary not found in PATH")
        options.add_argument("--headless=new")
        options.add_argument("--no-sandbox")
        options.add_argument("--disable-dev-shm-usage")
        options.add_experimental_option("prefs", {"download.default_directory": str(base),
                                                    "download.prompt_for_download": False})
        browser = None
        denied_fallback = None
        try:
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                if server.poll() is not None:
                    raise RuntimeError("compiled Findrail exited: " + server.stderr.read())
                try:
                    import urllib.request
                    urllib.request.urlopen(f"http://127.0.0.1:{http_port}/healthz", timeout=1).read()
                    break
                except Exception:
                    time.sleep(.1)
            driver = os.environ.get("FINDRAIL_CHROMEDRIVER")
            browser = webdriver.Chrome(service=Service(executable_path=driver) if driver else Service(), options=options)
            browser.set_page_load_timeout(15)
            browser.get(f"http://127.0.0.1:{http_port}/")
            wait = WebDriverWait(browser, 10)
            def search_preview(text, mode="literal", format_name="all", source_filter=source_id):
                browser.execute_script("""document.getElementById('query').value=arguments[0];
                    document.getElementById('mode').value=arguments[1];
                    document.getElementById('format').value=arguments[2];
                    document.getElementById('source').value=arguments[3];
                    document.getElementById('search-form').requestSubmit();""", text, mode, format_name, source_filter)
                button = wait.until(lambda d: next((b for b in d.find_elements(By.TAG_NAME, "button") if b.text == "Preview"), False))
                button.click()
                wait.until(lambda d: d.find_element(By.ID, "preview-body").text not in ("", "Loading preview…"))
            def pin_whole():
                browser.execute_script("document.getElementById('whole-preview').checked=true")
                browser.find_element(By.ID, "add-evidence").click()
                try:
                    wait.until(lambda d: "added" in d.execute_script("return document.getElementById('collection-status').textContent"))
                except Exception as error:
                    raise AssertionError((browser.execute_script("return document.getElementById('collection-status').textContent"), browser.find_element(By.ID, "preview-meta").text, browser.find_element(By.ID, "preview-body").text)) from error
            def close_preview():
                browser.find_element(By.ID, "close-preview").click()
            def edit_first_note(suffix):
                browser.execute_script("""const e=document.querySelector('.collection-row textarea');
                    e.value+=arguments[0];e.dispatchEvent(new Event('input',{bubbles:true}));""", suffix)
            wait.until(lambda d: source_id in d.execute_script("return [...document.getElementById('source').options].map(o=>o.value)"))
            query = wait.until(lambda d: d.find_element(By.ID, "query"))
            query.send_keys("alpha")
            browser.execute_script("document.getElementById('source').value=arguments[0]", source_id)
            browser.find_element(By.ID, "search-form").submit()
            preview_button = wait.until(lambda d: next((b for b in d.find_elements(By.TAG_NAME, "button")
                                                        if b.text == "Preview"), False))
            preview_button.click()
            wait.until(lambda d: d.find_element(By.ID, "preview-body").text.startswith("alpha"))
            browser.execute_script("""const b=document.getElementById('preview-body'),w=document.createTreeWalker(b,NodeFilter.SHOW_TEXT);
                let t=w.nextNode();while(t&&t.length===0)t=w.nextNode();
                const r=document.createRange();r.setStart(t,0);r.setEnd(t,Math.min(10,t.length));
                const s=getSelection();s.removeAllRanges();s.addRange(r);""")
            browser.find_element(By.ID, "add-evidence").click()
            pin_status=browser.execute_script("return document.getElementById('collection-status').textContent"); assert "added" in pin_status, pin_status
            assert not browser.execute_script("return window.__evidencePwned===true")
            assert browser.execute_script("return document.querySelectorAll('img').length") == 0
            browser.find_element(By.ID, "add-evidence").click()
            duplicate_status = browser.execute_script("return document.getElementById('collection-status').textContent")
            assert "Duplicate" in duplicate_status, duplicate_status
            browser.find_element(By.ID, "whole-preview").click()
            browser.find_element(By.ID, "add-evidence").click()
            browser.find_element(By.ID, "close-preview").click()
            browser.find_element(By.ID, "collection-open").click()
            wait.until(lambda d: len(d.find_elements(By.CLASS_NAME, "collection-row")) == 2)
            note = browser.find_elements(By.CSS_SELECTOR, ".collection-row textarea")[0]
            note.send_keys("literal note <img src=x onerror=alert(1)>\n~~~")
            rows = browser.find_elements(By.CLASS_NAME, "collection-row")
            rows[0].find_element(By.XPATH, ".//button[.='Move down']").click()
            excerpts = browser.execute_script("return [...document.querySelectorAll('.collection-row pre')].map(x=>x.textContent)")
            assert excerpts[0].startswith("alpha passage") and excerpts[1].startswith("alpha")
            assert not browser.execute_script("return window.__evidencePwned===true")

            browser.find_element(By.ID, "collection-close").click()
            search_preview("pdfpageone", format_name="pdf")
            assert browser.find_element(By.ID, "page-label").text == "Page 1 / 2"
            pin_whole()
            browser.find_element(By.ID, "next").click()
            wait.until(lambda d: d.find_element(By.ID, "page-label").text == "Page 2 / 2")
            pin_whole()
            close_preview()
            search_preview("runbook", mode="advanced", format_name="pdf")
            assert browser.find_element(By.ID, "page-label").text == "Title match · page not determined"
            pin_whole()
            close_preview()
            search_preview("docxunique", format_name="docx")
            assert browser.find_element(By.ID, "pagination").get_attribute("hidden") is not None
            pin_whole()
            close_preview()
            search_preview("pdfpageone", format_name="pdf", source_filter=archive_id)
            assert "archive" in browser.find_element(By.ID, "preview-meta").text.lower()
            pin_whole()
            close_preview()
            search_preview("github evidence", source_filter=github_id)
            assert "GitHub snapshot" in browser.find_element(By.ID, "preview-notice").text
            pin_whole()
            close_preview()
            browser.find_element(By.ID, "collection-open").click()
            wait.until(lambda d: len(d.find_elements(By.CLASS_NAME, "collection-row")) == 8)
            rows_text = browser.execute_script("return document.getElementById('collection-list').textContent")
            assert "page 1/2" in rows_text and "page 2/2" in rows_text and "title only (page 0)" in rows_text
            assert "no page" in rows_text and "archive" in rows_text.lower()
            browser.execute_script("const row=[...document.querySelectorAll('.collection-row')].find(r=>r.textContent.includes('Frozen synthetic archive'));row.querySelector('button').click()")
            wait.until(lambda d: d.find_element(By.ID, "preview-body").text.startswith("pdfpageone"))
            close_preview()
            browser.find_element(By.ID, "collection-open").click()
            browser.find_element(By.ID, "collection-close").send_keys(Keys.ESCAPE)
            wait.until(lambda d: not d.find_element(By.ID, "evidence-collection").get_attribute("open"))
            assert browser.execute_script("return document.activeElement.id") == "collection-open"
            browser.find_element(By.ID, "collection-open").click()

            # Reindex externally while keeping the collected snapshot frozen.
            primary.write_text("Current indexed alpha content is different from the saved historical passage.", encoding="utf-8")
            subprocess.run([str(binary), "index", "--data-dir", str(index), "--max-docx-bytes", str(1 << 20), "--json", str(notes)], check=True,
                           timeout=30, capture_output=True, text=True)

            # A completed network response arriving after a note edit must be
            # discarded by collection generation, despite fetch having resolved.
            browser.execute_script("""window.__realFetch=window.fetch.bind(window);window.__gateReady=false;
                window.__releaseGate=null;window.fetch=(input,init)=>{const u=String(input.url||input);
                return window.__realFetch(input,init).then(r=>u.includes('/api/v1/documents/')?
                new Promise(resolve=>{window.__gateReady=true;window.__releaseGate=()=>resolve(r)}):r)};""")
            browser.find_element(By.ID, "collection-check").click()
            wait.until(lambda d: d.execute_script("return window.__gateReady"))
            edit_first_note(" edited during check")
            browser.execute_script("window.__releaseGate();window.fetch=window.__realFetch")
            wait.until(lambda d: "Collection changed" in d.execute_script("return document.getElementById('collection-status').textContent"))

            retained_before_failure = browser.find_element(By.ID, "collection-count").text
            browser.execute_script("""window.__realFetch=window.fetch.bind(window);window.fetch=(input,init)=>
                String(input.url||input).includes('/api/v1/sources')?Promise.reject(new Error('synthetic inventory outage')):
                window.__realFetch(input,init);""")
            browser.find_element(By.ID, "collection-check").click()
            wait.until(lambda d: "Inventory unavailable" in d.execute_script("return document.getElementById('collection-status').textContent"))
            assert browser.find_element(By.ID, "collection-count").text == retained_before_failure
            assert "unavailable" in browser.execute_script("return document.getElementById('collection-list').textContent")
            browser.execute_script("window.fetch=window.__realFetch")

            browser.execute_script("""window.__realFetch=window.fetch.bind(window);window.fetch=(input,init)=>
                String(input.url||input).includes('/api/v1/sources')?Promise.resolve(new Response(
                JSON.stringify({sources:[{id:{},kind:'filesystem',name:'malformed',root:'/',documents:1}]}),
                {status:200,headers:{'Content-Type':'application/json'}})):window.__realFetch(input,init);""")
            browser.find_element(By.ID, "collection-check").click()
            wait.until(lambda d: "Inventory unavailable" in d.execute_script("return document.getElementById('collection-status').textContent"))
            assert browser.find_element(By.ID, "collection-count").text == retained_before_failure
            browser.execute_script("window.fetch=window.__realFetch")

            browser.find_element(By.ID, "collection-check").click()
            wait.until(lambda d: "changed/current version differs" in d.execute_script("return document.getElementById('collection-list').textContent"))
            excerpts = browser.execute_script("return [...document.querySelectorAll('.collection-row pre')].map(x=>x.textContent)")
            assert excerpts[0].startswith("alpha passage") and excerpts[1].startswith("alpha")

            def gate_response(fragment, flow=None):
                # Match the request's semantic caller, never an arbitrary Nth poll.
                # prepare() is the export/status flow; refresh() is its periodic
                # inventory poll and must remain free to run independently.
                browser.execute_script("""window.__realFetch=window.fetch.bind(window);window.__gateReady=false;window.__gateMatched=false;
                    window.__releaseGate=null;window.fetch=(input,init)=>{const u=String(input.url||input);
                    const stack=(new Error()).stack||'';const match=u.includes(arguments[0])&&
                    (!arguments[1]||stack.includes(arguments[1]+' ('));
                    const hold=match&&!window.__gateMatched;if(hold)window.__gateMatched=true;
                    return window.__realFetch(input,init).then(r=>hold?
                    new Promise(resolve=>{window.__gateReady=true;window.__releaseGate=()=>resolve(r)}):r)};""", fragment, flow)
            def gate_first_response(fragment):
                gate_response(fragment)
            def release_gate():
                browser.execute_script("window.__releaseGate?.();window.fetch=window.__realFetch")

            # A newer check can complete first; an older successful body is
            # ignored rather than changing status after the newer result.
            gate_first_response("/api/v1/documents/")
            browser.execute_script("document.getElementById('collection-status').textContent='newer-check-pending'")
            browser.find_element(By.ID, "collection-check").click()
            wait.until(lambda d: d.execute_script("return window.__gateReady"))
            browser.find_element(By.ID, "collection-check").click()
            wait.until(lambda d: d.execute_script("return document.getElementById('collection-status').textContent") == "Check finished; status reflects the index at check time.")
            release_gate()
            time.sleep(.15)
            assert browser.execute_script("return document.getElementById('collection-status').textContent") == "Check finished; status reflects the index at check time."

            # Removing a row during a pending document GET invalidates that
            # response; it cannot write status back to the changed collection.
            gate_first_response("/api/v1/documents/")
            browser.find_element(By.ID, "collection-check").click()
            wait.until(lambda d: d.execute_script("return window.__gateReady"))
            count_before_remove = int(browser.find_element(By.ID, "collection-count").text)
            browser.find_elements(By.CSS_SELECTOR, ".collection-row button:last-child")[0].click()
            assert int(browser.find_element(By.ID, "collection-count").text) == count_before_remove - 1
            release_gate()
            wait.until(lambda d: "Collection changed" in d.execute_script("return document.getElementById('collection-status').textContent"))

            browser.find_element(By.ID, "collection-check").click()
            wait.until(lambda d: "changed/current version differs" in d.execute_script("return document.getElementById('collection-list').textContent"))
            wait.until(lambda d: d.execute_script("return document.getElementById('collection-status').textContent") == "Check finished; status reflects the index at check time.")
            gate_first_response("/api/v1/documents/")
            browser.find_element(By.ID, "collection-check").click()
            wait.until(lambda d: d.execute_script("return window.__gateReady"))
            browser.execute_script("document.querySelector('.collection-row .collection-actions button:nth-child(3)').click()")
            release_gate()
            wait.until(lambda d: "Collection changed" in d.execute_script("return document.getElementById('collection-status').textContent"))
            browser.find_element(By.ID, "collection-check").click()
            wait.until(lambda d: d.execute_script("return document.getElementById('collection-status').textContent") == "Check finished; status reflects the index at check time.")
            browser.execute_script("const row=[...document.querySelectorAll('.collection-row')].find(r=>r.querySelector('h3').textContent.includes('<img onerror'));row.querySelector('button').click()")
            wait.until(lambda d: d.find_element(By.ID, "preview-body").text != "Loading preview…")
            current_body = browser.find_element(By.ID, "preview-body").text
            assert current_body.startswith("Current indexed alpha"), (browser.find_element(By.ID, "preview-title").text, current_body, browser.execute_script("return document.getElementById('collection-status').textContent"))
            browser.find_element(By.ID, "close-preview").click()
            browser.find_element(By.ID, "collection-open").click()
            assert "changed/current version differs" in browser.execute_script("return document.getElementById('collection-list').textContent")

            # An export's successful inventory body is stale if a note changes
            # before the promise completes. No surprising output is retained.
            gate_response("/api/v1/sources", "prepare")
            browser.find_element(By.ID, "collection-copy").click()
            wait.until(lambda d: d.execute_script("return window.__gateReady"))
            edit_first_note(" edited during export")
            release_gate()
            wait.until(lambda d: "Collection changed" in d.execute_script("return document.getElementById('collection-status').textContent"))
            assert browser.find_element(By.ID, "collection-output").get_attribute("value") == ""

            clipboard_result = "unavailable"
            try:
                browser.execute_cdp_cmd("Browser.grantPermissions", {
                    "origin": f"http://127.0.0.1:{http_port}",
                    "permissions": ["clipboardReadWrite", "clipboardSanitizedWrite"],
                })
                browser.find_element(By.ID, "collection-copy").click()
                wait.until(lambda d: "copied" in d.execute_script(
                    "return document.getElementById('collection-status').textContent"))
                clipboard_text = browser.execute_async_script(
                    "const done=arguments[arguments.length-1];navigator.clipboard.readText().then(done).catch(e=>done('ERROR:'+e.name))")
                clipboard_result = "actual clipboard read/write" if clipboard_text.startswith("# Findrail") else "write supported; read restricted"
                for permission in ("clipboard-read", "clipboard-write"):
                    browser.execute_cdp_cmd("Browser.setPermission", {
                        "origin": f"http://127.0.0.1:{http_port}",
                        "permission": {"name": permission}, "setting": "denied",
                    })
                browser.find_element(By.ID, "collection-copy").click()
                wait.until(lambda d: not d.find_element(By.ID, "collection-fallback").get_attribute("hidden"))
                fallback = browser.execute_script("return document.getElementById('collection-output').value")
                denied_fallback = fallback
                clipboard_result += "; denied permission fallback matched"
            except Exception:
                clipboard_result = "browser denied/unavailable; accessible fallback available"
                fallback = browser.execute_script("return document.getElementById('collection-output').value")
                assert fallback.startswith("# Findrail"), fallback[:120]
            browser.execute_script("""window.__createdURLs=0;window.__revokedURLs=0;
                window.__createObjectURL=URL.createObjectURL.bind(URL);window.__revokeObjectURL=URL.revokeObjectURL.bind(URL);
                URL.createObjectURL=b=>{window.__createdURLs++;return window.__createObjectURL(b)};
                URL.revokeObjectURL=u=>{window.__revokedURLs++;return window.__revokeObjectURL(u)};""")
            browser.find_element(By.ID, "collection-download").click()
            filename = "findrail-findrail-evidence-collection.md"
            wait.until(lambda _: (base / filename).exists())
            exported = (base / filename).read_text(encoding="utf-8")
            assert exported.startswith("# Findrail evidence collection\n")
            assert browser.find_element(By.ID, "collection-status").text == f"Downloaded {filename}."
            assert "Synthetic" in exported and "file://" in exported and "literal note" in exported
            assert "changed/current version differs" in exported
            assert "runbook.pdf — page 2" in exported and "#page=2" in exported
            assert "github.com/example/evidence/blob/0123456789abcdef0123456789abcdef01234567" in exported
            assert "Frozen synthetic archive" in exported and "docxunique" in exported
            assert "page 0" not in exported
            remote_requests = browser.execute_script("return performance.getEntriesByType('resource').map(e=>e.name).filter(n=>n.startsWith('http')&&!n.startsWith(arguments[0]))", f"http://127.0.0.1:{http_port}")
            assert remote_requests == [], remote_requests
            if denied_fallback is not None:
                stable = lambda value: "\n".join(line for line in value.splitlines() if not line.startswith("Exported "))
                assert stable(denied_fallback) == stable(exported), "denied clipboard fallback differs from downloaded Markdown"
            browser.find_element(By.ID, "collection-download").click()
            wait.until(lambda _: len(list(base.glob("findrail-findrail-evidence-collection*.md"))) >= 2)
            wait.until(lambda d: d.execute_script("return window.__createdURLs===2&&window.__revokedURLs>=2"))
            assert any("runbook.pdf — page 2" in p.read_text(encoding="utf-8") for p in base.glob("findrail-findrail-evidence-collection*.md"))

            # A confirmed 404 removes collected evidence even while its source
            # remains registered; it cannot be exported on a later action.
            primary.unlink()
            subprocess.run([str(binary), "index", "--data-dir", str(index), "--max-docx-bytes", str(1 << 20), "--json", str(notes)], check=True,
                           timeout=30, capture_output=True, text=True)
            old_count = int(browser.find_element(By.ID, "collection-count").text)
            browser.find_element(By.ID, "collection-check").click()
            wait.until(lambda d: int(d.find_element(By.ID, "collection-count").text) == old_count - 1)

            # Clear while a check has already received a body; its late
            # successful response must not resurrect status or evidence.
            gate_first_response("/api/v1/documents/")
            browser.find_element(By.ID, "collection-check").click()
            wait.until(lambda d: d.execute_script("return window.__gateReady"))
            browser.find_element(By.ID, "collection-clear").click()
            assert browser.find_element(By.ID, "collection-count").text == "0"
            release_gate()
            time.sleep(.15)
            assert browser.execute_script("return document.getElementById('collection-status').textContent") == "Collection cleared."

            # Re-register the document and pin it again; a known source removal
            # must invalidate an export that is waiting on its inventory body.
            primary.write_text("Restored alpha evidence for the source removal journey.", encoding="utf-8")
            subprocess.run([str(binary), "index", "--data-dir", str(index), "--max-docx-bytes", str(1 << 20), "--json", str(notes)], check=True,
                           timeout=30, capture_output=True, text=True)
            browser.execute_script("window.dispatchEvent(new Event('sources-changed'))")
            browser.find_element(By.ID, "collection-close").click()
            search_preview("alpha", source_filter=source_id)
            wait.until(lambda d: d.find_element(By.ID, "preview-body").text.startswith("alpha evidence"))
            browser.find_element(By.ID, "add-evidence").click()
            assert "added" in browser.execute_script("return document.getElementById('collection-status').textContent"), browser.execute_script("return document.getElementById('collection-status').textContent")
            browser.find_element(By.ID, "close-preview").click()
            browser.find_element(By.ID, "collection-open").click()
            wait.until(lambda d: d.find_element(By.ID, "collection-count").text == "1")
            browser.find_element(By.ID, "collection-close").click()
            search_preview("pdfpageone", format_name="pdf", source_filter=archive_id)
            pin_whole()
            close_preview()
            search_preview("github evidence", source_filter=github_id)
            pin_whole()
            close_preview()
            browser.find_element(By.ID, "collection-open").click()
            wait.until(lambda d: d.find_element(By.ID, "collection-count").text == "3")
            gate_response("/api/v1/sources", "prepare")
            browser.find_element(By.ID, "collection-copy").click()
            try:
                wait.until(lambda d: d.execute_script("return window.__gateReady"))
                subprocess.run([str(binary), "forget", "--data-dir", str(index), source_id], check=True,
                               timeout=20, capture_output=True, text=True)
                # Explicitly trigger the inventory refresh while export is held;
                # this proves the background poll can arrive before release.
                browser.execute_script("window.dispatchEvent(new Event('sources-changed'))")
                wait.until(lambda d: d.find_element(By.ID, "collection-count").text == "2")
            finally:
                release_gate()
            wait.until(lambda d: "Collection changed" in d.execute_script("return document.getElementById('collection-status').textContent"))
            assert browser.find_element(By.ID, "collection-output").get_attribute("value") == ""
            browser.find_element(By.ID, "collection-clear").click()
            assert browser.find_element(By.ID, "collection-count").text == "0"
            browser.refresh()
            assert browser.find_element(By.ID, "collection-count").text == "0"
            browser.set_window_size(375, 812)
            browser.find_element(By.ID, "collection-open").click()
            dlg = browser.find_element(By.ID, "evidence-collection")
            assert browser.execute_script("const d=arguments[0],r=d.getBoundingClientRect();return r.width<=innerWidth&&d.scrollWidth<=d.clientWidth+2", dlg)
            overflow = browser.execute_script("return {width:document.documentElement.scrollWidth,inner:innerWidth,items:[...document.querySelectorAll('body *')].map(e=>({tag:e.tagName,id:e.id,right:e.getBoundingClientRect().right,scroll:e.scrollWidth,client:e.clientWidth})).filter(x=>x.right>innerWidth+2).slice(0,12)}")
            assert overflow["width"] <= overflow["inner"] + 2, overflow
            browser.find_element(By.ID, "collection-close").click()
            # Apply real Chromium page zoom through the browser settings page,
            # then test the app at 375px physical width with a 200% layout zoom.
            browser.get("chrome://settings/appearance")
            zoom = browser.execute_script("""function find(r){for(const e of r.querySelectorAll('*')){
                if(e.id==='zoomLevel')return e;if(e.shadowRoot){const x=find(e.shadowRoot);if(x)return x}}
                return null}const e=find(document);if(!e)return null;e.value='2';
                e.dispatchEvent(new Event('change',{bubbles:true,composed:true}));return e.value""")
            assert zoom == "2", "Chromium settings did not apply 200% page zoom"
            browser.get(f"http://127.0.0.1:{http_port}/")
            wait.until(lambda d: d.find_element(By.ID, "collection-count").text == "0")
            zoom_view = browser.execute_script("return {width:innerWidth,dpr:devicePixelRatio,scroll:document.documentElement.scrollWidth}")
            assert 180 <= zoom_view["width"] <= 190 and zoom_view["dpr"] == 2, zoom_view
            assert zoom_view["scroll"] <= zoom_view["width"] + 2, zoom_view
            search_preview("github evidence", source_filter=github_id)
            assert browser.find_element(By.ID, "preview-title").text == "<img onerror=alert(2)>.md"
            pin_whole()
            close_preview()
            browser.find_element(By.ID, "collection-open").click()
            assert "<img onerror=alert(2)>.md" in browser.execute_script("return document.getElementById('collection-list').textContent")
            assert browser.execute_script("return document.querySelectorAll('img').length") == 0
            zoom_dialog = browser.execute_script("const d=document.getElementById('evidence-collection'),r=d.getBoundingClientRect();return {width:r.width,inner:innerWidth,scroll:d.scrollWidth,client:d.clientWidth,items:[...d.querySelectorAll('*')].map(e=>({tag:e.tagName,id:e.id,text:(e.textContent||'').slice(0,45),right:e.getBoundingClientRect().right,scroll:e.scrollWidth,client:e.clientWidth})).filter(x=>x.right>innerWidth||x.scroll>x.client+2).slice(0,16)}")
            assert zoom_dialog["width"]<=zoom_dialog["inner"] and zoom_dialog["scroll"]<=zoom_dialog["client"]+2, zoom_dialog
            zoom_overflow = browser.execute_script("return {width:document.documentElement.scrollWidth,inner:innerWidth,items:[...document.querySelectorAll('body *')].map(e=>({tag:e.tagName,id:e.id,text:(e.textContent||'').slice(0,40),right:e.getBoundingClientRect().right,scroll:e.scrollWidth,client:e.clientWidth})).filter(x=>x.right>innerWidth+2||x.scroll>x.client+2).slice(0,12)}")
            assert zoom_overflow["width"]<=zoom_overflow["inner"]+2, zoom_overflow
            browser.find_element(By.ID, "collection-clear").click()
            browser.refresh()
            assert browser.find_element(By.ID, "collection-count").text == "0"
            caps = browser.capabilities
            print(f"Chromium {caps.get('browserVersion')} on {sys.platform}; compiled-server journey: hostile synthetic text, PDF pages/title-only, DOCX, imported archive, pinned GitHub fixture, freshness/late-response/404/source-removal, keyboard Escape/focus, actual clipboard and denied fallback ({clipboard_result}), export bytes, clear/reload, 375px and actual 200% zoom.")
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
