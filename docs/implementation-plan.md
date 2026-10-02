# Implementation plan

## R00: repository foundation

Delivered components: module and executable, connector contract, bounded
filesystem adapter, SQLite migration, transactional ingestion, literal keyword
search, CLI, loopback HTTP, basic web UI, demo corpus, tests, and CI configuration.

Validation must include changed content, unchanged content, deletion, multiple
sources, source filtering, failed scans, Unicode, persistence after restart,
excluded entries, symlinks, and HTTP Host / Origin boundaries. CI results must not
be described as passing until a GitHub workflow actually completes.

## R01: local alpha tasks

| Task | Acceptance criteria |
|---|---|
| PDF text extractor | Bounded pages / bytes / time; text PDFs work; scanned PDFs are clearly unsupported; no silent empty indexing |
| Exclusion policy | User-editable ignore rules; explicit defaults; previews show inclusion decisions; rule-change reindex tested |
| File watching | Debounced events; periodic reconciliation; watcher overflow recovers; shutdown cancels jobs |
| Source health | Last success, pending work, failure, and freshness are visible without recording query contents |
| Evidence preview | Retrieve by indexed document ID; source confinement; content hash and modified time; safe text rendering |
| Query language | Documented phrases / filters; invalid queries yield useful errors; tests cover Unicode and punctuation |
| Evaluation corpus | Labeled tasks from public / consented documents; exact-match baseline; reproducible measurement commands |
| Packaging | Signed / checksummed release artifacts; installation instructions; platform smoke tests |

## R02: GitHub connector

Scope initially: README and repository documents, issues, and discussions.
Authentication is read-only and explicit. Private-source support is gated on a
credential-storage and authorization design; it is not inferred from a public
connector working.

The connector needs stable resource IDs, canonical URLs, pagination, cursor
checkpoints, rate-limit backoff, cancellation, and tests for changed / removed
resources. Deletions only publish after a trusted complete inventory or explicit
tombstones. A failed authentication check must mark the source unavailable and
follow the designed revocation policy rather than preserve leaked private access.

## R03: SDK and contributions

Freeze a versioned contract only after filesystem and GitHub experience. Provide
a connector manifest, error taxonomy, inventory vs incremental semantics,
conformance fixtures, and a complete sample adapter. External plugins should run
out of process with explicit capabilities; do not dynamically load arbitrary
code into the search server.

## R04: semantic retrieval

Design passage IDs, normalized extraction versions, document-to-chunk lineage,
embedding fingerprints, optional local provider integration, and deletion
propagation. Compare hybrid results against keyword-only on the same labeled
corpus. Publish failure cases and resource requirements.

## R05: access surfaces

Desktop: hotkey, keyboard navigation, platform file opener, update policy.
MCP: read-only tools, source scopes, response limits, source-backed passages, and
redaction rules. An MCP connection never grants an agent broader source access
than its declared scope.

## Definition of done

A feature has executable behaviour, documentation that matches that behaviour,
appropriate tests, no unexplained resource or privacy regression, and a migration
plan if storage changes. Roadmap items are not closed merely because directories
or interfaces exist.
