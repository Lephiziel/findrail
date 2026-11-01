package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Lephiziel/findrail/internal/store/sqlite"
	_ "modernc.org/sqlite"
)

func TestInspectSchemaReadOnlyMetadataAndTypedFailures(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := sqlite.InspectSchema(context.Background(), missing); !errors.Is(err, &sqlite.SchemaInspectionError{Code: sqlite.SchemaPathMissing}) {
		t.Fatalf("missing result: %v", err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspection created directory: %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.InspectSchema(context.Background(), file); !errors.Is(err, &sqlite.SchemaInspectionError{Code: sqlite.SchemaNotDirectory}) {
		t.Fatalf("file path result: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sqlite.InspectSchema(ctx, missing); !errors.Is(err, &sqlite.SchemaInspectionError{Code: sqlite.SchemaCanceled}) {
		t.Fatalf("canceled result: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "findrail.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE marker(value TEXT); INSERT INTO marker VALUES('untouched'); PRAGMA user_version=91"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := sqlite.InspectSchema(context.Background(), dir)
	if err != nil || info.IndexSchema != 91 || info.CurrentWriteSchema != sqlite.CurrentWriteSchema() {
		t.Fatalf("schema facts: %+v %v", info, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("metadata inspection changed database bytes")
	}
}
