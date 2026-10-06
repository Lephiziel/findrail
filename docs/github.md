# Public GitHub repository snapshots

Source builds can index supported UTF-8 text and source files from a public
`github.com` repository. Each successful operation stores one complete snapshot
pinned to a full commit SHA. Search, preview, citations, and MCP then work offline.
Refresh is explicit; Findrail never polls GitHub in the background.

## Quick start

```bash
go build -o bin/findrail ./cmd/findrail
./bin/findrail index-github --data-dir .findrail Lephiziel/findrail
./bin/findrail sources --data-dir .findrail --json
./bin/findrail search --data-dir .findrail --source SOURCE_ID "snapshot"
./bin/findrail refresh-github --data-dir .findrail SOURCE_ID
./bin/findrail serve --data-dir .findrail
./bin/findrail forget --data-dir .findrail SOURCE_ID
```

Flags precede positional arguments. Use `sources --json` to copy the real source
ID. An optional scope is `--ref heads/main --path docs`. Without `--ref`, refresh
resolves the current default branch. A full 40-character SHA is pinned. For MCP,
index first and configure `findrail mcp --data-dir /absolute/index --source
SOURCE_ID`; its three retrieval tools cannot download or refresh.

## Policy and failure behavior

The default per-file limit is 1 MiB and maximum is 8 MiB. Preparation also limits
metadata, compressed and expanded archive bytes, entries, documents, retained
text, and path length. Supported text/code extensions match the local policy,
plus case-insensitive `README`, `LICENSE`, `NOTICE`, `Makefile`, and `Dockerfile`.
Hidden paths except `.github`, dependencies/build outputs, credential-like names,
PDFs, binary/non-UTF-8 files, links/special entries, submodules, and Git LFS
pointers are skipped. Remote PDF and LFS content is not downloaded.

A missing selected directory, invalid archive, timeout, rate limit, identity
change, or incomplete operation preserves the previous snapshot. An existing
empty directory is a valid zero-document snapshot. SQLite stores repository and
selection identity, policy, SHA, commit timestamp, revision, bounded error state,
and plaintext contents. Normal open upgrades schema 1/2 to 3; read-only MCP never
migrates. GitHub `ModifiedAt` is snapshot commit time, not per-file modification.

Automated tests use an injected synthetic transport and generated archive
fixtures, so CI needs no GitHub quota. A manual live check is optional. See the
[short demo script](github-demo.md).
