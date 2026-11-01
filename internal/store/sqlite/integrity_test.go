package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Lephiziel/findrail/internal/store/sqlite"
	_ "modernc.org/sqlite"
)

func TestIntegrityCheckHealthyAndForeignKeyOrphan(t *testing.T) {
	for _, orphan := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy", true: "orphan"}[orphan], func(t *testing.T) {
			dir := t.TempDir()
			store, err := sqlite.Open(context.Background(), dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if orphan {
				db, err := sql.Open("sqlite", filepath.Join(dir, "findrail.db"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("PRAGMA foreign_keys=OFF; INSERT INTO evidence_collections(id,title,revision,created_at,updated_at) VALUES('c','c',1,'now','now'); INSERT INTO evidence_collection_entries(collection_id,entry_id,ordinal,source_id,document_id,snapshot_json) VALUES('c','e',0,'missing','missing','{}')"); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			readOnly, err := sqlite.OpenReadOnly(context.Background(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer readOnly.Close()
			got := readOnly.IntegrityCheck(context.Background())
			if !got.QuickCheck.Complete || !got.QuickCheck.Healthy {
				t.Fatalf("quick check: %+v", got.QuickCheck)
			}
			if !got.ForeignKey.Complete || got.ForeignKey.Healthy == orphan {
				t.Fatalf("foreign key check orphan=%v: %+v", orphan, got.ForeignKey)
			}
		})
	}
}

func TestIntegrityCheckPreCanceledIsIncomplete(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	readOnly, err := sqlite.OpenReadOnly(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := readOnly.IntegrityCheck(ctx)
	if got.QuickCheck.Complete || got.QuickCheck.Healthy || got.ForeignKey.Healthy {
		t.Fatalf("canceled checks reported healthy: %+v", got)
	}
}
