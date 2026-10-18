"""Cross-feature continuation in the packaged journey's existing browser."""
import json
import shutil
import time
import urllib.request

from selenium.common.exceptions import StaleElementReferenceException
from selenium.webdriver.common.by import By
from selenium.webdriver.common.keys import Keys
from selenium.webdriver.support.ui import Select, WebDriverWait


def run(browser, base, source, url, restart, recorder, paging):
    wait = WebDriverWait(browser, 25)
    element = lambda name: browser.find_element(By.ID, name)

    def source_option_present(source_id):
        return browser.execute_script(
            "return [...document.getElementById('source').options].some(option=>option.value===arguments[0])",
            source_id)
    archive = base / "integrated snapshot – 雪.zip"
    bundle = base / "findrail-evidence-bundle.json"
    preset = base / "findrail-search-preset.json"
    destination_index, destination_root = base / "second index – 雪", base / "second folder – 雪"
    state = {}

    def inventory():
        with urllib.request.urlopen(url + "/api/v1/sources", timeout=3) as response:
            return json.load(response)["sources"]

    def search(query, kind="all", mode="literal"):
        element("query").clear(); element("query").send_keys(query)
        Select(element("format")).select_by_value(kind); Select(element("mode")).select_by_value(mode)
        element("submit").click()
        wait.until(lambda _: element("submit").is_enabled() and "1 matching" in element("status").text)

    def preview(query):
        def click(_):
            for card in browser.find_elements(By.CSS_SELECTOR, "#results .result"):
                button = card.find_element(By.CSS_SELECTOR, ".result-preview")
                if button.is_enabled(): button.click(); return True
            return False
        WebDriverWait(browser, 25, ignored_exceptions=(StaleElementReferenceException,)).until(click)
        wait.until(lambda _: query in element("preview-body").text)

    def choose_quote(wanted):
        actual = browser.execute_script("""
            const body=document.getElementById('preview-body'), wanted=arguments[0], nodes=[];
            const walk=document.createTreeWalker(body,NodeFilter.SHOW_TEXT);let n;
            while(n=walk.nextNode())nodes.push(n);
            const text=nodes.map(n=>n.textContent).join(''),start=text.indexOf(wanted);
            if(start<0)return null;let a=start,b=start+wanted.length,left,right;
            for(const n of nodes){if(!left&&a<=n.length)left=[n,a];else if(!left)a-=n.length;
                if(!right&&b<=n.length)right=[n,b];else if(!right)b-=n.length}
            const range=document.createRange();range.setStart(...left);range.setEnd(...right);
            getSelection().removeAllRanges();getSelection().addRange(range);return getSelection().toString();
        """, wanted)
        assert actual == wanted
        element("add-evidence").click(); element("close-preview").click()

    def source_action(source_id, title):
        def click(_):
            button = browser.find_element(By.CSS_SELECTOR, f'.source-card[data-id="{source_id}"] button[title^="{title}"]')
            if not button.is_enabled(): return False
            button.click(); return True
        WebDriverWait(browser, 25, ignored_exceptions=(StaleElementReferenceException,)).until(click)

    def settle():
        previous, since = None, time.monotonic()
        def stable(_):
            nonlocal previous, since
            current = [(s["id"], s["documents"], s.get("last_indexed_at")) for s in inventory()]
            if current != previous: previous, since = current, time.monotonic()
            return current and time.monotonic() - since >= 2
        wait.until(stable)

    def diagnostics_and_late_quotes():
        element("diagnostics-open").click(); wait.until(lambda _: element("diagnostics-download").is_enabled())
        element("diagnostics-download").click(); downloaded = base / "findrail-diagnostics.json"
        wait.until(lambda _: downloaded.is_file())
        assert json.loads(downloaded.read_text(encoding="utf-8"))["index_schema"] == 8
        element("diagnostics-close").send_keys(Keys.ENTER)
        assert browser.switch_to.active_element.get_attribute("id") == "diagnostics-open"
        element("collection-open").click(); element("collection-new").click(); element("collection-close").click()
        quotes = []
        for query, wanted in (("needletextlate", "needletextlate exact text quote"),
                              ("needledocxlate", "needledocxlate exact Word quote")):
            search(query); preview(wanted)
            assert "indexed characters 80000" in element("preview-notice").text
            element("previous-text").click()
            wait.until(lambda _: element("text-range").text.startswith("14464–80000"))
            element("next-text").click(); wait.until(lambda _: wanted in element("preview-body").text)
            choose_quote(wanted); quotes.append(wanted)
        search("page2journey", "pdf"); preview("secondpage")
        assert element("page-label").text == "Page 2 / 2"
        choose_quote("secondpage page2journey unique beta"); quotes.append("secondpage page2journey unique beta")
        search("runbook", "pdf", "advanced"); browser.find_element(By.CSS_SELECTOR, ".result-preview").click()
        wait.until(lambda _: element("page-label").text == "Title match · page not determined")
        element("close-preview").click(); state["quotes"] = quotes
    recorder.run("schema8_diagnostics_late_md_docx_manual_windows_pdf_title_fallback", diagnostics_and_late_quotes)

    def bundle_export_and_paging():
        element("collection-open").click()
        wait.until(lambda _: len(browser.find_elements(By.CSS_SELECTOR, ".collection-row")) == 3)
        element("collection-title").clear(); element("collection-title").send_keys("Integrated evidence – 雪")
        element("collection-save").click(); wait.until(lambda _: "Collection saved" in element("collection-status").text)
        for index, area in enumerate(browser.find_elements(By.CSS_SELECTOR, ".collection-row textarea")):
            area.send_keys(f"Integrated note {index + 1} – 雪")
        browser.find_elements(By.CSS_SELECTOR, ".collection-row")[1].find_element(By.XPATH, ".//button[.='Move up']").click()
        element("collection-export-bundle").click()
        wait.until(lambda _: "Save collection edits" in element("collection-status").text)
        assert not bundle.exists(), "dirty collection exported a stale revision"
        element("collection-save").click(); wait.until(lambda _: "Collection saved" in element("collection-status").text)
        element("collection-export-bundle").click(); wait.until(lambda _: bundle.is_file())
        state["bundle"] = json.loads(bundle.read_text(encoding="utf-8"))
        assert [e["text"] for e in state["bundle"]["entries"]] == [state["quotes"][1], state["quotes"][0], state["quotes"][2]]
        state["pinned"] = browser.execute_script("return window.localEvidenceCollection.entries")
        element("collection-close").click()
        Select(element("mode")).select_by_value("literal"); Select(element("format")).select_by_value("all")
        paging()
        assert browser.execute_script("return window.localEvidenceCollection.entries") == state["pinned"]
    recorder.run("saved_bundle_notes_order_dirty_guard_and_cursor_navigation", bundle_export_and_paging)

    def preset_and_source_export():
        # The browser journey has just committed the larger cursor corpus from
        # another process. Wait for the watcher and the management panel to
        # converge before opening a source-bound operation; otherwise the UI's
        # deliberate stale-source guard correctly disables the export.
        settle()
        state["source_a"] = inventory()[0]["id"]
        source = next(row for row in inventory() if row["id"] == state["source_a"])
        wait.until(lambda _: browser.execute_script("""
            const card=document.querySelector('.source-card[data-id="'+CSS.escape(arguments[0])+'"]');
            const expected='Last successful update '+new Date(arguments[1]).toLocaleString();
            return !!card&&card.textContent.includes(expected);
        """, source["id"], source["last_indexed_at"]))
        search("needletextlate"); Select(element("source")).select_by_value(state["source_a"])
        element("preset-title").send_keys("Integrated preset – 雪")
        element("preset-save").click(); wait.until(lambda _: preset.is_file())
        source_action(state["source_a"], "Export indexed"); wait.until(lambda _: element("snapshot-submit").is_enabled())
        element("snapshot-path").send_keys(str(archive)); element("snapshot-submit").click()
        wait.until(lambda _: "Snapshot exported and verified" in element("snapshot-summary").text)
        assert archive.is_file() and "fingerprint" in element("snapshot-summary").text
        assert browser.execute_script("return window.localEvidenceCollection.entries") == state["pinned"]
        element("snapshot-close").send_keys(Keys.ENTER)
        wait.until(lambda _: browser.switch_to.active_element.text == "Export snapshot")
    recorder.run("explicit_preset_file_and_host_path_source_export_jobs", preset_and_source_export)

    def destination_bundle_import():
        shutil.copytree(source, destination_root); restart(destination_index)
        wait.until(lambda _: element("snapshot-inspect-open").is_displayed()); assert inventory() == []
        element("add-folder-open").click(); element("folder-path").send_keys(str(destination_root)); element("folder-form").submit()
        wait.until(lambda _: len(inventory()) == 1 and inventory()[0]["documents"] == 265)
        settle(); state["source_b"] = inventory()[0]["id"]; assert state["source_a"] != state["source_b"]
        element("collection-open").click(); element("collection-import-bundle").click()
        wait.until(lambda _: element("bundle-file").is_enabled()); element("bundle-file").send_keys(str(bundle))
        wait.until(lambda _: len(browser.find_elements(By.CSS_SELECTOR, "#bundle-mappings select")) == 1)
        mapping = browser.find_element(By.CSS_SELECTOR, "#bundle-mappings select")
        assert mapping.get_attribute("value") == "", "bundle implicitly remapped its source"
        Select(mapping).select_by_value(state["source_b"]); element("bundle-preview").send_keys(Keys.ENTER)
        wait.until(lambda _: "3 matches; 0 conflicts" in element("bundle-status").text)
        assert not element("bundle-commit").is_enabled()
        element("bundle-title").clear(); element("bundle-title").send_keys("Restored integrated evidence")
        element("bundle-confirm").send_keys(Keys.SPACE); element("bundle-commit").send_keys(Keys.ENTER)
        wait.until(lambda _: element("collection-title").get_attribute("value") == "Restored integrated evidence")
        assert browser.switch_to.active_element.get_attribute("id") == "collection-title"
        bundle.unlink(); element("collection-export-bundle").click(); wait.until(lambda _: bundle.is_file())
        restored = json.loads(bundle.read_text(encoding="utf-8"))
        assert restored["entries"] == state["bundle"]["entries"], "text/note/order/offset changed across indexes"
        element("collection-close").click()
    recorder.run("second_index_explicit_source_mapping_atomic_bundle_import", destination_bundle_import)

    def preset_remap_apply():
        browser.execute_script("""
            window.__cycleEffects={searches:[],writes:[],origins:[]};const fetch=window.fetch.bind(window);
            window.fetch=(input,options)=>{const u=new URL(String(input),location.href);
                if(u.pathname.startsWith('/api/v1/search'))window.__cycleEffects.searches.push(u.pathname);
                if(u.origin!==location.origin)window.__cycleEffects.origins.push(u.origin);return fetch(input,options)};
            const set=Storage.prototype.setItem;Storage.prototype.setItem=function(...a){window.__cycleEffects.writes.push('storage');return set.apply(this,a)};
            const open=indexedDB.open.bind(indexedDB);indexedDB.open=(...a)=>{window.__cycleEffects.writes.push('indexedDB');return open(...a)};
            for(const name of ['pushState','replaceState']){const f=history[name].bind(history);history[name]=(...a)=>{window.__cycleEffects.writes.push(name);return f(...a)}};
        """)
        element("preset-file").send_keys(str(preset)); wait.until(lambda _: not element("preset-preview").get_property("hidden"))
        assert not element("preset-apply").is_enabled()
        Select(element("preset-source-map")).select_by_value(state["source_b"]); element("preset-apply").send_keys(Keys.ENTER)
        assert browser.switch_to.active_element.get_attribute("id") == "query"
        assert browser.execute_script("return window.__cycleEffects.searches") == []
        assert browser.execute_script("return window.__cycleEffects.writes") == []
        element("submit").send_keys(Keys.ENTER)
        wait.until(lambda _: "1 matching" in element("status").text and element("submit").is_enabled())
        assert len(browser.execute_script("return window.__cycleEffects.searches")) == 1
    recorder.run("preset_preview_explicit_remap_apply_without_search_then_search", preset_remap_apply)

    def frozen_import_restart_forget():
        def inspect():
            element("snapshot-inspect-open").click(); wait.until(lambda _: element("snapshot-submit").is_enabled())
            element("snapshot-path").send_keys(str(archive)); element("snapshot-submit").click()
            wait.until(lambda _: element("snapshot-submit").text == "Import frozen source" and element("snapshot-submit").is_enabled())
            assert "265 documents" in element("snapshot-summary").text
        inspect(); element("snapshot-name").send_keys("Integrated frozen source"); element("snapshot-submit").click()
        wait.until(lambda _: "imported" in element("snapshot-summary").text); element("snapshot-close").click()
        wait.until(lambda _: len(inventory()) == 2)
        frozen = next(s for s in inventory() if s["kind"] == "archive")["id"]
        wait.until(lambda _: source_option_present(frozen))
        Select(element("source")).select_by_value(frozen)
        search("needledocxlate", "docx"); preview("needledocxlate")
        assert "Frozen imported snapshot" in element("preview-notice").text and element("open-original").get_property("hidden")
        element("close-preview").click()
        inspect(); element("snapshot-name").send_keys("Must keep existing name"); element("snapshot-submit").click()
        wait.until(lambda _: "already imported as" in element("snapshot-summary").text); element("snapshot-close").click()
        assert len(inventory()) == 2 and next(s for s in inventory() if s["id"] == frozen)["name"] == "Integrated frozen source"
        assert browser.execute_script("return window.__cycleEffects.origins") == []
        restart(destination_index); wait.until(lambda _: len(Select(element("collection-picker")).options) == 2)
        element("collection-open").click()
        picker = Select(element("collection-picker")); picker.select_by_value(picker.options[1].get_attribute("value"))
        wait.until(lambda _: len(browser.find_elements(By.CSS_SELECTOR, ".collection-row textarea")) == 3)
        assert [e.get_attribute("value") for e in browser.find_elements(By.CSS_SELECTOR, ".collection-row textarea")] == [e["note"] for e in state["bundle"]["entries"]]
        element("collection-close").click()
        wait.until(lambda _: source_option_present(frozen))
        Select(element("source")).select_by_value(frozen); search("needledocxlate", "docx")
        browser.execute_script("""
            window.__cycleFetch=window.fetch.bind(window);window.__cycleHold=true;
            window.fetch=async(input,options)=>{const response=await window.__cycleFetch(input,options);
                if(window.__cycleHold&&String(input).includes('/api/v1/documents/')){
                    window.__cycleHold=false;return new Promise(resolve=>{window.__releaseCyclePreview=()=>resolve(response)})}
                return response};
        """)
        browser.find_element(By.CSS_SELECTOR, ".result-preview").click()
        wait.until(lambda _: browser.execute_script("return typeof window.__releaseCyclePreview==='function'"))
        element("close-preview").click()
        source_action(frozen, "Removes only"); element("confirm-remove").send_keys(Keys.ENTER)
        wait.until(lambda _: len(inventory()) == 1)
        wait.until(lambda _: not source_option_present(frozen))
        inspect(); element("snapshot-name").send_keys("Re-added frozen source"); element("snapshot-submit").click()
        wait.until(lambda _: "imported" in element("snapshot-summary").text); element("snapshot-close").click()
        wait.until(lambda _: len(inventory()) == 2)
        assert next(s for s in inventory() if s["kind"] == "archive")["id"] == frozen
        browser.execute_script("window.__releaseCyclePreview();window.fetch=window.__cycleFetch")
        time.sleep(.2)
        assert not element("preview").get_property("open"), "late pre-Forget response reopened a preview after re-add"
        assert element("collection-count").text == "3", "late preview changed the saved collection"
        source_action(frozen, "Removes only"); element("confirm-remove").send_keys(Keys.ENTER)
        wait.until(lambda _: len(inventory()) == 1)
    recorder.run("portable_inspect_claim_import_frozen_search_duplicate_restart_forget", frozen_import_restart_forget)
