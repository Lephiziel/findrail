package sqlite_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
