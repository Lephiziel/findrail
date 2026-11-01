#!/usr/bin/env python3
"""Synthetic Selenium journey for diagnostics, download, retry and stale-response safety."""
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
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def main():
    binary = pathlib.Path(sys.argv[1]).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="findrail-diagnostics-") as temp:
        root = pathlib.Path(temp)
        data, downloads = root / "data", root / "downloads"
        downloads.mkdir()
        port = free_port()
        server = subprocess.Popen([str(binary), "serve", "--data-dir", str(data), "--no-sync", "--addr", f"127.0.0.1:{port}"], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
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
            options.add_experimental_option("prefs", {"download.default_directory": str(downloads), "download.prompt_for_download": False})
            browser = webdriver.Chrome(service=Service(executable_path=os.environ.get("FINDRAIL_CHROMEDRIVER") or shutil.which("chromedriver")), options=options)
            wait = WebDriverWait(browser, 12)
            browser.get(f"http://127.0.0.1:{port}/")
            button = wait.until(lambda d: d.find_element(By.ID, "diagnostics-open") if not d.find_element(By.ID, "diagnostics-open").get_property("hidden") else False)

            # Closing aborts an in-flight request; a late response cannot reopen the dialog.
            browser.execute_script("""
              const original=window.__diagOriginal=window.fetch.bind(window); window.__diagRelease=null;
              window.fetch=(url,options={})=>String(url).endsWith('/api/v1/diagnostics')
                ? new Promise(resolve=>{window.__diagRelease=()=>original(url,{...options,signal:undefined}).then(resolve)})
                : original(url,options);
            """)
            button.click()
            wait.until(lambda d: d.execute_script("return !!window.__diagRelease"))
            assert not list(downloads.iterdir()), "opening diagnostics must not download a report"
            browser.find_element(By.ID, "diagnostics-close").click()
            browser.execute_script("window.__diagRelease()")
            time.sleep(.2)
            assert not browser.find_element(By.ID, "diagnostics-dialog").get_property("open")
            assert browser.switch_to.active_element.get_attribute("id") == "diagnostics-open"

            browser.execute_script("""
              const original=window.__diagOriginal;let failed=false;
              window.fetch=(url,options)=>String(url).endsWith('/api/v1/diagnostics')&&!failed
                ? (failed=true,Promise.resolve(new Response('{}',{status:503}))) : original(url,options);
            """)
            button.click()
            wait.until(lambda d: not d.find_element(By.ID, "diagnostics-retry").get_property("hidden"))
            browser.find_element(By.ID, "diagnostics-retry").click()
            try:
                wait.until(lambda d: not d.find_element(By.ID, "diagnostics-download").get_property("disabled"))
            except Exception as error:
                raise AssertionError("diagnostics retry did not finish: " + browser.find_element(By.ID, "diagnostics-status").text) from error
            report = json.loads(browser.find_element(By.ID, "diagnostics-result").text)
            assert report["report_version"] == 1
            assert report["index_schema"] == report["current_write_schema"]
            browser.find_element(By.ID, "diagnostics-download").click()
            target = downloads / "findrail-diagnostics.json"
            wait.until(lambda _: target.exists())
            downloaded = json.loads(target.read_text(encoding="utf-8"))
            assert downloaded["index_schema"] == downloaded["current_write_schema"]
            browser.find_element(By.ID, "diagnostics-close").click()
        finally:
            if browser:
                browser.quit()
            server.terminate()
            try:
                server.wait(timeout=5)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait()


if __name__ == "__main__":
    main()
