package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/diagnostics"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func TestCommittedReportAndReadDetails(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "note.md"), []byte("hello report"), 0600)
	_ = os.WriteFile(filepath.Join(root, "picture.png"), []byte("not indexed"), 0600)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	conn, err := filesystem.New(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ingest.Run(ctx, store, conn)
	if err != nil {
		t.Fatal(err)
	}
	without, err := store.SourceReport(ctx, result.Source.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if without["available"] != true {
		t.Fatalf("report unavailable: %v", without)
	}
	sources, err := store.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].IndexingReport == nil || !sources[0].IndexingReport.Available || sources[0].IndexingReport.SkippedFiles != 1 {
		t.Fatalf("bounded inventory summary missing: %+v", sources)
	}
	encoded, _ := json.Marshal(without)
	if strings.Contains(string(encoded), "picture.png") {
		t.Fatal("default report leaked sample path")
	}
	with, err := store.SourceReport(ctx, result.Source.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(with)
	if !strings.Contains(string(body), "picture.png") {
		t.Fatalf("details omitted retained example: %s", body)
	}
	if err = store.ForgetSource(ctx, result.Source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SourceReport(ctx, result.Source.ID, true); err != sqlite.ErrSourceNotFound {
		t.Fatalf("forgotten source report: %v", err)
	}
}

type failingReportConnector struct{ source connector.Source }

func (c failingReportConnector) Source() connector.Source { return c.source }
func (c failingReportConnector) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	return connector.Report{}, errors.New("synthetic extraction failure")
}

func TestFailedRefreshPreservesCommittedReport(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "note.md"), []byte("initial"), 0600)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	conn, err := filesystem.New(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ingest.Run(ctx, store, conn)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.SourceReport(ctx, first.Source.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	failed := failingReportConnector{source: conn.Source()}
	if _, err = ingest.Refresh(ctx, store, failed); err == nil {
		t.Fatal("expected synthetic failure")
	}
	after, err := store.SourceReport(ctx, first.Source.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if before["report"].(diagnostics.Report).ID != after["report"].(diagnostics.Report).ID {
		t.Fatal("failed refresh replaced committed report")
	}
}

func TestSchemaFiveReportLookupIsReadOnlyAndUnavailable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "note.md"), []byte("note"), 0600)
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := filesystem.New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := ingest.Run(ctx, store, conn)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	db, err := sql.Open("sqlite", filepath.Join(dir, "findrail.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DROP TABLE indexing_reports; PRAGMA user_version=5"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	readonly, err := sqlite.OpenReadOnlyForReport(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	result, err := readonly.SourceReport(ctx, indexed.Source.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if result["available"] != false || result["availability"] != "not_yet_available" {
		t.Fatalf("schema-5 report status: %v", result)
	}
	_ = readonly.Close()
	check, err := sql.Open("sqlite", filepath.Join(dir, "findrail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var version int
	if err = check.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 5 {
		t.Fatalf("report lookup migrated schema: version=%d err=%v", version, err)
	}
}
