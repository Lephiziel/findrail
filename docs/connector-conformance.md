# Connector conformance kit

`pkg/connector` remains experimental. The public test profile
`full-inventory-v1` tests the existing `Source`/`Scan` contract; it is neither
Findrail v1.0 nor a compatibility promise. Successful full inventory authorizes
host pruning. Failed pages, cancellation, callback errors, malformed or
incomplete responses must fail; earlier callbacks are provisional.

From a clean clone (Go 1.26+):

```sh
python3 scripts/connector_kit_check.py
cd examples/connectors/catalog
GOWORK=off go test ./...
GOWORK=off go run ./cmd/catalog-demo
GOWORK=off go run ./cmd/catalog-scan -h
```

External adapters import `github.com/Lephiziel/findrail/pkg/connector` and
`.../conformance`. Call `conformance.Observe(ctx, adapter,
conformance.DefaultOptions())`, or use `conformance.Run(t, factory, options,
hooks)`. Factories should provide fresh synthetic fixtures. Hooks are optional
lifecycle coverage; absent cases are explicitly skipped. `Observe` checks one
capture; `CheckStableInventory`, `CheckExpectedInventory`,
`CheckSourceIsolation`, `CheckCallbackError`, `CheckPreCancellation`,
`CheckCancellationAfterFirst`, `CheckCancellationRecovery`,
`CheckConsumerIsolation`, `CheckContentHashChange` and
`CheckMetadataObservable` make focused checks available for fixture scenarios.
Invalid, overflowing and above-profile limits are rejected; callers may choose
smaller values. Defaults bound 1,000 documents, 256 KiB content per document,
4 MiB retained content (document plus PDF page text), 16 KiB non-content strings
per document, 4 MiB total retained metadata/overhead, and 1,000 page entries.
Accounting uses UTF-8 byte length plus conservative fixed charges of 128 bytes
per source, 256 bytes per document, and 32 bytes per PDF page entry. Capture is
checked before retaining a document. Each case deadline is finite (at most 30
seconds) and further bounded by the parent/test deadline. Context deadlines are
cooperative; an uncooperative
goroutine cannot be killed safely, so use `go test -timeout` as the process
backstop. Failure codes/check names are safe; adapter errors are returned for
`errors.Is/As`, not rendered by the harness.

Typical consumer test shape:

```go
func TestMyConnector(t *testing.T) {
    opts := conformance.DefaultOptions()
    opts.Limits.Documents = 40 // smaller bounded capture for this fixture
    conformance.Run(t, func() connector.Connector {
        return newSyntheticConnector() // fresh fixture per case
    }, opts, nil)
}
```

For mutable lifecycle behavior, populate hooks with independent fixtures for
body update, metadata update, one deletion, empty inventory, late-page failure,
recovery, and a second source. A late-failure hook must emit at least one
provisional document and return an error. Consumer callbacks own each emitted
document only for that call; retain a copy if needed. Preserve callback errors
with `%w`, propagate context into every I/O operation, and stop after callback
failure/cancellation. Use `errors.Is`/`errors.As` to inspect the cause; do not
print arbitrary remote errors or indexed document data in test output.

The runnable `ExampleObserve` and `TestExampleRunner` in the public package
demonstrate direct and testing-runner consumption. The runner applies mandatory
observable checks and names unsupported lifecycle fixtures as skipped rather
than passing them.

IDs must be source-bound and stable for the same logical resource. No universal
path or hash algorithm is imposed. Hashes should represent indexed body/page
text (and boundary changes); title/path-only changes can retain the hash while
metadata still changes. Preserve UTF-8 title/path/URI/media type, original
provenance and contiguous 1-based PDF pages. Empty successful inventory means
remove all; unsupported skips require an intentional documented policy and do
not excuse inaccessible subtrees. The suite is finite evidence over supplied
fixtures, not a sandbox or proof of arbitrary completeness, side effects,
private-data revocation, or asynchronous behavior.

Only actual extracted PDF page text belongs in `Document.Pages`; document-level
search hits and title-only PDF evidence are not page entries. The kit does not
require a SHA encoding, filesystem ID prefix, lexicographic callback order, or
path-derived rename policy. For path-derived identity, a rename is expected as
delete plus create; a stable remote resource ID may keep identity across a
title/path change.

The catalogue module shows fixed-origin opaque-cursor paging, explicit bounded
configuration, no constructor/source network I/O, and failure-safe scans. Its
synthetic fixture needs no account or network. `python3
scripts/connector_kit_check.py` also runs the tagged real SQLite host journey in
a temporary Go workspace. That repository-owned check uses internal ingestion
and SQLite; independent adapter authors need not read host internals.

| Check | Demonstrates | Does not prove |
|---|---|---|
| Bounded observation / expected-set check | UTF-8/provenance, unique source-bound IDs, report counters, PDF page sequence, limits, stable snapshots and expected completeness | Every remote resource is complete |
| Callback/cancellation checks | Sentinel wrapping, no continued callback, pre-cancel and cancel after acceptance | Killing non-cooperative goroutines or detecting all later asynchronous activity |
| External module build | Import and dependency boundary; actual public API compilation | Stable future API |
| Hostile synthetic API | Pagination, revision/collection, duplicate IDs, malformed/oversize/hostile input, access denial, callback and later-page failure | Live service behavior or private access revocation |
| SQLite lifecycle | Initial/unchanged/update/metadata/single-delete/empty, storage failure, late-page failure, cancellation, recovery, restart, isolation and forget/refresh behavior | Security sandbox or forensic erasure |

To adapt the example, keep your service configuration explicit, model stable
resource identity, add fresh fixture scenarios and lifecycle hooks, then list
what those fixtures do not exercise. Incremental cursors, authenticated/private
connectors, dynamic loading and out-of-process protocols remain deferred.
