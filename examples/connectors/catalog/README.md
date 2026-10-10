# Educational catalogue connector

This separate `example.com` module demonstrates a read-only, full-inventory
paginated adapter. It uses only the standard library and Findrail's experimental
public connector packages. The relative `replace` in `go.mod` is for repository
development: the placeholder version is not published or remotely installable.

```sh
cd examples/connectors/catalog
GOWORK=off go test ./...
GOWORK=off go vet ./...
```

`catalog.New` validates an explicit HTTPS origin and collection without I/O.
Loopback HTTP requires `AllowLoopbackHTTP` for tests/demo. Requests use fixed
resource paths and opaque cursors; original HTTPS URIs are provenance only and
are never fetched. Budgets are 100 pages, 1,000 documents, 1 MiB per response,
256 KiB per body, 4 MiB aggregate body, a five-second request and 30-second host
scan deadline. No retries are performed. These are example policies, not global
Findrail extraction limits. There is no account, token, production registration,
or private-data revocation guarantee.

The demonstration transport is not a plugin protocol. Emitted documents remain
provisional until Scan returns successfully; a host must commit only a complete
inventory. The conformance test profile is `full-inventory-v1`, not a Findrail
version or API stability promise.
