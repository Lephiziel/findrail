CREATE TABLE archive_sources (
    source_id TEXT PRIMARY KEY REFERENCES sources(id) ON DELETE CASCADE,
    origin_json TEXT NOT NULL,
    fingerprint TEXT NOT NULL UNIQUE,
    original_indexed_at TEXT NOT NULL,
    imported_at TEXT NOT NULL
);
CREATE INDEX archive_sources_fingerprint ON archive_sources(fingerprint);
PRAGMA user_version = 5;
