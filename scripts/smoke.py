#!/usr/bin/env python3
"""Exercise a compiled binary with only synthetic demo data and a temporary index."""

import argparse
import json
import pathlib
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path)
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    root = pathlib.Path(__file__).resolve().parents[1]
    with tempfile.TemporaryDirectory(prefix="findrail-smoke-") as temporary:
        def run(command, *arguments):
            result = subprocess.run(
                [str(binary), command, "--data-dir", temporary, *arguments],
                check=True, capture_output=True, text=True, timeout=15,
            )
            return json.loads(result.stdout)

        indexed = run("index", "--json", str(root / "examples" / "notes"))
        assert indexed["seen"] == 3, indexed
        matches = run("search", "--json", "webhook")
        assert matches["total"] == 1, matches
        assert "webhook" in matches["results"][0]["snippet"].lower()
        assert run("search", "--json", "поиск")["total"] == 1

        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        process = subprocess.Popen(
            [str(binary), "serve", "--data-dir", temporary,
             "--addr", f"127.0.0.1:{port}"],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        )
        # The chosen port is released before launch. If another process wins the
        # race, treat that as a smoke failure instead of probing an unrelated app.
        try:
            base = f"http://127.0.0.1:{port}"
            deadline = time.monotonic() + 5
            while True:
                if process.poll() is not None:
                    raise RuntimeError("server exited: " + process.stderr.read())
                try:
                    with urllib.request.urlopen(base + "/healthz", timeout=1) as response:
                        assert json.load(response) == {"status": "ok"}
                    break
                except (urllib.error.URLError, TimeoutError):
                    if time.monotonic() >= deadline:
                        raise
                    time.sleep(0.05)
            with urllib.request.urlopen(base + "/api/v1/search?q=webhook", timeout=3) as response:
                assert json.load(response)["total"] == 1
            with urllib.request.urlopen(base + "/api/v1/sources", timeout=3) as response:
                sources = json.load(response)["sources"]
                assert len(sources) == 1 and sources[0]["documents"] == 3
            with urllib.request.urlopen(base, timeout=3) as response:
                assert "Findrail" in response.read().decode("utf-8")
        finally:
            if process.poll() is None:
                process.terminate()
            try:
                process.communicate(timeout=7)
            except subprocess.TimeoutExpired:
                process.kill()
                process.communicate(timeout=2)
        assert process.returncode == 0, process.returncode
    print("Findrail CLI and loopback HTTP smoke passed.")


if __name__ == "__main__":
    main()
