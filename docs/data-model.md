# Data model

## Implemented schema 1

| Entity | Identity | Stored fields |
|---|---|---|
| Source | Hash of canonical filesystem root | Kind, name, root, last successful indexing time |
| Document | Hash of source ID and relative path | Title, source ID, original URI, path, content, hash, byte size, modification time, scan token |
| FTS entry | Document row ID | Tokenized title and content, maintained by triggers |

Source ownership is currently the OS account running the local application.
There is no multi-user authorization model in the foundation.

## Search results

Results return a stable ID, original URI, source metadata, relative path,
matching passage, and a BM25-derived ordering score. The score is not a
probability. Square brackets mark keyword matches in plain-text snippets.

## Future entities

Passage chunks map to a document ID, extraction version, offsets, and content
hash. Embeddings map to a passage ID and provider / model fingerprint. Sync jobs
map to source IDs and cursor versions. Credential references point to an OS
credential vault; credentials must not be stored in document text or public
configuration files.

Any future private connector requires access-scope metadata, revocation rules,
and index invalidation. Add those guarantees before sharing indexed content
between users or clients.
