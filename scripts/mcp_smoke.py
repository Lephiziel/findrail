#!/usr/bin/env python3
"""Exercise a compiled MCP server: both protocols, scope, PDF snapshots and shutdown."""
import argparse
import json
import os
import pathlib
import queue
import shutil
import subprocess
import sys
import tempfile
import threading

sys.dont_write_bytecode = True
from smoke import pdf_fixture

VERSIONS = ("2025-11-25", "2026-07-28")
META_VERSION = "io.modelcontextprotocol/protocolVersion"
META_CAPABILITIES = "io.modelcontextprotocol/clientCapabilities"
META_CLIENT = "io.modelcontextprotocol/clientInfo"
META_SERVER = "io.modelcontextprotocol/serverInfo"


def run(binary, *args):
    result = subprocess.run([str(binary), *map(str, args)], capture_output=True,
                            text=True, encoding="utf-8", timeout=20)
    if result.returncode:
        raise AssertionError(f"Findrail {args[0]} failed: {result.stderr}")
    return json.loads(result.stdout) if result.stdout.strip().startswith("{") else None


def result_byte_size(line):
    """Measure the actual result JSON, excluding the JSON-RPC envelope."""
    decoder = json.JSONDecoder()
    position = line.index("{") + 1
    while True:
        while line[position].isspace():
            position += 1
        key, position = decoder.raw_decode(line, position)
        while line[position].isspace() or line[position] == ":":
            position += 1
        start = position
        _, position = decoder.raw_decode(line, position)
        if key == "result":
            return len(line[start:position].encode("utf-8"))
        while line[position].isspace():
            position += 1
        if line[position] == "}":
            return 0
        assert line[position] == ",", line
        position += 1


