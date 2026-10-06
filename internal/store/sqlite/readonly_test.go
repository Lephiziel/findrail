package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	_ "modernc.org/sqlite"
)

func TestOpenReadOnlyDoesNotCreateOrMigrate(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-created")
	if _, err := sqlite.OpenReadOnly(context.Background(), missing); err == nil {
		t.Fatal("opened a missing index")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing directory changed: %v", err)
	}

	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "findrail.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.OpenReadOnly(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "open or update") {
		t.Fatalf("schema 1 error = %v", err)
	}
}

func TestOpenReadOnlyScopedEvidenceAndWriteSafety(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	store, err := sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "note.md"), "read only evidence")
	_, connector := index(t, store, root)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	readOnly, err := sqlite.OpenReadOnly(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	result := find(t, readOnly, "evidence", connector.Source().ID, 20)
	if result.Total != 1 {
		t.Fatalf("search = %+v", result)
	}
	evidence, err := readOnly.EvidenceForSource(context.Background(), connector.Source().ID, result.Results[0].ID, 0)
	if err != nil || evidence.Text != "read only evidence" {
		t.Fatalf("scoped evidence = %+v, %v", evidence, err)
	}
	if _, err := readOnly.EvidenceForSource(context.Background(), "other", result.Results[0].ID, 0); !errors.Is(err, search.ErrNotFound) {
		t.Fatalf("foreign evidence error = %v", err)
	}
	if _, err := readOnly.BeginScan(context.Background(), connector.Source()); err == nil {
		t.Fatal("read-only store accepted a scan")
	}
	if err := readOnly.ForgetSource(context.Background(), connector.Source().ID); err == nil {
		t.Fatal("read-only store accepted forget")
	}
	if err := readOnly.Close(); err != nil {
		t.Fatal(err)
	}
	if err := readOnly.Close(); err != nil {
		t.Fatal("second close: ", err)
	}
}

func TestOpenReadOnlyEscapesDataDirectory(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("question mark is not a portable Windows filename")
	}
	dir := filepath.Join(t.TempDir(), "индекс #?")
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
	if _, err := readOnly.Sources(context.Background()); err != nil {
		t.Fatal(err)
	}
}
