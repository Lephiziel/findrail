CREATE TABLE archive_sources (
    source_id TEXT PRIMARY KEY REFERENCES sources(id) ON DELETE CASCADE,
    origin_json TEXT NOT NULL,
    fingerprint TEXT NOT NULL UNIQUE,
    original_indexed_at TEXT NOT NULL,
    imported_at TEXT NOT NULL
);
ALTER TABLE documents ADD COLUMN origin_content_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE documents ADD COLUMN origin_document_id TEXT NOT NULL DEFAULT '';
CREATE INDEX archive_sources_fingerprint ON archive_sources(fingerprint);
PRAGMA user_version = 5;
