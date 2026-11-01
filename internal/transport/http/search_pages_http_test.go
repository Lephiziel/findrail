package http_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	transport "github.com/Lephiziel/findrail/internal/transport/http"
	"github.com/Lephiziel/findrail/pkg/connector"
)

type pageFuncBackend struct {
	transport.Backend
	page func(context.Context, search.PageRequest) (search.PageResponse, error)
}

func (b pageFuncBackend) SearchPage(ctx context.Context, req search.PageRequest) (search.PageResponse, error) {
	return b.page(ctx, req)
}

func pageHTTP(t *testing.T, handler http.Handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766/api/v1/search/pages?"+query, nil)
	handler.ServeHTTP(w, r)
	if w.Header().Get("Cache-Control") != "no-store" || w.Body.Len() > 524288 {
		t.Fatalf("uncacheable/bounded response contract: status=%d bytes=%d headers=%v", w.Code, w.Body.Len(), w.Header())
	}
	return w
}

func TestSearchPagesHTTPBoundsBackendDeadline(t *testing.T) {
	for _, shorter := range []bool{false, true} {
		t.Run(fmt.Sprint(shorter), func(t *testing.T) {
			ctx := t.Context()
			if shorter {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
			}
			backend := pageFuncBackend{page: func(call context.Context, _ search.PageRequest) (search.PageResponse, error) {
				deadline, ok := call.Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Error("optional pager did not receive the five-second HTTP budget")
				}
				if shorter && (!ok || deadline != deadlineOf(ctx)) {
					t.Error("HTTP transport extended the caller's shorter deadline")
				}
				return search.PageResponse{}, search.ErrPageUnavailable
			}}
			r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766/api/v1/search/pages?q=marker", nil).WithContext(ctx)
			transport.Handler(backend).ServeHTTP(httptest.NewRecorder(), r)
		})
	}
}

func deadlineOf(ctx context.Context) time.Time {
	deadline, _ := ctx.Deadline()
	return deadline
}

func TestSearchPagesHTTPCancelledSuccessIsNotWritten(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	backend := pageFuncBackend{page: func(call context.Context, _ search.PageRequest) (search.PageResponse, error) {
		cancel()
		<-call.Done()
		return search.PageResponse{Results: []search.PageResult{}}, nil
	}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766/api/v1/search/pages?q=marker", nil).WithContext(ctx)
	transport.Handler(backend).ServeHTTP(w, r)
	if w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" {
		t.Fatalf("response written after client cancellation: %s", w.Body.String())
	}
}

