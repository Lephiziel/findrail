package sqlite

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func TestSearchPageTxWalks237StableRanksAndMatchesSearch(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := connector.Source{ID: "page-query-source", Kind: "filesystem", Name: "Synthetic", Root: "/synthetic"}
	scan, err := store.BeginScan(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 237; i++ {
		id := fmt.Sprintf("doc-%03d", i)
		text := "shared needle résumé"
		d := connector.Document{ID: id, SourceID: source.ID, Title: "Shared note", Path: fmt.Sprintf("資料/δ-%03d.md", i), URI: "file:///synthetic/資料/δ", Content: text, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(text))), ModifiedAt: time.Unix(int64(i), 0), MediaType: "text/plain"}
		if _, err := scan.Upsert(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := scan.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	request := search.Request{Query: "shared needle", Limit: 20}
	expected := make([]string, 0, 237)
	for i := 0; i < 237; i++ {
		expected = append(expected, fmt.Sprintf("doc-%03d", i))
	}
	for _, limit := range []int{1, 20, 100} {
		request.Limit = limit
		gotIDs := []string{}
		for offset := 0; offset < search.PageWindowLimit && offset < len(expected); offset += limit {
			tx, e := store.readers.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			page, e := searchPageTx(ctx, tx, request, offset)
			if e != nil {
				tx.Rollback()
				t.Fatal(e)
			}
			if page.Total != 237 || page.Offset != offset || page.Returned != len(page.Results) {
				tx.Rollback()
				t.Fatalf("bad page metadata: %+v", page)
			}
			for _, r := range page.Results {
				gotIDs = append(gotIDs, r.ID)
			}
			if e = tx.Rollback(); e != nil {
				t.Fatal(e)
			}
		}
		if len(gotIDs) != len(expected) {
			t.Fatalf("limit %d got %d IDs", limit, len(gotIDs))
		}
		for i := range expected {
			if gotIDs[i] != expected[i] {
				t.Fatalf("limit %d index %d got %s want %s", limit, i, gotIDs[i], expected[i])
			}
		}
	}
	request.Limit = 100
	tx, err := store.readers.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	page, err := searchPageTx(ctx, tx, request, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	old, err := store.Search(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	for i := range old.Results {
		a, b := old.Results[i], page.Results[i].Result
		if a.ID != b.ID || a.Score != b.Score || a.SourceID != b.SourceID || a.OriginalIdentity != b.OriginalIdentity || a.Snippet != b.Snippet {
			t.Fatalf("legacy search differs at %d: %+v / %+v", i, a, b)
		}
	}
}

func TestSearchPageTxFiltersBoundsSnippetAndCancellation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := connector.Source{ID: "page-filter-source", Kind: "filesystem", Name: "Synthetic", Root: "/synthetic"}
	scan, err := store.BeginScan(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("界", 1100) + " marker"
	for _, d := range []connector.Document{{ID: "keep", SourceID: source.ID, Title: "Unicode marker title", Path: "資料/keep.md", URI: "file:///synthetic/keep", Content: long, Hash: "hash-keep", ModifiedAt: time.Now(), MediaType: "text/plain"}, {ID: "boundary", SourceID: source.ID, Title: "Marker", Path: "docs-other/a.md", URI: "file:///synthetic/a", Content: "marker", Hash: "hash-boundary", ModifiedAt: time.Now(), MediaType: "text/plain"}} {
		if _, err := scan.Upsert(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := scan.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	request := search.Request{Query: "marker", Limit: 20, PathPrefix: "資料", TitleContains: "Unicode", Format: "text", SourceID: source.ID}
	tx, err := store.readers.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	page, err := searchPageTx(ctx, tx, request, 0)
	if err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	if page.Total != 1 || len(page.Results) != 1 || page.Results[0].ID != "keep" || !page.Results[0].SnippetTruncated || utf8.RuneCountInString(page.Results[0].Snippet) > 1024 || !utf8.ValidString(page.Results[0].Snippet) {
		t.Fatalf("filtered/capped page: %+v", page)
	}
	request.PathPrefix = "missing"
	tx, err = store.readers.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := searchPageTx(ctx, tx, request, 0)
	tx.Rollback()
	if err != nil || empty.Total != 0 || empty.Results == nil || len(empty.Results) != 0 {
		t.Fatalf("empty result=%+v err=%v", empty, err)
	}
	for _, offset := range []int{-1, 1000} {
		tx, e := store.readers.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		_, e = searchPageTx(ctx, tx, request, offset)
		tx.Rollback()
		if e == nil {
			t.Fatalf("offset %d accepted", offset)
		}
	}
	for _, limit := range []int{0, 101} {
		request.Limit = limit
		tx, e := store.readers.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		_, e = searchPageTx(ctx, tx, request, 0)
		tx.Rollback()
		if e == nil {
			t.Fatalf("limit %d accepted", limit)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	request.Limit = 20
	tx, err = store.readers.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = searchPageTx(canceled, tx, request, 0)
	tx.Rollback()
	if err == nil {
		t.Fatal("canceled query succeeded")
	}
}

func TestSearchPageTxUsesPassedSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir := t.TempDir()
	store, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := connector.Source{ID: "snapshot-page-source", Kind: "filesystem", Name: "Synthetic", Root: "/synthetic"}
	seed := func(updated bool) {
		t.Helper()
		pages := []connector.Page{{Number: 1, Text: "snapshotneedle old evidence"}}
		if updated {
			source.Name = "Changed source"
			pages = []connector.Page{{Number: 1, Text: "unrelated replacement"}, {Number: 2, Text: "snapshotneedle new evidence"}}
		}
		scan, e := store.BeginScan(ctx, source)
		if e != nil {
			t.Fatal(e)
		}
		defer scan.Rollback()
		body := pages[0].Text
		if updated {
			body += "\n" + pages[1].Text
		}
		doc := connector.Document{ID: "snapshot-doc", SourceID: source.ID, Title: "Snapshot", Path: "snapshot.pdf", URI: "file:///synthetic/snapshot.pdf", Content: body, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(body))), ModifiedAt: time.Unix(1, 0), MediaType: "application/pdf", Pages: pages}
		if _, e = scan.Upsert(ctx, doc); e != nil {
			t.Fatal(e)
		}
		if updated {
			doc = connector.Document{ID: "snapshot-extra", SourceID: source.ID, Title: "Extra", Path: "extra.md", URI: "file:///synthetic/extra.md", Content: "snapshotneedle extra", Hash: "extra-hash", MediaType: "text/plain"}
			if _, e = scan.Upsert(ctx, doc); e != nil {
				t.Fatal(e)
			}
		}
		if _, e = scan.Commit(ctx); e != nil {
			t.Fatal(e)
		}
	}
	seed(false)
	request := search.Request{Query: "snapshotneedle", Limit: 20}
	old, err := store.Search(ctx, request)
	if err != nil || old.Total != 1 || len(old.Results) != 1 || old.Results[0].Page != 1 {
		t.Fatalf("old snapshot: %+v, %v", old, err)
	}
	tx, err := store.readers.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var before int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM documents").Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Commit changed rows, source registration and PDF pages after the caller's
	// read snapshot is established. A helper that uses a pooled reader must fail.
	seed(true)
	page, err := searchPageTx(ctx, tx, request, 0)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	var after int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM documents").Scan(&after); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if before != 1 || after != 1 || page.Total != 1 || len(page.Results) != 1 || !reflect.DeepEqual(page.Results[0].Result, old.Results[0]) {
		t.Fatalf("snapshot changed inside tx: before=%d after=%d page=%+v", before, after, page)
	}
	current, err := store.Search(ctx, request)
	if err != nil || current.Total != 2 || len(current.Results) != 2 {
		t.Fatalf("writer did not commit independently: %+v, %v", current, err)
	}
	for _, result := range current.Results {
		if result.ID == "snapshot-doc" && (result.Page != 2 || result.OriginalIdentity == old.Results[0].OriginalIdentity || result.SourceName != "Changed source") {
			t.Fatalf("new snapshot not visible: %+v", result)
		}
	}
}

func TestSearchPageTxBoundsWindowAndAdvancedPDFAttribution(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := connector.Source{ID: "page-window-source", Kind: "filesystem", Name: "Synthetic", Root: "/synthetic"}
	scan, err := store.BeginScan(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1001; i++ {
		body := "windowtoken common"
		if _, err := scan.Upsert(ctx, connector.Document{ID: fmt.Sprintf("window-%04d", i), SourceID: source.ID, Title: "Window document", Path: fmt.Sprintf("window/%04d.md", i), URI: "file:///synthetic/window", Content: body, Hash: fmt.Sprintf("window-hash-%d", i), ModifiedAt: time.Unix(int64(i), 0), MediaType: "text/plain"}); err != nil {
			t.Fatal(err)
		}
	}
	pdfBody := "pdfalpha other\nphrase here pdfomega"
	if _, err = scan.Upsert(ctx, connector.Document{ID: "window-pdf", SourceID: source.ID, Title: "PDF title", Path: "資料/report.pdf", URI: "file:///synthetic/report.pdf", Content: pdfBody, Hash: "pdf-hash", ModifiedAt: time.Unix(2000, 0), MediaType: "application/pdf", Pages: []connector.Page{{Number: 1, Text: "pdfalpha other"}, {Number: 2, Text: "phrase here pdfomega"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = scan.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	request := search.Request{Query: "windowtoken", Limit: 20}
	tx, err := store.readers.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := searchPageTx(ctx, tx, request, 0)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	last, err := searchPageTx(ctx, tx, request, 990)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if first.Total != 1001 || len(first.Results) != 20 || last.Total != 1001 || last.Returned != 10 || last.Offset != 990 {
		tx.Rollback()
		t.Fatalf("window page metadata first=%+v last=%+v", first, last)
	}
	if _, err = searchPageTx(ctx, tx, request, 1000); err == nil {
		tx.Rollback()
		t.Fatal("offset 1000 accepted")
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query, mode string
		total, page int
	}{
		{`"phrase here"`, "advanced", 1, 2},
		{`pdfalpha OR pdfomega`, "advanced", 1, 1},
		{`pdfomega OR pdfalpha`, "advanced", 1, 2},
		{`pdfalpha -absent`, "advanced", 1, 1},
		{`pdfalpha -pdfomega`, "advanced", 0, 0},
		{`pdfome*`, "advanced", 1, 2},
		{`"other phrase"`, "advanced", 0, 0},
		{`"PDF title"`, "advanced", 1, 0},
		{`PDF title`, "", 1, 0},
		{`pdfomega`, "", 1, 2},
	} {
		query := tc.query
		r := search.Request{Query: query, Mode: tc.mode, Limit: 20, Format: "pdf"}
		tx, e := store.readers.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		got, e := searchPageTx(ctx, tx, r, 0)
		if e != nil {
			tx.Rollback()
			t.Fatalf("%q: %v", query, e)
		}
		if e = tx.Rollback(); e != nil {
			t.Fatal(e)
		}
		if got.Total != tc.total || len(got.Results) != tc.total || (tc.total > 0 && got.Results[0].Page != tc.page) {
			t.Fatalf("%q unexpected PDF evidence: %+v, want total %d page %d", query, got, tc.total, tc.page)
		}
		old, e := store.Search(ctx, r)
		if e != nil {
			t.Fatalf("legacy %q: %v", query, e)
		}
		if len(got.Results) != len(old.Results) {
			t.Fatalf("%q page count differs: %+v / %+v", query, got, old)
		}
		for i := range old.Results {
			a, b := old.Results[i], got.Results[i].Result
			if a.ID != b.ID || a.Page != b.Page || a.PageCount != b.PageCount || a.URI != b.URI || a.Snippet != b.Snippet || a.OriginalIdentity != b.OriginalIdentity {
				t.Errorf("%q PDF result mismatch: %+v / %+v", query, a, b)
			}
		}
	}
}
