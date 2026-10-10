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
```

External adapters import `github.com/Lephiziel/findrail/pkg/connector` and
`.../conformance`. Call `conformance.Observe(ctx, adapter,
conformance.DefaultOptions())`, or use `conformance.Run(t, factory, options,
hooks)`. Factories should provide fresh synthetic fixtures. Hooks are optional
lifecycle coverage; absent cases are explicitly skipped. Invalid/zero limits
are rejected. Defaults bound 1,000 documents, 256 KiB content per document,
4 MiB retained content, 16 KiB strings of metadata per document, 4 MiB total
metadata, and page count. Context deadlines are cooperative; an uncooperative
goroutine cannot be killed safely, so use `go test -timeout` as the process
backstop. Failure codes/check names are safe; adapter errors are returned for
`errors.Is/As`, not rendered by the harness.

IDs must be source-bound and stable for the same logical resource. No universal
path or hash algorithm is imposed. Hashes should represent indexed body/page
text (and boundary changes); title/path-only changes can retain the hash while
metadata still changes. Preserve UTF-8 title/path/URI/media type, original
provenance and contiguous 1-based PDF pages. Empty successful inventory means
remove all; unsupported skips require an intentional documented policy and do
not excuse inaccessible subtrees. The suite is finite evidence over supplied
fixtures, not a sandbox or proof of arbitrary completeness, side effects,
private-data revocation, or asynchronous behavior.

The catalogue module shows fixed-origin opaque-cursor paging, explicit bounded
configuration, no constructor/source network I/O, and failure-safe scans. Its
synthetic fixture needs no account or network. `python3
scripts/connector_kit_check.py` also runs the tagged real SQLite host journey in
a temporary Go workspace. That repository-owned check uses internal ingestion
and SQLite; independent adapter authors need not read host internals.

| Check | Demonstrates | Does not prove |
|---|---|---|
| Bounded observation | Stable source, identity/provenance, UTF-8/pages/report, finite retained data | Every remote resource is complete |
| External module build | Public import boundary | Stable future API |
| Synthetic fixture | Cursor/snapshot and failure behavior | Private access revocation |
| SQLite lifecycle | Host commits successful inventory and preserves prior state on failure | Security sandbox |

To adapt the example, keep your service configuration explicit, model stable
resource identity, add fresh fixture scenarios and lifecycle hooks, then list
what those fixtures do not exercise. Incremental cursors, authenticated/private
connectors, dynamic loading and out-of-process protocols remain deferred.
