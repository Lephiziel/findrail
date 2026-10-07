# Local-alpha validation

Local checks performed on 2026-10-05 for alpha.2, Linux amd64. This report describes a
working alpha, not production certification or a security audit.

| Check | Result |
|---|---|
| Go 1.27.1 unit / lifecycle tests | Passed |
| Go 1.27.1 race detector | Passed |
| Go 1.27.1 vet | Passed |
| Go 1.26 compatibility | GitHub CI race job; see Actions for this commit |
| Module checksums | Verified |
| Five CGO-free distribution targets | Compiled and archived |
| Compiled demo / start / CLI / PDF worker / HTTP / refresh smoke | Passed locally |
| OpenAPI and workflow YAML parsing | Passed |
| Embedded UI JavaScript syntax and local Markdown links | Passed |

Lifecycle coverage includes source isolation, Unicode / literal query handling,
update / rename / deletion, unchanged scans, rollback, persistence, identity
collisions, bounded text and PDF pages, exclusions, source forgetting and HTTP
Host / Origin checks. Migration 2 is tested with an existing schema 1 index.
A held write transaction is tested against concurrent reads of the previous
snapshot. Native notifications and polling both drive automatic lifecycle tests.
A missing root preserves results and recovers after it returns.

The smoke script uses temporary copies of synthetic notes and generated PDF
objects, and separately runs the embedded three-document demo from the actual
executable. The demo test verifies three matches, PDF page 2, note refresh and
workspace removal after a graceful Unix stop. Windows CI terminates the process
with an OS kill, so cleanup there is covered by the Go command lifecycle tests.
It exercises the actual executable and child process, PDF page
attribution, textless-PDF skipping, preview, folder updates / deletion, invalid
PDF rollback and recovery, and a real loopback HTTP listener.

