package sqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/pkg/connector"

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
	if _, err := readOnly.EvidenceForSource(context.Background(), "", result.Results[0].ID, 0); !errors.Is(err, search.ErrNotFound) {
		t.Fatalf("empty scope bypassed evidence restriction: %v", err)
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
	name := "индекс #"
	if os.PathSeparator != '\\' {
		name += "?"
	}
	dir := filepath.Join(t.TempDir(), name)
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

func TestOpenReadOnlyPreservesDatabaseAndPermissions(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "findrail.db")
	if os.PathSeparator != '\\' {
		if err := os.Chmod(path, 0640); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	readOnly, err := sqlite.OpenReadOnly(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readOnly.Sources(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := readOnly.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	newInfo, _ := os.Stat(path)
	if !bytes.Equal(before, after) || info.Mode().Perm() != newInfo.Mode().Perm() {
		t.Fatal("read-only open changed database bytes or permissions")
	}
	for _, version := range []int{0, 1, 2, 4} {
		t.Run(fmt.Sprintf("schema%d", version), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "findrail.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(fmt.Sprintf("CREATE TABLE marker(value TEXT); INSERT INTO marker VALUES('preserved'); PRAGMA user_version=%d", version)); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			if _, err := sqlite.OpenReadOnly(context.Background(), dir); err == nil {
				t.Fatal("opened incompatible schema")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("incompatible schema was changed")
			}
		})
	}
	empty := t.TempDir()
	if _, err := sqlite.OpenReadOnly(context.Background(), empty); err == nil {
		t.Fatal("opened missing database")
	}
	if _, err := os.Stat(filepath.Join(empty, "findrail.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created missing database")
	}
}

func TestReadOnlySnapshotUpdatesAndScopedPDF(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writer, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	sourceA := connector.Source{ID: "a", Kind: "test", Name: "Allowed", MaxTextBytes: 1 << 20}
	sourceB := connector.Source{ID: "b", Kind: "test", Name: "Other", MaxTextBytes: 1 << 20}
	doc := connector.Document{ID: "doc-a", SourceID: "a", Title: "guide.pdf", URI: "file:///guide.pdf", Path: "guide.pdf", MediaType: "application/pdf", Content: "intro old evidence", Hash: "v1", ModifiedAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), Pages: []connector.Page{{Number: 1, Text: "intro"}, {Number: 2, Text: "old evidence"}}}
	put := func(source connector.Source, doc connector.Document) {
		t.Helper()
		scan, err := writer.BeginScan(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		defer scan.Rollback()
		if _, err := scan.Upsert(ctx, doc); err != nil {
			t.Fatal(err)
		}
		if _, err := scan.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	put(sourceA, doc)
	foreign := doc
	foreign.ID, foreign.SourceID, foreign.Content, foreign.Hash = "doc-b", "b", "PRIVATE_FOREIGN", "private"
	foreign.Pages = []connector.Page{{Number: 1, Text: "PRIVATE_FOREIGN"}}
	put(sourceB, foreign)
	reader, err := sqlite.OpenReadOnly(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, id := range []string{"doc-b", "missing"} {
		e, err := reader.EvidenceForSource(ctx, "a", id, 999)
		if !errors.Is(err, search.ErrNotFound) || e.Text != "" || e.URI != "" {
			t.Fatalf("foreign/missing identity escaped scope: %+v %v", e, err)
		}
	}
	read := func(text, hash string) {
		t.Helper()
		e, err := reader.EvidenceForSource(ctx, "a", "doc-a", 2)
		if err != nil || e.Text != text || e.ContentHash != hash || e.Page != 2 || e.PageCount != 2 || e.URI != "file:///guide.pdf#page=2" || e.ModifiedAt != doc.ModifiedAt.Format(time.RFC3339Nano) {
			t.Fatalf("snapshot mismatch: %+v %v", e, err)
		}
	}
	read("old evidence", "v1")
	scan, err := writer.BeginRefresh(ctx, sourceA)
	if err != nil {
		t.Fatal(err)
	}
	defer scan.Rollback()
	doc.Hash, doc.Content, doc.Pages[1].Text = "v2", "intro new evidence", "new evidence"
	if _, err := scan.Upsert(ctx, doc); err != nil {
		t.Fatal(err)
	}
	read("old evidence", "v1")
	if got := find(t, reader, "new", "a", 10); got.Total != 0 {
		t.Fatal("uncommitted text visible")
	}
	if _, err := scan.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	read("new evidence", "v2")
	if got := find(t, reader, "new", "a", 10); got.Total != 1 {
		t.Fatal("new committed text absent")
	}
	for range 5 {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := reader.EvidenceForSource(cancelled, "a", "doc-a", 2); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled evidence: %v", err)
		}
	}
	read("new evidence", "v2")
	if err := writer.ForgetSource(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.EvidenceForSource(ctx, "a", "doc-a", 2); !errors.Is(err, search.ErrNotFound) {
		t.Fatalf("deleted document: %v", err)
	}
	sources, err := reader.Sources(ctx)
	if err != nil || len(sources) != 1 || sources[0].ID != "b" {
		t.Fatalf("deleted source remains: %+v %v", sources, err)
	}
	if _, err := reader.BeginRefresh(ctx, sourceB); err == nil {
		t.Fatal("read-only refresh accepted")
	}
}
