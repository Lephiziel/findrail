# Read-only MCP

Source builds include a local, read-only Model Context Protocol server. It runs
over stdio as `findrail mcp`; it does not start the web
server, watch folders, index files, open a browser, call a model, or contact a
network service.

The first build containing this command is a source build after this change;
existing alpha archives do not gain MCP retroactively.

The MCP client receives indexed snapshots. A client may send that returned text
to its own AI provider, according to the client's settings; local indexing does
not guarantee that another client keeps the text offline.

## Build and configure

Build the source binary and create an index in the usual way:

```bash
go build -o bin/findrail ./cmd/findrail
./bin/findrail index --data-dir .findrail-mcp examples/demo
./bin/findrail sources --data-dir .findrail-mcp --json
```

Copy one real source ID from the JSON result into the MCP client's configuration:

```json
{
  "mcpServers": {
    "findrail": {
      "command": "/absolute/path/to/findrail",
      "args": ["mcp", "--data-dir", "/absolute/path/to/.findrail-mcp", "--source", "REAL_SOURCE_ID"]
    }
  }
}
```

On Windows, use the executable path and JSON-escaped backslashes, for example:

```json
{
  "mcpServers": {
    "findrail": {
      "command": "C:\\Users\\you\\findrail\\findrail.exe",
      "args": ["mcp", "--data-dir", "C:\\Users\\you\\findrail\\.findrail-mcp", "--source", "REAL_SOURCE_ID"]
    }
  }
}
```

This `mcpServers` shape is a common client configuration form, not a promise
that every client uses the same file or field names. Use the client's own
configuration location and keep `command` and every path absolute.

To allow another folder, index it normally, run `sources --json` again, and add
its new ID as another `--source` argument. The allowlist is fixed when the MCP
process starts. Each search and evidence request must name one allowed source;
there is no all-sources search, file reader, shell tool, or write tool.

## Tools and limits

The server exposes exactly three tools:

- `findrail_list_sources` returns only allowed sources that are still registered.
- `findrail_search` searches one allowed source using Findrail's existing literal
  AND query language and preserves its result order.
- `findrail_get_evidence` returns one bounded indexed snapshot. For a PDF, pass
  a positive `page` to obtain page text and a URI ending in `#page=N`.

Defaults and bounds are configurable at startup:

```text
--max-results       10, allowed 1–20
--max-text-chars    8000, allowed 256–32768 Unicode characters
--request-timeout   5s, allowed 1s–30s
```

Search snippets are limited to 1,024 Unicode characters. Tool results are also
limited to 256 KiB after JSON serialization, including the structured result
and its compatibility text representation. Evidence reports `truncated` and
`truncation_reasons` such as `indexed_preview`, `mcp_text_limit`, and
`mcp_response_budget` when applicable. Cancellation releases the backend slot.

The result is an indexed snapshot, not a fresh read of the original file.
Findrail does not claim that the snapshot is current when the original changes.
The server opens an existing schema-2 index read-only and does not migrate,
create, or modify it.

## Updating and troubleshooting

Update sources separately with the ordinary commands while the MCP process is
stopped or running:

```bash
./bin/findrail index --data-dir /absolute/path/to/.findrail-mcp /absolute/path/to/notes
./bin/findrail serve --data-dir /absolute/path/to/.findrail-mcp
# or: ./bin/findrail watch --data-dir /absolute/path/to/.findrail-mcp
```

The MCP process requires an existing index and at least one currently registered
source ID. A missing database, unsupported schema, unknown source, or invalid
startup limit fails before stdio transport starts; diagnostics go to stderr and
stdout remains free for MCP JSON-RPC. Ctrl+C, SIGTERM, client close, and stdin
EOF close the process and its read-only database.

The automated client tests verify MCP `2025-11-25` (legacy initialization) and
`2026-07-28` (discovery and per-request metadata), using Go SDK v1.8.0. To run
the compiled integration checks on the source binary:

```bash
python3 scripts/mcp_smoke.py bin/findrail
```

On Windows, use `python scripts/mcp_smoke.py bin/findrail.exe` after building
that executable. CI runs this check on Linux, macOS and Windows. It verifies
source and document denials, scoped totals, PDF snapshots, response budgets,
source deletion and EOF shutdown. No particular AI client has been verified by
these automated tests.

## Record a demo

Use the synthetic public files in `examples/demo`, not a private folder. Build
and index them, obtain the actual source ID, and configure the client with the
absolute paths above. Ask:

> Find the passage about webhook idempotency in my indexed documents. Show the original source and PDF page.

The visible flow should be `findrail_list_sources`, `findrail_search`, then
`findrail_get_evidence` with `page=2`. Show the returned passage and its URI with
`#page=2`. If the client supports it, a second call using an unallowed source
demonstrates the access boundary. The compiled smoke test checks the protocol
and data; wording of any AI response depends on the selected client.
