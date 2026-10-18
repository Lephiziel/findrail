#!/usr/bin/env python3
"""Fresh-index, UI-added-folder, restart browser journey on an extracted binary."""
import argparse
import json
import os
import pathlib
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

from selenium import webdriver
from selenium.common.exceptions import StaleElementReferenceException
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support.ui import Select

sys.dont_write_bytecode = True
from smoke import docx_fixture, pdf_fixture
from candidate_record import CaseRecorder, digest
from source_cycle_browser import run as source_cycle


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def write_paging_fixture(root):
    """Shared >200-result corpus for standalone and integrated candidate cases."""
    for index in range(260):
        (root / f"paging-{index:03}.md").write_text(
            f"pagingjourney shared ranking unicode雪 synthetic document {index:03}\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path)
    parser.add_argument("--report", type=pathlib.Path)
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    metadata = json.loads((binary.parent / "BUILD-INFO.json").read_text(encoding="utf-8"))
    report = {"status": "running", "cases": [], "candidate_sha": metadata["revision"],
              "binary_sha256": digest(binary)}
    recorder = CaseRecorder(report, args.report)
    basic_case = {"case": "empty_start_folder_paging_late_response_corpus_fence_quotes_reload_restart", "status": "running"}
    report["cases"].append(basic_case)
    basic_started = time.monotonic()
    recorder.save()
    with tempfile.TemporaryDirectory(prefix="Findrail packaged journey – ") as temporary:
        base = pathlib.Path(temporary)
        root, data = base / "fixture notes – 雪", base / "new private index"
        adapters = base / "controlled native adapters"
        adapters.mkdir()
        adapter_log = base / "native adapter calls.jsonl"
        adapter = adapters / "gedit"
        adapter.write_text(f"#!{sys.executable}\nimport json,os,sys\nwith open(os.environ['FINDRAIL_ADAPTER_LOG'],'a',encoding='utf-8') as f:f.write(json.dumps([os.path.basename(sys.argv[0]),*sys.argv[1:]])+'\\n')\n", encoding="utf-8")
        adapter.chmod(0o755)
        folder_adapter = adapters / "xdg-open"
        folder_adapter.write_text(adapter.read_text(encoding="utf-8"), encoding="utf-8")
        folder_adapter.chmod(0o755)
        env = {**os.environ, "PATH": str(adapters) + os.pathsep + os.environ.get("PATH", ""),
               "FINDRAIL_ADAPTER_LOG": str(adapter_log)}
        root.mkdir()
        (root / "meeting notes.md").write_text(
            "idempotencymarker familiar synthetic note. <script>window.fixturePwned=true</script>", encoding="utf-8")
        (root / "runbook.pdf").write_bytes(pdf_fixture(["firstpage unique alpha", "secondpage page2journey unique beta"]))
        (root / "meeting.docx").write_bytes(docx_fixture("docxjourney unique word text"))
        (root / "late.md").write_text("padding " * 10000 + "needletextlate exact text quote 😀e\u0301", encoding="utf-8")
        (root / "late.docx").write_bytes(docx_fixture("padding " * 10000 + "needledocxlate exact Word quote 😀e\u0301"))
        write_paging_fixture(root)
        port = free_port()
        address = f"127.0.0.1:{port}"
        url = f"http://{address}"
        browser = None

        def launch():
            return subprocess.Popen([str(binary), "start", "--data-dir", str(data), "--enable-originals", "--no-open", "--addr", address],
                                    cwd=base, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, text=True)

        server = launch()

        def ready(proc):
            deadline = time.monotonic() + 20
            while time.monotonic() < deadline:
                if proc.poll() is not None:
                    raise AssertionError("candidate start exited with status " + str(proc.returncode))
                try:
                    with urllib.request.urlopen(url + "/healthz", timeout=1) as response:
                        if response.status == 200:
                            return
                except (OSError, urllib.error.URLError):
                    time.sleep(.05)
            raise AssertionError("candidate loopback listener deadline expired")

        try:
            ready(server)
            with urllib.request.urlopen(url + "/api/v1/capabilities", timeout=3) as response:
                capabilities = json.load(response)
            assert capabilities["client_api_version"] == 1
            assert all(capabilities.get(name) is True for name in ("management", "passage_preview", "search_pagination", "evidence_bundle", "portable_snapshots"))
            options = webdriver.ChromeOptions()
            options.binary_location = os.environ.get("FINDRAIL_CHROME") or shutil.which("chromium") or shutil.which("google-chrome") or ""
            if not options.binary_location:
                raise RuntimeError("real Chromium/Chrome executable not found")
            options.add_argument("--headless=new")
            options.add_argument("--no-sandbox")
            options.add_argument("--disable-dev-shm-usage")
            options.add_argument("--no-proxy-server")
            options.add_experimental_option("prefs", {"download.default_directory": str(base), "download.prompt_for_download": False})
            driver = os.environ.get("FINDRAIL_CHROMEDRIVER")
            browser = webdriver.Chrome(service=Service(executable_path=driver) if driver else Service(), options=options)
            report["chrome"] = browser.capabilities["browserVersion"]
            report["chromedriver"] = browser.capabilities["chrome"]["chromedriverVersion"].split()[0]
            assert report["chrome"] == report["chromedriver"] == "139.0.7258.66", "browser/driver differ from the controlled pin"
            browser.set_page_load_timeout(15)
            wait = WebDriverWait(browser, 20)
            browser.get(url + "/")
            wait.until(lambda d: not d.find_element(By.ID, "empty").get_attribute("hidden"))
            assert "empty" in browser.find_element(By.ID, "empty").text.lower()
            with urllib.request.urlopen(url + "/api/v1/sources", timeout=3) as response:
                assert json.load(response)["sources"] == [], "fresh start registered a source"
            browser.find_element(By.ID, "add-folder-open").click()
            browser.find_element(By.ID, "folder-path").send_keys(str(root.resolve()))
            browser.find_element(By.ID, "folder-form").submit()
            wait.until(lambda d: "succeeded" in d.find_element(By.ID, "management-status").text.lower())
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".source-card")) == 1)
            source_card = browser.find_element(By.CSS_SELECTOR, ".source-card")
            assert str(root.resolve()) in source_card.text and "DOCX enabled" in source_card.text, source_card.text
            # A watcher scan may immediately follow the management job's first
            # committed scan. Wait for the known fixture inventory to settle so
            # cursor-staleness tests below exercise pagination, not setup races.
            stable_inventory = None
            stable_since = time.monotonic()
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                with urllib.request.urlopen(url + "/api/v1/sources", timeout=3) as response:
                    inventory = json.load(response)["sources"]
                fingerprint = tuple((item["documents"], item.get("last_indexed_at")) for item in inventory)
                if inventory and inventory[0]["documents"] == 265 and fingerprint == stable_inventory:
                    if time.monotonic() - stable_since >= 2:
                        break
                else:
                    stable_inventory = fingerprint
                    stable_since = time.monotonic()
                time.sleep(.2)
            else:
                raise AssertionError("fixture source inventory did not settle before browser search")

            def search(query, format_name="all"):
                browser.find_element(By.ID, "query").clear()
                browser.find_element(By.ID, "query").send_keys(query)
                Select(browser.find_element(By.ID, "format")).select_by_value(format_name)
                browser.find_element(By.ID, "search-form").submit()
                wait.until(lambda d: not d.find_element(By.ID, "submit").get_property("disabled")
                           and "matching documents" in d.find_element(By.ID, "status").text)
                assert "1 matching" in browser.find_element(By.ID, "status").text

            def open_preview(query):
                def click_matching_result(driver):
                    if driver.find_element(By.ID, "submit").get_property("disabled"):
                        return False
                    for card in driver.find_elements(By.CSS_SELECTOR, ".result"):
                        if query not in card.find_element(By.CLASS_NAME, "snippet").text:
                            continue
                        button = card.find_element(By.XPATH, ".//button[.='Preview']")
                        if button.is_displayed() and button.is_enabled():
                            button.click()
                            return True
                    return False

                # Live inventory refresh can replace a result between lookup
                # and click. Reacquire the matching card and use a real click.
                WebDriverWait(browser, 20, ignored_exceptions=(StaleElementReferenceException,)).until(click_matching_result)

            def run_paging_journey():
                browser.find_element(By.ID, "query").clear()
                Select(browser.find_element(By.ID, "format")).select_by_value("all")
                browser.find_element(By.ID, "query").send_keys("pagingjourney")
                browser.find_element(By.ID, "search-form").submit()
                wait.until(lambda d: d.find_element(By.ID, "page-range").text.startswith("1–20"))
                assert "260" in browser.find_element(By.ID, "page-range").text
                ids = []
                ranges = []
                while True:
                    ranges.append(browser.find_element(By.ID, "page-range").text)
                    ids.extend(card.get_attribute("data-document-id") for card in browser.find_elements(By.CSS_SELECTOR, "#results .result"))
                    next_button = browser.find_element(By.ID, "page-next")
                    if not next_button.is_enabled():
                        break
                    previous_range = browser.find_element(By.ID, "page-range").text
                    next_button.click()
                    wait.until(lambda d: d.find_element(By.ID, "page-range").text != previous_range)
                assert len(ids) == 260 and len(set(ids)) == 260, (len(ids), len(set(ids)), ranges,
                    browser.find_element(By.ID, "status").text, browser.find_element(By.ID, "query-error").text)
                assert browser.switch_to.active_element.get_attribute("id") == "page-previous", "terminal Next left keyboard focus on a disabled control"
                browser.find_element(By.ID, "page-previous").click()
                wait.until(lambda d: d.find_element(By.ID, "page-range").text.startswith("221–240"))
                browser.find_element(By.ID, "page-first").click()
                wait.until(lambda d: d.find_element(By.ID, "page-range").text.startswith("1–20"))
                first_page_ids = [card.get_attribute("data-document-id") for card in browser.find_elements(By.CSS_SELECTOR, "#results .result")]
                with urllib.request.urlopen(url + "/api/v1/search/pages?q=pagingjourney&limit=20", timeout=5) as response:
                    backend_ids = [item["id"] for item in json.load(response)["results"]]
                assert first_page_ids == backend_ids, "browser page IDs differ from the production paginated API"

                Select(browser.find_element(By.ID, "page-size")).select_by_value("50")
                wait.until(lambda d: d.find_element(By.ID, "page-range").text.startswith("1–50"))
                browser.find_element(By.ID, "page-next").click()
                wait.until(lambda d: d.find_element(By.ID, "page-range").text.startswith("51–100"))
                Select(browser.find_element(By.ID, "page-size")).select_by_value("20")
                wait.until(lambda d: d.find_element(By.ID, "page-range").text.startswith("1–20"))

                # Delay a real continuation response across a new explicit
                # search. Releasing it must not restore the previous page.
                browser.execute_script("""
                    window.__pagingFetch=window.fetch.bind(window);window.__holdNextPage=true;
                    window.fetch=async(input,options)=>{const response=await window.__pagingFetch(input,options);
                        const u=new URL(String(input),location.href);
                        if(window.__holdNextPage&&u.pathname==='/api/v1/search/pages'&&u.searchParams.has('cursor')){
                            window.__holdNextPage=false;return new Promise(resolve=>{window.__releaseLatePage=()=>resolve(response)})}
                        return response};
                """)
                try:
                    browser.find_element(By.ID, "page-next").click()
                    wait.until(lambda d: d.execute_script("return typeof window.__releaseLatePage==='function'"))
                    search("docxjourney")
                    browser.execute_async_script("const done=arguments[0];window.__releaseLatePage();setTimeout(done,100)")
                    assert len(browser.find_elements(By.CSS_SELECTOR, "#results .result")) == 1
                    assert "docxjourney" in browser.find_element(By.CSS_SELECTOR, "#results .snippet").text
                finally:
                    browser.execute_script("window.fetch=window.__pagingFetch;delete window.__releaseLatePage")
                browser.find_element(By.ID, "query").clear()
                browser.find_element(By.ID, "query").send_keys("pagingjourney")
                browser.find_element(By.ID, "search-form").submit()
                wait.until(lambda d: d.find_element(By.ID, "page-range").text.startswith("1–20"))

                # Keep inventory polling one response behind while a real
                # source commit lands. The continuation itself still uses the
                # production API, which must reject its now-stale cursor.
                browser.execute_async_script("""
                    const done=arguments[0];window.__inventoryFetch=window.fetch.bind(window);
                    window.__inventoryFetch('/api/v1/sources').then(r=>r.text()).then(text=>{
                        window.fetch=(input,options)=>new URL(String(input),location.href).pathname==='/api/v1/sources'
                            ?Promise.resolve(new Response(text,{status:200,headers:{'Content-Type':'application/json'}}))
                            :window.__inventoryFetch(input,options);done()});
                """)
                marker = root / "cursor-corpus.md"
                def commit_corpus():
                    result = subprocess.run([str(binary), "index", "--data-dir", str(data), "--max-docx-bytes", str(8 << 20), "--json", str(root)],
                                            cwd=base, capture_output=True, text=True, encoding="utf-8", timeout=30, check=True)
                    return json.loads(result.stdout)
                try:
                    marker.write_text("Unrelated synthetic corpus change for cursor fencing.\n", encoding="utf-8")
                    assert commit_corpus()["seen"] == 266
                    browser.find_element(By.ID, "page-next").click()
                    wait.until(lambda d: "Data changed; restart" in d.find_element(By.ID, "status").text)
                    assert browser.switch_to.active_element.get_attribute("id") == "page-first"
                    assert not browser.find_elements(By.CSS_SELECTOR, "#results .result"), "stale pages were mixed into a new snapshot"
                    assert browser.find_element(By.ID, "query").get_attribute("value") == "pagingjourney"
                finally:
                    marker.unlink(missing_ok=True)
                    try:
                        assert commit_corpus()["seen"] == 265
                    finally:
                        browser.execute_script("window.fetch=window.__inventoryFetch")
                # A watcher can publish one final scan following the external
                # index command; let it settle before requesting a new cursor.
                last, since = None, time.monotonic()
                def settled(_):
                    nonlocal last, since
                    with urllib.request.urlopen(url + "/api/v1/sources", timeout=3) as response:
                        rows = json.load(response)["sources"]
                    current = [(s["documents"], s.get("last_indexed_at")) for s in rows]
                    if current != last: last, since = current, time.monotonic()
                    return rows and rows[0]["documents"] == 265 and time.monotonic() - since >= 2
                wait.until(settled)
                if browser.find_element(By.ID, "page-first").is_enabled():
                    browser.find_element(By.ID, "page-first").click()
                else:
                    browser.find_element(By.ID, "search-form").submit()
                wait.until(lambda d: d.find_element(By.ID, "page-range").text.startswith("1–20"))

            run_paging_journey()
            search("idempotencymarker")
            assert "1 matching" in browser.find_element(By.ID, "status").text
            open_preview("idempotencymarker")
            wait.until(lambda d: "idempotencymarker" in d.find_element(By.ID, "preview-body").text)
            browser.execute_script("document.querySelector('#preview .preview-actions + p.notice').id='original-action-status'")
            assert browser.execute_script("return window.fixturePwned===true") is False
            browser.find_element(By.ID, "open-original").click()
            wait.until(lambda d: "Opening the current file" in d.find_element(By.ID, "original-action-status").text)
            browser.find_element(By.ID, "open-folder").click()
            wait.until(lambda d: len(adapter_log.read_text(encoding="utf-8").splitlines()) == 2)
            adapter_calls = [json.loads(line) for line in adapter_log.read_text(encoding="utf-8").splitlines()]
            assert adapter_calls == [["gedit", "--", str(root / "meeting notes.md")],
                                     ["xdg-open", str(root)]], adapter_calls
            selected = browser.execute_script("""
                const body=document.getElementById('preview-body');
                const walker=document.createTreeWalker(body,NodeFilter.SHOW_TEXT),nodes=[];
                let node; while(node=walker.nextNode()) if(node.length) nodes.push(node);
                const range=document.createRange(); range.setStart(nodes[0],0);
                let remaining=20;
                for(const text of nodes){
                    if(remaining<=text.length){range.setEnd(text,remaining);break}
                    remaining-=text.length;
                }
                const selection=getSelection();selection.removeAllRanges();selection.addRange(range);
                return selection.toString();
            """)
            assert selected == "idempotencymarker fa", selected
            browser.find_element(By.ID, "add-evidence").click()
            wait.until(lambda d: "added" in d.find_element(By.ID, "collection-status").get_property("textContent").lower())
            browser.find_element(By.ID, "add-evidence").click()
            assert "Duplicate" in browser.find_element(By.ID, "collection-status").get_property("textContent")
            browser.find_element(By.ID, "close-preview").click()
            search("page2journey", "pdf")
            open_preview("page2journey")
            wait.until(lambda d: "Page 2 / 2" in d.find_element(By.ID, "page-label").text)
            assert "secondpage" in browser.find_element(By.ID, "preview-body").text
            browser.execute_script("document.getElementById('whole-preview').checked=true")
            browser.find_element(By.ID, "add-evidence").click()
            browser.find_element(By.ID, "close-preview").click()
            browser.find_element(By.ID, "collection-open").click()
            wait.until(lambda d: len(d.find_elements(By.CLASS_NAME, "collection-row")) == 2)
            def add_note(_):
                try:
                    textarea = browser.find_element(By.CSS_SELECTOR, ".collection-row textarea")
                    if textarea.get_attribute("value") == "note from packaged journey":
                        return True
                    textarea.send_keys("note from packaged journey")
                    return textarea.get_attribute("value") == "note from packaged journey"
                except StaleElementReferenceException:
                    return False
            wait.until(add_note)
            browser.execute_script("""const input=document.getElementById('collection-title');
                input.value='Packaged notes / week 1';input.dispatchEvent(new Event('input'));""")
            browser.find_element(By.ID, "collection-download").click()
            deadline = time.monotonic() + 10
            exported = base / "findrail-packaged-notes-week-1.md"
            while time.monotonic() < deadline and not exported.exists():
                time.sleep(.05)
            text = exported.read_text(encoding="utf-8")
            assert text.startswith("# Packaged notes / week 1\n")
            assert browser.find_element(By.ID, "collection-status").text == f"Downloaded {exported.name}."
            assert selected in text and "note from packaged journey" in text
            assert "runbook.pdf — page 2" in text and (root / "runbook.pdf").as_uri() + "#page=2" in text
            browser.find_element(By.ID, "collection-close").click()
            run_paging_journey()
            browser.find_element(By.ID, "collection-open").click()
            wait.until(lambda d: len(d.find_elements(By.CLASS_NAME, "collection-row")) == 2)
            assert browser.find_element(By.CSS_SELECTOR, ".collection-row textarea").get_attribute("value") == "note from packaged journey"
            browser.find_element(By.ID, "collection-close").click()
            search("docxjourney", "docx")
            open_preview("docxjourney")
            wait.until(lambda d: "docxjourney" in d.find_element(By.ID, "preview-body").text)
            assert "page numbers are unavailable" in browser.find_element(By.ID, "preview-notice").text.lower()
            assert "page" not in browser.find_element(By.ID, "preview-meta").text.lower()
            browser.find_element(By.ID, "close-preview").click()
            browser.refresh()
            wait.until(lambda d: d.find_element(By.ID, "collection-count").text == "0")
            browser.quit(); browser = None
            server.terminate(); server.communicate(timeout=8)
            server = launch(); ready(server)
            browser = webdriver.Chrome(service=Service(executable_path=driver) if driver else Service(), options=options)
            browser.get(url + "/")
            wait = WebDriverWait(browser, 20)
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".source-card")) == 1)
            assert browser.find_element(By.ID, "collection-count").text == "0"
            search("idempotencymarker")
            assert "1 matching" in browser.find_element(By.ID, "status").text
            basic_case.update(status="passed", elapsed_seconds=round(time.monotonic()-basic_started, 3))

            def restart(destination):
                nonlocal data, server
                server.terminate(); server.communicate(timeout=8)
                data = destination
                server = launch(); ready(server)
                browser.get(url + "/")

            source_cycle(browser, base, root, url, restart, recorder, run_paging_journey)
            report["status"] = "passed"
            print("Common packaged browser journey passed: Unicode folder/paging/late citations/notes/bundle transfer/preset remap/snapshot jobs/frozen search/restart/Forget.")
        except BaseException as exc:
            report["status"] = "failed"
            if basic_case["status"] == "running": basic_case.update(status="failed", error_type=type(exc).__name__, elapsed_seconds=round(time.monotonic()-basic_started, 3))
            raise
        finally:
            recorder.save()
            if browser:
                browser.quit()
            if server.poll() is None:
                server.terminate()
                try:
                    server.communicate(timeout=8)
                except subprocess.TimeoutExpired:
                    server.kill(); server.communicate(timeout=3)


if __name__ == "__main__":
    main()
