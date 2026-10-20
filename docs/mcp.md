# Read-only MCP

Published alpha.3 and later packages include a local, read-only Model Context Protocol server. It runs
over stdio as `findrail mcp`; it does not start the web
server, watch folders, index files, open a browser, call a model, or contact a
network service.

The `mcp-config` helper is included in alpha.5. Earlier alpha.4 archives use the
manual configuration below, which also works with the published MCP command.

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

Generate a client-ready configuration instead of assembling the command and
arguments by hand. Use the real registered source ID from `sources --json`:

```bash
./bin/findrail mcp-config --client vscode --data-dir /absolute/path/to/.findrail-mcp --source REAL_SOURCE_ID
./bin/findrail mcp-config --client claude-desktop --data-dir /absolute/path/to/.findrail-mcp --source REAL_SOURCE_ID
```

The command writes a single JSON object to stdout. `--binary` can select another
absolute executable path (a native `.exe` on Windows); it is validated but never run by config generation.
To permit another source, repeat `--source ID`; every ID is checked against the
existing index. The generated allowlist is explicit and does not include future
sources automatically.

For **VS Code**, save the generated object in the workspace-root `.mcp.json`
file (portable `mcpServers` format), preserving any existing server entries.
Open the workspace in VS Code and use its MCP server management UI to start and
inspect `findrail`. The VS Code-specific `.vscode/mcp.json` `servers` format is
a compatibility destination and is not generated here.

For **Claude Desktop**, open Settings → Developer → Edit Config, preserve any
existing entries in `claude_desktop_config.json`, add the generated `findrail`
entry under `mcpServers`, save, and restart Claude Desktop. Its documented config
locations are `~/Library/Application Support/Claude/claude_desktop_config.json`
on macOS and `%APPDATA%\\Claude\\claude_desktop_config.json` on Windows. The
official instructions also list Linux availability, but this project has not
verified a Linux GUI session.

The two client profiles currently emit the same minimal stdio config structure;
this is a client configuration, not a universal MCP wire schema. No VS Code or
Claude Desktop GUI session has been verified. Automated verification means only
“config parsed + real protocol/SDK child checked.”

The selected client may transmit retrieved indexed text to its configured AI
provider. Findrail's MCP server itself does not send data to a model.

For manual configuration, the equivalent object is:

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

The generated `mcpServers` shape is portable workspace format for VS Code and
the documented local-server format for Claude Desktop. Keep paths absolute.

To allow another folder, index it normally, run `sources --json` again, and add
its new ID as another `--source` argument. The allowlist is fixed when the MCP
process starts. Each search and evidence request must name one allowed source;
there is no all-sources search, file reader, shell tool, or write tool.

An imported archive can be allowed by explicitly adding its destination source ID
to `--source`. Import never changes an existing MCP allowlist automatically.

## Tools and limits

The server exposes exactly three tools:

- `findrail_list_sources` returns only allowed sources that are still registered.
- `findrail_search` searches one allowed source using Literal mode by default;
  optional mode and filters use the shared query contract in [search-query.md](search-query.md).
- `findrail_get_evidence` returns one bounded indexed snapshot. For a PDF, pass
  a positive `page` to obtain page text and a URI ending in `#page=N`. For a
  non-PDF passage, pass either `query` plus `original_identity` (from the search
  result) for a literal body anchor, or `start_char` plus `original_identity`
  for a manual Unicode code-point offset. These options cannot be combined with
  each other or with `page`. A passage `query` is literal and is not Advanced
  search grammar. Calls using only `source_id`, `document_id`, and optional
  `page` retain the original prefix/PDF behavior.

Evidence and search results include `original_identity`, so clients can guard a
follow-up against a changed indexed snapshot. Passage evidence additionally
returns `text_start_char`, `text_total_chars`, and `match_kind`; `has_more` and
`next_start_char` indicate bounded continuation. Offsets count Unicode code
points from indexed text. `match_kind` distinguishes body anchors, title-only
matches and unavailable anchors; title matches do not claim a body phrase.
Continuation fields are present only for passage requests; legacy prefix/PDF
reads do not claim a known total or continuation. A response byte budget that
cannot return even one code point fails rather than repeating an empty window.
Prefix/PDF calls may also supply `original_identity` to reject a changed indexed
snapshot; omitting it retains their previous behavior.

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
The server opens an existing supported current index read-only (schema 8 in this
source candidate) and does not migrate, create, or modify it. MCP and
`mcp-config` both use the ordinary current-schema read-only profile. The special
legacy schema-5 profile belongs to the source-report command, not MCP; it does
not imply that arbitrary old schemas can be opened or migrated by MCP.

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
