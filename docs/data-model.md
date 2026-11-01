# Data model

> Schema 8 describes the current source candidate after migration 008; it is not
> the published alpha.4 format. Published alpha.4 archives remain on schema 7.

## Implemented schema 8

Migration 008 adds `preview_start_char` to evidence collection entries, defaulting
legacy citations to the prefix (0). Stored citation JSON and the column retain
the same window start. Passage windows contain no more than 65,536 Unicode code
points or 256 KiB UTF-8; automatic FTS anchoring reads at most 1 MiB of indexed
text. A larger body reports an unavailable automatic anchor rather than claiming
that no match exists.

| Entity | Identity | Stored fields |
|---|---|---|
| Source | Filesystem root identity, GitHub selection identity, or verified archive fingerprint namespace | Kind, name, active root (empty for archives), last success/import, extraction limits, fresh registration token and revision |
| Document | Hash of destination source ID and relative path for imported snapshots | Title, source ID, original ID/hash provenance, original URI, path, full indexed content, destination content hash, byte size, modification time, media type, page count |
| FTS entry | Document row ID | Tokenized title and content, maintained by triggers |
| PDF page | Document ID + one-based page number | Plain text for the original page |
| GitHub snapshot | Source ID | Repository identity, ref/path policy, full SHA, commit time, registration token and revision |
| Page FTS | Page row ID | Page tokens, maintained by triggers |
| Archive source | Source ID | Original provenance JSON, semantic fingerprint, original successful indexing time, local import time |
| Indexing report | Source ID (one current report) | Bounded committed report JSON, unique report/snapshot IDs and commit time; cascades on source removal |
| Evidence collection | Random collection ID | UTF-8 name, optimistic revision and timestamps; explicit create/save/delete |
| Collection entry | Collection + random entry key | Immutable bounded evidence snapshot JSON, source/document binding, passage, note and order; cascades on source Forget/document prune |

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
Migration 6 adds the bounded last-successful indexing report. It does not fabricate
reports for existing sources or change portable snapshot v1.
Migration 7 adds persistent named collections. Collection writes are bounded to
4 MiB. Entry rows cascade on source/document deletion and retain historical snapshots across
same-ID content refresh; revision checks prevent stale tab writes. Source snapshot
v1 remains source-only and does not export collection data.
Migration 8 adds the nonnegative `preview_start_char` column; existing entries
start at zero. Window-aware citations preserve their indexed offset while keeping
the full document content hash and URI provenance unchanged.
See [upgrade instructions](local-alpha.md#upgrade).

An alpha.5 source/CI candidate migrates schema 7 forward transactionally. Back up
the complete data directory while all Findrail processes are stopped. The
published alpha.4 binary cannot open schema 8; rollback means restoring a separate
complete schema-7 backup and opening that copy with alpha.4, never downgrading the
upgraded database in place.

## Future entities

Passage chunks map to a document ID, extraction version, offsets, and content
hash. Embeddings map to a passage ID and provider / model fingerprint. Sync jobs
map to source IDs and cursor versions. Credential references point to an OS
credential vault; credentials must not be stored in document text or public
configuration files.

Any future private connector requires access-scope metadata, revocation rules,
and index invalidation. Add those guarantees before sharing indexed content
between users or clients.
