ALTER TABLE sources ADD COLUMN max_docx_bytes INTEGER NOT NULL DEFAULT 0;
PRAGMA user_version = 4;
