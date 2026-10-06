# Data handling and trust boundaries

## Current behaviour

- Local files are indexed only from explicit roots passed to `index`.
- Public GitHub files are downloaded only by explicit `index-github` or
  `refresh-github`; retrieval reads the committed local snapshot.
- The index contains extracted text, locations, source names, and timestamps.
- Runtime local search does not call a hosted model, analytics service, or source API.
- The database is plaintext, protected by local filesystem permissions rather than encryption.
- Common credential-like names, hidden entries, and symlinks are skipped. This is not a universal secret detector.
- Document contents are treated as text and never executed.
- HTTP is loopback-only with Host / Origin checks and no source-mutation endpoint.
- `forget` removes searchable source data logically. Old disk / WAL / backup bytes may remain.

## User controls

Choose a dedicated index directory, inspect `sources`, and remove a source with
`forget`. Original files stay untouched. Deleting the complete dedicated data
directory while Findrail is stopped removes the active index; secure disk erasure
depends on the OS and storage device.

## Connected adapters

Public GitHub file snapshots use no credentials and refresh manually. Future
private adapters need explicit scopes, credential vault integration, visible sync
status, deletion propagation, and revocation handling. API permissions at the
source must determine what can be indexed. Private-data support is not unlocked
just by accepting a token string.

## Planned AI access

An optional embedding / model service needs an explicit explanation of data
transmission. MCP clients receive only scoped, bounded retrieval results.
Source content is untrusted input: it may contain instructions, but those
instructions do not authorize actions. Findrail's initial AI-facing interface
will be read-only.

## Residual limits

Other processes running as the same OS user may access the local database or
loopback server. A malicious plugin, compromised account, or copied index can
expose data. A future encrypted-storage feature needs a separate key-management
design. The current browser interface does not provide network or multi-user
deployment security.