func TestSearchPagesHTTPBackendValidationKeepsPosition(t *testing.T) {
	backend := pageFuncBackend{page: func(context.Context, search.PageRequest) (search.PageResponse, error) {
		return search.PageResponse{}, fmt.Errorf("wrapped: %w", &search.ValidationError{Code: "syntax", Position: 7, Message: "expected a term"})
	}}
	w := pageHTTP(t, transport.Handler(backend), "q=marker")
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 400 || body["code"] != "syntax" || body["position"] != float64(7) {
		t.Fatalf("validation envelope lost query position: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSearchPagesHTTPRejectsInvalidInputBeforeBackend(t *testing.T) {
	calls := 0
	backend := pageFuncBackend{page: func(context.Context, search.PageRequest) (search.PageResponse, error) {
		calls++
		return search.PageResponse{}, search.ErrPageUnavailable
	}}
	handler := transport.Handler(backend)
	for _, query := range []string{
		"", "q=", "q=%zz", "q=marker;ignored", "q=marker&q=other", "q=marker&unknown=1",
		"q=marker&limit=0", "q=marker&limit=-1", "q=marker&limit=101", "q=marker&limit=x", "q=marker&limit=1&limit=2",
		"q=marker&offset=", "q=marker&page=", "q=marker&mode=other", "q=marker&format=other",
		"q=marker&path_prefix=/", "q=marker&path_prefix=docs//", "q=marker&title_contains=%00",
		"q=marker&cursor=" + strings.Repeat("x", 4097),
	} {
		w := pageHTTP(t, handler, query)
		if w.Code != 400 || !json.Valid(w.Body.Bytes()) {
			t.Errorf("invalid parameters reached backend: status=%d query=%q", w.Code, query)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766/api/v1/search/pages?q=marker", nil).WithContext(ctx)
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if calls != 0 {
		t.Fatalf("backend called %d times for invalid or already cancelled requests", calls)
	}
	for _, origin := range []string{"", "https://attacker.example"} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766/api/v1/search/pages?q=marker", nil)
		if origin == "" {
			r.Host = "attacker.example"
		} else {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 || calls != 0 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("page route bypassed browser boundary: status=%d calls=%d", w.Code, calls)
		}
	}
}

func TestSearchPagesHTTPBuffersExactJSONBound(t *testing.T) {
	for _, delta := range []int{0, 1} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			reason := "complete"
			page := search.PageResponse{Results: []search.PageResult{{Result: search.Result{ID: "synthetic", Snippet: "<😀&"}}}, Total: 1, Returned: 1, Limit: 1, WindowLimit: 1000, EndReason: &reason}
			body, err := json.Marshal(page)
			if err != nil {
				t.Fatal(err)
			}
			page.Results[0].URI = strings.Repeat("u", 524288-len(body)-1+delta)
			backend := pageFuncBackend{page: func(context.Context, search.PageRequest) (search.PageResponse, error) { return page, nil }}
			w := pageHTTP(t, transport.Handler(backend), "q=marker&limit=1")
			if delta == 0 {
				if w.Code != 200 || w.Body.Len() != 524288 || !strings.HasSuffix(w.Body.String(), "\n") || !json.Valid(w.Body.Bytes()) {
					t.Fatalf("exact boundary rejected: status=%d bytes=%d", w.Code, w.Body.Len())
				}
			} else if w.Code != 500 || w.Body.Len() > 256 || strings.Contains(w.Body.String(), "synthetic") || !json.Valid(w.Body.Bytes()) {
				t.Fatalf("oversized success leaked before error: status=%d bytes=%d", w.Code, w.Body.Len())
			}
		})
	}
	backend := pageFuncBackend{page: func(context.Context, search.PageRequest) (search.PageResponse, error) {
		return search.PageResponse{Results: []search.PageResult{{Result: search.Result{Score: math.NaN()}}}}, nil
	}}
	w := pageHTTP(t, transport.Handler(backend), "q=marker")
	if w.Code != 500 || !json.Valid(w.Body.Bytes()) || strings.Contains(w.Body.String(), "NaN") {
		t.Fatalf("encoding failure wrote a partial success: status=%d body=%s", w.Code, w.Body.String())
	}
}

