package sqlite

import (
	"context"
	"database/sql/driver"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func TestSearchPageCursorBoundsCanonicalDefaultsAndFixedExpiry(t *testing.T) {
	s, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedConcurrentPages(t, s, "old")
	now := time.Unix(1_800_000_000, 0)
	s.pageNow = func() time.Time { return now }
	request := search.PageRequest{Request: search.Request{Query: "controlledmarker", Limit: 1, PathPrefix: "docs/"}}
	first, err := s.SearchPage(t.Context(), request)
	if err != nil || first.NextCursor == nil {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	token, err := base64.RawURLEncoding.DecodeString(*first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(*first.NextCursor) > 4096 || len(token) > 2048 {
		t.Fatal("cursor exceeded encoded/decoded bounds")
	}
	cases := []string{strings.Repeat("a", 4097), "not-a-cursor", *first.NextCursor + "=", "!" + (*first.NextCursor)[1:]}
	for _, mutate := range []func([]byte){
		func(b []byte) { b[0] = 2 },
		func(b []byte) { b[20] ^= 1 },
		func(b []byte) { binary.BigEndian.PutUint32(b[49:53], 1000) },
		func(b []byte) { binary.BigEndian.PutUint64(b[53:61], ^uint64(0)) },
	} {
		changed := append([]byte(nil), token...)
		mutate(changed)
		cases = append(cases, base64.RawURLEncoding.EncodeToString(changed))
	}
	for _, cursor := range cases {
		bad := request
		bad.Cursor = cursor
		if _, e := s.SearchPage(t.Context(), bad); !errors.Is(e, search.ErrCursorInvalid) {
			t.Fatalf("invalid cursor was accepted/misclassified: %v", e)
		}
	}
	request.Cursor = *first.NextCursor
	request.Request.PathPrefix, request.Request.Mode, request.Request.Format = "docs", "literal", "all"
	now = now.Add(2 * time.Minute)
	second, err := s.SearchPage(t.Context(), request)
	if err != nil || second.Offset != 1 || second.NextCursor == nil {
		t.Fatalf("canonical defaults/trailing slash: %+v, %v", second, err)
	}
	secondToken, err := base64.RawURLEncoding.DecodeString(*second.NextCursor)
	if err != nil || binary.BigEndian.Uint64(secondToken[61:69]) != uint64(time.Unix(1_800_000_000, 0).Add(5*time.Minute).UnixNano()) {
		t.Fatal("continuation extended the original chain expiry")
	}
	now = now.Add(3 * time.Minute)
	request.Cursor = *second.NextCursor
	if _, e := s.SearchPage(t.Context(), request); !errors.Is(e, search.ErrCursorExpired) {
		t.Fatalf("continuation expiry boundary: %v", e)
	}
}

func TestSearchPageLostConnectionFailsUntilReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedConcurrentPages(t, s, "old")
	request := search.PageRequest{Request: search.Request{Query: "controlledmarker", Limit: 1}}
	first, err := s.SearchPage(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	p := s.pager
	if e := p.conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(e, driver.ErrBadConn) {
		t.Fatal(e)
	}
	request.Cursor = *first.NextCursor
	if _, e := s.SearchPage(t.Context(), request); !errors.Is(e, search.ErrPageUnavailable) {
		t.Fatalf("lost connection reused the old fence: %v", e)
	}
	request.Cursor = ""
	if _, e := s.SearchPage(t.Context(), request); !errors.Is(e, search.ErrPageUnavailable) || s.pager != p {
		t.Fatalf("failed runtime silently replaced its connection/session: %v", e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, err := OpenReadOnly(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	request.Cursor = *first.NextCursor
	if _, e := reopened.SearchPage(t.Context(), request); !errors.Is(e, search.ErrCursorSessionChanged) {
		t.Fatalf("reopened Store accepted the old session: %v", e)
	}
	request.Cursor = ""
	if page, e := reopened.SearchPage(t.Context(), request); e != nil || page.Total != 4 {
		t.Fatalf("fresh read-only chain: %+v, %v", page, e)
	}
}

func TestSearchPageFenceIncludesMetadataForgetAndPruneButNotRollback(t *testing.T) {
	for _, action := range []string{"rollback", "metadata", "forget", "forget-readd", "prune"} {
		t.Run(action, func(t *testing.T) {
			s, err := Open(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			seedConcurrentPages(t, s, "old")
			request := search.PageRequest{Request: search.Request{Query: "controlledmarker", Limit: 1}}
			first, err := s.SearchPage(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			switch action {
			case "rollback":
				tx, e := s.db.BeginTx(t.Context(), nil)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = tx.ExecContext(t.Context(), "UPDATE documents SET title='changed'"); e != nil {
					_ = tx.Rollback()
					t.Fatal(e)
				}
				err = tx.Rollback()
			case "metadata":
				_, err = s.CreateCollection(t.Context(), "Synthetic notes")
			case "forget", "forget-readd":
				err = s.ForgetSource(t.Context(), "concurrent-pages")
				if err == nil && action == "forget-readd" {
					seedConcurrentPages(t, s, "readded")
				}
			case "prune":
				var e error
				scan, e := s.BeginScan(t.Context(), connector.Source{ID: "concurrent-pages", Kind: "filesystem", Name: "Synthetic"})
				if e != nil {
					t.Fatal(e)
				}
				_, err = scan.Commit(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			request.Cursor = *first.NextCursor
			page, err := s.SearchPage(t.Context(), request)
			if action == "rollback" {
				if err != nil || page.Offset != 1 {
					t.Fatalf("rollback invalidated an unchanged snapshot: %+v, %v", page, err)
				}
			} else if !errors.Is(err, search.ErrCursorStale) {
				t.Fatalf("committed %s did not invalidate the cursor: %v", action, err)
			}
		})
	}
}

func TestSearchPageExactJSONBoundaryEscapingAndCancellation(t *testing.T) {
	p, err := newPageRuntime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer p.cancel()
	reason := "complete"
	page := search.PageResponse{Results: []search.PageResult{{Result: search.Result{ID: "fixture", Snippet: "<😀&"}}}, Total: 1, Returned: 1, Limit: 1, WindowLimit: 1000, EndReason: &reason}
	baseline, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	page.Results[0].URI = strings.Repeat("u", pageJSONLimit-len(baseline)-1)
	fit, err := fitSearchPage(t.Context(), p, page, [32]byte{}, 1, time.Now().Add(pageTTL).UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(fit)
	if err != nil || len(encoded)+1 != pageJSONLimit || fit.Returned != 1 || fit.Results[0].Snippet != "<😀&" {
		t.Fatalf("exact encoded boundary: %d bytes, %v", len(encoded)+1, err)
	}
	page.Results[0].URI += "u"
	if _, e := fitSearchPage(t.Context(), p, page, [32]byte{}, 1, 0); !errors.Is(e, search.ErrPageResultTooLarge) {
		t.Fatalf("one byte past the boundary was accepted: %v", e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e := fitSearchPage(ctx, p, page, [32]byte{}, 1, 0); !errors.Is(e, context.Canceled) {
		t.Fatalf("response encoding ignored cancellation: %v", e)
	}
}

func TestSearchPageExactlyOneThousandCompletes(t *testing.T) {
	s, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	scan, err := s.BeginScan(t.Context(), connector.Source{ID: "thousand", Kind: "filesystem", Name: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	defer scan.Rollback()
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("exact-%04d", i)
		if _, err := scan.Upsert(t.Context(), connector.Document{ID: id, SourceID: "thousand", Title: id, Path: id, URI: "file:///synthetic/" + id, Content: "exactthousand", Hash: id, MediaType: "text/plain"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := scan.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	request := search.PageRequest{Request: search.Request{Query: "exactthousand", Limit: 100}}
	for offset := 0; offset < 1000; offset += 100 {
		page, e := s.SearchPage(t.Context(), request)
		if e != nil || page.Offset != offset || page.Returned != 100 || page.Total != 1000 {
			t.Fatalf("page %d: %+v, %v", offset, page, e)
		}
		if offset == 900 {
			if page.NextCursor != nil || page.EndReason == nil || *page.EndReason != "complete" {
				t.Fatalf("exact window was reported incomplete: %+v", page)
			}
		} else {
			if page.NextCursor == nil {
				t.Fatal("page chain ended too soon")
			}
			request.Cursor = *page.NextCursor
		}
	}
}
