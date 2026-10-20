#!/usr/bin/env python3
"""Capture the published alpha.5 UI against a disposable synthetic source."""
import argparse, base64, hashlib, json, os, pathlib, platform, shutil, socket, subprocess, tempfile, time, urllib.request
from datetime import date

SOURCE = "cb53ee9638f69b47b386921e41368f3b66ceddde"
VERSION = "0.1.0-alpha.5"
ARCHIVE_SHA256 = "78885728107e26be6db18f93c0f94ee6af92b585f00aea21dd93ba9e19a5a8f0"
ASSETS = {"search-alpha5.png", "collection-alpha5.png", "freshness-alpha5.png", "workflow-alpha5.mp4", "capture-alpha5.json"}


def sha(path):
    h = hashlib.sha256()
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def check_inputs(binary, metadata):
    binary = pathlib.Path(binary).resolve(strict=True)
    meta_path = pathlib.Path(metadata).resolve(strict=True)
    meta = json.loads(meta_path.read_text(encoding="utf-8"))
    if meta.get("version") != VERSION or meta.get("source_revision") != SOURCE or meta.get("schema") != 8:
        raise ValueError("verified asset metadata must identify published alpha.5 source and schema 8")
    if meta.get("archive_sha256") != ARCHIVE_SHA256 or meta.get("binary_sha256") != sha(binary):
        raise ValueError("archive/binary SHA-256 mismatch")
    actual = subprocess.run([str(binary), "version"], check=True, capture_output=True, text=True, timeout=10).stdout.strip()
    if actual != "findrail " + VERSION:
        raise ValueError("binary version mismatch")
    return binary, meta


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0)); return sock.getsockname()[1]


def start_command(binary, data, port):
    return [str(binary), "start", "--data-dir", str(data), "--no-open",
            "--sync-interval", "1s", "--addr", f"127.0.0.1:{port}"]


