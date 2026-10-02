# ADR 0002: embedded SQLite and FTS5

Status: accepted for the foundation.

SQLite holds metadata and extracted text; FTS5 supplies lexical ranking and
passages. The modernc driver permits a CGO-free build. Schema and FTS changes
share transactions, making source updates and pruning atomic.

Costs include a substantial compiled driver, a limited baseline tokenizer,
plaintext storage, and a serialized foundation connection. Measure index size,
latency, build footprint, and concurrent reads before changing the engine.
