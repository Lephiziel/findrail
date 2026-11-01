#!/usr/bin/env python3
"""Compiled CLI/HTTP smoke for Advanced queries and server-side search filters."""
import json
import pathlib
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request


def run(binary, *args):
    result = subprocess.run([str(binary), *map(str, args)], check=True, capture_output=True, text=True)
    return json.loads(result.stdout)


def main():
    binary = pathlib.Path(sys.argv[1]).resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="findrail-search-smoke-") as temp:
        base = pathlib.Path(temp)
        root, data = base / "notes", base / "index"
        (root / "docs" / "nested").mkdir(parents=True)
        (root / "docs-old").mkdir()
        (root / "docs" / "guide.md").write_text("webhook arrives with retry budget", encoding="utf-8")
        (root / "docs" / "nested" / "guide.md").write_text("webhook arrives with retry budget", encoding="utf-8")
        (root / "docs" / "backoff.md").write_text("backoff idempotency", encoding="utf-8")
        (root / "docs-old" / "guide.md").write_text("webhook arrives retry budget deprecated", encoding="utf-8")
        indexed = run(binary, "index", "--data-dir", data, "--json", root)
        source = indexed["source"]["id"]
        args = ("search", "--data-dir", data, "--source", source, "--mode", "advanced",
                "--format", "text", "--path-prefix", "docs/", "--title-contains", "guide",
                "--limit", "1", "--json", '"webhook arrives" OR backoff')
        cli = run(binary, *args)
        assert cli["total"] == 2 and len(cli["results"]) == 1, cli
        assert all(item["path"].startswith("docs/") for item in cli["results"]), cli

        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        process = subprocess.Popen([str(binary), "serve", "--data-dir", str(data), "--no-sync",
                                    "--addr", f"127.0.0.1:{port}"], stdout=subprocess.DEVNULL,
                                   stderr=subprocess.PIPE, text=True)
        base_url = f"http://127.0.0.1:{port}"

        def get(path):
            with urllib.request.urlopen(base_url + path, timeout=3) as response:
                return json.load(response)

        try:
            deadline = time.monotonic() + 10
            while True:
                try:
                    get("/healthz")
                    break
                except (urllib.error.URLError, TimeoutError):
                    if process.poll() is not None or time.monotonic() >= deadline:
                        raise RuntimeError("search smoke server did not become ready")
                    time.sleep(0.05)
            params = {"q": '"webhook arrives" OR backoff', "mode": "advanced", "format": "text",
                      "source": source, "path_prefix": "docs/", "title_contains": "guide", "limit": "1"}
            http = get("/api/v1/search?" + urllib.parse.urlencode(params))
            assert http["total"] == cli["total"] and [x["id"] for x in http["results"]] == [x["id"] for x in cli["results"]], http
            try:
                urllib.request.urlopen(base_url + "/api/v1/search?q=retry&q=backoff", timeout=3)
                raise AssertionError("repeated q parameter was accepted")
            except urllib.error.HTTPError as error:
                assert error.code == 400
        finally:
            process.terminate()
            try:
                process.wait(timeout=7)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
    print("Findrail search smoke passed: Advanced phrase/OR, source/format/path/title filters, total-before-limit, and HTTP duplicate rejection.")


if __name__ == "__main__":
    main()