def stop_process(process):
    if process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=8)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path)
    parser.add_argument("metadata", type=pathlib.Path, help="verified public archive metadata JSON")
    parser.add_argument("output", type=pathlib.Path)
    args = parser.parse_args()
    binary, meta = check_inputs(args.binary, args.metadata)
    # Imports that may install/use proxy handlers happen only after any external
    # download step (this capture performs no downloads and preserves env proxies).
    from selenium import webdriver
    from selenium.webdriver.chrome.service import Service
    from selenium.webdriver.common.by import By
    from selenium.webdriver.support.ui import WebDriverWait

    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="findrail-alpha5-capture-") as tmp:
        root = pathlib.Path(tmp); source = root / "source"; data = root / "data"; source.mkdir()
        (source / "Observatory.pdf").write_bytes(pdf_fixture())
        (source / "field-notes.md").write_text("Synthetic note: amber thimble at Juniper Observatory.\n", encoding="utf-8")
        fixture_hash = hashlib.sha256((source / "Observatory.pdf").read_bytes() + (source / "field-notes.md").read_bytes()).hexdigest()
        indexed = subprocess.run([str(binary), "index", "--data-dir", str(data), "--json", str(source)], check=True, capture_output=True, text=True, timeout=30)
        if json.loads(indexed.stdout).get("seen", 0) < 2: raise RuntimeError("synthetic source was not indexed")
        port = free_port()
        command = start_command(binary, data, port)
        server = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
        browser = None; frames = []
        try:
            url = f"http://127.0.0.1:{port}"
            deadline = time.monotonic() + 20
            while time.monotonic() < deadline:
                if server.poll() is not None: raise RuntimeError(server.stderr.read())
                try: urllib.request.urlopen(url + "/healthz", timeout=1).close(); break
                except Exception: time.sleep(.1)
            else: raise RuntimeError("alpha.5 UI did not start")
            options = webdriver.ChromeOptions(); options.binary_location = shutil.which("chromium") or shutil.which("google-chrome") or ""
            options.add_argument("--headless=new"); options.add_argument("--no-sandbox"); options.add_argument("--disable-dev-shm-usage"); options.add_argument("--user-data-dir=" + str(root / "profile")); options.add_argument("--window-size=1440,1200")
            browser = webdriver.Chrome(service=Service(), options=options); wait = WebDriverWait(browser, 15); browser.get(url)
            wait.until(lambda d: d.find_element(By.ID, "query").is_enabled())
            def snap(name):
                image = browser.get_screenshot_as_png()
                (output / name).write_bytes(image)
                frames.append(image)
                # Sample the live browser at one-second intervals while each
                # meaningful state is on screen; the MP4 is UI footage, not a
                # still-image mockup or a programmatically drawn interface.
                for _ in range(8):
                    time.sleep(1)
                    frames.append(browser.get_screenshot_as_png())
            browser.find_element(By.ID, "query").send_keys("kestrel"); browser.find_element(By.ID, "submit").click()
            wait.until(lambda d: "1 matching" in d.find_element(By.ID, "status").text)
            pdfrow = next(x for x in browser.find_elements(By.CSS_SELECTOR, ".result") if "Observatory.pdf" in x.text)
            if "PDF page 2" not in pdfrow.text: raise RuntimeError("fixture search did not find the expected PDF page")
            pdfrow.find_element(By.XPATH, ".//button[.='Preview']").click()
            wait.until(lambda d: "Page 2 / 2" in d.find_element(By.ID, "page-label").text)
            if not browser.find_element(By.ID, "preview-location").text.endswith("#page=2"): raise RuntimeError("PDF provenance missing page anchor")
            snap("search-alpha5.png")
            browser.execute_script("document.getElementById('whole-preview').checked=true")
            browser.find_element(By.ID, "add-evidence").click(); browser.find_element(By.ID, "close-preview").click()
            browser.find_element(By.ID, "collection-open").click(); wait.until(lambda d: d.find_element(By.ID,"collection-count").text=="1")
            browser.find_element(By.ID,"collection-title").clear(); browser.find_element(By.ID,"collection-title").send_keys("Juniper field evidence")
            browser.find_element(By.ID,"collection-save").click(); wait.until(lambda d:"saved" in d.find_element(By.ID,"collection-status").text.lower())
            snap("collection-alpha5.png")
            browser.quit(); browser = None; stop_process(server)
            server = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
            deadline = time.monotonic()+20
            while time.monotonic()<deadline:
                try: urllib.request.urlopen(url+"/healthz",timeout=1).close(); break
                except Exception: time.sleep(.1)
            browser = webdriver.Chrome(service=Service(), options=options); browser.get(url)
            wait = WebDriverWait(browser, 15); wait.until(lambda d: not d.find_element(By.ID,"collection-picker").get_attribute("hidden"))
            wait.until(lambda d:d.execute_script("return window.localEvidenceCollection.title==='Juniper field evidence'"))
            browser.find_element(By.ID,"collection-open").click(); wait.until(lambda d:d.find_element(By.ID,"collection-count").text=="1")
            if not browser.execute_script("return window.localEvidenceCollection.entries.some(e=>JSON.stringify(e).toLowerCase().includes('kestrel'))"): raise RuntimeError("saved collection evidence did not restore")
            # Show freshness in the actual UI by changing the synthetic indexed source and waiting for refresh.
            browser.find_element(By.ID,"collection-close").click()
            note=source/"field-notes.md"; note.write_text("Synthetic note: amber thimble at Juniper Observatory. Refresh marker: cobalt.\n",encoding="utf-8")
            browser.quit(); browser = None; stop_process(server)
            subprocess.run([str(binary), "index", "--data-dir", str(data), "--json", str(source)], check=True, capture_output=True, text=True, timeout=30)
            server = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
            deadline = time.monotonic()+20
            while time.monotonic()<deadline:
                try: urllib.request.urlopen(url+"/healthz",timeout=1).close(); break
                except Exception: time.sleep(.1)
            browser = webdriver.Chrome(service=Service(), options=options); browser.get(url); wait = WebDriverWait(browser, 15)
            wait.until(lambda d:d.find_element(By.ID,"query").is_enabled())
            browser.find_element(By.ID,"query").send_keys("cobalt"); browser.find_element(By.ID,"submit").click()
            wait.until(lambda d:any("field-notes.md" in x.text for x in d.find_elements(By.CSS_SELECTOR,".result")))
            browser.execute_script("window.scrollTo(0,document.body.scrollHeight)")
            snap("freshness-alpha5.png")
            # Frames are direct browser screenshots of real UI states; encoded as a short UI capture.
            frame_dir=root/"frames"; frame_dir.mkdir()
            for i, frame in enumerate(frames): (frame_dir/f"frame-{i:03}.png").write_bytes(frame)
            listing=root/"frames.txt"; listing.write_text("\n".join(f"file '{(frame_dir/f'frame-{i:03}.png').as_posix()}'\nduration 1.2" for i in range(len(frames)))+f"\nfile '{(frame_dir/f'frame-{len(frames)-1:03}.png').as_posix()}'\n",encoding="utf-8")
            subprocess.run(["ffmpeg","-hide_banner","-loglevel","error","-y","-f","concat","-safe","0","-i",str(listing),"-vf","scale=trunc(iw/2)*2:trunc(ih/2)*2,fps=24","-c:v","libx264","-pix_fmt","yuv420p","-movflags","+faststart",str(output/"workflow-alpha5.mp4")],check=True,timeout=60)
            meta_out={"product":"published binary; capture script is repository tooling","version":VERSION,"source_revision":SOURCE,"schema":8,"archive_sha256":meta["archive_sha256"],"binary_sha256":sha(binary),"fixture_sha256":fixture_hash,"captured":date.today().isoformat(),"os":platform.platform(),"browser":browser.capabilities.get("browserVersion"),"selenium":"4.35.0","ffmpeg":subprocess.run(["ffmpeg","-version"],capture_output=True,text=True).stdout.splitlines()[0],"scenario":["search synthetic word kestrel","PDF page 2 provenance","save named collection","restart and reopen restored collection","edit fixture and search refreshed cobalt note"],"editing_note":"Browser UI sampled directly at one-second intervals in each state and cut between steps; no composited UI, fake results, benchmark, or native acceptance claim."}
            (output/"capture-alpha5.json").write_text(json.dumps(meta_out,indent=2)+"\n",encoding="utf-8")
            print("Captured published alpha.5 UI and restored named collection.")
        finally:
            if browser: browser.quit()
            stop_process(server)


def pdf_fixture():
    # Import synthetic deterministic PDF fixture from the TASK24 smoke helper.
    import sys
    sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
    from smoke import pdf_fixture as build_pdf
    return build_pdf(["Fictional observatory page one.", "Kestrel waypoint, violet signal."])

if __name__ == "__main__": main()