func seedHTTPPages(t *testing.T, store *sqlite.Store, count int, uriSuffix string) {
	t.Helper()
	scan, err := store.BeginScan(t.Context(), connector.Source{ID: "http-pages", Kind: "filesystem", Name: "Synthetic pages"})
	if err != nil {
		t.Fatal(err)
	}
	defer scan.Rollback()
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("http-%04d", i)
		content := "httpwalkmarker exactthousand"
		if i >= 1000 {
			content = "httpwalkmarker"
		}
		if _, err := scan.Upsert(t.Context(), connector.Document{ID: id, SourceID: "http-pages", Title: "資料 <&> " + id, Path: "docs/" + id + ".md", URI: "file:///synthetic/" + id + uriSuffix, MediaType: "text/plain", Content: content, Hash: id}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := scan.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func decodeHTTPPage(t *testing.T, w *httptest.ResponseRecorder) search.PageResponse {
	t.Helper()
	var page search.PageResponse
	if w.Code != 200 {
		t.Fatalf("page failed: status=%d body=%s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"results", "total", "offset", "returned", "limit", "window_limit", "next_cursor", "end_reason"} {
		if _, ok := envelope[key]; !ok {
			t.Fatalf("missing required page field %s", key)
		}
	}
	if page.Results == nil || page.Returned != len(page.Results) || page.WindowLimit != 1000 {
		t.Fatalf("invalid page envelope: %+v", page)
	}
	if !strings.HasSuffix(w.Body.String(), "\n") {
		t.Fatal("missing JSON newline")
	}
	return page
}

func TestSearchPagesHTTPRealWindowAndByteFitting(t *testing.T) {
	store, err := sqlite.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedHTTPPages(t, store, 1001, "")
	handler := transport.Handler(store)
	for _, tc := range []struct {
		query string
		total int
		end   string
	}{
		{"absentmarker", 0, "complete"}, {"exactthousand", 1000, "complete"}, {"httpwalkmarker", 1001, "window_limit"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			values := url.Values{"q": {tc.query}, "limit": {"100"}}
			seen := map[string]bool{}
			for {
				page := decodeHTTPPage(t, pageHTTP(t, handler, values.Encode()))
				if page.Total != tc.total || page.Offset != len(seen) || page.Limit != 100 {
					t.Fatalf("window count/offset mismatch: %+v", page)
				}
				for _, row := range page.Results {
					if seen[row.ID] || row.OriginalIdentity == "" || row.SourceID != "http-pages" || !strings.HasPrefix(row.URI, "file:///synthetic/") {
						t.Fatalf("duplicate or lost provenance: %+v", row)
					}
					seen[row.ID] = true
				}
				if page.NextCursor == nil {
					if len(seen) != min(tc.total, 1000) || page.EndReason == nil || *page.EndReason != tc.end {
						t.Fatalf("bad terminal page: rows=%d end=%v", len(seen), page.EndReason)
					}
					break
				}
				if page.Returned == 0 || page.EndReason != nil {
					t.Fatal("nonterminal page made no progress or reported an end")
				}
				values.Set("cursor", *page.NextCursor)
			}
		})
	}
	seedHTTPPages(t, store, 31, strings.Repeat("<😀&", 6000))
	values := url.Values{"q": {"httpwalkmarker"}, "limit": {"20"}}
	seen := map[string]bool{}
	for {
		page := decodeHTTPPage(t, pageHTTP(t, handler, values.Encode()))
		if page.Total != 31 || page.Offset != len(seen) || page.Returned < 1 || page.Returned >= 20 {
			t.Fatalf("byte fitting did not reduce page size: offset=%d returned=%d total=%d", page.Offset, page.Returned, page.Total)
		}
		for _, row := range page.Results {
			if seen[row.ID] || !strings.HasSuffix(row.URI, strings.Repeat("<😀&", 6000)) {
				t.Fatal("adaptive page skipped/duplicated a row or changed Unicode metadata")
			}
			seen[row.ID] = true
		}
		if page.NextCursor == nil {
			if len(seen) != 31 || page.EndReason == nil || *page.EndReason != "complete" {
				t.Fatalf("adaptive walk incomplete: %d", len(seen))
			}
			break
		}
		values.Set("cursor", *page.NextCursor)
	}
}

func TestSearchPagesHTTPRealCursorInvalidation(t *testing.T) {
	for _, action := range []string{"scan", "prune", "forget", "readd", "note"} {
		t.Run(action, func(t *testing.T) {
			store, err := sqlite.Open(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			seedHTTPPages(t, store, 3, "")
			collection, err := store.CreateCollection(t.Context(), "Synthetic notes")
			if err != nil {
				t.Fatal(err)
			}
			evidence, err := store.Evidence(t.Context(), "http-0000", 0)
			if err != nil {
				t.Fatal(err)
			}
			collection, err = store.PinCollectionEntry(t.Context(), collection.ID, collection.Revision, evidence.ID, 0, "httpwalkmarker", false, evidence.OriginalIdentity)
			if err != nil {
				t.Fatal(err)
			}
			handler := transport.Handler(store)
			first := decodeHTTPPage(t, pageHTTP(t, handler, "q=httpwalkmarker&limit=1"))
			if first.NextCursor == nil {
				t.Fatal("fixture has no continuation")
			}
			switch action {
			case "scan":
				seedHTTPPages(t, store, 4, "")
			case "prune":
				seedHTTPPages(t, store, 1, "")
			case "forget", "readd":
				if err := store.ForgetSource(t.Context(), "http-pages"); err != nil {
					t.Fatal(err)
				}
				if action == "readd" {
					seedHTTPPages(t, store, 3, "")
				}
			case "note":
				if _, err := store.SaveCollection(t.Context(), collection.ID, collection.Revision, collection.Title, []sqlite.CollectionEdit{{Key: collection.Entries[0].Key, Note: "Synthetic note updated"}}); err != nil {
					t.Fatal(err)
				}
			}
			w := pageHTTP(t, handler, "q=httpwalkmarker&limit=1&cursor="+url.QueryEscape(*first.NextCursor))
			if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"cursor_stale"`) {
				t.Fatalf("committed %s reused an old cursor: status=%d body=%s", action, w.Code, w.Body.String())
			}
			decodeHTTPPage(t, pageHTTP(t, handler, "q=httpwalkmarker&limit=1"))
		})
	}
}

func TestSearchPagesHTTPRealCursorScopeAndRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlite.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedHTTPPages(t, store, 3, "")
	handler := transport.Handler(store)
	values := url.Values{"q": {"httpwalkmarker"}, "mode": {"literal"}, "source": {"http-pages"}, "format": {"text"}, "path_prefix": {"docs"}, "title_contains": {"資料"}, "limit": {"1"}}
	first := decodeHTTPPage(t, pageHTTP(t, handler, values.Encode()))
	if first.NextCursor == nil {
		t.Fatal("missing continuation")
	}
	values.Set("cursor", *first.NextCursor)
	for key, changed := range map[string]string{"q": "exactthousand", "mode": "advanced", "source": "missing", "format": "pdf", "path_prefix": "other", "title_contains": "other", "limit": "2"} {
		original := values.Get(key)
		values.Set(key, changed)
		w := pageHTTP(t, handler, values.Encode())
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"cursor_mismatch"`) {
			t.Errorf("cursor was not bound to %s: status=%d body=%s", key, w.Code, w.Body.String())
		}
		values.Set(key, original)
	}
	values.Set("cursor", *first.NextCursor+"=")
	if w := pageHTTP(t, handler, values.Encode()); w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"cursor_invalid"`) {
		t.Fatalf("noncanonical cursor accepted: status=%d body=%s", w.Code, w.Body.String())
	}
	values.Set("cursor", *first.NextCursor)
	for i := 0; i < 2; i++ {
		page := decodeHTTPPage(t, pageHTTP(t, handler, values.Encode()))
		if page.Offset != 1 || page.Results[0].ID != "http-0001" {
			t.Fatal("saved cursor could not replay its page")
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if w := pageHTTP(t, handler, values.Encode()); w.Code != 503 {
		t.Fatalf("closed pager returned status %d", w.Code)
	}
	reopened, err := sqlite.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	w := pageHTTP(t, transport.Handler(reopened), values.Encode())
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"cursor_session_changed"`) {
		t.Fatalf("restart reused a process-local cursor: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSearchPagesHTTPRealPDFProvenance(t *testing.T) {
	store, err := sqlite.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	scan, err := store.BeginScan(t.Context(), connector.Source{ID: "pdf-pages", Kind: "filesystem", Name: "Synthetic PDF"})
	if err != nil {
		t.Fatal(err)
	}
	defer scan.Rollback()
	if _, err := scan.Upsert(t.Context(), connector.Document{ID: "pdf", SourceID: "pdf-pages", Title: "titlemarker", URI: "file:///synthetic/report.pdf", Path: "report.pdf", MediaType: "application/pdf", Hash: "pdf-hash", Content: "other text\nnested phrase", Pages: []connector.Page{{Number: 1, Text: "other text"}, {Number: 2, Text: "nested phrase"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := scan.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		page  int
	}{
		{`"nested phrase"`, 2}, {"titlemarker", 0},
	} {
		page := decodeHTTPPage(t, pageHTTP(t, transport.Handler(store), url.Values{"q": {tc.query}, "mode": {"advanced"}, "format": {"pdf"}}.Encode()))
		if page.Total != 1 || len(page.Results) != 1 {
			t.Fatalf("missing PDF result for %s", tc.query)
		}
		row := page.Results[0]
		wantURI := "file:///synthetic/report.pdf"
		if tc.page > 0 {
			wantURI += fmt.Sprintf("#page=%d", tc.page)
		}
		if row.Page != tc.page || row.PageCount != 2 || row.SourceID != "pdf-pages" || row.URI != wantURI || row.OriginalIdentity == "" || row.SnippetTruncated {
			t.Fatalf("lost PDF page/provenance: %+v", row)
		}
	}
}
