ALTER TABLE sources ADD COLUMN max_text_bytes INTEGER NOT NULL DEFAULT 1048576;
ALTER TABLE sources ADD COLUMN max_pdf_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE documents ADD COLUMN media_type TEXT NOT NULL DEFAULT 'text/plain';
ALTER TABLE documents ADD COLUMN page_count INTEGER NOT NULL DEFAULT 0;
CREATE TABLE document_pages (
    rowid INTEGER PRIMARY KEY,
    document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    page_number INTEGER NOT NULL CHECK(page_number > 0),
    content TEXT NOT NULL,
    UNIQUE(document_id, page_number)
);
CREATE VIRTUAL TABLE pages_fts USING fts5(
    content, content='document_pages', content_rowid='rowid',
    tokenize='unicode61 remove_diacritics 2'
);
CREATE TRIGGER pages_insert AFTER INSERT ON document_pages BEGIN
    INSERT INTO pages_fts(rowid, content) VALUES (new.rowid, new.content);
END;
CREATE TRIGGER pages_delete AFTER DELETE ON document_pages BEGIN
    INSERT INTO pages_fts(pages_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
END;
PRAGMA user_version = 2;
