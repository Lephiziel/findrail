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

## Filesystem example

Read `internal/connectors/filesystem/filesystem.go` and the lifecycle tests under
`internal/store/sqlite/`. The filesystem connector uses a selected root, skips
symlinks, reads through an `os.Root`, limits document bytes, and reports errors
that prevent partial pruning.

## Planned conformance suite

The future suite will cover deterministic IDs, pagination, unchanged content,
metadata-only updates, deletions, cancellation, failed scans, authentication
revocation, and source isolation. Incremental connectors will get a separate
cursor / tombstone contract; do not overload full-inventory success semantics.
