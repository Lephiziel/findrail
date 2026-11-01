package sqlite

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func readPageForTest(t *testing.T, store *Store, request search.Request) search.PageResponse {
	t.Helper()
	tx, err := store.readers.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	page, err := searchPageTx(t.Context(), tx, request, 0)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestSearchPageTxAppliesEveryFilterBeforeCountAndLimit(t *testing.T) {
	ctx := t.Context()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	docx := "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	fixtures := []struct {
		source, id, path, title, media string
	}{
		{"a", "a-text", "docs/資料/a.md", "Keep α", "text/plain"},
		{"a", "a-pdf", "docs/report.pdf", "Other report", "application/pdf"},
		{"a", "a-docx", "docs/report.docx", "Keep word", docx},
		{"a", "a-boundary", "docs-other/out.md", "Keep external", "text/plain"},
		{"b", "b-text", "docs/資料/b.md", "Keep β", "text/plain"},
	}
	for _, sourceID := range []string{"a", "b"} {
		scan, e := store.BeginScan(ctx, connector.Source{ID: sourceID, Kind: "filesystem", Name: sourceID})
		if e != nil {
			t.Fatal(e)
		}
		for _, f := range fixtures {
			if f.source != sourceID {
				continue
			}
			doc := connector.Document{ID: f.id, SourceID: sourceID, Title: f.title, Path: f.path, URI: "file:///synthetic/" + f.id, Content: "filtermarker", Hash: "fixture-hash", MediaType: f.media}
			if f.media == "application/pdf" {
				doc.Pages = []connector.Page{{Number: 1, Text: doc.Content}}
			}
			if _, e = scan.Upsert(ctx, doc); e != nil {
				scan.Rollback()
				t.Fatal(e)
			}
		}
		if _, e = scan.Commit(ctx); e != nil {
			t.Fatal(e)
		}
	}
	for _, tc := range []struct {
		name string
		req  search.Request
		ids  []string
	}{
		{"all", search.Request{}, []string{"a-boundary", "a-docx", "a-pdf", "a-text", "b-text"}},
		{"source a", search.Request{SourceID: "a"}, []string{"a-boundary", "a-docx", "a-pdf", "a-text"}},
		{"source b", search.Request{SourceID: "b"}, []string{"b-text"}},
		{"text", search.Request{Format: "text"}, []string{"a-boundary", "a-text", "b-text"}},
		{"pdf", search.Request{Format: "pdf"}, []string{"a-pdf"}},
		{"docx", search.Request{Format: "docx"}, []string{"a-docx"}},
		{"path boundary", search.Request{PathPrefix: "docs"}, []string{"a-docx", "a-pdf", "a-text", "b-text"}},
		{"trailing slash", search.Request{PathPrefix: "docs/"}, []string{"a-docx", "a-pdf", "a-text", "b-text"}},
		{"exact path", search.Request{PathPrefix: "docs/report.pdf"}, []string{"a-pdf"}},
		{"unicode path", search.Request{PathPrefix: "docs/資料"}, []string{"a-text", "b-text"}},
		{"nonsegment", search.Request{PathPrefix: "docs/report"}, []string{}},
		{"title", search.Request{TitleContains: "Keep"}, []string{"a-boundary", "a-docx", "a-text", "b-text"}},
		{"combined", search.Request{SourceID: "a", Format: "docx", PathPrefix: "docs", TitleContains: "Keep"}, []string{"a-docx"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := tc.req
			request.Query, request.Limit = "filtermarker", 100
			page := readPageForTest(t, store, request)
			ids := []string{}
			for _, result := range page.Results {
				ids = append(ids, result.ID)
			}
			sort.Strings(ids)
			if page.Total != len(tc.ids) || !reflect.DeepEqual(ids, tc.ids) {
				t.Fatalf("got total %d IDs %v, want %v", page.Total, ids, tc.ids)
			}
			legacy, e := store.Search(ctx, request)
			if e != nil || legacy.Total != page.Total || len(legacy.Results) != len(page.Results) {
				t.Fatalf("legacy mismatch: %+v / %+v, %v", legacy, page, e)
			}
			for i := range legacy.Results {
				if !reflect.DeepEqual(legacy.Results[i], page.Results[i].Result) || page.Results[i].SnippetTruncated {
					t.Fatalf("result %d differs from legacy: %+v / %+v", i, legacy.Results[i], page.Results[i])
				}
			}
			request.Limit = 1
			limited := readPageForTest(t, store, request)
			if limited.Total != len(tc.ids) || limited.Returned != min(1, len(tc.ids)) {
				t.Fatalf("filters/count/limit order wrong: %+v", limited)
			}
		})
	}
}

