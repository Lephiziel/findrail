package sqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/diagnostics"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/snapshot"
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
	if _, ok := without["report"].(sqlite.ReportSummary); !ok {
		t.Fatalf("default lookup loaded detail response: %T", without["report"])
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
	if _, ok := with["report"].(diagnostics.Report); !ok {
		t.Fatalf("explicit details response missing samples: %T", with["report"])
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

var errSyntheticScan = errors.New("synthetic extraction failure from private path")

func (c failingReportConnector) Source() connector.Source { return c.source }
func (c failingReportConnector) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	return connector.Report{}, errSyntheticScan
}

type uninstrumentedConnector struct{ inner connector.Connector }

func (c uninstrumentedConnector) Source() connector.Source { return c.inner.Source() }
func (c uninstrumentedConnector) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	r, err := c.inner.Scan(ctx, emit)
	return connector.Report{Seen: r.Seen, Skipped: r.Skipped, SkippedPDF: r.SkippedPDF, SkippedDOCX: r.SkippedDOCX}, err
}

func TestUninstrumentedSuccessInvalidatesPriorReport(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "note.md"), []byte("unmodified document"), 0600)
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	conn, err := filesystem.New(root, 1024)
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
	if before["available"] != true {
		t.Fatal("initial instrumented report missing")
	}
	if _, err = ingest.Refresh(ctx, store, uninstrumentedConnector{inner: conn}); err != nil {
		t.Fatal(err)
	}
	after, err := store.SourceReport(ctx, first.Source.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if after["available"] != false || after["availability"] != "not_yet_available" {
		t.Fatalf("stale diagnostics were retained: %v", after)
	}
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
	attempt, scanErr := ingest.Refresh(ctx, store, failed)
	err = scanErr
	if err == nil {
		t.Fatal("expected synthetic failure")
	}
	if !errors.Is(err, errSyntheticScan) || strings.Contains(err.Error(), "private path") || attempt.Attempt == nil || attempt.Attempt.Committed || attempt.Attempt.Complete || attempt.Attempt.FailureCode != "scan_failed" {
		t.Fatalf("failure attempt is not bounded/truthful: result=%+v err=%v", attempt, err)
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

func TestSchemaSixMigrationPreservesFrozenArchiveAndPortableFingerprint(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	origin := snapshot.Origin{ID: "schema5-origin", Kind: "filesystem", Name: "Synthetic source", Location: "/synthetic/origin", IndexedAt: "2026-10-09T12:00:00Z"}
	docs := []snapshot.Document{{ID: "origin-document", Path: "note.md", Title: "Synthetic note", URI: "file:///synthetic/origin/note.md", MediaType: "text/markdown", Text: "schema five archive evidence", ContentHash: strings.Repeat("a", 64), SizeBytes: 28, ModifiedAt: "2026-10-09T11:00:00Z"}}
	encoded, err := snapshot.Encode(snapshot.Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: origin}, docs, nil)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := snapshot.Inspect(encoded)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.ImportSnapshot(ctx, "Frozen migration fixture", archive)
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
	store, err = sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sources, err := store.Sources(ctx)
	if err != nil || len(sources) != 1 || sources[0].Kind != "archive" || sources[0].Archive == nil || sources[0].Archive.Fingerprint != archive.Manifest.Fingerprint || sources[0].Documents != 1 {
		t.Fatalf("archive changed during migration: %+v %v", sources, err)
	}
	if result, err := store.Search(ctx, search.Request{Query: "schema five", SourceID: id, Limit: 10}); err != nil || result.Total != 1 {
		t.Fatalf("archive content changed: %+v %v", result, err)
	}
	report, err := store.SourceReport(ctx, id, false)
	if err != nil || report["availability"] != "frozen_origin_report_not_in_portable_v1" {
		t.Fatalf("archive diagnostics invented during migration: %v %v", report, err)
	}
	var exported bytes.Buffer
	manifest, _, _, err := store.StreamSnapshot(ctx, id, "test-after-migration", time.Now().UTC().Format(time.RFC3339Nano), &exported)
	if err != nil || manifest.Fingerprint != archive.Manifest.Fingerprint {
		t.Fatalf("portable fingerprint changed: %s vs %s, %v", manifest.Fingerprint, archive.Manifest.Fingerprint, err)
	}
}
