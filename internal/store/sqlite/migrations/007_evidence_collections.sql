CREATE TABLE evidence_collections (
 id TEXT PRIMARY KEY, title TEXT NOT NULL CHECK(length(CAST(title AS BLOB)) BETWEEN 1 AND 256),
 revision INTEGER NOT NULL CHECK(revision>0), created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE evidence_collection_entries (
 collection_id TEXT NOT NULL REFERENCES evidence_collections(id) ON DELETE CASCADE,
 entry_id TEXT NOT NULL, ordinal INTEGER NOT NULL CHECK(ordinal>=0),
 source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
 document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
 snapshot_json TEXT NOT NULL CHECK(length(CAST(snapshot_json AS BLOB))<=4194304),
 PRIMARY KEY(collection_id,entry_id), UNIQUE(collection_id,ordinal)
);
CREATE INDEX evidence_collection_entries_document ON evidence_collection_entries(document_id);
PRAGMA user_version=7;
