# alpha.3 release-candidate rehearsal (not a release)

This document records local rehearsal work for a candidate. It does not mean
alpha.3 is published, reviewed, signed, or ready to announce. Current published
downloads remain alpha.2. No tag or release was created and
`build/package/VERSION` remains unchanged.

## Candidate scope

The package builder now emits BUILD-INFO.json, deterministic archive metadata,
and an explicit checksum manifest. The extracted Linux/amd64 candidate was
smoke-tested from a path containing spaces and Unicode, outside the repository.
The archive included the existing CLI, HTTP and MCP functionality, including
DOCX. `findrail doctor` offers read-only, redacted JSON/human diagnostics and
distinguishes an absent index from unsupported/corrupt data.

Candidate version: `0.1.0-alpha.3` (explicit build override only).
Source revision: `b414799e3cec45a70275c39bb952e478a065d18c`; candidate was built
from a clean checkout (`BUILD-INFO.json` marks provenance clean). Go toolchain:
`go version go1.27.1-X:nodwarf5 linux/amd64` (local rehearsal environment).
Current schema: 4 (migrations 1–4); legacy folder DOCX policy defaults disabled.

## Artifact and platform status

| Target | Build | Archive/checksum | Extracted native smoke | Browser QA |
|---|---|---|---|---|
| linux-amd64 | built | metadata/checksum passed | smoke.py + mcp_smoke.py + upgrade passed | not run |
| linux-arm64 | cross-built | metadata/checksum passed | not run (not native QA) | not run |
| darwin-amd64 | cross-built | metadata/checksum passed | not run (not native QA) | not run |
| darwin-arm64 | cross-built | metadata/checksum passed | not run (not native QA) | not run |
| windows-amd64 | cross-built | metadata/checksum passed | not run (not native QA) | not run |

Repository checks passed locally: `gofmt`, `git diff --check`, `go test ./...`,
`go test -race ./...`, `go vet ./...`, `go mod verify`, CGO-free build, UI Node
tests, and packaging unit tests. The required native CI matrix has not run yet.

Local artifact staging: `/tmp/opencode/alpha3-candidate-final4` (not committed and
not uploaded). A repeated native Linux/amd64 package run produced byte-identical
archive and checksum manifest. These results do not establish Gatekeeper
approval, Finder behavior, or browser interaction. Native matrix workflow
results are pending until CI runs.

## Upgrade / rollback contract and outstanding work

The published alpha.2 tag `v0.1.0-alpha.2` resolves to
`7573802a45962702a66eeecc8fb79a4ac2d680db`; its source contains migrations 001
and 002 only, so its index schema is 2. The packaged Linux binary was exercised
against a synthetic schema-2 index generated directly from those historical SQL
migrations (not by opening it with the current writable store). The journey
confirms source inventory, PDF page-2 FTS evidence, migration to schema 4, and
the legacy DOCX disabled default. The separate existing migration-3 lifecycle
test verifies GitHub revision metadata survives migration to schema 4. It copies
and hashes a stopped whole-directory backup and
restores that backup into a separate test location; it does not run an old binary
against schema 4. The packaged UI journey then explicitly enables legacy DOCX,
waits for the atomic Configure job, restarts, and verifies the indexed body and
persisted policy. No reverse migration is supported.

**Release blockers:** simulated migration-failure atomicity is covered by a
storage test, but not by fault injection in the packaged journey. Doctor has
canceled-context, busy-timeout, and read-only-permission test coverage; the busy
case passed locally, while permission enforcement depends on the unprivileged
CI runner. Native Windows/macOS/ARM64 jobs have not completed. Manual browser
interaction and macOS Gatekeeper/Finder behavior remain untested. No release
should be made based on this report.

## Future publication checklist

1. Review and merge the readiness change.
2. Wait for full main CI and package-native rehearsal to pass.
3. In a separately authorized change, update `build/package/VERSION` and
   published release notes.
4. Verify the alpha-release workflow's actual trigger: successful CI `push` to
   `main` can invoke it, and the workflow creates a GitHub prerelease when its
   version tag is absent.
5. Inspect every uploaded archive, embedded instructions, provenance and
   SHA256SUMS; verify installed journeys.
6. Only then update README downloads and press kit to the published release.
