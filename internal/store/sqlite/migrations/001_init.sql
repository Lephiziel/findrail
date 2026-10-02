CREATE TABLE sources (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    root TEXT NOT NULL,
    last_indexed_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE documents (
    rowid INTEGER PRIMARY KEY,
    id TEXT NOT NULL UNIQUE,
    source_id TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    uri TEXT NOT NULL,
    path TEXT NOT NULL,
    content TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    modified_at TEXT NOT NULL,
    scan_token TEXT NOT NULL,
    UNIQUE(source_id, path)
);
CREATE INDEX documents_source ON documents(source_id);
CREATE VIRTUAL TABLE documents_fts USING fts5(
    title, content,
    content='documents', content_rowid='rowid',
    tokenize='unicode61 remove_diacritics 2'
);
CREATE TRIGGER documents_insert AFTER INSERT ON documents BEGIN
    INSERT INTO documents_fts(rowid, title, content) VALUES (new.rowid, new.title, new.content);
END;
CREATE TRIGGER documents_delete AFTER DELETE ON documents BEGIN
    INSERT INTO documents_fts(documents_fts, rowid, title, content) VALUES ('delete', old.rowid, old.title, old.content);
END;
CREATE TRIGGER documents_update AFTER UPDATE OF title, content ON documents BEGIN
    INSERT INTO documents_fts(documents_fts, rowid, title, content) VALUES ('delete', old.rowid, old.title, old.content);
    INSERT INTO documents_fts(rowid, title, content) VALUES (new.rowid, new.title, new.content);
END;
PRAGMA user_version = 1;