Cross-builds establish compilation only. GitHub CI separately runs unit tests
and the compiled smoke on Linux, macOS and Windows. Check
[Actions](https://github.com/Lephiziel/findrail/actions) for the actual result;
remote success is not implied by local checks. ARM runtime testing, binary
signing, relevance on a labeled corpus and large-collection performance remain
open. Cloud sources, OCR, semantic retrieval, desktop launching and MCP are not
part of this alpha.

Reproduce from the repository root:

```bash
go test ./...
go test -race ./...
go vet ./...
go mod verify
CGO_ENABLED=0 go build -trimpath -o bin/findrail ./cmd/findrail
python3 scripts/smoke.py bin/findrail
python3 scripts/package.py
```

## Web source-management validation · 2026-10-07

The `feat/web-source-management` source build was checked on Linux. The new
application-service tests use real SQLite stores and fake GitHub HTTP responses;
no test calls public GitHub. The compiled smoke exercises empty `start`,
same-origin API authorization, folder add, the existing PDF child process,
search/preview, watcher discovery, manual refresh, logical removal, cached
original files, and the demo read-only boundary. A two-store-handle storage test
orders filesystem refresh against forget; source-app barriers cover update/remove,
queue limits, cancellation cleanup, post-commit cancellation, shutdown and
history retention. `start` and its watcher share per-source process coordination;
SQLite guards remain authoritative for separate processes.

| Check | Result |
|---|---|
| `gofmt` and `git diff --check` | Passed |
| `go test ./...` | Passed |
| `go test -race ./...` | Passed |
| `go vet ./...` | Passed |
| `go mod verify` | Passed |
| `CGO_ENABLED=0 go build -o bin/findrail ./cmd/findrail` | Passed |
| `python3 scripts/smoke.py bin/findrail` | Passed |
| `python3 scripts/mcp_smoke.py bin/findrail` | Passed |
| `node --test internal/transport/http/web/citation_test.mjs` | Passed (11 tests) |
| Embedded module syntax and OpenAPI / CI YAML parse | Passed |
| Chromium headless browser workflow | Passed on local Linux (empty start → Add folder → search → preview → Copy as Markdown → refresh → confirmed remove) |

Browser automation used the locally available Chromium/ChromeDriver through a
temporary WebDriver harness; it is not a CI dependency. No manual native Windows
or macOS QA was performed. The PR's native GitHub Actions jobs passed on Windows
and macOS, including their compiled smoke tests; Linux cross-compilation alone
would not establish native platform behavior. No search evaluation code,
fixtures, command or documentation was changed.

### 60–90 second demonstration

1. Create `notes/weekly-plan.md` containing fictional text: “Orbit project
   checklist: call the fictional supplier on Thursday.”
2. Run `findrail start --data-dir ./demo-index --no-open` and open the printed
   loopback URL. Start with the empty Sources panel.
3. Choose **Add folder**, enter the absolute path to `notes`, and submit. Point
   out the queued/indexing job and the source appearing without a page reload.
4. Search for `fictional supplier`, open **Preview**, and copy the location or
   **Copy as Markdown** citation.
5. Edit the note to say “Friday” and use **Refresh** (or wait for folder
   reconciliation); search for the updated word.
6. Choose **Remove from index**, confirm the source name, and show search results
   disappear while `weekly-plan.md` remains on disk.
7. Mention that public GitHub Add/Refresh is explicit and downloads a bounded
   local snapshot; `serve` and `demo` stay read-only.

### PR #13 review fixes (2026-10-07)

Independent review reproduced duplicate inventory polling loops and stale
evidence arriving after source removal. The UI now coalesces inventory refreshes,
stops that polling in hidden tabs, and invalidates pending evidence by source ID.
Management status is visible even when the add-source forms are closed.
Four event-driven Node regression tests exercise the embedded search client;
CI runs these alongside the eleven citation tests. All fifteen passed locally.
Go tests and race checks passed on the original PR head. The review's Chromium
launch failed with SIGSEGV before loading the page; this run does not claim an
additional browser validation beyond the author's recorded checks.

## DOCX source-build validation · 2026-10-07

The DOCX fixtures are synthetic ZIP/XML generated in Go tests and in the smoke
script; no Word/LibreOffice, personal files, or runtime Python dependency is
required. The compiled Linux smoke uses the production extractor and covers CLI
search/preview, source-build Add-folder policy, authenticated Configure API,
disable/enable re-index, no-page evidence, temp-file rename save, rollback and
source removal/original preservation. MCP smoke retrieves the DOCX snapshot with
no page attribution through both supported protocols. SQLite lifecycle tests exercise Configure
rollback, cross-handle revision compare-and-swap, stale refresh rejection and
forget/re-add ABA prevention. Node checks retain the four polling/invalidation
regressions and cover pending-preview invalidation after Configure. A
short extractor fuzz run completed (2-second requested duration; 78,133
executions in the final run). No crash was found.

Resource spot check (Linux, compiled CGO-free binary; elapsed includes fresh
SQLite setup and indexing; output is extracted text bytes):

| Fixture | DOCX input bytes | Extracted output bytes | Elapsed | Result |
|---|---:|---:|---:|---|
| Valid package near default input cap; ignored stored media | 7,501,120 | 19 | 0.006 s | Indexed |
| High-ratio package with 16 MiB declared ignored media | 17,187 | 19 | 0.006 s | Indexed; media was not inflated |

Checks run on this working branch: `go test ./...`, `go test -race ./...`,
`go vet ./...`, `go mod verify`, `CGO_ENABLED=0 go build`, compiled CLI/HTTP
smoke, MCP smoke, `node --test internal/transport/http/web/*_test.mjs` (16
tests), `git diff --check`, and bounded `FuzzExtract` smoke all passed. Native
Windows/macOS execution and browser automation were not performed locally. The
PR CI's native Linux/macOS/Windows compile-and-smoke jobs and race/UI jobs passed
on the reviewed head; the transient Linux test timeout was rerun successfully.

Configure policy and snapshot share one SQLite transaction. Schema 4
registration tokens and revisions guard independent handles and forget/re-add
ABA; GitHub metadata remains under its separate revision guard. Canceled workers
cannot republish watcher status after a source has been forgotten.
