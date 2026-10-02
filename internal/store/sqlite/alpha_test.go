package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func TestMigrationPreservesSchemaOneIndex(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "findrail.db"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO sources(id,kind,name,root) VALUES('legacy','filesystem','Legacy','/legacy'); INSERT INTO documents(id,source_id,title,uri,path,content,content_hash,size_bytes,modified_at,scan_token) VALUES('old','legacy','legacy.md','file:///legacy/legacy.md','legacy.md','migration evidence','old',18,'2026-01-01T00:00:00Z','old')"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := find(t, s, "migration", "", 20); got.Total != 1 {
		t.Fatalf("old index lost: %+v", got)
	}
	sources, err := s.Sources(context.Background())
	if err != nil || len(sources) != 1 || sources[0].MaxTextBytes != 1<<20 || sources[0].MaxPDFBytes != 0 {
		t.Fatalf("migration source settings: %+v %v", sources, err)
	}
	evidence, err := s.Evidence(context.Background(), "old", 0)
	if err != nil || evidence.Text != "migration evidence" {
		t.Fatalf("legacy preview: %+v %v", evidence, err)
	}
}

func TestSearchReadsPreviousSnapshotDuringWrite(t *testing.T) {
	s := open(t)
	root := t.TempDir()
	write(t, filepath.Join(root, "notes.md"), "previous snapshot")
	_, conn := index(t, s, root)
	scan, err := s.BeginRefresh(context.Background(), conn.Source())
	if err != nil {
		t.Fatal(err)
	}
	defer scan.Rollback()
	if _, err := scan.Upsert(context.Background(), connector.Document{ID: "pending", SourceID: conn.Source().ID, Title: "next", Content: "pending update", Hash: "next"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := s.Search(ctx, search.Request{Query: "previous", Limit: 20})
	if err != nil || got.Total != 1 {
		t.Fatalf("read blocked or snapshot lost: %+v %v", got, err)
	}
	got, err = s.Search(ctx, search.Request{Query: "pending", Limit: 20})
	if err != nil || got.Total != 0 {
		t.Fatalf("partial update visible: %+v %v", got, err)
	}
	if _, err := scan.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := find(t, s, "pending", "", 20); got.Total != 1 {
		t.Fatalf("committed snapshot absent: %+v", got)
	}
}

func TestPDFEvidenceUpdateAndDelete(t *testing.T) {
	s := open(t)
	source := connector.Source{ID: "pdfsource", Kind: "test", Name: "PDF"}
	doc := connector.Document{ID: "pdfdoc", SourceID: source.ID, Title: "guide.pdf", URI: "file:///guide.pdf", Path: "guide.pdf", MediaType: "application/pdf", Content: "intro webhook retry", Hash: "v1", Pages: []connector.Page{{Number: 1, Text: "intro"}, {Number: 2, Text: "webhook retry"}}}
	if _, err := ingest.Run(context.Background(), s, collisionConnector{source, doc}); err != nil {
		t.Fatal(err)
	}
	got := find(t, s, "webhook", "", 20)
	if got.Total != 1 || got.Results[0].Page != 2 || !strings.HasSuffix(got.Results[0].URI, "#page=2") {
		t.Fatalf("page citation: %+v", got)
	}
	e, err := s.Evidence(context.Background(), doc.ID, 2)
	if err != nil || e.Text != "webhook retry" || e.PageCount != 2 {
		t.Fatalf("preview: %+v %v", e, err)
	}
	if _, err := s.Evidence(context.Background(), doc.ID, 3); !errors.Is(err, search.ErrPage) {
		t.Fatalf("page range: %v", err)
	}
	doc.Hash = "v2"
	doc.Content = "new evidence"
	doc.Pages = []connector.Page{{Number: 1, Text: "new evidence"}}
	if _, err := ingest.Run(context.Background(), s, collisionConnector{source, doc}); err != nil {
		t.Fatal(err)
	}
	if got := find(t, s, "webhook", "", 20); got.Total != 0 {
		t.Fatalf("stale PDF text: %+v", got)
	}
	if got := find(t, s, "evidence", "", 20); got.Total != 1 || got.Results[0].Page != 1 {
		t.Fatalf("page index update: %+v", got)
	}
	if err := s.ForgetSource(context.Background(), source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Evidence(context.Background(), doc.ID, 0); !errors.Is(err, search.ErrNotFound) {
		t.Fatalf("forgotten snapshot: %v", err)
	}
	if _, err := s.BeginRefresh(context.Background(), source); !errors.Is(err, ingest.ErrSourceGone) {
		t.Fatalf("watcher could recreate forgotten source: %v", err)
	}
	sources, err := s.Sources(context.Background())
	if err != nil || len(sources) != 0 {
		t.Fatalf("source resurrected: %+v %v", sources, err)
	}
}

func TestEvidenceBoundsAndSnapshot(t *testing.T) {
	s := open(t)
	root := t.TempDir()
	path := filepath.Join(root, "long.md")
	write(t, path, strings.Repeat("文", 65540))
	_, c := index(t, s, root)
	got := find(t, s, "long", "", 20).Results[0]
	write(t, path, "new content outside index")
	e, err := s.Evidence(context.Background(), got.ID, 0)
	if err != nil || !e.Truncated || len([]rune(e.Text)) != 65536 || strings.Contains(e.Text, "new content") {
		t.Fatalf("bounded snapshot: %d %+v %v", len([]rune(e.Text)), e.Truncated, err)
	}
	if _, err := s.Evidence(context.Background(), got.ID, 1); !errors.Is(err, search.ErrPage) {
		t.Fatalf("text page accepted: %v", err)
	}
	if err := s.ForgetSource(context.Background(), c.Source().ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Refresh(context.Background(), s, c); !errors.Is(err, ingest.ErrSourceGone) {
		t.Fatalf("refresh after forget: %v", err)
	}
}
