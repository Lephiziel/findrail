# Connector guide

The `pkg/connector` API is experimental. It is a useful implementation seam, not
a stable plugin ABI or a remote execution protocol.

## Contract

`Source()` identifies one explicit collection. `Scan(ctx, emit)` performs a full
inventory and emits normalized UTF-8 documents. IDs must remain stable for the
same source resource, and each document must carry its source ID, original URI,
content hash, title, and path.

Returning success permits pruning old documents not seen in this inventory.
Therefore pagination failures, inaccessible subtrees, cancellation, malformed
remote responses, or exceeded extraction budgets must be treated deliberately.
An incomplete inventory must return an error. Never silently report an
incomplete enumeration as a complete source scan.

Unsupported formats can be skipped according to a documented policy. If that
policy changes, the next complete scan may remove previously indexed documents.

## Host responsibilities

The host owns transactions, retention, indexing, retrieval, result rendering,
authorization, and logical removal. Connectors should not write database tables,
edit source documents, store credentials in content, or send analytics.

## Public GitHub files

`internal/connectors/github` resolves a public ref to a commit, validates a
bounded tar/gzip inventory, and returns a prepared in-memory full scan. The host
publishes it atomically and stores its settings. See [GitHub snapshots](github.md).

## Filesystem example

Read `internal/connectors/filesystem/filesystem.go` and the lifecycle tests under
`internal/store/sqlite/`. The filesystem connector uses a selected root, skips
symlinks, reads through an `os.Root`, limits document bytes, and reports errors
that prevent partial pruning.

## Conformance kit

The contributor-facing bounded test kit is implemented at
[connector-conformance.md](connector-conformance.md), with a separate-module
paginated example at [examples/connectors/catalog](../examples/connectors/catalog/).
Its test-local profile is `full-inventory-v1`, not an API stability promise.
The kit checks observable fixture behavior, not arbitrary adapter safety or
complete remote inventory. Authentication revocation, incremental cursors,
dynamic loading and a stable plugin ABI are not implemented. No external
connector is registered automatically in production.
