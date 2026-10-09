package sqlite_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func open(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshTransactionAndForgetAcrossStoreHandles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	root := t.TempDir()
	write(t, filepath.Join(root, "note.md"), "race snapshot")
	c, err := filesystem.New(root, filesystem.DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Run(ctx, first, c); err != nil {
		t.Fatal(err)
	}
	refresh, err := first.BeginRefresh(ctx, c.Source())
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	forgotten := make(chan error, 1)
	go func() { close(started); forgotten <- second.ForgetSource(ctx, c.Source().ID) }()
	<-started
	if _, err := refresh.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-forgotten; err != nil {
		t.Fatal(err)
	}
	if sources, err := first.Sources(ctx); err != nil || len(sources) != 0 {
		t.Fatalf("old refresh resurrected forgotten source: %+v %v", sources, err)
	}
	// A genuinely new explicit Add after the forget linearization may register it.
	if _, err := ingest.Run(ctx, first, c); err != nil {
		t.Fatal(err)
	}
	if err := second.ForgetSource(ctx, c.Source().ID); err != nil {
		t.Fatal(err)
	}
	if _, err := first.BeginRefresh(ctx, c.Source()); !errors.Is(err, ingest.ErrSourceGone) {
		t.Fatalf("refresh after forget: %v", err)
	}
}

func index(t *testing.T, s *sqlite.Store, root string) (ingest.Result, *filesystem.Connector) {
	t.Helper()
	c, err := filesystem.New(root, filesystem.DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ingest.Run(context.Background(), s, c)
	if err != nil {
		t.Fatal(err)
	}
	return r, c
}

func find(t *testing.T, s *sqlite.Store, q, source string, limit int) search.Response {
	t.Helper()
	r, err := s.Search(context.Background(), search.Request{Query: q, SourceID: source, Limit: limit})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestIndexLifecycleAndSourceIsolation(t *testing.T) {
	s := open(t)
	root, other := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "payments.md"), "webhook retry payment event")
	write(t, filepath.Join(root, "notes.txt"), "локальный поиск документ")
	write(t, filepath.Join(other, "payments.md"), "webhook external source")
	r, conn := index(t, s, root)
	if r.Updated != 2 || r.Seen != 2 {
		t.Fatalf("initial scan: %+v", r)
	}
	index(t, s, other)
	if response := find(t, s, "webhook", "", 1); response.Total != 2 || len(response.Results) != 1 {
		t.Fatalf("limit or total: %+v", response)
	}
	if response := find(t, s, "webhook", conn.Source().ID, 20); response.Total != 1 || response.Results[0].SourceID != conn.Source().ID {
		t.Fatalf("source filter: %+v", response)
	}
	if response := find(t, s, "ПОИСК", conn.Source().ID, 20); response.Total != 1 {
		t.Fatalf("unicode case folding: %+v", response)
	}
	r, _ = index(t, s, root)
	if r.Updated != 0 || r.Unchanged != 2 {
		t.Fatalf("unchanged scan: %+v", r)
	}
	write(t, filepath.Join(root, "payments.md"), "reconciliation transaction")
	if err := os.Remove(filepath.Join(root, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	r, _ = index(t, s, root)
	if r.Updated != 1 || r.Removed != 1 {
		t.Fatalf("changed scan: %+v", r)
	}
	if response := find(t, s, "webhook", conn.Source().ID, 20); response.Total != 0 {
		t.Fatalf("stale FTS text: %+v", response)
	}
	if response := find(t, s, "поиск", "", 20); response.Total != 0 {
		t.Fatalf("deleted document searchable: %+v", response)
	}
	if response := find(t, s, "webhook", "", 20); response.Total != 1 {
		t.Fatalf("other source removed: %+v", response)
	}
	if err := s.ForgetSource(context.Background(), conn.Source().ID); err != nil {
		t.Fatal(err)
	}
	if response := find(t, s, "reconciliation", "", 20); response.Total != 0 {
		t.Fatalf("forgotten source searchable: %+v", response)
	}
	if _, err := os.Stat(filepath.Join(root, "payments.md")); err != nil {
		t.Fatalf("original was changed: %v", err)
	}
}

func TestAdvancedSearchAndServerFilters(t *testing.T) {
	s := open(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "docs", "guide.md"), "retry budget deprecated backoff")
	write(t, filepath.Join(root, "docs-old.md"), "retry budget backoff")
	write(t, filepath.Join(root, "other.md"), "retry budget")
	_, c := index(t, s, root)
	r, err := s.Search(context.Background(), search.Request{Query: "retry -deprecated OR backoff", Mode: "advanced", SourceID: c.Source().ID, Limit: 1, PathPrefix: "docs", TitleContains: "guide"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != 1 || len(r.Results) != 1 || r.Results[0].Path != "docs/guide.md" {
		t.Fatalf("advanced filtered result: %+v", r)
	}
	r, err = s.Search(context.Background(), search.Request{Query: `"retry budget"`, Mode: "advanced", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != 3 {
		t.Fatalf("phrase: %+v", r)
	}
	quoteRoot := t.TempDir()
	write(t, filepath.Join(quoteRoot, "quote.md"), `he said "go" now`)
	index(t, s, quoteRoot)
	r, err = s.Search(context.Background(), search.Request{Query: `"said \"go\" now"`, Mode: "advanced", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != 1 {
		t.Fatalf("escaped phrase did not follow SQLite tokenizer: %+v", r)
	}
	r, err = s.Search(context.Background(), search.Request{Query: "backoff", Mode: "advanced", Limit: 10, PathPrefix: "docs/"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != 1 {
		t.Fatalf("path boundary: %+v", r)
	}
	specialSource := connector.Source{ID: "special-source", Kind: "filesystem", Name: "special", Root: "/synthetic-special"}
	specialScan, err := s.BeginScan(context.Background(), specialSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := specialScan.Upsert(context.Background(), connector.Document{ID: "special-id", SourceID: specialSource.ID, Title: `literal_%_".md`, URI: "file:///synthetic-special/literal.md", Path: `docs/literal_%_".md`, Content: "specialmarker", Hash: "special", MediaType: "text/markdown", ModifiedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := specialScan.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	r, err = s.Search(context.Background(), search.Request{Query: "specialmarker", Limit: 10, PathPrefix: `docs/literal_%_".md`, TitleContains: `%_"`})
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != 1 {
		t.Fatalf("literal percent/underscore/quote filters: %+v", r)
	}
}

func TestAdvancedPDFPhraseBoundariesAndTitleOnlyPage(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	source := connector.Source{ID: "synthetic-pdf", Kind: "filesystem", Name: "synthetic", Root: "/synthetic"}
	scan, err := s.BeginScan(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	docs := []connector.Document{
		{ID: "split", SourceID: source.ID, Title: "Split", URI: "file:///synthetic/split.pdf", Path: "split.pdf", Content: "alpha\nbeta", Hash: "split", MediaType: "application/pdf", Pages: []connector.Page{{Number: 1, Text: "alpha"}, {Number: 2, Text: "beta"}}},
		{ID: "whole", SourceID: source.ID, Title: "Whole", URI: "file:///synthetic/whole.pdf", Path: "whole.pdf", Content: "alpha beta", Hash: "whole", MediaType: "application/pdf", Pages: []connector.Page{{Number: 1, Text: "alpha beta"}}},
		{ID: "title", SourceID: source.ID, Title: "alpha beta title", URI: "file:///synthetic/title.pdf", Path: "title.pdf", Content: "unrelated", Hash: "title", MediaType: "application/pdf", Pages: []connector.Page{{Number: 1, Text: "unrelated"}}},
	}
	for _, doc := range docs {
		if _, err := scan.Upsert(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := scan.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := s.Search(ctx, search.Request{Query: `"alpha beta"`, Mode: "advanced", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != 2 {
		t.Fatalf("page phrase boundary/title: %+v", r)
	}
	r, err = s.Search(ctx, search.Request{Query: "alpha beta", Mode: "advanced", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var splitPage int
	for _, item := range r.Results {
		if item.ID == "split" {
			splitPage = item.Page
		}
	}
	if splitPage != 1 {
		t.Fatalf("distributed positive evidence page=%d results=%+v", splitPage, r.Results)
	}
	r, err = s.Search(ctx, search.Request{Query: `"alpha beta" OR unrelated`, Mode: "advanced", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range r.Results {
		if item.ID == "title" && (item.Page != 0 || item.URI != "file:///synthetic/title.pdf") {
			t.Fatalf("losing branch page used for title-only winning branch: %+v", item)
		}
	}
	for _, item := range r.Results {
		if item.ID == "split" {
			t.Fatal("phrase crossed PDF page boundary")
		}
		if item.ID == "title" && (item.Page != 0 || item.URI != "file:///synthetic/title.pdf") {
			t.Fatalf("title-only citation: %+v", item)
		}
	}
	r, err = s.Search(ctx, search.Request{Query: `alpha -"alpha beta"`, Mode: "advanced", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	foundSplit := false
	for _, item := range r.Results {
		if item.ID == "split" {
			foundSplit = true
		}
	}
	if !foundSplit {
		t.Fatalf("negative phrase crossed PDF page boundary: %+v", r)
	}
	r, err = s.Search(ctx, search.Request{Query: "alpha -beta", Mode: "advanced", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range r.Results {
		if item.ID == "split" {
			t.Fatal("negative bare term on another page was ignored")
		}
	}
}

func TestCanceledSearchStopsBeforeReturningResults(t *testing.T) {
	s := open(t)
	root := t.TempDir()
	write(t, filepath.Join(root, "note.md"), "retry budget")
	index(t, s, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Search(ctx, search.Request{Query: "retry", Mode: "advanced", Limit: 10})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled advanced search error=%v", err)
	}
}

type brokenConnector struct{ source connector.Source }

func (b brokenConnector) Source() connector.Source { return b.source }
func (b brokenConnector) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	if err := emit(connector.Document{ID: "partial", SourceID: b.source.ID, Title: "partial", Path: "partial", Content: "corruption", Hash: "new"}); err != nil {
		return connector.Report{}, err
	}
	return connector.Report{Seen: 1}, errors.New("directory became unreadable")
}

func TestFailedScanRollsBackUpdatesAndPruning(t *testing.T) {
	s := open(t)
	root := t.TempDir()
	write(t, filepath.Join(root, "original.md"), "original reliable")
	_, conn := index(t, s, root)
	_, err := ingest.Run(context.Background(), s, brokenConnector{source: conn.Source()})
	if err == nil {
		t.Fatal("expected failure")
	}
	if response := find(t, s, "original", "", 20); response.Total != 1 {
		t.Fatalf("previous snapshot lost: %+v", response)
	}
	if response := find(t, s, "corruption", "", 20); response.Total != 0 {
		t.Fatalf("partial write committed: %+v", response)
	}
}

func TestLiteralQueryValidationAndPersistence(t *testing.T) {
	dir, root := t.TempDir(), t.TempDir()
	s, err := sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "literal.md"), "payment webhook")
	index(t, s, root)
	if response := find(t, s, `payment OR webhook`, "", 20); response.Total != 0 {
		t.Fatalf("operator was interpreted: %+v", response)
	}
	for _, q := range []string{"", `"`, "+++"} {
		_, err := s.Search(context.Background(), search.Request{Query: q, Limit: 20})
		if !errors.Is(err, search.ErrQuery) {
			t.Fatalf("invalid query %q: %v", q, err)
		}
	}
	if _, err := s.Search(context.Background(), search.Request{Query: "payment", Limit: 101}); !errors.Is(err, search.ErrLimit) {
		t.Fatalf("invalid limit: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if response := find(t, s, "payment webhook", "", 20); response.Total != 1 || response.Results[0].Snippet == "" {
		t.Fatalf("reopen or snippet: %+v", response)
	}
}

type collisionConnector struct {
	source   connector.Source
	document connector.Document
}

func (c collisionConnector) Source() connector.Source { return c.source }
func (c collisionConnector) Scan(_ context.Context, emit func(connector.Document) error) (connector.Report, error) {
	err := emit(c.document)
	return connector.Report{Seen: 1}, err
}

func TestConnectorCannotOverwriteAnotherSource(t *testing.T) {
	s := open(t)
	root := t.TempDir()
	write(t, filepath.Join(root, "original.md"), "original evidence")
	index(t, s, root)
	original := find(t, s, "original", "", 20).Results[0]
	other := connector.Source{ID: "other", Kind: "test", Name: "Other"}
	_, err := ingest.Run(context.Background(), s, collisionConnector{
		source: other,
		document: connector.Document{ID: original.ID, SourceID: other.ID,
			Title: "replacement", Path: "replacement", Content: "corruption", Hash: "changed"},
	})
	if err == nil {
		t.Fatal("expected a cross-source identity collision to fail")
	}
	if got := find(t, s, "original", "", 20); got.Total != 1 || got.Results[0].SourceID != original.SourceID {
		t.Fatalf("original source was changed: %+v", got)
	}
	if got := find(t, s, "corruption", "", 20); got.Total != 0 {
		t.Fatalf("colliding document was committed: %+v", got)
	}
	sources, err := s.Sources(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("failed source was committed: %+v, %v", sources, err)
	}
}
