#!/usr/bin/env python3
"""Cross-feature journey on one extracted candidate, with independent A/B/C indexes.

The successful path always uses the real server, SQLite, jobs and DOM. Fetch
seams hold or discard *real* responses only at the explicitly tested races.
TASK 11 must be present; missing snapshot controls are a failure, never a skip.
"""
import argparse
from contextlib import ExitStack
import copy
import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

from selenium import webdriver
from selenium.common.exceptions import StaleElementReferenceException
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.common.keys import Keys
from selenium.webdriver.support.ui import Select, WebDriverWait

from packaged_browser_journey import free_port, write_paging_fixture
from packaged_journey import CaseReport, command, validate_candidate
from smoke import docx_fixture, pdf_fixture


class Session:
    def __init__(self, binary, base, name, stack):
        self.binary, self.base = binary, base
        self.data = base / ("index " + name + " 雪")
        self.downloads = base / ("downloads " + name)
        self.downloads.mkdir()
        self.address = f"127.0.0.1:{free_port()}"
        self.url = "http://" + self.address
        self.process = self.browser = None
        self.stack = stack
        stack.callback(self.stop)

    def start(self, mode="start"):
        args = [str(self.binary), mode, "--addr", self.address]
        if mode != "demo":
            args += ["--data-dir", str(self.data)]
        # Keep normal watcher behavior. A one-second full-rescan interval would
        # deliberately invalidate every pager fence while the UI is navigating.
        args += ["--no-open"] if mode in ("start", "demo") else ["--no-sync"]
        env = {**os.environ, "NO_PROXY": "127.0.0.1,localhost", "no_proxy": "127.0.0.1,localhost"}
        # Output is not piped: an unattended server cannot block on a full pipe.
        self.process = subprocess.Popen(args, cwd=self.base, env=env,
                                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        def ready(_):
            if self.process.poll() is not None:
                raise AssertionError("candidate server exited during startup")
            try:
                return self.api("/healthz") == {"status": "ok"}
            except (OSError, urllib.error.URLError):
                return False
        WebDriverWait(None, 20, poll_frequency=.1).until(ready)
        options = webdriver.ChromeOptions()
        options.binary_location = os.environ.get("FINDRAIL_CHROME") or shutil.which("chromium") or shutil.which("google-chrome") or ""
        if not options.binary_location:
            raise RuntimeError("real Chromium/Chrome executable not found")
        for flag in ("--headless=new", "--no-sandbox", "--disable-dev-shm-usage", "--no-proxy-server", "--disable-background-networking"):
            options.add_argument(flag)
        options.add_experimental_option("prefs", {"download.default_directory": str(self.downloads), "download.prompt_for_download": False})
        options.set_capability("goog:loggingPrefs", {"performance": "ALL"})
        driver = os.environ.get("FINDRAIL_CHROMEDRIVER") or shutil.which("chromedriver")
        self.browser = webdriver.Chrome(service=Service(executable_path=driver) if driver else Service(), options=options)
        self.browser.set_page_load_timeout(20)
        self.browser.execute_cdp_cmd("Page.addScriptToEvaluateOnNewDocument", {"source": """
            window.__effects={searches:[],writes:[],mutations:[]};
            const original=window.fetch.bind(window);
            window.fetch=(input,options={})=>{
                const path=String(input);if(path.includes('/api/v1/search'))window.__effects.searches.push(path);
                if(options.method&&!['GET','HEAD'].includes(options.method))window.__effects.mutations.push(path);
                return original(input,options);
            };
            const set=Storage.prototype.setItem;
            Storage.prototype.setItem=function(...args){window.__effects.writes.push('storage');return set.apply(this,args)};
            const open=indexedDB.open.bind(indexedDB);
            indexedDB.open=(...args)=>{window.__effects.writes.push('indexedDB');return open(...args)};
            for(const key of ['pushState','replaceState']){const fn=history[key].bind(history);
                history[key]=(...args)=>{window.__effects.writes.push(key);return fn(...args)}};
        """})
        self.page()

    def page(self):
        self.browser.get(self.url + "/")
        self.wait(lambda: self.browser.execute_script("return !!window.localEvidenceCollection"))
        self.token = self.browser.execute_async_script("const done=arguments[0];window.findrailBootstrap.then(x=>done(x.token));")

    def stop(self):
        try:
            if self.browser:
                browser, self.browser = self.browser, None
                browser.quit()
        finally:
            if self.process:
                process, self.process = self.process, None
                if process.poll() is None:
                    process.terminate()
                    try:
                        process.wait(timeout=8)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=3)
                        raise AssertionError("candidate server failed to drain")

    def wait(self, predicate, seconds=30):
        return WebDriverWait(self.browser, seconds, poll_frequency=.1,
                             ignored_exceptions=(StaleElementReferenceException,)).until(lambda _: predicate())

    def el(self, name):
        return self.browser.find_element(By.ID, name)

    def fill(self, name, value):
        element = self.el(name)
        element.clear()
        element.send_keys(value)

    def api(self, path, method="GET", body=None, expected=200):
        headers = {"Origin": self.url, "Content-Type": "application/json"}
        if getattr(self, "token", None):
            headers["X-Findrail-Token"] = self.token
        request = urllib.request.Request(self.url + path, method=method, headers=headers,
                                         data=None if body is None else json.dumps(body, ensure_ascii=False).encode())
        try:
            response = urllib.request.urlopen(request, timeout=8)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            assert response.status == expected, (path, response.status, expected)
            if response.status == 204:
                return None
            if response.headers.get_content_type() != "application/json":
                return response.read(512)
            return json.load(response)

    def sources(self):
        return self.api("/api/v1/sources")["sources"]

    def add_folder(self, root):
        self.wait(lambda: self.el("add-folder-open").is_displayed())
        self.el("add-folder-open").click()
        self.fill("folder-path", str(root))
        self.el("folder-form").submit()
        self.wait(lambda: "succeeded" in self.el("management-status").text.lower())
        self.wait(lambda: any(s["root"] == str(root) for s in self.sources()))
        return next(s["id"] for s in self.sources() if s["root"] == str(root))

    def settled(self, source_id, count):
        previous, since = None, time.monotonic()
        def stable():
            nonlocal previous, since
            source = next((s for s in self.sources() if s["id"] == source_id), None)
            fingerprint = (source["documents"], source.get("last_indexed_at")) if source else None
            if fingerprint != previous:
                previous, since = fingerprint, time.monotonic()
            return source and source["documents"] == count and time.monotonic() - since >= 2
        self.wait(stable)

    def search(self, query, source="", kind="all"):
        self.fill("query", query)
        for name, value in (("source", source), ("format", kind), ("mode", "literal")):
            Select(self.el(name)).select_by_value(value)
        for name in ("path-prefix", "title-contains"):
            self.el(name).clear()
        self.el("submit").click()
        self.wait(lambda: self.el("submit").is_enabled() and "matching documents" in self.el("status").text)

    def pin(self, query, quote, source="", kind="all", page=0, frozen=False):
        self.search(query, source, kind)
        self.wait(lambda: len(self.browser.find_elements(By.CSS_SELECTOR, "#results .result")) == 1)
        self.browser.find_element(By.CSS_SELECTOR, ".result-preview").click()
        self.wait(lambda: quote in self.el("preview-body").text and self.el("add-evidence").is_enabled())
        if page:
            assert self.el("page-label").text == f"Page {page} / 2"
        if frozen:
            assert "Frozen imported snapshot" in self.el("preview-notice").text
            assert self.el("open-original").get_property("hidden") and self.el("open-folder").get_property("hidden")
        if self.el("whole-preview").is_selected():
            self.el("whole-preview").click()
        chosen = self.browser.execute_script("""
            const body=document.getElementById('preview-body'),quote=arguments[0];
            const at=body.textContent.indexOf(quote),walker=document.createTreeWalker(body,NodeFilter.SHOW_TEXT);
            if(at<0)throw Error('quote absent');let n,pos=0,start,end;
            while(n=walker.nextNode()){
                if(!start&&at>=pos&&at<pos+n.length)start=[n,at-pos];
                if(at+quote.length>pos&&at+quote.length<=pos+n.length){end=[n,at+quote.length-pos];break}
                pos+=n.length;
            }
            const range=document.createRange();range.setStart(...start);range.setEnd(...end);
            getSelection().removeAllRanges();getSelection().addRange(range);
            document.dispatchEvent(new Event('selectionchange'));return getSelection().toString();
        """, quote)
        assert chosen == quote
        self.el("add-evidence").click()
        self.el("close-preview").click()

    def collection(self):
        return self.browser.execute_script("return window.localEvidenceCollection.entries")

    def save_export(self, title):
        self.el("collection-open").click()
        self.fill("collection-title", title)
        for i in range(len(self.collection())):
            note = self.browser.find_elements(By.CSS_SELECTOR, ".collection-row textarea")[i]
            note.clear()
            note.send_keys(f"note {i + 1} for {title}")
        self.browser.find_element(By.XPATH, "(//section[contains(@class,'collection-row')])[2]//button[.='Move up']").click()
        self.el("collection-save").click()
        self.wait(lambda: "Collection saved." in self.el("collection-status").text)
        collection_id = self.el("collection-picker").get_attribute("value")
        bundle = self.downloads / "findrail-evidence-bundle.json"
        bundle.unlink(missing_ok=True)
        self.el("collection-export-bundle").click()
        self.wait(lambda: bundle.exists())
        payload = json.loads(bundle.read_text(encoding="utf-8"))
        self.el("collection-download").click()
        markdown = self.downloads / ("findrail-" + title.lower().replace(" ", "-") + ".md")
        self.wait(lambda: markdown.exists())
        text = markdown.read_text(encoding="utf-8")
        positions = [text.index(entry["text"]) for entry in payload["entries"]]
        assert positions == sorted(positions)
        assert all(entry["note"] in text for entry in payload["entries"])
        assert "page 2" in text
        self.el("collection-close").click()
        return collection_id, bundle, payload

    def source_action(self, source, title):
        def click():
            button = self.browser.find_element(By.CSS_SELECTOR, f'.source-card[data-id="{source}"] button[title^="{title}"]')
            if not button.is_enabled():
                return False
            button.click()
            return True
        self.wait(click)

    def inspect(self, archive):
        self.el("snapshot-inspect-open").click()
        self.wait(lambda: self.el("snapshot-submit").is_enabled())
        self.fill("snapshot-path", str(archive))
        self.el("snapshot-submit").click()
        self.wait(lambda: self.el("snapshot-submit").text == "Import frozen source" and self.el("snapshot-submit").is_enabled())
        assert self.browser.execute_script("return document.activeElement.id") == "snapshot-name"

    def import_snapshot(self, archive, name, duplicate=False):
        self.inspect(archive)
        self.fill("snapshot-name", name)
        self.el("snapshot-submit").click()
        expected = "already imported as" if duplicate else "imported"
        self.wait(lambda: expected in self.el("snapshot-summary").text)
        self.el("snapshot-close").click()
        self.wait(lambda: any(s["kind"] == "archive" for s in self.sources()))
        self.wait(lambda: len(Select(self.el("source")).options) == len(self.sources()) + 1)
        return next(s["id"] for s in self.sources() if s["kind"] == "archive")

    def open_bundle(self, path, destination):
        self.el("collection-open").click()
        self.el("collection-import-bundle").click()
        self.wait(lambda: self.el("bundle-file").is_enabled())
        self.el("bundle-file").send_keys(str(path))
        self.wait(lambda: len(self.browser.find_elements(By.CSS_SELECTOR, "#bundle-mappings select")) == 1)
        mapping = self.browser.find_element(By.CSS_SELECTOR, "#bundle-mappings select")
        assert mapping.get_attribute("value") == "", "destination selected by display name"
        Select(mapping).select_by_value(destination)
        self.el("bundle-preview").click()
        self.wait(lambda: "conflicts" in self.el("bundle-status").text)

    def privacy(self):
        assert self.browser.execute_script("return window.__effects.writes") == []
        requests = []
        for entry in self.browser.get_log("performance"):
            message = json.loads(entry["message"])["message"]
            if message["method"] == "Network.requestWillBeSent":
                target = message["params"]["request"]["url"]
                if target.startswith(("http:", "https:", "ws:", "wss:")):
                    requests.append(target)
        assert all(urllib.parse.urlsplit(url).netloc == self.address for url in requests), "external-origin page request"


