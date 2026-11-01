package sqlite

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func TestSearchPageWalkAndCursorValidation(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Unix(1_800_000_000, 0)
	s.pageNow = func() time.Time { return now }
	scan, err := s.BeginScan(ctx, connector.Source{ID: "pages", Kind: "filesystem", Name: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 237; i++ {
		id := fmt.Sprintf("doc-%03d", i)
		if _, err := scan.Upsert(ctx, connector.Document{ID: id, SourceID: "pages", Title: id, Path: id + ".txt", URI: "file:///synthetic/" + id, Content: "pagewalkmarker", Hash: id, MediaType: "text/plain"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := scan.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	req := search.PageRequest{Request: search.Request{Query: "pagewalkmarker", Limit: 37}}
	var firstCursor string
	for {
		page, err := s.SearchPage(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 237 || page.Returned != len(page.Results) {
			t.Fatalf("bad page metadata: %+v", page)
		}
		for _, r := range page.Results {
			if seen[r.ID] {
				t.Fatalf("duplicate %s", r.ID)
			}
			seen[r.ID] = true
		}
		if req.Cursor == "" && page.NextCursor != nil {
			firstCursor = *page.NextCursor
		}
		if page.NextCursor == nil {
			if page.EndReason == nil || *page.EndReason != "complete" {
				t.Fatalf("unexpected end: %+v", page)
			}
			break
		}
		req.Cursor = *page.NextCursor
	}
	if len(seen) != 237 {
		t.Fatalf("walk returned %d documents", len(seen))
	}
	for _, limit := range []int{1, 20, 100} {
		walk := search.PageRequest{Request: search.Request{Query: "pagewalkmarker", Limit: limit}}
		position := 0
		for {
			page, e := s.SearchPage(ctx, walk)
			if e != nil || page.Offset != position || page.Total != 237 || page.Returned == 0 {
				t.Fatalf("limit %d at %d: %+v, %v", limit, position, page, e)
			}
			for _, row := range page.Results {
				if row.ID != fmt.Sprintf("doc-%03d", position) {
					t.Fatalf("limit %d gap/order at %d: %s", limit, position, row.ID)
				}
				position++
			}
			if page.NextCursor == nil {
				if position != 237 || page.EndReason == nil || *page.EndReason != "complete" {
					t.Fatalf("limit %d incomplete walk: %d, %+v", limit, position, page)
				}
				break
			}
			walk.Cursor = *page.NextCursor
		}
	}
	prev, err := s.SearchPage(ctx, search.PageRequest{Request: search.Request{Query: "pagewalkmarker", Limit: 37, Mode: "literal", Format: "all"}, Cursor: firstCursor})
	if err != nil {
		t.Fatal(err)
	}
	if prev.Offset != 37 {
		t.Fatalf("cursor offset=%d", prev.Offset)
	}
	for name, changed := range map[string]search.Request{
		"limit":       {Query: "pagewalkmarker", Limit: 36},
		"query":       {Query: "othermarker", Limit: 37},
		"mode":        {Query: "pagewalkmarker", Limit: 37, Mode: "advanced"},
		"source":      {Query: "pagewalkmarker", Limit: 37, SourceID: "absent"},
		"format":      {Query: "pagewalkmarker", Limit: 37, Format: "pdf"},
		"path prefix": {Query: "pagewalkmarker", Limit: 37, PathPrefix: "missing"},
		"title":       {Query: "pagewalkmarker", Limit: 37, TitleContains: "absent"},
	} {
		if _, err := s.SearchPage(ctx, search.PageRequest{Request: changed, Cursor: firstCursor}); !errors.Is(err, search.ErrCursorMismatch) {
			t.Errorf("%s mismatch error=%v", name, err)
		}
	}
	b, err := base64.RawURLEncoding.DecodeString(firstCursor)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-1] ^= 1
	tampered := base64.RawURLEncoding.EncodeToString(b)
	if _, err := s.SearchPage(ctx, search.PageRequest{Request: search.Request{Query: "pagewalkmarker", Limit: 37}, Cursor: tampered}); !errors.Is(err, search.ErrCursorInvalid) {
		t.Fatalf("tampered token error=%v", err)
	}
	foreignBytes, _ := base64.RawURLEncoding.DecodeString(firstCursor)
	foreignBytes[1] ^= 1
	foreign := base64.RawURLEncoding.EncodeToString(foreignBytes)
	if _, err := s.SearchPage(ctx, search.PageRequest{Request: search.Request{Query: "pagewalkmarker", Limit: 37}, Cursor: foreign}); !errors.Is(err, search.ErrCursorSessionChanged) {
		t.Fatalf("foreign epoch error=%v", err)
	}
	now = now.Add(5 * time.Minute)
	if _, err := s.SearchPage(ctx, search.PageRequest{Request: search.Request{Query: "pagewalkmarker", Limit: 37}, Cursor: firstCursor}); !errors.Is(err, search.ErrCursorExpired) {
		t.Fatalf("cursor at expiry boundary error=%v", err)
	}
}

func TestSearchPageFenceInvalidatesAfterCommit(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	scan, err := s.BeginScan(ctx, connector.Source{ID: "fence", Kind: "filesystem", Name: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("f-%d", i)
		if _, err = scan.Upsert(ctx, connector.Document{ID: id, SourceID: "fence", Title: id, Path: id, URI: "file:///" + id, Content: "fencemarker", Hash: id, MediaType: "text/plain"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = scan.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := s.SearchPage(ctx, search.PageRequest{Request: search.Request{Query: "fencemarker", Limit: 1}})
	if err != nil {
		t.Fatal(err)
	}
	change, err := s.BeginScan(ctx, connector.Source{ID: "fence", Kind: "filesystem", Name: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = change.Upsert(ctx, connector.Document{ID: "new", SourceID: "fence", Title: "new", Path: "new", URI: "file:///new", Content: "fencemarker", Hash: "new", MediaType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	if _, err = change.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SearchPage(ctx, search.PageRequest{Request: search.Request{Query: "fencemarker", Limit: 1}, Cursor: *page.NextCursor}); !errors.Is(err, search.ErrCursorStale) {
		t.Fatalf("stale fence error=%v", err)
	}
}

func TestSearchPageReadOnlyAndCloseLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writable, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writable.Close(); err != nil {
		t.Fatal(err)
	}
	readonly, err := OpenReadOnly(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readonly.SearchPage(ctx, search.PageRequest{Request: search.Request{Query: "readonlymarker"}}); err != nil {
		t.Fatal(err)
	}
	if err := readonly.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readonly.SearchPage(ctx, search.PageRequest{Request: search.Request{Query: "readonlymarker"}}); !errors.Is(err, search.ErrPageClosed) {
		t.Fatalf("post-close error=%v", err)
	}
	if readonly.SchemaInfo().IndexSchema != currentSchemaVersion {
		t.Fatalf("read-only schema changed: %+v", readonly.SchemaInfo())
	}
}

func TestSearchPageStopsAtWindowAndReportsFullTotal(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	scan, err := s.BeginScan(ctx, connector.Source{ID: "window", Kind: "filesystem", Name: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1005; i++ {
		id := fmt.Sprintf("w-%04d", i)
		if _, err = scan.Upsert(ctx, connector.Document{ID: id, SourceID: "window", Title: id, Path: id, URI: "file:///" + id, Content: "windowmarker", Hash: id, MediaType: "text/plain"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = scan.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	request := search.PageRequest{Request: search.Request{Query: "windowmarker", Limit: 100}}
	count := 0
	for {
		page, err := s.SearchPage(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 1005 {
			t.Fatalf("total=%d want 1005", page.Total)
		}
		count += page.Returned
		if page.NextCursor == nil {
			if page.EndReason == nil || *page.EndReason != "window_limit" {
				t.Fatalf("end=%v", page.EndReason)
			}
			break
		}
		request.Cursor = *page.NextCursor
	}
	if count != search.PageWindowLimit {
		t.Fatalf("returned %d rows, expected window %d", count, search.PageWindowLimit)
	}
}

func TestSearchPageFitsEncodedJSONPrefix(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	scan, err := s.BeginScan(ctx, connector.Source{ID: "bytes", Kind: "filesystem", Name: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("b-%d", i)
		uri := "file:///synthetic/" + strings.Repeat("u", 120000) + id
		if _, err = scan.Upsert(ctx, connector.Document{ID: id, SourceID: "bytes", Title: id, Path: id, URI: uri, Content: "budgetmarker", Hash: id, MediaType: "text/plain"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = scan.Upsert(ctx, connector.Document{ID: "giant", SourceID: "bytes", Title: "giant", Path: "giant", URI: "file:///synthetic/" + strings.Repeat("g", 600000), Content: "giantmarker", Hash: "giant", MediaType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	if _, err = scan.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := s.SearchPage(ctx, search.PageRequest{Request: search.Request{Query: "budgetmarker", Limit: 6}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Returned < 1 || first.Returned >= 6 || first.NextCursor == nil {
		t.Fatalf("expected a nonempty fitting prefix: returned=%d cursor=%v", first.Returned, first.NextCursor != nil)
	}
	if first.Offset != 0 {
		t.Fatalf("first offset=%d", first.Offset)
	}
	second, err := s.SearchPage(ctx, search.PageRequest{Request: search.Request{Query: "budgetmarker", Limit: 6}, Cursor: *first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if second.Offset != first.Returned || second.Returned != 6-first.Returned {
		t.Fatalf("remainder skipped: first=%+v second=%+v", first, second)
	}
	if _, err = s.SearchPage(ctx, search.PageRequest{Request: search.Request{Query: "giantmarker", Limit: 1}}); !errors.Is(err, search.ErrPageResultTooLarge) {
		t.Fatalf("oversized singleton error=%v", err)
	}
}
