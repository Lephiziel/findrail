# Optional semantic retrieval design

Planned; current ranking is SQLite FTS5 BM25.

Keyword search remains available without models. A semantic worker will chunk
extracted documents, generate embeddings through an explicitly selected local
or remote provider, and record model ID, model revision, chunk offsets, source
scope, and content hash. Remote providers require a clear explanation of the
text sent to them.

Document changes invalidate stale vectors; source removal removes its chunks and
vectors. Combine semantic and keyword candidates only after a reproducible
evaluation on synthetic / public tasks shows improved source recovery. Expose
passages and original locations instead of treating similarity as certainty.

Model acquisition, storage size, cancellation, and hardware requirements belong
in the user-visible configuration and documentation.
