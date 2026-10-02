# Findrail

**You remember the idea. Find the original.**

Findrail is an open-source, local-first search app built in Go. Choose folders
of notes, code and text PDFs; search remembered words, preview the matching
source and copy its original location. Changes in those folders refresh the
index while the app runs. Keyword search needs no AI account and uploads no
documents. Connected services and optional semantic retrieval are the next
product stages.

**Status: local alpha, `0.1.0-alpha.1`.** It is ready for testing, not a stable
release. GitHub ingestion, OCR, DOCX, semantic search, desktop launching and MCP
remain on the roadmap.

[Releases](https://github.com/Lephiziel/findrail/releases) ·
[Product](docs/product.md) · [Architecture](docs/architecture.md) ·
[Roadmap](docs/roadmap.md) · [Contributing](CONTRIBUTING.md) ·
[Русский](docs/ru/overview.md)

## Install and try

Download the archive for Linux, macOS or Windows from
[Releases](https://github.com/Lephiziel/findrail/releases), compare it against
`SHA256SUMS.txt`, and extract it. Go and an external database are not required.
These alpha binaries are unsigned; macOS may require an explicit allowance in
Privacy & Security. Each archive contains installation instructions and license
notices.

```bash
# Linux / macOS, inside the extracted directory:
./findrail index /path/to/your/notes
./findrail serve
```

```powershell
# Windows, inside the extracted directory:
.\findrail.exe index "C:\Users\YOU\Documents\Notes"
.\findrail.exe serve
```

Open **http://127.0.0.1:7766**. Search, select **Preview**, and use the page
controls for a PDF. **Copy location** gives the original file URI, including
`#page=N` for PDF matches. Browsers restrict `file:` navigation; the web client
provides location copying rather than a desktop file opener.

Keep `serve` running for automatic refresh. Ctrl+C stops it. No background daemon
is installed. You can register another folder using `index` while the server
runs; it will appear automatically.

### Build from source

Requires Go 1.26 or newer. No CGO is required.

```bash
git clone https://github.com/Lephiziel/findrail.git
cd findrail
go build -o bin/findrail ./cmd/findrail
./bin/findrail index --data-dir .findrail examples/notes
./bin/findrail search --data-dir .findrail "webhook"
./bin/findrail serve --data-dir .findrail
```

Options precede positional arguments. If you use `--data-dir`, use the same
value for index, search and serve.

## What works today

- Explicit local folders containing UTF-8 text, Markdown and common source files.
- Text PDFs with original page numbers; a bounded extraction child process.
- Durable SQLite FTS5 search, BM25 ranking, Unicode terms and source filters.
- Matching passages and plain-text previews of the indexed snapshot.
- Automatic refresh using directory notifications, debouncing and periodic scans.
- Source health: current refresh state, last success, errors and watcher fallback.
- Atomic full-source scans: failures preserve the previous inventory.
- Search reads the last committed snapshot while an update is in progress.
- CLI / JSON output, loopback web UI and a read-only HTTP API.
- Logical source removal, leaving original files untouched.

See [alpha behaviour and limits](docs/local-alpha.md) and the
[validation report](docs/validation.md).

## Indexing and privacy

Text input defaults to **1 MiB per file**, configurable up to 32 MiB. PDFs default
to **16 MiB input**, with at most 500 pages, 1 MiB of extracted text and a 10-second
extraction deadline. Image-only / scanned PDFs are skipped: OCR is not included.
Malformed or encrypted PDFs fail the source scan, preserving its previous index.

Hidden entries, symlinks, dependency directories, binary / non-UTF-8 text and
common credential filenames are skipped. The index directory is excluded. This
policy cannot detect every secret inside an ordinary document.

The index stores plaintext text and original paths. It is **not encrypted at
rest**. On Unix, newly created directories and database files use private modes.
There is no runtime telemetry. See [data handling](docs/data-handling.md).

Upgrading from the foundation migrates the existing index automatically. Existing
sources retain their text-only policy; re-run `index` to enable PDFs. Back up the
data directory while Findrail is stopped before upgrading; the older binary
cannot read schema 2.

## Commands

```bash
findrail index /path/to/notes
findrail index --max-pdf-bytes 0 /path/to/text-only
findrail search --limit 10 "payment webhook"
findrail search --source SOURCE_ID --json "architecture"
findrail sources --json
findrail serve --sync-interval 5m
findrail serve --no-sync
findrail watch --sync-interval 5m
findrail forget SOURCE_ID
findrail version
```

`watch` refreshes registered sources without starting HTTP. Periodic full refresh
is every five minutes by default; the minimum configurable interval is one
second. Native notifications normally trigger earlier scans.

Default data directory: `$XDG_DATA_HOME/findrail` or `~/.local/share/findrail` on
Linux; `~/Library/Application Support/Findrail` on macOS;
`%LOCALAPPDATA%\Findrail` on Windows. Override with `--data-dir`.

## Product growth

| Capability | Local alpha | Next stages |
|---|---|---|
| Documents | Text / Markdown / code / PDF text | DOCX, optional OCR |
| Sources | Local folders | Read-only GitHub, bookmarks, work tools |
| Freshness | File watching and full reconciliation | Resumable remote sync |
| Retrieval | Literal AND terms, source filter, PDF page attribution | Query evaluation, structured filters, optional semantics |
| Clients | CLI, local web UI, HTTP | Desktop launcher, read-only MCP, editors |
| Extensions | Experimental connector contract | Versioned SDK and conformance harness |

The advantage to validate is easy installation, useful retrieval, inspectable
sources and local ownership. Unified search already exists; user testing must
establish where Findrail helps. [Launch plan](docs/ru/launch.md).

## Repository layout

The project follows useful conventions from
[golang-standards/project-layout](https://github.com/golang-standards/project-layout)
and [official Go module guidance](https://go.dev/doc/modules/layout).

| Path | Responsibility |
|---|---|
| `cmd/findrail/` | Executable entry point |
| `internal/cli/`, `internal/config/` | Composition and commands |
| `internal/connectors/` | Source adapters |
| `internal/extract/`, `internal/ingest/` | Text / PDF extraction and atomic indexing |
| `internal/store/sqlite/`, `internal/search/` | Persistence, FTS and evidence |
| `internal/sync/` | Source discovery, watchers, retries and health |
| `internal/transport/` | HTTP and embedded UI; planned MCP |
| `internal/semantic/` | Future semantic retrieval design |
| `pkg/connector/` | Experimental public connector contract |
| `api/`, `docs/` | Implemented API and product documentation |
| `build/`, `scripts/` | Distribution, smoke tests and developer tools |
| `examples/`, `test/`, `configs/`, `web/`, `deployments/` | Demo data and extension designs |

Tests live beside their packages. Future modules contain design notes, not
placeholder functions claiming implemented features.

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
go mod verify
go build -o bin/findrail ./cmd/findrail
python3 scripts/smoke.py bin/findrail
python3 scripts/package.py
```

Contributions to extraction fixtures, search evaluation, accessibility and
platform installation are welcome. See [implementation tasks](docs/implementation-plan.md).

## License

Apache-2.0. The local product remains independently usable. Future hosted
services may charge for operated infrastructure. See [governance](GOVERNANCE.md).
