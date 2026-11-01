package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSchemaSevenCollectionWindowsMigrationAndRollback(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	ev := passageDocument(t, s, "legacy", "first quote and second quote")
	c, err := s.CreateCollection(ctx, "Legacy collection")
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.SaveCollection(ctx, c.ID, c.Revision, c.Title, []CollectionEdit{{DocumentID: ev.ID, Excerpt: "second quote", OriginalIdentity: ev.OriginalIdentity, Note: "second note"}, {DocumentID: ev.ID, Excerpt: "first quote", OriginalIdentity: ev.OriginalIdentity, Note: "first note"}})
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the actual schema-7 shape and historical JSON, not a current
	// schema-8 database carrying a fabricated user_version alone.
	_, err = s.db.ExecContext(ctx, `UPDATE evidence_collection_entries SET snapshot_json=json_remove(snapshot_json,'$.evidence.text_start_char','$.evidence.text_total_chars','$.evidence.match_kind'); ALTER TABLE evidence_collection_entries DROP COLUMN preview_start_char; PRAGMA user_version=7`)
	if err != nil {
		t.Fatal(err)
	}
	var before string
	if err = s.db.QueryRowContext(ctx, "SELECT group_concat(snapshot_json,'|') FROM (SELECT snapshot_json FROM evidence_collection_entries ORDER BY ordinal)").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadCollection(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != c.Title || got.Revision != c.Revision || len(got.Entries) != 2 || got.Entries[0].Note != "second note" || got.Entries[1].Excerpt != "first quote" {
		t.Fatalf("legacy collection changed: %+v", got)
	}
	var after string
	var nonzero int
	if err = s.db.QueryRowContext(ctx, "SELECT group_concat(snapshot_json,'|') FROM (SELECT snapshot_json FROM evidence_collection_entries ORDER BY ordinal)").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM evidence_collection_entries WHERE preview_start_char!=0").Scan(&nonzero); err != nil {
		t.Fatal(err)
	}
	if before != after || nonzero != 0 || got.Entries[0].Evidence.TextStartChar != 0 {
		t.Fatal("migration rewrote legacy snapshots or offsets")
	}
	// A conflicting schema fails without claiming a completed migration or
	// dropping the pre-existing saved collection.
	if _, err = s.db.ExecContext(ctx, "PRAGMA user_version=7"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if broken, err := Open(ctx, dir); err == nil {
		broken.Close()
		t.Fatal("expected duplicate-column migration failure")
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "findrail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, count int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 7 {
		t.Fatalf("failed migration changed schema: %d %v", version, err)
	}
	if err = db.QueryRow("SELECT count(*) FROM evidence_collection_entries").Scan(&count); err != nil || count != 2 {
		t.Fatalf("failed migration changed entries: %d %v", count, err)
	}
}
