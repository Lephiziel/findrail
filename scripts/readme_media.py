#!/usr/bin/env python3
"""Capture the real published alpha.3 demo; no UI compositing or private files."""
import argparse
import base64
import json
import pathlib
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait

urllib.request.install_opener(urllib.request.build_opener(urllib.request.ProxyHandler({})))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    version = subprocess.check_output([str(binary), "version"], text=True).strip()
    if version != "findrail 0.1.0-alpha.3":
        raise SystemExit("This capture is pinned to published alpha.3.")
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    url = f"http://127.0.0.1:{port}"
    with tempfile.TemporaryDirectory(prefix="findrail-readme-media-") as temporary:
        log = pathlib.Path(temporary) / "demo.log"
        browser = None
        with log.open("w", encoding="utf-8") as stream:
            server = subprocess.Popen(
                [str(binary), "demo", "--no-open", "--addr", f"127.0.0.1:{port}"],
                stdout=stream, stderr=stream)
            try:
                deadline = time.monotonic() + 20
                while time.monotonic() < deadline:
                    if server.poll() is not None:
                        raise RuntimeError("Demo exited: " + log.read_text())
                    try:
                        with urllib.request.urlopen(url + "/healthz", timeout=1) as response:
                            if response.status == 200:
                                break
                    except (OSError, urllib.error.URLError):
                        time.sleep(.1)
                else:
                    raise RuntimeError("Demo listener was not ready.")
                options = webdriver.ChromeOptions()
                options.binary_location = shutil.which("google-chrome") or shutil.which("chromium") or ""
                options.add_argument("--headless=new")
                options.add_argument("--no-sandbox")
                options.add_argument("--disable-dev-shm-usage")
                options.add_argument("--no-proxy-server")
                options.add_argument("--window-size=1320,1180")
                browser = webdriver.Chrome(service=Service(), options=options)
                browser.set_page_load_timeout(20)
                wait = WebDriverWait(browser, 20)
                browser.get(url + "/")
                wait.until(lambda d: d.find_element(By.ID, "query").is_enabled())
                browser.find_element(By.ID, "query").send_keys("idempotency")
                browser.find_element(By.ID, "submit").click()
                wait.until(lambda d: "3 matching documents" in d.find_element(By.ID, "status").text
                           and d.find_element(By.ID, "submit").is_enabled())
                cards = browser.find_elements(By.CSS_SELECTOR, ".result")
                assert len(cards) == 3
                assert any("PDF page 2" in card.text for card in cards)
                size = browser.execute_cdp_cmd("Page.getLayoutMetrics", {})["cssContentSize"]
                captured = browser.execute_cdp_cmd("Page.captureScreenshot", {
                    "format": "png", "captureBeyondViewport": True,
                    "clip": {"x": 0, "y": 0, "width": size["width"],
                             "height": size["height"], "scale": 1}})
                (output / "search-alpha3.png").write_bytes(base64.b64decode(captured["data"]))
                pdf = next(card for card in cards if "webhook-runbook.pdf" in card.text)
                pdf.find_element(By.XPATH, ".//button[.='Preview']").click()
                wait.until(lambda d: "Page 2 / 2" in d.find_element(By.ID, "page-label").text)
                preview = browser.find_element(By.ID, "preview")
                assert "Idempotency" in browser.find_element(By.ID, "preview-body").text
                assert browser.find_element(By.ID, "preview-location").text.endswith("#page=2")
                (output / "preview-alpha3.png").write_bytes(preview.screenshot_as_png)
                (output / "overview-alpha3.png").write_bytes(browser.get_screenshot_as_png())
                (output / "capture.json").write_text(json.dumps({
                    "version": version, "source_revision": "d5dc1bec50efcb3c26d09e9b24b14cf3c0b2f4cb",
                    "schema": 6, "query": "idempotency", "matching_documents": 3,
                    "pdf_page": 2, "pdf_page_count": 2, "synthetic_documents": True,
                    "capture": "Direct Chromium screenshots of the published binary; no compositing."
                }, indent=2) + "\n", encoding="utf-8")
                print("Captured published alpha.3 search (3 documents) and PDF preview (page 2 / 2).")
            finally:
                if browser is not None:
                    browser.quit()
                server.terminate()
                try:
                    server.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait()


if __name__ == "__main__":
    main()
