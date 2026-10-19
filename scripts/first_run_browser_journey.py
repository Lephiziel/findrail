#!/usr/bin/env python3
"""Real-browser empty-index, keyboard, passage, collection, and restart case."""
import argparse
import pathlib
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.request

from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.support import expected_conditions as EC
from selenium.webdriver.support.ui import WebDriverWait

from smoke import docx_fixture, pdf_fixture


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path)
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="findrail-first-run-") as temp:
        root = pathlib.Path(temp)
        notes, data = root / "notes", root / "index"
        notes.mkdir()
        (notes / "late.docx").write_bytes(docx_fixture("padding " * 12000 + "lateword remembered quote"))
        (notes / "runbook.pdf").write_bytes(pdf_fixture(["synthetic first page", "secondpage page2journey remembered pdf quote"]))
        port = free_port()
        address = f"127.0.0.1:{port}"
        url = f"http://{address}"
        options = webdriver.ChromeOptions()
        options.binary_location = shutil.which("chromium") or shutil.which("google-chrome")
        options.add_argument("--headless=new")
        options.add_argument("--no-sandbox")
        options.add_argument("--disable-dev-shm-usage")
        options.add_argument("--no-proxy-server")
        driver = None
        server = None

        def launch():
            return subprocess.Popen([str(binary), "desktop", "--data-dir", str(data), "--no-open", "--addr", address],
                                    cwd=root, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

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

        try:
            server = launch()
            ready()
            driver = webdriver.Chrome(service=Service(shutil.which("chromedriver")), options=options)
            wait = WebDriverWait(driver, 20)
            driver.get(url)
            wait.until(EC.visibility_of_element_located((By.ID, "empty")))
            assert "read-only" in driver.find_element(By.ID, "empty").text.lower(), driver.find_element(By.ID, "empty").text
            original_size = driver.get_window_size()
            driver.set_window_size(320, 900)
            driver.execute_script("document.body.style.zoom='200%'")
            assert driver.execute_script("return document.documentElement.scrollWidth <= window.innerWidth"), "320px/200% layout overflows horizontally"
            assert driver.find_element(By.ID, "add-folder-open").is_displayed(), "Add folder is not visible at 320px/200%"
            driver.execute_script("document.body.style.zoom='100%'")
            driver.set_window_size(original_size["width"], original_size["height"])
            driver.find_element(By.ID, "add-folder-open").click()
            driver.find_element(By.ID, "folder-path").send_keys(str(notes))
            driver.find_element(By.ID, "folder-form").submit()
            wait.until(lambda d: "succeeded" in d.find_element(By.ID, "management-status").text.lower())
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".source-card")) == 1)

            # A deliberate failed attempt must be retryable from the keyboard.
            driver.execute_script("""window.__realFetch=window.fetch.bind(window);window.__failSearch=true;
              window.fetch=(input,options)=>{if(window.__failSearch&&new URL(String(input),location.href).pathname.includes('/search')){
                window.__failSearch=false;return Promise.resolve(new Response('{\"error\":\"synthetic retry error\"}',{status:503,headers:{'Content-Type':'application/json'}}))}
                return window.__realFetch(input,options)}""")
            query = driver.find_element(By.ID, "query")
            query.send_keys("lateword")
            query.send_keys("\n")
            wait.until(lambda d: "synthetic retry error" in d.find_element(By.ID, "status").text.lower()
                       or "request unavailable" in d.find_element(By.ID, "status").text.lower())
            query = driver.find_element(By.ID, "query")
            query.send_keys("\n")
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".result")) == 1)
            assert "lateword" in driver.find_element(By.CSS_SELECTOR, ".result .snippet").text, repr(driver.find_element(By.CSS_SELECTOR, ".result .snippet").text)
            driver.find_element(By.CSS_SELECTOR, ".result-preview").click()
            wait.until(lambda d: "lateword remembered quote" in d.find_element(By.ID, "preview-body").text)
            driver.execute_script("document.getElementById('whole-preview').checked=true")
            driver.find_element(By.ID, "add-evidence").click()
            driver.find_element(By.ID, "close-preview").click()
            driver.find_element(By.ID, "collection-open").click()
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".collection-row")) == 1)
            driver.find_element(By.CSS_SELECTOR, ".collection-row textarea").send_keys("saved first-run note")
            driver.find_element(By.ID, "collection-title").clear()
            driver.find_element(By.ID, "collection-title").send_keys("First run")
            assert driver.find_element(By.ID, "collection-save").text == "Save changes"
            assert "unsaved changes" in driver.find_element(By.ID, "collection-status").text.lower()
            driver.find_element(By.ID, "collection-save").click()
            wait.until(lambda d: "collection saved" in d.find_element(By.ID, "collection-status").text.lower())
            driver.find_element(By.ID, "collection-close").click()

            query = driver.find_element(By.ID, "query")
            query.clear(); query.send_keys("page2journey\n")
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".result")) == 1)
            driver.find_element(By.CSS_SELECTOR, ".result-preview").click()
            wait.until(lambda d: "Page 2 / 2" in d.find_element(By.ID, "page-label").text)
            assert "secondpage" in driver.find_element(By.ID, "preview-body").text
            driver.find_element(By.ID, "close-preview").click()

            quit_button = driver.find_element(By.XPATH, "//button[normalize-space()='Quit Findrail']")
            quit_button.click()
            driver.switch_to.alert.accept()
            server.wait(timeout=12)
            assert server.returncode == 0, "Quit did not stop the desktop process cleanly"
            server = launch(); ready()
            driver.refresh()
            wait = WebDriverWait(driver, 20)
            driver.find_element(By.ID, "collection-open").click()
            wait.until(lambda d: len(d.find_elements(By.CSS_SELECTOR, ".collection-row")) == 1)
            assert driver.find_element(By.ID, "collection-title").get_attribute("value") == "First run"
            assert driver.find_element(By.CSS_SELECTOR, ".collection-row textarea").get_attribute("value") == "saved first-run note"
            assert driver.find_element(By.CSS_SELECTOR, ".collection-row h3").text == "late.docx"
            print("First-run browser journey passed: empty index, Add folder, keyboard retry, late DOCX quote, PDF page 2, saved note and collection restored after Quit/reopen.")
        finally:
            if driver:
                driver.quit()
            if server and server.poll() is None:
                server.terminate()
                server.wait(timeout=8)


if __name__ == "__main__":
    main()
