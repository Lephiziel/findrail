package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/evidencebundle"
)

func TestBundleImportReturnsCommittedSnapshotWithoutReadPool(t *testing.T) {
	s, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b := evidencebundle.Bundle{Title: "Synthetic empty bundle"}
	preview, err := s.PreviewEvidenceBundle(t.Context(), b, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	// A successful writer transaction must not depend on a later pooled read.
	for i := 0; i < 4; i++ {
		conn, err := s.readers.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	c, err := s.ImportEvidenceBundle(ctx, b, map[string]string{}, "Imported atomically", preview.Binding)
	var count int
	if e := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM evidence_collections").Scan(&count); e != nil {
		t.Fatal(e)
	}
	if err != nil {
		t.Fatalf("import reported failure after writing %d collections: %v", count, err)
	}
	if count != 1 || c.ID == "" || c.Title != "Imported atomically" || c.Revision != 1 || c.Entries == nil || len(c.Entries) != 0 || c.CreatedAt == "" || c.UpdatedAt != c.CreatedAt {
		t.Fatalf("invalid committed collection snapshot: %+v count=%d", c, count)
	}
}
