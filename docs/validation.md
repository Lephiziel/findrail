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
