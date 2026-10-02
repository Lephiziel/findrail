# Architecture decisions

Findrail keeps a local search index. SQLite FTS5 supplies keyword ranking and
matching passages. A connector emits normalized documents with source identity
and an original location. Semantic retrieval is planned as an optional layer.
