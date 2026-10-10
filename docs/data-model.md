# Data model

## Implemented schema 5

| Entity | Identity | Stored fields |
|---|---|---|
| Source | Filesystem root identity, GitHub selection identity, or verified archive fingerprint namespace | Kind, name, active root (empty for archives), last success/import, extraction limits, fresh registration token and revision |
| Document | Hash of destination source ID and relative path for imported snapshots | Title, source ID, original ID/hash provenance, original URI, path, full indexed content, destination content hash, byte size, modification time, media type, page count |
| FTS entry | Document row ID | Tokenized title and content, maintained by triggers |
| PDF page | Document ID + one-based page number | Plain text for the original page |
| GitHub snapshot | Source ID | Repository identity, ref/path policy, full SHA, commit time, registration token and revision |
| Page FTS | Page row ID | Page tokens, maintained by triggers |
| Archive source | Source ID | Original provenance JSON, semantic fingerprint, original successful indexing time, local import time |

Source ownership is currently the OS account running the local application.
There is no multi-user authorization model in the foundation.

## Search results

Results return a stable ID, original URI, source metadata, relative path,
matching passage, and a BM25-derived ordering score. The score is not a
probability. Square brackets mark keyword matches in plain-text snippets.

Migration 3 preserves schema 1/2 documents, FTS and PDF pages and adds GitHub
metadata with cascade deletion. Migration 2 leaves existing sources text-only until
explicitly re-indexed. Migration 4 adds DOCX policy with a disabled legacy default,
plus source registration guards for stale Configure/Remove operations.
Migration 5 adds cascaded frozen archive provenance and an original-content-hash
provenance field while retaining a separately computed destination content hash.
Schema 4 indexes upgrade transactionally; archive metadata is removed with its
source. No plaintext token or credential fields are introduced.
See [upgrade instructions](local-alpha.md#upgrade).

## Future entities

Passage chunks map to a document ID, extraction version, offsets, and content
hash. Embeddings map to a passage ID and provider / model fingerprint. Sync jobs
map to source IDs and cursor versions. Credential references point to an OS
credential vault; credentials must not be stored in document text or public
configuration files.

Any future private connector requires access-scope metadata, revocation rules,
and index invalidation. Add those guarantees before sharing indexed content
between users or clients.
