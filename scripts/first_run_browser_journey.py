#!/usr/bin/env python3
"""Keyboard first-run journey on a supplied, clean extracted alpha.5 candidate.

Use FINDRAIL_CHROME/FINDRAIL_CHROMEDRIVER for a preinstalled matched browser;
the existing passage-window smoke runs this check under its bounded runner.
Build metadata and doctor bind the result to the supplied executable. Reflow
profiles include the effective viewport of 200% zoom, not a screen-reader audit.
"""
import argparse
import json
import os
import pathlib
import shutil
import signal
import socket
import subprocess
import tempfile
import time
import urllib.request

from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.common.keys import Keys
from selenium.webdriver.support import expected_conditions as EC
from selenium.webdriver.support.ui import WebDriverWait

from candidate_record import digest
from packaged_journey import command, stop_process_tree, validate_candidate
from smoke import docx_fixture, pdf_fixture


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def run(binary):
    binary = binary.resolve(strict=True)
    metadata = json.loads((binary.parent / "BUILD-INFO.json").read_text(encoding="utf-8"))
    validate_candidate(binary, metadata.get("revision", ""), metadata.get("version", ""))
    chrome = os.environ.get("FINDRAIL_CHROME") or shutil.which("chromium") or shutil.which("google-chrome")
    chromedriver = os.environ.get("FINDRAIL_CHROMEDRIVER") or shutil.which("chromedriver")
    if not chrome or not chromedriver:
        raise RuntimeError("Supply FINDRAIL_CHROME and FINDRAIL_CHROMEDRIVER; this journey never downloads a browser")
    with tempfile.TemporaryDirectory(prefix="findrail-first-run-") as temp:
        root = pathlib.Path(temp)
        notes, data = root / "notes", root / "index"
        notes.mkdir()
        (notes / "late.docx").write_bytes(docx_fixture("padding " * 12000 + "lateword remembered quote"))
        (notes / "runbook.pdf").write_bytes(pdf_fixture(["synthetic first page", "secondpage page2journey remembered pdf quote"]))
        address = f"127.0.0.1:{free_port()}"
        url = f"http://{address}"
        options = webdriver.ChromeOptions()
        options.binary_location = chrome
        for flag in ("--headless=new", "--no-sandbox", "--disable-dev-shm-usage", "--no-proxy-server"):
            options.add_argument(flag)
        driver = server = None

        def launch():
            return subprocess.Popen([str(binary), "desktop", "--data-dir", str(data), "--no-open", "--addr", address],
                                    cwd=root, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                    start_new_session=os.name == "posix")

        def ready():
            deadline = time.monotonic() + 20
            while time.monotonic() < deadline:
                if server.poll() is not None:
                    raise RuntimeError("candidate exited while starting")
                try:
                    urllib.request.urlopen(url + "/healthz", timeout=1).close()
                    return
                except OSError:
                    time.sleep(.1)
            raise TimeoutError("candidate did not become ready")

        def tab_to(by, value):
            wait.until(EC.element_to_be_clickable((by, value)))
            for _ in range(100):
                target = driver.find_element(by, value)
                active = driver.switch_to.active_element
                if active == target:
                    return active
                active.send_keys(Keys.TAB)
            raise AssertionError("Tab order did not reach the requested control")

        def activate(by, value):
            tab_to(by, value).send_keys(Keys.ENTER)

        def narrow_controls(controls):
            # Last profile has the effective CSS viewport and DPR of a 320px
            # window at 200% zoom; it checks reflow, not assistive technology.
            try:
                for width, scale in ((320, 1), (375, 1), (160, 2)):
                    driver.execute_cdp_cmd("Emulation.setDeviceMetricsOverride", {
                        "width": width, "height": 900, "deviceScaleFactor": scale, "mobile": False})
                    assert driver.execute_script("return innerWidth") == width
                    assert driver.execute_script("return document.documentElement.scrollWidth <= innerWidth+1"), "first-run layout overflows"
                    for control in controls:
                        tab_to(By.ID, control)
            finally:
                driver.execute_cdp_cmd("Emulation.clearDeviceMetricsOverride", {})

        def search(query):
            field = tab_to(By.ID, "query")
            field.clear()
            field.send_keys(query, Keys.ENTER)
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".result")) == 1)
            activate(By.CSS_SELECTOR, ".result-preview")

        def collect_and_close():
            checkbox = tab_to(By.ID, "whole-preview")
            if not checkbox.is_selected():
                checkbox.send_keys(Keys.SPACE)
            activate(By.ID, "add-evidence")
            driver.switch_to.active_element.send_keys(Keys.ESCAPE)
            wait.until(lambda d: not d.find_element(By.ID, "preview").get_property("open"))
            wait.until(lambda d: "result-preview" in (d.switch_to.active_element.get_attribute("class") or ""))

        def save():
            assert driver.find_element(By.ID, "collection-save").text == "Save changes"
            assert "unsaved changes" in driver.find_element(By.ID, "collection-dirty").text.lower()
            activate(By.ID, "collection-save")
            wait.until(lambda d: "collection saved" in d.find_element(By.ID, "collection-status").text.lower()
                       and not d.find_element(By.ID, "collection-save").get_property("disabled"))
            assert driver.find_element(By.ID, "collection-dirty").text == ""
            assert driver.find_element(By.ID, "collection-save").text == "Save collection"

        try:
            server = launch()
            ready()
            doctor = json.loads(command([str(binary), "doctor", "--data-dir", str(data), "--json"], timeout=20).stdout)
            assert doctor.get("revision") == metadata["revision"] and doctor.get("provenance") == "clean"
            assert doctor.get("index_schema") == doctor.get("current_write_schema") == 8
            driver = webdriver.Chrome(service=Service(chromedriver), options=options)
            driver.set_page_load_timeout(20)
            driver.set_script_timeout(20)
            browser_version = driver.capabilities.get("browserVersion", "")
            driver_version = driver.capabilities.get("chrome", {}).get("chromedriverVersion", "").split(" ")[0]
            assert browser_version and browser_version == driver_version, "browser and driver must match"
            wait = WebDriverWait(driver, 20)
            driver.get(url)
            wait.until(EC.visibility_of_element_located((By.ID, "empty")))
            assert "read-only" in driver.find_element(By.ID, "empty").text.lower()
            narrow_controls(["add-folder-open", "query"])
            activate(By.ID, "add-folder-open")
            tab_to(By.ID, "folder-path").send_keys(str(notes), Keys.ENTER)
            wait.until(lambda d: "succeeded" in d.find_element(By.ID, "management-status").text.lower())
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".source-card")) == 1)

            driver.execute_script("""window.__realFetch=window.fetch.bind(window);window.__failSearch=true;
              window.fetch=(input,options)=>{if(window.__failSearch&&new URL(String(input),location.href).pathname.includes('/search')){
                window.__failSearch=false;return Promise.resolve(new Response('{"error":"synthetic retry error"}',{status:503,headers:{'Content-Type':'application/json'}}))}
                return window.__realFetch(input,options)}""")
            query = tab_to(By.ID, "query")
            query.send_keys("lateword", Keys.ENTER)
            wait.until(lambda d: "synthetic retry error" in d.find_element(By.ID, "status").text.lower())
            assert driver.switch_to.active_element.get_attribute("id") == "query"
            query.send_keys(Keys.ENTER)
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".result")) == 1)
            activate(By.CSS_SELECTOR, ".result-preview")
            wait.until(lambda d: "lateword remembered quote" in d.find_element(By.ID, "preview-body").text)
            for control, label in (("open-original", "Open original"), ("open-folder", "Open containing folder")):
                button = driver.find_element(By.ID, control)
                assert button.get_attribute("textContent") == label and not button.is_displayed()
            narrow_controls(["whole-preview", "add-evidence", "close-preview"])
            collect_and_close()
            activate(By.ID, "collection-open")
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".collection-row")) == 1)
            tab_to(By.CSS_SELECTOR, ".collection-row textarea").send_keys("saved first-run note")
            title = tab_to(By.ID, "collection-title")
            title.clear()
            title.send_keys("First run")
            save()
            activate(By.ID, "collection-close")

            search("page2journey")
            wait.until(lambda d: "Page 2 / 2" in d.find_element(By.ID, "page-label").text)
            assert "secondpage" in driver.find_element(By.ID, "preview-body").text
            collect_and_close()
            activate(By.ID, "collection-open")
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".collection-row")) == 2)
            tab_to(By.CSS_SELECTOR, ".collection-row:nth-child(2) textarea").send_keys("saved PDF note")
            activate(By.XPATH, "//section[contains(@class,'collection-row')][2]//button[normalize-space()='Move up']")
            save()
            narrow_controls(["collection-save", "collection-close"])
            activate(By.ID, "collection-close")
            activate(By.XPATH, "//button[normalize-space()='Quit Findrail']")
            driver.switch_to.alert.accept()
            server.wait(timeout=12)
            assert server.returncode == 0, "Quit did not stop the desktop process cleanly"
            stop_process_tree(server)
            server = launch()
            ready()
            driver.refresh()
            activate(By.ID, "collection-open")
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".collection-row")) == 2)
            assert driver.find_element(By.ID, "collection-title").get_attribute("value") == "First run"
            assert [row.text for row in driver.find_elements(By.CSS_SELECTOR, ".collection-row h3")] == ["runbook.pdf", "late.docx"]
            assert [row.get_attribute("value") for row in driver.find_elements(By.CSS_SELECTOR, ".collection-row textarea")] == ["saved PDF note", "saved first-run note"]
            result = {"case": "first_run_keyboard_save_quit_restore", "status": "passed",
                      "candidate_sha": metadata["revision"], "candidate_version": metadata["version"],
                      "schema": 8, "source_clean": True, "binary_sha256": digest(binary),
                      "chrome": browser_version, "chromedriver": driver_version,
                      "keyboard_only_actions": True, "notes_and_order_restored": True,
                      "narrow_css_widths": [320, 375, 160], "zoom_equivalent_scale": 2}
        finally:
            try:
                if server is not None:
                    stop_process_tree(server)
            finally:
                if driver is not None:
                    driver.quit()
        return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path)
    args = parser.parse_args()
    previous = signal.getsignal(signal.SIGTERM)

    def interrupted(_signum, _frame):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, interrupted)
    try:
        print(json.dumps(run(args.binary), sort_keys=True))
    finally:
        signal.signal(signal.SIGTERM, previous)


if __name__ == "__main__":
    main()
