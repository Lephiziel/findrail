#!/usr/bin/env python3
"""Real late text/DOCX citation, process restart, export and freshness journey."""
import json
import os
import pathlib
import shutil
import socket
import sqlite3
import subprocess
import sys
import tempfile
import urllib.request

from selenium import webdriver
from selenium.webdriver.common.action_chains import ActionChains
from selenium.common.exceptions import StaleElementReferenceException
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.common.keys import Keys
from selenium.webdriver.support.ui import Select, WebDriverWait

from smoke import docx_fixture, pdf_fixture
from packaged_journey import command


def main():
    binary = pathlib.Path(sys.argv[1]).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="Findrail late passages – ") as temporary:
        root = pathlib.Path(temporary)
        notes, data = root / "fictional notes", root / "private index"
        notes.mkdir()
        text_quote, word_quote = "needletextlate exact text quote", "needledocxlate exact Word quote"
        (notes / "late.md").write_text("padding " * 10000 + text_quote + " 😀e\u0301\n", encoding="utf-8")
        (notes / "late.docx").write_bytes(docx_fixture("padding " * 10000 + word_quote + " 😀e\u0301"))
        (notes / "runbook.pdf").write_bytes(pdf_fixture(["first page filler", "secondpage page2journey unique beta"]))
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        url = f"http://127.0.0.1:{port}"
        options = webdriver.ChromeOptions()
        options.binary_location = os.environ.get("FINDRAIL_CHROME") or shutil.which("chromium") or shutil.which("google-chrome") or ""
        for flag in ("--headless=new", "--no-sandbox", "--disable-dev-shm-usage"):
            options.add_argument(flag)
        options.add_experimental_option("prefs", {"download.default_directory": str(root), "download.prompt_for_download": False})
        driver = os.environ.get("FINDRAIL_CHROMEDRIVER") or shutil.which("chromedriver")
        browser = server = None

        def stop():
            nonlocal browser, server
            if browser:
                browser.quit()
                browser = None
            if server and server.poll() is None:
                server.terminate()
                try:
                    server.communicate(timeout=8)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.communicate(timeout=3)

        def launch(mode="desktop"):
            nonlocal browser, server
            args = [str(binary), mode, "--data-dir", str(data), "--addr", f"127.0.0.1:{port}"]
            if mode in ("start", "desktop"):
                args += ["--no-open", "--sync-interval", "1s"]
            server = subprocess.Popen(args, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
            def ready(_):
                if server.poll() is not None:
                    raise AssertionError("passage server exited: " + server.stderr.read())
                try:
                    with urllib.request.urlopen(url + "/api/v1/capabilities", timeout=1) as response:
                        capabilities = json.load(response)
                        return capabilities.get("passage_preview") is True and (mode == "serve" or (capabilities.get("management") is True and capabilities.get("desktop") is True))
                except OSError:
                    return False
            WebDriverWait(None, 20).until(ready)
            browser = webdriver.Chrome(service=Service(executable_path=driver) if driver else Service(), options=options)
            browser.get(url + "/")
            if mode in ("start", "desktop"):
                WebDriverWait(browser, 20).until(lambda d: not d.find_element(By.ID, "collection-save").get_attribute("hidden"))
            else:
                WebDriverWait(browser, 20).until(lambda d: d.find_element(By.ID, "management").get_property("hidden"))
                assert browser.find_element(By.ID, "collection-save").get_property("hidden"), "read-only serve exposed collection writes"

        def wait_for_quote(quote):
            WebDriverWait(browser, 20).until(lambda d: quote in d.find_element(By.ID, "preview-body").text)

        def collection_note(index):
            try:
                return browser.find_elements(By.CSS_SELECTOR, ".collection-row")[index].find_element(By.TAG_NAME, "textarea").get_attribute("value")
            except (IndexError, StaleElementReferenceException):
                return None

        def collection_note_enabled(index):
            try:
                rows = browser.find_elements(By.CSS_SELECTOR, ".collection-row")
                return len(rows) == 2 and not rows[index].find_element(By.TAG_NAME, "textarea").get_property("disabled")
            except (IndexError, StaleElementReferenceException):
                return False

        def tab_until(predicate, label, limit=40):
            for _ in range(limit):
                active = browser.switch_to.active_element
                if predicate(active):
                    return active
                ActionChains(browser).send_keys(Keys.TAB).perform()
            raise AssertionError("Tab order did not reach " + label)

        def shift_tab_until(predicate, label, limit=50):
            for _ in range(limit):
                active = browser.switch_to.active_element
                if predicate(active):
                    return active
                ActionChains(browser).key_down(Keys.SHIFT).send_keys(Keys.TAB).key_up(Keys.SHIFT).perform()
            raise AssertionError("Shift+Tab order did not reach " + label)

        def focused_result(document_id):
            try:
                active = browser.switch_to.active_element
                return active if "result-preview" in (active.get_attribute("class") or "") and active.get_attribute("data-document-id") == document_id else False
            except StaleElementReferenceException:
                return False

        def verify_refresh_focus(document_id, quote):
            old = browser.switch_to.active_element
            assert focused_result(document_id), "Preview did not return to its still-present document"
            browser.execute_script("""
                window.__refreshSearches=0;window.__refreshFetch=window.fetch;
                window.fetch=(input,options)=>{if(new URL(String(input),location.href).pathname.startsWith('/api/v1/search'))window.__refreshSearches++;
                    return window.__refreshFetch(input,options)};
            """)
            (notes / "keyboard-refresh.md").write_text("Unrelated synthetic refresh fixture.", encoding="utf-8")
            # The CI runner also executes the long package/journey matrix. Give
            # the periodic source scan room to finish under contention rather
            # than treating delayed indexing as a broken UI refresh.
            WebDriverWait(browser, 45, ignored_exceptions=(StaleElementReferenceException,)).until(
                lambda d: "4 documents" in d.find_element(By.CSS_SELECTOR, ".source-card").text)
            assert old.is_enabled(), "status refresh replaced the existing result snapshot"
            assert browser.switch_to.active_element == old, "inventory refresh moved keyboard focus"
            assert browser.execute_script("return window.__refreshSearches") == 0, "inventory polling implicitly reran Search"
            browser.execute_script("window.fetch=window.__refreshFetch")
            old.send_keys(Keys.ENTER)
            wait_for_quote(quote)
            browser.switch_to.active_element.send_keys(Keys.ESCAPE)
            WebDriverWait(browser, 20).until(lambda d: not d.find_element(By.ID, "preview").get_property("open"))
            WebDriverWait(browser, 10).until(lambda d: focused_result(document_id))

        def verify_dialog_escapes():
            diagnostics = browser.find_element(By.ID, "diagnostics-open")
            diagnostics.click()
            dialog = browser.find_element(By.ID, "diagnostics-dialog")
            WebDriverWait(browser, 20).until(lambda d: dialog.get_property("open"))
            assert dialog.get_attribute("aria-labelledby") == "diagnostics-title"
            assert dialog.get_attribute("aria-describedby") == "diagnostics-description"
            browser.switch_to.active_element.send_keys(Keys.ESCAPE)
            WebDriverWait(browser, 20).until(lambda d: not dialog.get_property("open"))
            assert browser.switch_to.active_element.get_attribute("id") == "diagnostics-open"

            configure = browser.find_element(By.CSS_SELECTOR, ".source-card button[title*='Change DOCX policy']")
            configure.click()
            dialog = browser.find_element(By.ID, "configure-source")
            assert dialog.get_attribute("aria-labelledby") == "configure-title"
            assert dialog.get_attribute("aria-describedby") == "configure-description"
            browser.switch_to.active_element.send_keys(Keys.ESCAPE)
            WebDriverWait(browser, 20).until(lambda d: not dialog.get_property("open"))

            browser.find_element(By.CSS_SELECTOR, ".source-card button[title*='Removes only']").click()
            dialog = browser.find_element(By.ID, "remove-confirm")
            assert dialog.get_attribute("aria-describedby") == "remove-description"
            browser.switch_to.active_element.send_keys(Keys.ESCAPE)
            WebDriverWait(browser, 20).until(lambda d: not dialog.get_property("open"))
            assert len(browser.find_elements(By.CSS_SELECTOR, ".source-card")) == 1, "Escape unexpectedly removed the source"

        def open_result(query, keyboard=False):
            field = browser.find_element(By.ID, "query")
            field.clear()
            field.send_keys(query)
            if keyboard:
                field.send_keys("/")
                assert field.get_attribute("value") == query + "/", "slash was intercepted while editing Search"
                field.send_keys(Keys.BACKSPACE)
                field.send_keys(Keys.ENTER)
                WebDriverWait(browser, 20).until(lambda d: not d.find_element(By.ID, "submit").get_property("disabled"))
                result = tab_until(lambda element: "result-preview" in (element.get_attribute("class") or ""), "result Preview")
                # Both documented shortcuts return from a real result control
                # without changing the query; Enter remains normal form submit.
                result.send_keys(Keys.CONTROL, "k")
                assert browser.switch_to.active_element.get_attribute("id") == "query"
                browser.switch_to.active_element.send_keys(Keys.ENTER)
                WebDriverWait(browser, 20).until(lambda d: not d.find_element(By.ID, "submit").get_property("disabled"))
                result = tab_until(lambda element: "result-preview" in (element.get_attribute("class") or ""), "result Preview")
                result.send_keys("/")
                assert browser.switch_to.active_element.get_attribute("id") == "query"
                browser.switch_to.active_element.send_keys(Keys.ENTER)
                WebDriverWait(browser, 20).until(lambda d: not d.find_element(By.ID, "submit").get_property("disabled"))
                button = tab_until(lambda element: "result-preview" in (element.get_attribute("class") or ""), "result Preview")
                document_id = button.get_attribute("data-document-id")
                ActionChains(browser).send_keys(Keys.ENTER).perform()
                wait_for_quote(query)
                return document_id
            browser.find_element(By.ID, "submit").click()
            def click_matching(d):
                try:
                    for row in d.find_elements(By.CSS_SELECTOR, ".result"):
                        if query in row.text:
                            row.find_element(By.CSS_SELECTOR, ".location button").click()
                            return True
                except StaleElementReferenceException:
                    return False
                return False
            WebDriverWait(browser, 20).until(click_matching)

        def select_quote(quote):
            browser.execute_script("""
                const body=document.getElementById('preview-body'),quote=arguments[0];
                const at=body.textContent.indexOf(quote);if(at<0)throw Error('quote absent');
                const walker=document.createTreeWalker(body,NodeFilter.SHOW_TEXT);
                let n,position=0,start=null,end=null;
                while(n=walker.nextNode()){
                  if(!start&&at>=position&&at<position+n.length)start=[n,at-position];
                  if(at+quote.length>position&&at+quote.length<=position+n.length){end=[n,at+quote.length-position];break}
                  position+=n.length;
                }
                const range=document.createRange();range.setStart(...start);range.setEnd(...end);
                const selection=getSelection();selection.removeAllRanges();selection.addRange(range);
                document.dispatchEvent(new Event('selectionchange'));
            """, quote)

        try:
            launch()
            WebDriverWait(browser, 15).until(lambda d: not d.find_element(By.ID, "empty").get_attribute("hidden"))
            browser.find_element(By.ID, "add-folder-open").click()
            browser.find_element(By.ID, "folder-path").send_keys(str(notes.resolve()))
            browser.find_element(By.ID, "folder-form").submit()
            WebDriverWait(browser, 30).until(lambda d: "succeeded" in d.find_element(By.ID, "management-status").text.lower())
            WebDriverWait(browser, 20).until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".source-card")) == 1)
            # The browser downloads the report produced by this real schema-8 index.
            browser.find_element(By.ID, "diagnostics-open").click()
            WebDriverWait(browser, 15).until(lambda d: not d.find_element(By.ID, "diagnostics-download").get_attribute("disabled"))
            diagnostic = json.loads(browser.find_element(By.ID, "diagnostics-result").text)
            assert diagnostic["index_schema"] == diagnostic["current_write_schema"] == 8, diagnostic
            browser.find_element(By.ID, "diagnostics-download").click()
            downloaded = root / "findrail-diagnostics.json"
            WebDriverWait(browser, 15).until(lambda _: downloaded.exists())
            assert json.loads(downloaded.read_text(encoding="utf-8"))["index_schema"] == 8
            browser.find_element(By.ID, "diagnostics-close").click()

            # Preserve the PDF page-2 citation and the Advanced title-only fallback.
            Select(browser.find_element(By.ID, "format")).select_by_value("pdf")
            open_result("page2journey")
            WebDriverWait(browser, 20).until(lambda d: d.find_element(By.ID, "page-label").text == "Page 2 / 2")
            browser.find_element(By.ID, "close-preview").click()
            Select(browser.find_element(By.ID, "mode")).select_by_value("advanced")
            open_result("runbook")
            WebDriverWait(browser, 20).until(lambda d: d.find_element(By.ID, "page-label").text == "Title match · page not determined")
            browser.find_element(By.ID, "close-preview").click()
            Select(browser.find_element(By.ID, "mode")).select_by_value("literal")
            Select(browser.find_element(By.ID, "format")).select_by_value("all")

            verify_dialog_escapes()
            browser.find_element(By.ID, "collection-open").click()
            browser.find_element(By.ID, "collection-new").click()
            title = browser.find_element(By.ID, "collection-title")
            title.clear()
            title.send_keys("Late windows")
            browser.find_element(By.ID, "collection-close").click()
            for count, (query, quote) in enumerate((("needletextlate", text_quote), ("needledocxlate", word_quote)), 1):
                opened_id = open_result(query, keyboard=count == 1)
                wait_for_quote(quote)
                notice = browser.find_element(By.ID, "preview-notice").text
                assert "indexed characters 80000" in notice, notice
                assert browser.find_element(By.ID, "next-text").get_attribute("disabled"), "a tail must not offer an empty next window"
                if count == 1:
                    browser.find_element(By.ID, "previous-text").click()
                    WebDriverWait(browser, 20).until(lambda d: d.find_element(By.ID, "text-range").text.startswith("14464–80000"))
                    browser.find_element(By.ID, "next-text").click()
                    wait_for_quote(quote)
                else:
                    assert "page numbers are unavailable" in notice
                select_quote(quote)
                if count == 1:
                    # Use actual Tab/Enter after creating an exact selection;
                    # keyboard focus must not discard the captured excerpt.
                    add = tab_until(lambda element: element.get_attribute("id") == "add-evidence", "Add evidence")
                    add.send_keys(Keys.ENTER)
                else:
                    browser.find_element(By.ID, "add-evidence").click()
                WebDriverWait(browser, 20).until(lambda d: d.find_element(By.ID, "collection-count").text == str(count))
                if count == 1:
                    browser.switch_to.active_element.send_keys(Keys.ESCAPE)
                    WebDriverWait(browser, 20).until(lambda d: not d.find_element(By.ID, "preview").get_property("open"))
                    WebDriverWait(browser, 5).until(lambda d: focused_result(opened_id))
                    verify_refresh_focus(opened_id, quote)
                    # From the same document after refresh, use the shortcut.
                    browser.switch_to.active_element.send_keys("/")
                    assert browser.switch_to.active_element.get_attribute("id") == "query"
                    # Native reverse tab order opens the evidence collection.
                    collection_button = shift_tab_until(lambda element: element.get_attribute("id") == "collection-open", "Evidence collection")
                    collection_button.send_keys(Keys.ENTER)
                    WebDriverWait(browser, 20).until(lambda d: d.find_element(By.ID, "evidence-collection").get_property("open"))
                    note = tab_until(lambda element: element.tag_name == "textarea" and (element.get_attribute("aria-label") or "").startswith("Personal note for"), "personal note")
                    note.send_keys("/Late")
                    browser.execute_script("window.__keyboardInventory=false;window.addEventListener('findrail-inventory',()=>window.__keyboardInventory=true,{once:true})")
                    (notes / "keyboard-note-refresh.md").write_text("Another unrelated synthetic refresh fixture.", encoding="utf-8")
                    WebDriverWait(browser, 20).until(lambda d: d.execute_script("return window.__keyboardInventory"))
                    assert browser.switch_to.active_element == note, "inventory refresh interrupted note editing"
                    browser.switch_to.active_element.send_keys(" text note")
                    assert browser.switch_to.active_element.get_attribute("value") == "/Late text note"
                    browser.switch_to.active_element.send_keys(Keys.ESCAPE)
                    WebDriverWait(browser, 20).until(lambda d: not d.find_element(By.ID, "evidence-collection").get_property("open"))
                    with sqlite3.connect((data / "findrail.db").as_uri() + "?mode=ro", uri=True) as db:
                        assert db.execute("SELECT count(*) FROM evidence_collections").fetchone()[0] == 0, "Escape silently saved the collection"
                    browser.switch_to.active_element.send_keys(Keys.ENTER)
                    WebDriverWait(browser, 20).until(lambda d: d.find_element(By.ID, "evidence-collection").get_property("open"))
                    note = tab_until(lambda element: element.tag_name == "textarea" and (element.get_attribute("aria-label") or "").startswith("Personal note for"), "restored personal note")
                    assert note.get_attribute("value") == "/Late text note", "Escape discarded the unsaved note"
                    save = tab_until(lambda element: element.get_attribute("id") == "collection-save", "Save collection", limit=50)
                    save.send_keys(Keys.ENTER)
                    WebDriverWait(browser, 20).until(lambda d: "saved" in d.find_element(By.ID, "collection-status").text.lower() and not d.find_element(By.ID, "collection-save").get_property("disabled"))
                    browser.switch_to.active_element.send_keys(Keys.ESCAPE)
                    WebDriverWait(browser, 20).until(lambda d: not d.find_element(By.ID, "evidence-collection").get_property("open"))
                else:
                    browser.find_element(By.ID, "close-preview").click()
            browser.find_element(By.ID, "collection-open").click()
            rows = browser.find_elements(By.CSS_SELECTOR, ".collection-row")
            rows[0].find_element(By.TAG_NAME, "textarea").clear()
            rows[0].find_element(By.TAG_NAME, "textarea").send_keys("/Late text note")
            WebDriverWait(browser, 20).until(lambda d: collection_note(0) == "/Late text note")
            WebDriverWait(browser, 20).until(lambda d: collection_note_enabled(1))
            rows = browser.find_elements(By.CSS_SELECTOR, ".collection-row")
            rows[1].find_element(By.TAG_NAME, "textarea").send_keys("Late Word note")
            WebDriverWait(browser, 20).until(lambda d: collection_note(1) == "Late Word note")
            browser.find_elements(By.CSS_SELECTOR, ".collection-row")[0].find_element(By.XPATH, ".//button[.='Move down']").click()
            browser.find_element(By.ID, "collection-save").click()
            WebDriverWait(browser, 20).until(lambda d: "saved" in d.find_element(By.ID, "collection-status").text.lower() and not d.find_element(By.ID, "collection-save").get_attribute("disabled"))
            collection_id = browser.find_element(By.ID, "collection-picker").get_attribute("value")
            assert collection_id, "the named collection was not persisted"
            stop()
            with sqlite3.connect((data / "findrail.db").as_uri() + "?mode=ro", uri=True) as db:
                assert db.execute("PRAGMA user_version").fetchone()[0] == 8
                saved = [(start, json.loads(raw)) for start, raw in db.execute("SELECT preview_start_char,snapshot_json FROM evidence_collection_entries WHERE collection_id=? ORDER BY ordinal", (collection_id,))]
            assert [entry["excerpt"] for _, entry in saved] == [word_quote, text_quote]
            assert [entry["note"] for _, entry in saved] == ["Late Word note", "/Late text note"], [entry["note"] for _, entry in saved]
            for start, entry in saved:
                assert start == entry["evidence"]["text_start_char"] == 80000
                assert entry["evidence"]["truncated"] is True
                assert entry["evidence"].get("page", 0) == 0
            launch()
            browser.find_element(By.ID, "collection-open").click()
            Select(browser.find_element(By.ID, "collection-picker")).select_by_value(collection_id)
            WebDriverWait(browser, 20).until(lambda d: d.find_element(By.ID, "collection-count").text == "2")
            def inspect_restored(d):
                try:
                    rows = d.find_elements(By.CSS_SELECTOR, ".collection-row")
                    for row in rows:
                        if not row.find_element(By.TAG_NAME, "pre").is_displayed():
                            row.find_element(By.TAG_NAME, "summary").click()
                    return [row.find_element(By.TAG_NAME, "pre").text for row in rows] == [word_quote, text_quote]
                except StaleElementReferenceException:
                    return False
            WebDriverWait(browser, 20).until(inspect_restored)
            # Dialog Escape returns keyboard focus to its opener.
            browser.find_element(By.ID, "collection-close").send_keys("\ue00c")
            WebDriverWait(browser, 10).until(lambda d: not d.find_element(By.ID, "evidence-collection").get_attribute("open"))
            assert browser.switch_to.active_element.get_attribute("id") == "collection-open"
            browser.find_element(By.ID, "collection-open").click()
            browser.find_element(By.ID, "collection-check").click()
            WebDriverWait(browser, 20).until(lambda d: d.find_element(By.ID, "collection-list").text.count("unchanged at last check") == 2)
            browser.execute_script("const original=URL.createObjectURL;URL.createObjectURL=function(blob){window.__passageDownload=blob.text();return original.call(this,blob)}")
            browser.find_element(By.ID, "collection-download").click()
            WebDriverWait(browser, 20).until(lambda d: d.execute_script("return !!window.__passageDownload"))
            markdown = browser.execute_async_script("const done=arguments[0];window.__passageDownload.then(done)")
            assert all(value in markdown for value in (text_quote, word_quote, "/Late text note", "Late Word note", (notes / "late.md").as_uri(), (notes / "late.docx").as_uri()))
            (notes / "late.md").write_text("Replacement short text with a different snapshot.", encoding="utf-8")
            old = saved[1][1]["evidence"]
            def changed(_):
                with urllib.request.urlopen(url + "/api/v1/documents/" + old["id"], timeout=2) as response:
                    return json.load(response)["content_hash"] != old["content_hash"]
            WebDriverWait(browser, 20).until(changed)
            browser.find_element(By.ID, "collection-check").click()
            WebDriverWait(browser, 20).until(lambda d: "changed/current version differs" in d.find_element(By.ID, "collection-list").text)
            WebDriverWait(browser, 20).until(inspect_restored)
            stop()
            launch("serve")
            opened_id = open_result("Replacement", keyboard=True)
            browser.switch_to.active_element.send_keys(Keys.ESCAPE)
            WebDriverWait(browser, 20).until(lambda d: not d.find_element(By.ID, "preview").get_property("open"))
            WebDriverWait(browser, 5).until(lambda d: focused_result(opened_id))
            browser.switch_to.active_element.send_keys("/")
            field = browser.switch_to.active_element
            assert field.get_attribute("id") == "query"
            field.clear()
            field.send_keys("absentkeyboardtoken", Keys.ENTER)
            WebDriverWait(browser, 20).until(lambda d: "0 matching documents" in d.find_element(By.ID, "status").text)
            assert browser.switch_to.active_element.get_attribute("id") == "query"
            assert not browser.find_elements(By.CSS_SELECTOR, ".result-preview")
            stop()
            launch("desktop")
            # Forget the only source, verify collection evidence is purged, then
            # exercise the protected desktop Quit control and owner cleanup.
            browser.find_element(By.XPATH, ".//article[contains(@class,'source-card')]//button[.='Remove from index']").click()
            browser.find_element(By.ID, "confirm-remove").click()
            WebDriverWait(browser, 20).until(lambda d: not d.find_elements(By.CSS_SELECTOR, ".source-card"))
            browser.find_element(By.ID, "collection-open").click()
            WebDriverWait(browser, 15).until(lambda d: d.execute_script("return window.localEvidenceCollection.entries.length") == 0)
            assert "purged" in browser.find_element(By.ID, "collection-status").text.lower()
            browser.find_element(By.ID, "collection-close").click()
            browser.execute_script("window.confirm=()=>true")
            browser.find_element(By.XPATH, "//button[.='Quit Findrail']").click()
            server.communicate(timeout=12)
            assert server.returncode == 0
            assert not (data / ".desktop-instance.json").exists()
            print("Extracted desktop browser journey passed: fresh start, UI folder, schema-8 diagnostics download, PDF page 2/Advanced fallback, late text/DOCX exact quote, named collection note/order/export/restart, keyboard focus across refresh, read-only serve, changed evidence, Forget purge and protected Quit/drain.")
        finally:
            stop()

    # Reuse the supplied extracted candidate and pinned browser. Failure here
    # fails this existing packaged-browser boundary; no second app is built.
    print(command([sys.executable, str(pathlib.Path(__file__).with_name("first_run_browser_journey.py")),
                   str(binary)], timeout=240).stdout.strip())


if __name__ == "__main__":
    main()
