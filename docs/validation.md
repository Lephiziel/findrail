# Foundation validation

Local checks performed on 2026-10-01, Linux amd64. This report describes the
development foundation, not a production or security certification.

| Check | Result |
|---|---|
| Go 1.27.1 `go vet ./...` | Passed |
| Go 1.27.1 `go test ./...` | Passed |
| Go 1.27.1 `go test -race ./...` | Passed |
| Go 1.26.8 `go test ./...` | Passed |
| `go mod verify` | Passed |
| CGO-disabled Linux amd64 build | Passed |
| CGO-disabled Windows amd64 cross-build | Passed |
| CGO-disabled macOS arm64 cross-build | Passed |
| Compiled CLI and real loopback HTTP smoke | Passed |
| Markdown local links | Passed |
| OpenAPI and workflow YAML parsing | Passed |

Behavioural coverage includes multiple source inventories, Unicode queries,
query / limit validation, updates, deletion and FTS cleanup, source filtering,
unchanged scans, rollback, reopening the index, cross-source identity collision,
bounded extraction, exclusions, symlinks, CLI output, and HTTP Host / Origin
checks.

The smoke script indexes the synthetic three-document demo corpus, performs
English and Russian searches, and checks a real HTTP listener, sources, and
HTML. It removes its temporary index and terminates the process.

Cross-builds establish compilation only. Windows / macOS runtime tests are
configured in GitHub Actions and must be confirmed after publication; no remote
CI result is claimed here. Retrieval relevance and 100k-document performance
targets have not yet been measured. PDF, watching, remote connectors, semantic
retrieval, desktop clients, and MCP are not part of these checks.

Reproduce from the repository root:

```bash
go vet ./...
go test ./...
go test -race ./...
go mod verify
CGO_ENABLED=0 go build -trimpath -o bin/findrail ./cmd/findrail
python3 scripts/smoke.py bin/findrail
```