class MCP:
    def __init__(self, binary, data, source, version):
        self.version = version
        self.process = subprocess.Popen(
            [str(binary), "mcp", "--data-dir", str(data), "--source", source,
             "--max-text-chars", "32768"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            text=True, encoding="utf-8", bufsize=1)
        self.lines = queue.Queue()
        self.request_id = 0
        self.reader = threading.Thread(target=self.read_stdout, daemon=True)
        self.reader.start()

    def read_stdout(self):
        try:
            for line in self.process.stdout:
                self.lines.put(line)
        finally:
            self.lines.put(None)

    def request(self, method, params=None):
        self.request_id += 1
        params = dict(params or {})
        if self.version == VERSIONS[1]:
            params["_meta"] = {
                META_VERSION: self.version, META_CAPABILITIES: {},
                META_CLIENT: {"name": "findrail-mcp-smoke", "version": "1"},
            }
        message = {"jsonrpc": "2.0", "id": self.request_id, "method": method, "params": params}
        self.process.stdin.write(json.dumps(message, ensure_ascii=False) + "\n")
        self.process.stdin.flush()
        line = self.lines.get(timeout=10)
        assert line is not None, "MCP process closed stdout"
        response = json.loads(line)
        assert response.get("id") == self.request_id and "error" not in response, response
        result = response["result"]
        if self.version == VERSIONS[1]:
            assert result["_meta"][META_SERVER]["name"] == "findrail", result
        if method == "tools/call":
            assert result_byte_size(line) <= 256 << 10, "serialized tool result exceeds byte budget"
            assert len(result["content"]) == 1 and result["content"][0]["type"] == "text", result
            assert json.loads(result["content"][0]["text"]) == result["structuredContent"], result
        return result

    def initialize(self):
        if self.version == VERSIONS[1]:
            discovered = self.request("server/discover")
            assert set(discovered["supportedVersions"]) == set(VERSIONS), discovered
            assert "tools" in discovered["capabilities"], discovered
        else:
            initialized = self.request("initialize", {
                "protocolVersion": self.version, "capabilities": {},
                "clientInfo": {"name": "findrail-mcp-smoke", "version": "1"},
            })
            assert initialized["protocolVersion"] == self.version, initialized
            self.process.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n")
            self.process.stdin.flush()

    def call(self, name, **arguments):
        return self.request("tools/call", {"name": name, "arguments": arguments})

    def close(self):
        try:
            self.process.stdin.close()
            self.process.wait(timeout=10)
        except (subprocess.TimeoutExpired, BrokenPipeError):
            self.process.kill()
            self.process.wait(timeout=5)
            raise
        finally:
            self.reader.join(timeout=2)
        diagnostics = self.process.stderr.read()
        assert self.process.returncode == 0, diagnostics
        assert not self.reader.is_alive(), "stdout reader did not finish"
        self.process.stdout.close()
        self.process.stderr.close()


def expect_error(result, code):
    assert result.get("isError") and result["structuredContent"]["error"]["code"] == code, result
    assert "FOREIGN_MCP_MARKER" not in json.dumps(result), result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=pathlib.Path)
    binary = parser.parse_args().binary.resolve(strict=True)
    repo = pathlib.Path(__file__).resolve().parents[1]
    with tempfile.TemporaryDirectory(prefix="findrail-mcp-smoke-") as temporary:
        base = pathlib.Path(temporary)
        docs, other, data = base / "notes", base / "other", base / "index"
        shutil.copytree(repo / "examples" / "demo", docs)
        other.mkdir()
        (other / "foreign.md").write_text("idempotency FOREIGN_MCP_MARKER", encoding="utf-8")
        pdf_path = docs / "mcp-runbook.pdf"
        pdf_path.write_bytes(pdf_fixture(["introduction", "webhook idempotency evidence"]))
        (docs / "large.md").write_text("mcpbudget " + "🙂" * 32768, encoding="utf-8")
        indexed = run(binary, "index", "--data-dir", data, "--json", docs)
        assert indexed["seen"] >= 1
        run(binary, "index", "--data-dir", data, "--json", other)
        sources = run(binary, "sources", "--data-dir", data, "--json")["sources"]
        source = next(s["id"] for s in sources if s["name"] == "notes")
        foreign_source = next(s["id"] for s in sources if s["name"] == "other")
        foreign_id = run(binary, "search", "--data-dir", data, "--source", foreign_source,
                         "--json", "idempotency")["results"][0]["id"]
        # MCP must retrieve the indexed PDF even when its original no longer exists.
        pdf_path.unlink()

        for version in VERSIONS:
            client = MCP(binary, data, source, version)
            try:
                client.initialize()
                tools = client.request("tools/list")["tools"]
                assert {tool["name"] for tool in tools} == {
                    "findrail_list_sources", "findrail_search", "findrail_get_evidence"}, tools
                for tool in tools:
                    hints = tool["annotations"]
                    assert hints["readOnlyHint"] and hints["idempotentHint"], hints
                    assert hints["destructiveHint"] is False and hints["openWorldHint"] is False, hints
                    assert tool["inputSchema"]["additionalProperties"] is False and "outputSchema" in tool, tool
                listed = client.call("findrail_list_sources")["structuredContent"]
                assert len(listed["sources"]) == 1 and listed["sources"][0]["id"] == source, listed
                assert "root" not in listed["sources"][0], listed
                result = client.call("findrail_search", query="idempotency", source_id=source, limit=5)["structuredContent"]
                assert result["total"] == 4 and result["returned"] == len(result["results"]), result
                assert all(item["source_id"] == source for item in result["results"]), result
                assert "FOREIGN_MCP_MARKER" not in json.dumps(result), result
                pdf = next(item for item in result["results"] if item["title"] == "mcp-runbook.pdf")
                assert pdf["page"] == 2 and pdf["uri"].endswith("#page=2"), pdf
                evidence = client.call("findrail_get_evidence", document_id=pdf["id"], source_id=source, page=2)["structuredContent"]
                assert "webhook idempotency evidence" in evidence["text"] and evidence["uri"].endswith("#page=2"), evidence
                assert evidence["text_chars"] == len(evidence["text"]) and evidence["page"] == 2, evidence
                assert evidence["indexed_snapshot"] and evidence["content_untrusted"], evidence
                default_page = client.call("findrail_get_evidence", document_id=pdf["id"], source_id=source)["structuredContent"]
                assert default_page["page"] == 1 and "introduction" in default_page["text"], default_page
                expect_error(client.call("findrail_get_evidence", document_id=pdf["id"], source_id=source, page=3), "invalid_page")
                for denied in (foreign_source, "not-allowed"):
                    expect_error(client.call("findrail_search", query="idempotency", source_id=denied), "source_unavailable")
                    expect_error(client.call("findrail_get_evidence", document_id=pdf["id"], source_id=denied), "source_unavailable")
                for denied_id in (foreign_id, "missing-document"):
                    expect_error(client.call("findrail_get_evidence", document_id=denied_id, source_id=source), "document_unavailable")
                empty = client.call("findrail_search", query="noexistingterm", source_id=source)["structuredContent"]
                assert empty["results"] == [] and empty["total"] == 0 and not empty["results_truncated"], empty
                large = client.call("findrail_search", query="mcpbudget", source_id=source)["structuredContent"]["results"][0]
                bounded = client.call("findrail_get_evidence", document_id=large["id"], source_id=source)["structuredContent"]
                assert bounded["truncated"] and "mcp_response_budget" in bounded["truncation_reasons"], bounded
                assert bounded["text_chars"] == len(bounded["text"]), "invalid Unicode text count"
                expect_error(client.call("findrail_get_evidence", document_id=large["id"], source_id=source, page=1), "invalid_page")
                if version == VERSIONS[-1]:
                    run(binary, "forget", "--data-dir", data, source)
                    assert client.call("findrail_list_sources")["structuredContent"]["sources"] == []
                    expect_error(client.call("findrail_search", query="idempotency", source_id=source), "source_unavailable")
                    expect_error(client.call("findrail_get_evidence", document_id=pdf["id"], source_id=source), "source_unavailable")
            finally:
                client.close()

        missing = base / "missing-index"
        failed = subprocess.run([str(binary), "mcp", "--data-dir", str(missing), "--source", source],
                                input="", capture_output=True, text=True, encoding="utf-8", timeout=10)
        assert failed.returncode != 0 and failed.stdout == "" and failed.stderr and not missing.exists(), failed
        if os.name != "nt":
            client = MCP(binary, data, foreign_source, VERSIONS[1])
            try:
                client.initialize()
                client.process.terminate()
            finally:
                client.close()
    print("Findrail MCP smoke passed: both protocols, scoped counts, PDF snapshots, byte budgets, deletion, EOF and shutdown.")


if __name__ == "__main__":
    main()
