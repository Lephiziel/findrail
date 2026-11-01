ALTER TABLE sources ADD COLUMN max_docx_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sources ADD COLUMN registration_token TEXT NOT NULL DEFAULT '';
ALTER TABLE sources ADD COLUMN revision INTEGER NOT NULL DEFAULT 0;
UPDATE sources SET registration_token=lower(hex(randomblob(16))), revision=1;
PRAGMA user_version = 4;
