# Educational catalogue connector

This separate `example.com` module demonstrates a read-only, full-inventory
paginated adapter. It uses only the standard library and Findrail's experimental
public connector packages. The relative `replace` in `go.mod` is for repository
development: the placeholder version is not published or remotely installable.

```sh
cd examples/connectors/catalog
GOWORK=off go test ./...
GOWORK=off go vet ./...
GOWORK=off go run ./cmd/catalog-demo
GOWORK=off go run ./cmd/catalog-scan -h
```

`catalog.New` validates an explicit HTTPS origin, collection, and
`RequestTimeout` (positive and at most five seconds) without I/O.
Loopback HTTP requires `AllowLoopbackHTTP` for tests/demo. Redirects are rejected,
the transport does not consult ambient proxies, and there is no credential lookup.
Requests use fixed resource paths and opaque cursors; original HTTPS URIs are
provenance only and are never fetched. Budgets are 100 pages, 1,000 documents,
1 MiB per response, 16 KiB metadata per document, 256 KiB per body, 4 MiB
aggregate body/response budget, a five-second request and 30-second scan
deadline. No retries are performed. These are example policies, not global
Findrail extraction limits.

Synthetic page shape (unknown additive fields are ignored):

```json
{"collection":"demo","snapshot":"rev-1","complete":false,"next":"opaque-cursor","items":[{"id":"resource-1","title":"Example","path":"guide/start.txt","uri":"https://catalog.example/guide/start","body":"UTF-8 text","modified":"2026-01-01T00:00:00Z"}]}
```

Each page must repeat the collection and snapshot revision. `complete:true`
requires an empty `next`; otherwise `next` is a bounded opaque token, never a URL.
IDs hash the configured origin, collection and resource ID using SHA-256 with a
domain separator. Content hashes use SHA-256 over `catalog-content-v1`, a domain
separator and normalized body UTF-8. Title/path/URI-only changes preserve that
hash. 401/403 return a typed safe `UnavailableError`; no retry or private
credential flow is implemented. There is no production registration or private
data revocation guarantee. The example has no intentional per-document skip
class: an unsupported/malformed resource fails the inventory rather than being
silently treated as absent.

`catalog-demo` uses an OS-assigned loopback port and an in-memory synthetic
fixture. `catalog-scan` requires explicit `--origin` and `--collection`; it emits
a source header and bounded JSON document stream, with a completion record only
after Scan succeeds. `--allow-loopback-http` is a fixture-only explicit opt-in.
A failed stream can contain provisional documents; accept
it only if the process exits successfully and the completion record is present.

The demonstration transport is not a plugin protocol. Emitted documents remain
provisional until Scan returns successfully; a host must commit only a complete
inventory. The conformance test profile is `full-inventory-v1`, not a Findrail
version or API stability promise.
