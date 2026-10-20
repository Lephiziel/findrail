package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/pkg/connector"
)

// This plan probe is intentionally synthetic and diagnostic rather than a
// brittle assertion on SQLite's version-specific plan wording.
func TestSearchSyntheticQueryPlans(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	scan, err := s.BeginScan(ctx, connector.Source{ID: "plan-source", Kind: "filesystem", Name: "Synthetic", Root: "/synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		body := "commonmark body synthetic"
		media := "text/plain"
		pages := []connector.Page(nil)
		if i%10 == 0 {
			media = "application/pdf"
			pages = []connector.Page{{Number: 1, Text: "commonmark early"}, {Number: 2, Text: "commonmark late"}}
			body += " early late"
		}
		_, err := scan.Upsert(ctx, connector.Document{ID: fmt.Sprintf("plan-%03d", i), SourceID: "plan-source", Title: "Synthetic title", URI: "file:///synthetic/doc", Path: fmt.Sprintf("docs/%03d", i), Content: body, Hash: fmt.Sprintf("hash-%d", i), ModifiedAt: time.Unix(int64(i+1), 0), MediaType: media, Pages: pages})
		if err != nil {
			_ = scan.Rollback()
			t.Fatal(err)
		}
	}
	if _, err := scan.Upsert(ctx, connector.Document{ID: "literal-wildcard-path", SourceID: "plan-source", Title: "Wildcard literal", URI: "file:///synthetic/literal", Path: "docs/100%_literal/file.md", Content: "commonmark literal path", Hash: "literal-path-hash", ModifiedAt: time.Unix(500, 0), MediaType: "text/plain"}); err != nil {
		_ = scan.Rollback()
		t.Fatal(err)
	}
	if _, err := scan.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := s.SearchPage(ctx, search.PageRequest{Request: search.Request{Query: "commonmark", Limit: 20, PathPrefix: "docs/100%_literal"}})
	if err != nil || page.Total != 1 || len(page.Results) != 1 || page.Results[0].ID != "literal-wildcard-path" {
		t.Fatalf("percent/underscore path must be literal: page=%+v err=%v", page, err)
	}
	plans := []struct {
		name, query string
		args        []any
	}{
		{"fts_exact_count", `EXPLAIN QUERY PLAN SELECT COUNT(*) FROM documents_fts JOIN documents d ON d.rowid=documents_fts.rowid WHERE documents_fts MATCH ?`, []any{"commonmark"}},
		{"ranked_rows", `EXPLAIN QUERY PLAN SELECT d.id,snippet(documents_fts,1,'[',']','…',32),bm25(documents_fts,4.0,1.0) FROM documents_fts JOIN documents d ON d.rowid=documents_fts.rowid JOIN sources s ON s.id=d.source_id WHERE documents_fts MATCH ? ORDER BY bm25(documents_fts,4.0,1.0),d.id LIMIT ?`, []any{"commonmark", 20}},
		{"pdf_page_attribution", `EXPLAIN QUERY PLAN SELECT p.page_number,snippet(pages_fts,0,'[',']','…',32) FROM pages_fts JOIN document_pages p ON p.rowid=pages_fts.rowid WHERE pages_fts MATCH ? AND p.document_id=? ORDER BY bm25(pages_fts),p.page_number LIMIT 1`, []any{"commonmark", "plan-000"}},
	}
	for _, plan := range plans {
		t.Run(plan.name, func(t *testing.T) {
			rows, err := s.readers.QueryContext(ctx, plan.query, plan.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				t.Logf("id=%d parent=%d detail=%s", id, parent, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
