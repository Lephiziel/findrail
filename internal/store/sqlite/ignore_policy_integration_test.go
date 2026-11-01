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

type failAfterDocument struct{ inner connector.Connector }

func (f failAfterDocument) Source() connector.Source { return f.inner.Source() }
func (f failAfterDocument) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	return f.inner.Scan(ctx, func(doc connector.Document) error {
		if err := emit(doc); err != nil {
			return err
		}
		return errors.New("synthetic callback failure")
	})
}

func TestIgnorePolicySQLiteEvidenceRollbackRepairRemovalAndForget(t *testing.T) {
	ctx := context.Background()
	root, index := t.TempDir(), t.TempDir()
	data := map[string]string{"drop.md": "alpha baseline evidence", "keep.md": "rescue searchable evidence"}
	for name, body := range data {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := sqlite.Open(ctx, index)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := filesystem.New(root, 1<<20, index)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, store, conn); err != nil {
		t.Fatal(err)
	}
	searchCount := func(q string) int {
		t.Helper()
		r, e := store.Search(ctx, search.Request{Query: q, Limit: 10})
		if e != nil {
			t.Fatal(e)
		}
		return r.Total
	}
	reportID := func() string {
		t.Helper()
		r, e := store.SourceReport(ctx, conn.Source().ID, false)
		if e != nil {
			t.Fatal(e)
		}
		return r["report"].(sqlite.ReportSummary).ReportID
	}
	initialReport := reportID()
	baseline := storeSearch(t, store, "alpha")
	if baseline.Total != 1 {
		t.Fatalf("baseline FTS result: %+v", baseline)
	}
	evidence, err := store.Evidence(ctx, baseline.Results[0].ID, 0)
	if err != nil || evidence.Text != "alpha baseline evidence" {
		t.Fatalf("baseline evidence: %+v %v", evidence, err)
	}
	policy := filepath.Join(root, ".findrailignore")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(policy, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("*.md\n!/keep.md\n")
	pruned, err := ingest.Refresh(ctx, store, conn)
	if err != nil || pruned.Removed != 1 || searchCount("alpha") != 0 || searchCount("rescue") != 1 {
		t.Fatalf("rule-driven prune/reinclude: %+v %v", pruned, err)
	}
	committedReport := reportID()
	if committedReport == initialReport {
		t.Fatal("successful ignore scan did not update durable report")
	}
	summary, err := store.SourceReport(ctx, conn.Source().ID, false)
	if err != nil {
		t.Fatal(err)
	}
	foundReason := false
	for _, reason := range summary["report"].(sqlite.ReportSummary).Reasons {
		if reason.Code == "excluded_path" && reason.Unit == "file" && reason.Count == 1 {
			foundReason = true
		}
	}
	if !foundReason {
		t.Fatalf("missing durable policy diagnostic: %+v", summary["report"])
	}
	write("[malformed\n")
	if _, err := ingest.Refresh(ctx, store, conn); err == nil {
		t.Fatal("malformed policy refresh succeeded")
	}
	if reportID() != committedReport || searchCount("rescue") != 1 {
		t.Fatal("malformed policy replaced committed FTS/report snapshot")
	}
	write("*.md\n!/keep.md\n")
	if _, err := ingest.Refresh(ctx, store, failAfterDocument{inner: conn}); err == nil {
		t.Fatal("synthetic callback failure committed")
	}
	if reportID() != committedReport || searchCount("rescue") != 1 {
		t.Fatal("callback failure replaced prior FTS/report snapshot")
	}
	if err := os.Remove(policy); err != nil {
		t.Fatal(err)
	}
	restored, err := ingest.Refresh(ctx, store, conn)
	if err != nil || restored.Seen != 2 || searchCount("alpha") != 1 {
		t.Fatalf("policy removal reindex: %+v %v", restored, err)
	}
	if reportID() == committedReport {
		t.Fatal("successful policy removal did not publish a new report")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.Open(ctx, index)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got := storeSearch(t, store, "alpha"); got.Total != 1 {
		t.Fatalf("restart lost committed searchable snapshot: %+v", got)
	}
	if err := store.ForgetSource(ctx, conn.Source().ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Refresh(ctx, store, conn); !errors.Is(err, ingest.ErrSourceGone) {
		t.Fatalf("forgotten source refresh: %v", err)
	}
}

func storeSearch(t *testing.T, store *sqlite.Store, q string) search.Response {
	t.Helper()
	r, err := store.Search(context.Background(), search.Request{Query: q, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
