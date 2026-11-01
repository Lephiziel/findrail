ALTER TABLE evidence_collection_entries
 ADD COLUMN preview_start_char INTEGER NOT NULL DEFAULT 0 CHECK(preview_start_char >= 0);
PRAGMA user_version=8;
