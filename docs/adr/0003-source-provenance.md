# ADR 0003: provenance-preserving documents

Status: accepted.

Every document carries a stable source identity, original URI, relative path,
content hash, and modification metadata. Search exposes a matching passage and
the original location. A full source inventory publishes atomically; failed
enumerations do not prune.

Future passage retrieval retains offsets and document lineage. A generated
answer is not a replacement for original evidence.
