CREATE TABLE indexing_reports (
    source_id TEXT PRIMARY KEY REFERENCES sources(id) ON DELETE CASCADE,
    report_id TEXT NOT NULL UNIQUE,
    snapshot_id TEXT NOT NULL,
    report_json TEXT NOT NULL CHECK(length(CAST(report_json AS BLOB)) <= 65536),
    committed_at TEXT NOT NULL,
    operation TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    indexed_documents INTEGER NOT NULL CHECK(indexed_documents >= 0),
    skipped_files INTEGER NOT NULL CHECK(skipped_files >= 0),
    pruned_directories INTEGER NOT NULL CHECK(pruned_directories >= 0),
    coverage TEXT NOT NULL,
    reasons_json TEXT NOT NULL
);
PRAGMA user_version = 6;