def same_entries(bundle, expected):
    keys = ("relative_path", "media_type", "content_hash", "page", "start_char", "text", "note")
    assert [{k: e[k] for k in keys} for e in bundle["entries"]] == [{k: e[k] for k in keys} for e in expected["entries"]]


def run(binary, report):
    cases = CaseReport(report)
    with tempfile.TemporaryDirectory(prefix="Findrail transfer 雪 ") as temporary, ExitStack() as stack:
        base = pathlib.Path(temporary)
        notes = base / "live documents Ж 雪"
        notes.mkdir()
        text_quote = "transfertextneedle точная цитата 😀 𐐷"
        word_quote = "transferwordneedle точная Word цитата 😀 𐐷"
        prefix = "начало 😀 𐐷 " + "padding " * 10000
        (notes / "late.md").write_text(prefix + text_quote + " end", encoding="utf-8")
        (notes / "late.docx").write_bytes(docx_fixture(prefix + word_quote + " end"))
        pdf_quote = "transferpdfneedle selected page two"
        pdf_prefix = "different page two prefix "
        (notes / "manual.pdf").write_bytes(pdf_fixture(["irrelevant page one", pdf_prefix + pdf_quote]))
        write_paging_fixture(notes)
        a, b, c = (Session(binary, base, name, stack) for name in ("A", "B", "C"))
        cases.run("A_fresh_candidate_server_browser", a.start)
        expected_offsets = {text_quote: len(prefix), word_quote: len(prefix), pdf_quote: len(pdf_prefix)}
        source_a = None
        def collect_a():
            nonlocal source_a
            assert a.sources() == []
            source_a = a.add_folder(notes)
            a.settled(source_a, 263)
            for query, quote, kind, page in (("transfertextneedle", text_quote, "text", 0),
                                              ("transferwordneedle", word_quote, "docx", 0),
                                              ("transferpdfneedle", pdf_quote, "pdf", 2)):
                a.pin(query, quote, source_a, kind, page)
        cases.run("A_ui_folder_late_unicode_passages_pdf2", collect_a)
        saved_a = {}
        def export_a():
            cid, path, bundle = a.save_export("Transfer A")
            for entry in bundle["entries"]:
                assert entry["start_char"] == expected_offsets[entry["text"]]
                assert entry["page"] == (2 if entry["text"] == pdf_quote else 0)
            assert [e["note"] for e in bundle["entries"]] == ["note 2 for Transfer A", "note 1 for Transfer A", "note 3 for Transfer A"]
            saved_a.update(id=cid, path=path, bundle=bundle)
        cases.run("A_saved_markdown_bundle_order_notes_offsets", export_a)
        def collection_cursor_fence():
            a.search("pagingjourney", source_a)
            a.wait(lambda: a.el("page-range").text.startswith("1–20"))
            a.el("page-next").click()
            a.wait(lambda: a.el("page-range").text.startswith("21–40"))
            a.el("page-previous").click()
            a.wait(lambda: a.el("page-range").text.startswith("1–20"))
            ids = [e.get_attribute("data-document-id") for e in a.browser.find_elements(By.CSS_SELECTOR, "#results .result")]
            params = urllib.parse.urlencode({"q": "pagingjourney", "source": source_a, "limit": 20})
            api = a.api("/api/v1/search/pages?" + params)
            assert ids == [e["id"] for e in api["results"]]
            assert api["total"] == 260
            a.el("collection-open").click()
            a.browser.find_element(By.CSS_SELECTOR, ".collection-row textarea").send_keys(" fence edit")
            a.el("collection-save").click()
            a.wait(lambda: "Collection saved." in a.el("collection-status").text)
            evidence = a.collection()
            a.el("collection-close").click()
            a.el("page-next").click()
            a.wait(lambda: "Data changed; restart" in a.el("status").text)
            assert not a.browser.find_elements(By.CSS_SELECTOR, "#results .result")
            assert a.collection() == evidence
            assert a.browser.execute_script("return document.activeElement.id") == "page-first"
            a.el("page-first").click()
            a.wait(lambda: a.el("page-range").text.startswith("1–20"))
        cases.run("A_saved_note_commit_invalidates_cursor_without_mixing_pages", collection_cursor_fence)
        preset_path = a.downloads / "findrail-search-preset.json"
        def preset_with_pending_preview():
            a.el("collection-open").click()
            a.browser.find_element(By.CSS_SELECTOR, ".collection-row textarea").send_keys(" unsaved edit")
            pinned = a.collection()
            a.el("collection-close").click()
            a.search("transfertextneedle", source_a, "text")
            a.fill("preset-title", "Transfer preset")
            a.fill("path-prefix", "late.md")
            a.fill("title-contains", "late")
            a.el("preset-save").click()
            a.wait(lambda: preset_path.exists())
            preset = json.loads(preset_path.read_text(encoding="utf-8"))
            assert preset["source"]["id_hint"] == source_a
            assert preset["filters"] == {"format": "text", "path_prefix": "late.md", "title_contains": "late"}
            # Retain the real passage response, ignore the client's abort only
            # for this seam, then release it after Apply invalidates the chain.
            a.browser.execute_script("""
                window.__fetchBeforeHold=window.fetch;
                window.fetch=async(input,options={})=>{
                    if(!String(input).includes('/passage?'))return window.__fetchBeforeHold(input,options);
                    const response=await window.__fetchBeforeHold(input,{...options,signal:undefined});
                    return new Promise(resolve=>{window.__releasePassage=()=>{resolve(response);window.__passageReleased=true}});
                };
            """)
            a.browser.find_element(By.CSS_SELECTOR, ".result-preview").click()
            a.wait(lambda: a.browser.execute_script("return typeof window.__releasePassage==='function'"))
            # Loading a preset uses the real native file input while a response
            # is outstanding; close the modal through its normal close action.
            a.el("close-preview").click()
            searches = a.browser.execute_script("return window.__effects.searches.length")
            a.el("preset-file").send_keys(str(preset_path))
            a.wait(lambda: not a.el("preset-preview").get_property("hidden"))
            assert a.el("preset-source-map").get_attribute("value") == ""
            Select(a.el("preset-source-map")).select_by_value(source_a)
            a.el("preset-apply").click()
            a.browser.execute_script("window.__releasePassage();window.fetch=window.__fetchBeforeHold")
            a.wait(lambda: a.browser.execute_script("return window.__passageReleased===true"))
            assert a.browser.execute_script("return window.__effects.searches.length") == searches
            assert not a.browser.find_elements(By.CSS_SELECTOR, "#results .result")
            assert not a.el("preview").get_property("open")
            assert a.collection() == pinned
            assert a.browser.execute_script("return document.activeElement.id") == "query"
            a.el("submit").click()
            a.wait(lambda: "1 matching" in a.el("status").text and a.el("submit").is_enabled())
            calls = a.browser.execute_script("return window.__effects.searches")
            params = urllib.parse.parse_qs(urllib.parse.urlsplit(calls[-1]).query)
            assert params["source"] == [source_a] and "cursor" not in params
            assert a.collection() == pinned
            # Saved notes remain untouched by local edits and preset actions.
            remote = a.api("/api/v1/collections/" + saved_a["id"])
            assert remote["entries"][0]["note"] == "note 2 for Transfer A fence edit"
        cases.run("A_preset_scope_late_preview_dirty_evidence", preset_with_pending_preview)

        def require_snapshot_ui():
            assert a.browser.find_elements(By.ID, "snapshot-inspect-open"), "TASK 11 Sources UI is missing; merge dependency #79 before full TASK 12 acceptance"
            assert a.el("snapshot-inspect-open").is_displayed()
        cases.run("snapshot_ui_dependency", require_snapshot_ui)
        archive = base / "transfer snapshot Ж.zip"
        def export_snapshot():
            pinned = a.collection()
            a.source_action(source_a, "Export indexed")
            a.wait(lambda: a.el("snapshot-submit").is_enabled())
            a.fill("snapshot-path", str(archive))
            a.el("snapshot-submit").click()
            a.wait(lambda: "Snapshot exported and verified" in a.el("snapshot-summary").text)
            assert archive.is_file() and a.collection() == pinned
            summary = command([str(binary), "inspect-export", str(archive), "--json"])
            inspected = json.loads(summary.stdout)
            assert inspected["documents"] == 263 and inspected["pages"] == 2
            a.el("snapshot-close").click()
        cases.run("A_real_host_path_export_and_inspect", export_snapshot)
        cases.run("B_independent_candidate_server_browser", b.start)
        frozen_b = cases.run("B_UI_inspect_explicit_frozen_import", lambda: b.import_snapshot(archive, "Transfer frozen"))
        def collect_frozen():
            card = b.browser.find_element(By.CSS_SELECTOR, f'.source-card[data-id="{frozen_b}"]')
            assert str(notes) not in card.text
            assert all(button.text not in ("Refresh", "Configure") for button in card.find_elements(By.TAG_NAME, "button"))
            for query, quote, kind, page in (("transfertextneedle", text_quote, "text", 0),
                                              ("transferwordneedle", word_quote, "docx", 0),
                                              ("transferpdfneedle", pdf_quote, "pdf", 2)):
                b.pin(query, quote, frozen_b, kind, page, frozen=True)
        cases.run("B_frozen_passages_pdf_no_live_actions", collect_frozen)
        cid_b, bundle_b_path, bundle_b = cases.run("B_save_export_frozen_bundle", lambda: b.save_export("Transfer B"))
        for entry in bundle_b["entries"]:
            assert entry["start_char"] == expected_offsets[entry["text"]]
        def frozen_independence():
            for path in notes.iterdir():
                path.unlink()
            a.settled(source_a, 0)
            b.search("transferpdfneedle", frozen_b, "pdf")
            assert "1 matching" in b.el("status").text
            assert b.import_snapshot(archive, "Must not rename", duplicate=True) == frozen_b
            assert next(s for s in b.sources() if s["id"] == frozen_b)["name"] == "Transfer frozen"
        cases.run("A_originals_deleted_B_unchanged_duplicate_preserves_name", frozen_independence)
        cases.run("C_independent_candidate_server_browser", c.start)
        frozen_c = cases.run("C_UI_inspect_explicit_frozen_import", lambda: c.import_snapshot(archive, "Transfer frozen"))
        # A same-name live source supplies ambiguity without compatible identity.
        similar = base / "different" / "Transfer frozen"
        similar.mkdir(parents=True)
        (similar / "late.md").write_text("different synthetic contents", encoding="utf-8")
        unrelated = c.add_folder(similar)
        c.settled(unrelated, 1)
        imported_c = {}
        def compatible_bundle():
            assert sum(s["name"] == "Transfer frozen" for s in c.sources()) == 2
            c.open_bundle(bundle_b_path, frozen_c)
            assert "3 matches; 0 conflicts" in c.el("bundle-status").text
            c.fill("bundle-title", "Transfer C")
            c.el("bundle-confirm").click()
            # Hold a real successful commit response, exercise duplicate clicks,
            # and preserve newer local edits while reconciling an unknown result.
            c.browser.execute_script("""
                window.__fetchBeforeCommit=window.fetch;window.__commitCount=0;
                window.fetch=async(input,options={})=>{
                    if(!String(input).endsWith('/collections/import'))return window.__fetchBeforeCommit(input,options);
                    window.__commitCount++;const response=await window.__fetchBeforeCommit(input,options);
                    return new Promise((resolve,reject)=>{window.__commitSaved=response;window.__loseCommitResponse=()=>reject(new TypeError('synthetic lost response'))});
                };
            """)
            c.el("bundle-commit").click()
            c.wait(lambda: c.browser.execute_script("return typeof window.__loseCommitResponse==='function'"))
            c.browser.execute_script("document.getElementById('bundle-commit').click()")
            assert c.browser.execute_script("return window.__commitCount") == 1
            c.el("bundle-cancel").click()
            c.fill("collection-title", "Newer local draft")
            c.browser.execute_script("window.__loseCommitResponse();window.fetch=window.__fetchBeforeCommit")
            c.wait(lambda: "uncertain" in c.el("collection-status").text)
            assert c.browser.execute_script("return window.localEvidenceCollection.title") == "Newer local draft"
            collections = c.api("/api/v1/collections")["collections"]
            assert len(collections) == 1 and collections[0]["title"] == "Transfer C"
            imported_c["id"] = collections[0]["id"]
            c.wait(lambda: any(o.get_attribute("value") == imported_c["id"] for o in Select(c.el("collection-picker")).options))
            Select(c.el("collection-picker")).select_by_value(imported_c["id"])
            c.wait(lambda: c.el("collection-title").get_attribute("value") == "Transfer C")
            bundle_path = c.downloads / "findrail-evidence-bundle.json"
            c.el("collection-export-bundle").click()
            c.wait(lambda: bundle_path.exists())
            same_entries(json.loads(bundle_path.read_text(encoding="utf-8")), bundle_b)
            c.el("collection-close").click()
        cases.run("B_to_C_explicit_compatible_mapping_uncertain_commit_reconciled", compatible_bundle)

        def conflicts_atomic():
            before = c.api("/api/v1/collections")["collections"]
            variants = [("relative_path", "missing.md"), ("media_type", "text/plain"),
                        ("content_hash", "0" * 64), ("text", "not the selected quote"), ("page", 1)]
            for field, value in variants:
                bad = copy.deepcopy(bundle_b)
                index = next(i for i, entry in enumerate(bad["entries"]) if entry["page"] == 2) if field == "page" else 0
                bad["entries"][index][field] = value
                path = base / ("conflict-" + field + ".json")
                path.write_text(json.dumps(bad, ensure_ascii=False), encoding="utf-8")
                c.open_bundle(path, frozen_c)
                assert "0 conflicts" not in c.el("bundle-status").text
                assert not c.el("bundle-commit").is_enabled()
                c.el("bundle-cancel").click()
                c.el("collection-close").click()
                assert c.api("/api/v1/collections")["collections"] == before
            # Live filesystem hashes are not the imported archive hash contract.
            c.open_bundle(saved_a["path"], frozen_c)
            assert "changed_hash" in c.el("bundle-results").text
            assert not c.el("bundle-commit").is_enabled()
            c.el("bundle-cancel").click()
            c.el("collection-close").click()
        cases.run("C_path_media_hash_quote_page_and_live_identity_conflicts_atomic", conflicts_atomic)

        def restart_and_forget():
            cursor_params = urllib.parse.urlencode({"q": "pagingjourney", "source": frozen_c, "limit": 20})
            old_cursor = c.api("/api/v1/search/pages?" + cursor_params)["next_cursor"]
            assert old_cursor
            inspection = c.api("/api/v1/snapshots/inspect", "POST", {"path": str(archive)}, expected=202)["job"]["id"]
            inspection_result = {}
            def inspected():
                inspection_result.update(c.api("/api/v1/snapshots/inspections/" + inspection))
                return inspection_result["status"] == "succeeded"
            c.wait(inspected)
            old_bundle_claim = c.api("/api/v1/collections/import/preview", "POST",
                                     {"bundle": bundle_b, "mappings": {bundle_b["sources"][0]["alias"]: frozen_c}})["claim"]
            for session, source, cid, title, expected in ((b, frozen_b, cid_b, "Transfer B", bundle_b),
                                                         (c, frozen_c, imported_c["id"], "Transfer C", bundle_b)):
                session.privacy()
                session.stop()
                session.start()
                session.search("transferpdfneedle", source, "pdf")
                assert "1 matching" in session.el("status").text
                session.el("collection-open").click()
                Select(session.el("collection-picker")).select_by_value(cid)
                session.wait(lambda: session.el("collection-title").get_attribute("value") == title)
                path = session.downloads / "findrail-evidence-bundle.json"
                path.unlink(missing_ok=True)
                session.el("collection-export-bundle").click()
                session.wait(lambda: path.exists())
                same_entries(json.loads(path.read_text(encoding="utf-8")), expected)
                session.el("collection-close").click()
            c.api("/api/v1/search/pages?" + cursor_params + "&" + urllib.parse.urlencode({"cursor": old_cursor}), expected=409)
            c.api("/api/v1/snapshots/import", "POST", {"claim": inspection_result["claim"], "name": "Old claim"}, expected=409)
            c.api("/api/v1/snapshots/inspections/" + inspection, expected=404)
            c.api("/api/v1/collections/import", "POST", {"bundle": bundle_b, "claim": old_bundle_claim, "title": "Old preview"}, expected=409)
            assert len(c.api("/api/v1/collections")["collections"]) == 1
            c.open_bundle(bundle_b_path, frozen_c)
            assert "3 matches; 0 conflicts" in c.el("bundle-status").text
            c.el("bundle-confirm").click()
            assert c.el("bundle-commit").is_enabled()
            # Another real client forgets the destination while this tab holds
            # a ready preview. No synthetic inventory event authorizes the test.
            removal = c.api("/api/v1/sources/" + frozen_c, "DELETE", expected=202)["job"]["id"]
            c.wait(lambda: all(s["id"] != frozen_c for s in c.sources()))
            c.wait(lambda: not c.el("bundle-commit").is_enabled())
            assert not c.el("bundle-confirm").is_selected()
            c.el("bundle-cancel").click()
            c.el("collection-close").click()
            assert c.api("/api/v1/jobs/" + removal)["status"] == "succeeded"
            c.wait(lambda: len(Select(c.el("source")).options) == 2)
            assert c.sources()[0]["id"] == unrelated
            assert c.api("/api/v1/collections/" + imported_c["id"])["entries"] == []
            c.search("different", unrelated)
            assert "1 matching" in c.el("status").text
            assert notes.is_dir() and archive.is_file() and saved_a["path"].is_file() and bundle_b_path.is_file()
            assert list(a.downloads.glob("*.md")) and list(b.downloads.glob("*.md"))
        cases.run("B_C_restart_exact_evidence_Forget_cascade_unrelated_preserved", restart_and_forget)
        for session in (a, b, c):
            session.privacy()
        def read_only_modes():
            for mode in ("serve", "demo"):
                c.stop()
                c.start(mode)
                capabilities = c.api("/api/v1/capabilities")
                assert all(capabilities.get(name, False) is False for name in (
                    "management", "collection_persistence", "evidence_bundle", "portable_snapshots"))
                for control in ("snapshot-inspect-open", "collection-save", "collection-export-bundle", "collection-import-bundle"):
                    assert c.el(control).get_property("hidden")
                c.api("/api/v1/snapshots/inspect", "POST", {"path": str(archive)}, expected=404)
                c.api("/api/v1/collections/import/preview", "POST", {}, expected=404)
                query = "different" if mode == "serve" else "idempotency"
                c.search(query)
                c.browser.find_element(By.CSS_SELECTOR, ".result-preview").click()
                c.wait(lambda: c.el("add-evidence").is_enabled())
                if not c.el("whole-preview").is_selected():
                    c.el("whole-preview").click()
                c.el("add-evidence").click()
                c.el("close-preview").click()
                assert len(c.collection()) == 1
                pinned = c.collection()
                path = c.downloads / "findrail-search-preset.json"
                path.unlink(missing_ok=True)
                c.fill("preset-title", "Read only preset")
                c.el("preset-save").click()
                c.wait(lambda: path.is_file())
                searches = c.browser.execute_script("return window.__effects.searches.length")
                c.el("preset-file").send_keys(str(path))
                c.wait(lambda: not c.el("preset-preview").get_property("hidden"))
                c.el("preset-apply").click()
                assert c.browser.execute_script("return window.__effects.searches.length") == searches
                assert c.collection() == pinned
                c.privacy()
        cases.run("serve_demo_no_writable_routes_temporary_evidence_and_local_presets", read_only_modes)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path)
    parser.add_argument("--candidate-sha", required=True)
    parser.add_argument("--candidate-version", required=True)
    parser.add_argument("--report", type=pathlib.Path)
    args = parser.parse_args()
    report = {"status": "running", "candidate_sha": args.candidate_sha,
              "candidate_version": args.candidate_version, "cases": []}
    started = time.monotonic()
    try:
        binary = args.binary.resolve(strict=True)
        CaseReport(report).run("candidate_identity", lambda: validate_candidate(binary, args.candidate_sha, args.candidate_version))
        run(binary, report)
        report["status"] = "passed"
        return 0
    except Exception:
        report["status"] = "failed"
        raise
    finally:
        report["duration_seconds"] = round(time.monotonic() - started, 3)
        if args.report:
            args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(json.dumps(report, ensure_ascii=False))


if __name__ == "__main__":
    sys.exit(main())
