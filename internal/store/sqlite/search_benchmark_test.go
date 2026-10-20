package sqlite_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/Lephiziel/findrail/pkg/connector"
)

// Search benchmark corpora are synthetic and indexed before ResetTimer. Large
// corpora are opt-in so the documented 100/1000 command stays inexpensive.
func BenchmarkSearch(b *testing.B) {
	counts := []int{100, 1000}
	if os.Getenv("FINDRAIL_BENCH_LARGE") == "1" {
		counts = append(counts, 10_000, 100_000)
	}
	for _, count := range counts {
		b.Run(fmt.Sprintf("documents_%d", count), func(b *testing.B) {
			store, sourceID, setup := benchmarkStore(b, count, false)
			cases := []struct {
				name  string
				query search.Request
			}{
				{"common", search.Request{Query: "commonmark", Limit: 20}},
				{"rare", search.Request{Query: "raretoken", Limit: 20}},
				{"empty", search.Request{Query: "no_such_token", Limit: 20}},
				{"title_filter", search.Request{Query: "commonmark", TitleContains: "Title 000", Limit: 20}},
				{"path_filter", search.Request{Query: "commonmark", PathPrefix: "docs/group-0", Limit: 20}},
				{"source_filter", search.Request{Query: "commonmark", SourceID: sourceID, Limit: 20}},
				{"format_text", search.Request{Query: "commonmark", Format: "text", Limit: 20}},
			}
			for _, tc := range cases {
				b.Run(tc.name, func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if _, err := store.Search(context.Background(), tc.query); err != nil {
							b.Fatal(err)
						}
					}
					b.StopTimer()
					b.ReportMetric(float64(setup.Microseconds()), "setup-us")
				})
			}
		})
	}
}

// BenchmarkSearchPages captures the production cursor API, including exact
// count, ranked selection, response sizing and cursor fence checks.
func BenchmarkPageSearch(b *testing.B) {
	counts := []int{1000}
	if os.Getenv("FINDRAIL_BENCH_LARGE") == "1" {
		counts = []int{10_000, 100_000}
	}
	for _, count := range counts {
		b.Run(fmt.Sprintf("documents_%d", count), func(b *testing.B) {
			store, _, setup := benchmarkStore(b, count, false)
			ctx := context.Background()
			for _, tc := range []struct {
				name   string
				query  string
				format string
				cursor bool
			}{
				{"first_common", "commonmark", "", false},
				{"next_common", "commonmark", "", true},
				{"empty", "no_such_token", "", false},
				{"pdf_page_attribution", "pdfcommon", "pdf", false},
			} {
				b.Run(tc.name, func(b *testing.B) {
					req := search.PageRequest{Request: search.Request{Query: tc.query, Format: tc.format, Limit: 20}}
					if tc.cursor {
						first, err := store.SearchPage(ctx, req)
						if err != nil || first.NextCursor == nil {
							b.Fatalf("prepare cursor: page=%+v err=%v", first, err)
						}
						req.Cursor = *first.NextCursor
					}
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if _, err := store.SearchPage(ctx, req); err != nil {
							b.Fatal(err)
						}
					}
					b.StopTimer()
					b.ReportMetric(float64(setup.Microseconds()), "setup-us")
				})
			}
		})
	}
}

// BenchmarkSearchPDFHeavy keeps PDF fixtures bounded and includes matches on
// distinct pages so page-attribution cost is measured on the real Store API.
func BenchmarkPDFSearch(b *testing.B) {
	count := 250
	store, _, setup := benchmarkStore(b, count, true)
	for _, query := range []string{"pdfcommon", "pagealpha", "pageomega", "no_such_token"} {
		b.Run(query, func(b *testing.B) {
			req := search.Request{Query: query, Format: "pdf", Limit: 20}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := store.Search(context.Background(), req); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(setup.Microseconds()), "setup-us")
		})
	}
}

func benchmarkStore(b *testing.B, count int, pdfHeavy bool) (*sqlite.Store, string, time.Duration) {
	b.Helper()
	setupStarted := time.Now()
	ctx := context.Background()
	dir := filepath.Join(b.TempDir(), "index")
	store, err := sqlite.Open(ctx, dir)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	sourceID := "benchmark-source"
	source := connector.Source{ID: sourceID, Kind: "filesystem", Name: "Synthetic benchmark", Root: "/synthetic"}
	scan, err := store.BeginScan(ctx, source)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < count; i++ {
		mediaType, ext := "text/plain", ".md"
		content := fmt.Sprintf("commonmark shared synthetic body document %d", i)
		title := fmt.Sprintf("Commonmark Title %06d", i)
		pages := []connector.Page(nil)
		if pdfHeavy || i%10 == 2 {
			mediaType, ext = "application/pdf", ".pdf"
			pages = []connector.Page{{Number: 1, Text: "pdfcommon pagealpha opening"}, {Number: 2, Text: "pdfcommon middle passage"}, {Number: 3, Text: "pdfcommon pageomega ending"}}
			content = "pdfcommon pagealpha opening\npdfcommon middle passage\npdfcommon pageomega ending"
		} else if i%10 == 3 {
			mediaType, ext = "application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx"
		}
		if i%97 == 0 {
			content += " raretoken"
		}
		doc := connector.Document{ID: fmt.Sprintf("doc-%06d", i), SourceID: sourceID, Title: title, URI: "file:///synthetic/" + fmt.Sprintf("doc-%06d", i) + ext, Path: fmt.Sprintf("docs/group-%d/doc-%06d%s", i%10, i, ext), Content: content, Hash: fmt.Sprintf("synthetic-hash-%d", i), ModifiedAt: time.Unix(int64(i+1), 0), MediaType: mediaType, Pages: pages}
		if _, err := scan.Upsert(ctx, doc); err != nil {
			_ = scan.Rollback()
			b.Fatal(err)
		}
	}
	if _, err := scan.Commit(ctx); err != nil {
		b.Fatal(err)
	}
	return store, sourceID, time.Since(setupStarted)
}
