#!/usr/bin/env python3
"""Exercise the compiled read-only MCP server over its real stdio transport."""
import argparse
import json
import pathlib
import queue
import shutil
import subprocess
import sys
import tempfile
import threading

sys.dont_write_bytecode = True
from smoke import pdf_fixture


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path)
    binary = parser.parse_args().binary.resolve(strict=True)
    repo = pathlib.Path(__file__).resolve().parents[1]
    with tempfile.TemporaryDirectory(prefix="findrail-mcp-smoke-") as temporary:
        base = pathlib.Path(temporary)
        docs, data = base / "notes", base / "index"
        shutil.copytree(repo / "examples" / "demo", docs)
        (docs / "mcp-runbook.pdf").write_bytes(pdf_fixture(["introduction", "webhook idempotency evidence"]))
        indexed = subprocess.run([str(binary), "index", "--data-dir", str(data), "--json", str(docs)],
                                 check=True, capture_output=True, text=True, encoding="utf-8", timeout=20)
        source = json.loads(subprocess.check_output([str(binary), "sources", "--data-dir", str(data), "--json"],
                                                    text=True, encoding="utf-8", timeout=20))["sources"][0]["id"]
        assert json.loads(indexed.stdout)["seen"] >= 1

        process = subprocess.Popen([str(binary), "mcp", "--data-dir", str(data), "--source", source],
                                   stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, encoding="utf-8", bufsize=1)
        lines = queue.Queue()

        def read_stdout():
            for line in process.stdout:
                lines.put(line)
            lines.put(None)

        reader = threading.Thread(target=read_stdout, daemon=True)
        reader.start()
        request_id = 0

        def request(method, params=None):
            nonlocal request_id
            request_id += 1
            message = {"jsonrpc": "2.0", "id": request_id, "method": method}
            if params is not None:
                message["params"] = params
            process.stdin.write(json.dumps(message, ensure_ascii=False) + "\n")
            process.stdin.flush()
            while True:
                line = lines.get(timeout=10)
                if line is None:
                    raise AssertionError("MCP process closed stdout")
                response = json.loads(line)
                if response.get("id") == request_id:
                    return response

        try:
            initialized = request("initialize", {
                "protocolVersion": "2025-11-25",
                "capabilities": {},
                "clientInfo": {"name": "findrail-mcp-smoke", "version": "1"},
            })
            assert initialized["result"]["protocolVersion"] == "2025-11-25", initialized
            process.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n")
            process.stdin.flush()
            tools = request("tools/list", {})["result"]["tools"]
            assert {tool["name"] for tool in tools} == {
                "findrail_list_sources", "findrail_search", "findrail_get_evidence"
            }, tools
            listed = request("tools/call", {"name": "findrail_list_sources", "arguments": {}})
            assert listed["result"]["structuredContent"]["sources"][0]["id"] == source, listed
            found = request("tools/call", {"name": "findrail_search", "arguments": {
                "query": "idempotency", "source_id": source, "limit": 5
            }})
            result = found["result"]["structuredContent"]
            pdf = next(item for item in result["results"] if item["title"] == "mcp-runbook.pdf")
            assert pdf["page"] == 2 and pdf["uri"].endswith("#page=2"), pdf
            evidence = request("tools/call", {"name": "findrail_get_evidence", "arguments": {
                "document_id": pdf["id"], "source_id": source, "page": 2
            }})["result"]["structuredContent"]
            assert "webhook idempotency evidence" in evidence["text"] and evidence["uri"].endswith("#page=2"), evidence
            denied = request("tools/call", {"name": "findrail_search", "arguments": {
                "query": "idempotency", "source_id": "not-allowed"
            }})["result"]
            assert denied["isError"] and denied["structuredContent"]["error"]["code"] == "source_unavailable", denied
        finally:
            process.stdin.close()
            process.wait(timeout=10)
            reader.join(timeout=2)
            if process.returncode != 0:
                raise AssertionError(process.stderr.read())
    print("Findrail MCP smoke passed: stdio discovery, scoped search, PDF evidence and source denial.")


if __name__ == "__main__":
    main()
