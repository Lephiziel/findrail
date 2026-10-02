# Findrail

**You remember the idea. Find the original.**

Findrail is an open-source, local-first search product built in Go. It is being
developed to connect your files, notes, repositories, and work tools in one
searchable index, with matching passages and a direct route back to each source.
Keyword search works without an AI account. Semantic search will be optional.

**Status: development foundation, not a stable release.** The current code
indexes local UTF-8 text, Markdown, and source files, searches with SQLite FTS5,
and serves a small local web interface. PDF, GitHub ingestion, continuous sync,
semantic search, and MCP are designed in the roadmap and are not shipped yet.

[Product](docs/product.md) · [Architecture](docs/architecture.md) ·
[Roadmap](docs/roadmap.md) · [Contributing](CONTRIBUTING.md) ·
[Русский](docs/ru/overview.md)

## Try the working foundation

Requires Go 1.26 or newer. No external database or CGO is required.

```bash
git clone https://github.com/Lephiziel/findrail.git
cd findrail
go build -o bin/findrail ./cmd/findrail

./bin/findrail index --data-dir .findrail examples/notes
./bin/findrail search --data-dir .findrail "webhook"
./bin/findrail search --data-dir .findrail --json "поиск"
./bin/findrail serve --data-dir .findrail
```

Open **http://127.0.0.1:7766**. The interface shows matching passages, source
names, and original file locations. Browsers restrict opening `file:` links from
web pages; this foundation provides **Copy location** instead of claiming a
working desktop file opener.

Options precede positional arguments. Run `./bin/findrail COMMAND --help` for
command-specific options. When using `--data-dir`, use the same directory for
indexing, searching, and serving.

## What works today

- Explicitly selected local folders; UTF-8 text, Markdown, and common source files.
- Durable SQLite index with BM25 keyword ranking, matching passages, and Unicode tokenization.
- Multiple independent sources, source filtering, JSON output, and bounded results.
- Changed-content detection and pruning after a complete source scan.
- Atomic ingestion: failed or cancelled scans preserve the previous source inventory.
- Logical removal with `findrail forget SOURCE_ID`, leaving original files untouched.
- Local web UI and read-only HTTP API, restricted to loopback addresses.
- Tests covering updates, deletion, rollback, persistence, query handling, and the local HTTP boundary.

## What Findrail is being built to become

| Capability | Foundation | Planned product |
|---|---|---|
| Local documents | UTF-8 text / Markdown / source files | PDF text, DOCX, optional OCR |
| Connected tools | Connector contract | GitHub, bookmarks, Notion, cloud drives |
| Freshness | Explicit re-index | File watching, resumable sync, deletion propagation |
| Retrieval | Literal keyword terms with AND semantics | Filters, typo tolerance, optional semantic retrieval |
| Clients | CLI, local web interface, HTTP | Desktop launcher, editor integrations, read-only MCP |
| Trust | Explicit sources, local index, loopback boundary | Credential vault, per-source scopes, verified exports |

The intended advantage is a practical combination of easy installation,
source-backed results, local ownership, and useful retrieval without a mandatory
model service. This is a product hypothesis to validate against existing tools,
not a claim that unified search is a new category.

## Indexing behaviour

The default limit is **1 MiB per file**, configurable up to 32 MiB. Hidden files
and directories, symlinks, binary / non-UTF-8 content, dependency directories, and
some common credential filenames are skipped. The index directory is excluded.
This policy cannot detect every secret inside an otherwise ordinary document.

Re-run `index` to refresh a source. Unchanged content avoids FTS rewrites, but the
foundation still reads the files to verify their hashes. A scan that fails does
not publish partial updates or prune missing documents.

The index includes extracted text and original locations. It is **not encrypted
at rest**. On Unix, new data directories and database files use private modes;
existing directory permissions are not changed. See [data handling](docs/data-handling.md).

## Commands

```bash
findrail index /path/to/notes
findrail search --limit 10 "payment webhook"
findrail sources --json
findrail search --source SOURCE_ID "architecture"
findrail forget SOURCE_ID
findrail serve --addr 127.0.0.1:7766
findrail version
```

Default data location: `$XDG_DATA_HOME/findrail` or `~/.local/share/findrail` on
Linux; `~/Library/Application Support/Findrail` on macOS; `%LOCALAPPDATA%\Findrail`
on Windows. Override with `--data-dir`.

## Repository layout

The repository uses the useful conventions from
[golang-standards/project-layout](https://github.com/golang-standards/project-layout),
and the official [Go module layout guidance](https://go.dev/doc/modules/layout).

| Path | Responsibility |
|---|---|
| `cmd/findrail/` | Executable entry point |
| `internal/cli/`, `internal/config/` | Commands and platform configuration |
| `internal/connectors/` | Source adapters |
| `internal/extract/`, `internal/ingest/` | Bounded extraction and atomic indexing |
| `internal/store/sqlite/`, `internal/search/` | Persistence, FTS, and retrieval contracts |
| `internal/transport/` | HTTP today; planned read-only MCP |
| `internal/sync/`, `internal/semantic/` | Documented future modules |
| `pkg/connector/` | Experimental connector contract |
| `api/` | Implemented HTTP API specification |
| `web/` | Product client design; current embedded UI is under HTTP transport |
| `configs/`, `build/`, `deployments/` | Configuration and packaging plans |
| `examples/`, `test/`, `scripts/` | Demo data, integration fixtures, developer tools |
| `docs/` | Product, architecture, decisions, growth plan, and milestones |

Unit tests live beside their packages. Planned modules contain design notes, not
placeholder functions that pretend to implement features.

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
go mod verify
make build
```

See the [implementation plan](docs/implementation-plan.md) for tasks and
acceptance criteria. Small contributions to connector conformance, extraction,
query quality, accessibility, and test corpora are welcome.

The [validation report](docs/validation.md) records the foundation's local checks
and their limits.

## License

Apache-2.0. The core, local clients, and connector interfaces are open source.
Future hosted offerings may charge for operated infrastructure; the local
product should remain independently usable. See [governance](GOVERNANCE.md).