func TestSearchPageTxSnippetBoundariesAndJSONContract(t *testing.T) {
	ctx := t.Context()
	store, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scan, err := store.BeginScan(ctx, connector.Source{ID: "snippets", Kind: "filesystem", Name: "Synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	defer scan.Rollback()
	for _, n := range []int{1023, 1024, 1025} {
		body := strings.Repeat("界", n)
		id := fmt.Sprintf("unicode-%d", n)
		doc := connector.Document{ID: id, SourceID: "snippets", Title: "titlemarker", Path: id + ".md", URI: "file:///synthetic/" + id, Content: body, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(body))), MediaType: "text/plain"}
		if _, err = scan.Upsert(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}
	body := "pdfmarker " + strings.Repeat("界", 1100)
	if _, err = scan.Upsert(ctx, connector.Document{ID: "long-pdf", SourceID: "snippets", Title: "PDF", Path: "long.pdf", URI: "file:///synthetic/long.pdf", Content: body, Hash: "long-pdf-hash", MediaType: "application/pdf", Pages: []connector.Page{{Number: 1, Text: body}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = scan.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "advanced"} {
		for _, query := range []string{"titlemarker", "pdfmarker"} {
			request := search.Request{Query: query, Mode: mode, Limit: 100}
			page := readPageForTest(t, store, request)
			legacy, e := store.Search(ctx, request)
			if e != nil || len(legacy.Results) == 0 || len(legacy.Results) != len(page.Results) {
				t.Fatalf("fixture did not match: %+v / %+v, %v", legacy, page, e)
			}
			for i, result := range page.Results {
				want := legacy.Results[i]
				runes := []rune(want.Snippet)
				truncated := len(runes) > 1024
				if truncated {
					want.Snippet = string(runes[:1024])
				}
				if !reflect.DeepEqual(result.Result, want) || result.SnippetTruncated != truncated || !utf8.ValidString(result.Snippet) || utf8.RuneCountInString(result.Snippet) > 1024 {
					t.Fatalf("%s/%s: wrong snippet/identity/page: %+v, want %+v, truncated %v", mode, result.ID, result, want, truncated)
				}
				if result.ID == "long-pdf" && (result.Page != 1 || !result.SnippetTruncated) {
					t.Fatalf("long PDF evidence was not capped: %+v", result)
				}
			}
		}
	}
	for _, query := range []string{"titlemarker", "absentmarker"} {
		page := readPageForTest(t, store, search.Request{Query: query, Limit: 20})
		encoded, e := json.Marshal(page)
		if e != nil {
			t.Fatal(e)
		}
		var fields map[string]json.RawMessage
		if e = json.Unmarshal(encoded, &fields); e != nil {
			t.Fatal(e)
		}
		if len(fields) != 8 || string(fields["next_cursor"]) != "null" || string(fields["end_reason"]) != "null" || (page.Total == 0 && string(fields["results"]) != "[]") {
			t.Fatalf("invalid page JSON contract: %s", encoded)
		}
		var results []map[string]json.RawMessage
		if e = json.Unmarshal(fields["results"], &results); e != nil {
			t.Fatal(e)
		}
		for i, result := range results {
			if string(result["snippet_truncated"]) != fmt.Sprint(page.Results[i].SnippetTruncated) {
				t.Fatalf("missing boolean snippet_truncated: %s", fields["results"])
			}
		}
	}
}
