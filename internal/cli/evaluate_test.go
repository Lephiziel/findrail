package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/evaluation"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
)

func TestRunEvaluationHelp(t *testing.T) {
	var out bytes.Buffer
	var stderr bytes.Buffer

	ctx := context.Background()

	err := Run(ctx, []string{"evaluate", "--help"}, &out, &stderr, "test")
	if err != nil {
		t.Fatalf("runEvaluate() error=%v, want no error", err)
	}
	if out.Len() != 0 {
		t.Errorf("out.Len()=%d, want 0", out.Len())
	}

	help := stderr.String()
	if !strings.Contains(help, "-queries") {
		t.Errorf("help does not contain -queries: %q", help)
	}
	if !strings.Contains(help, "-source") {
		t.Errorf("help does not contain -source: %q", help)
	}
	if !strings.Contains(help, "-data-dir") {
		t.Errorf("help does not contain -data-dir: %q", help)
	}
	if !strings.Contains(help, "-json") {
		t.Errorf("help does not contain -json: %q", help)
	}
}

func TestRunEvaluateDoesNotMigrateIndex(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, err := sqlite.Open(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "findrail.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE indexing_reports; PRAGMA user_version=5"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	queriesPath := filepath.Join(t.TempDir(), "queries.json")
	if err := os.WriteFile(queriesPath, []byte(`[{"query":"hello","expected_paths":[]}]`), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	var stderr bytes.Buffer
	err = runEvaluate(ctx, []string{"--queries", queriesPath, "--data-dir", dataDir, "--source", "missing"}, &out, &stderr)
	if err == nil {
		t.Fatal("runEvaluate() error=nil, want unsupported index error")
	}
	db, err = sql.Open("sqlite", filepath.Join(dataDir, "findrail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 5 {
		t.Fatalf("schema version=%d, want unchanged version 5", version)
	}
}

func TestRunEvaluateInvalidArgs(t *testing.T) {
	type testCase struct {
		name string
		args []string
	}

	scenes := []testCase{
		{
			name: "no-queries",
			args: []string{"--source", "test_source"},
		},
		{
			name: "no-source",
			args: []string{"--queries", "queries.json"},
		},
		{
			name: "unknown-flag",
			args: []string{"--unknown"},
		},
		{
			name: "positional-argument",
			args: []string{"extra"},
		},
	}

	for _, scene := range scenes {
		t.Run(scene.name, func(t *testing.T) {
			var out bytes.Buffer
			var stderr bytes.Buffer

			ctx := context.Background()

			err := runEvaluate(ctx, scene.args, &out, &stderr)
			if err == nil {
				t.Fatalf("runEvaluate() error=nil, want error")
			}
			if out.Len() != 0 {
				t.Errorf("out.Len()=%d, want 0", out.Len())
			}
		})
	}
}

func TestRunEvaluateInvalidCorpus(t *testing.T) {
	var out bytes.Buffer
	var stderr bytes.Buffer

	ctx := context.Background()

	dir := t.TempDir()
	queriesPath := filepath.Join(dir, "queries.json")
	dbPath := filepath.Join(dir, "findrail.db")
	if err := os.WriteFile(queriesPath, []byte("["), 0644); err != nil {
		t.Fatalf("os.WriteFile() error=%v", err)
	}

	args := []string{"--queries", queriesPath, "--source", "test_source", "--data-dir", dir}

	err := runEvaluate(ctx, args, &out, &stderr)
	if err == nil {
		t.Fatalf("runEvaluate() error=nil, want error")
	}
	if !strings.Contains(err.Error(), "load queries:") {
		t.Errorf("error=%q, want load queries error", err.Error())
	}
	if out.Len() != 0 {
		t.Errorf("out.Len()=%d, want 0", out.Len())
	}
	_, infoErr := os.Stat(dbPath)
	if !errors.Is(infoErr, os.ErrNotExist) {
		t.Errorf("infoErr=%v, want os.ErrNotExist", infoErr)
	}
}

func TestRunEvaluateMissingIndex(t *testing.T) {
	var out bytes.Buffer
	var stderr bytes.Buffer

	ctx := context.Background()

	dir := t.TempDir()
	queriesPath := filepath.Join(dir, "queries.json")
	dbPath := filepath.Join(dir, "findrail.db")

	if err := os.WriteFile(queriesPath, []byte(`[{"query":"hello","expected_paths":[]}]`), 0644); err != nil {
		t.Fatalf("os.WriteFile() error=%v", err)
	}

	args := []string{"--queries", queriesPath, "--source", "test_source", "--data-dir", dir}

	err := runEvaluate(ctx, args, &out, &stderr)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runEvaluate() error=%v, want os.ErrNotExist", err)
	}
	if !strings.Contains(err.Error(), "stat index") {
		t.Errorf("error=%v, want stat index error", err)
	}
	if out.Len() != 0 {
		t.Errorf("out.Len()=%d, want 0", out.Len())
	}

	_, infoErr := os.Stat(dbPath)
	if !errors.Is(infoErr, os.ErrNotExist) {
		t.Errorf("infoErr=%v, want os.ErrNotExist", infoErr)
	}
}

func TestRunEvaluateUnknownSource(t *testing.T) {
	var out bytes.Buffer
	var stderr bytes.Buffer

	ctx := context.Background()

	dir := t.TempDir()

	queriesPath := filepath.Join(dir, "queries.json")

	store, storeErr := sqlite.Open(ctx, dir)
	if storeErr != nil {
		t.Fatalf("sqlite.Open() error=%v, want no error", storeErr)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close() error=%v, want no error", err)
	}

	if err := os.WriteFile(queriesPath, []byte(`[{"query":"hello","expected_paths":[]}]`), 0644); err != nil {
		t.Fatalf("os.WriteFile() error=%v", err)
	}

	args := []string{"--queries", queriesPath, "--source", "test_source", "--data-dir", dir}

	err := runEvaluate(ctx, args, &out, &stderr)
	if err == nil {
		t.Fatalf("runEvaluate() error=nil, want error")
	}
	if !strings.Contains(err.Error(), `source "test_source" not found`) {
		t.Errorf("error=%v, want source not found", err)
	}
	if out.Len() != 0 {
		t.Errorf("out.Len()=%d, want 0", out.Len())
	}
}

func TestRunEvaluateSuccessJSON(t *testing.T) {
	var out bytes.Buffer
	var stderr bytes.Buffer

	ctx := context.Background()

	dataDir := t.TempDir()
	sourceDir := t.TempDir()

	queriesPath := filepath.Join(dataDir, "queries.json")
	filePath := filepath.Join(sourceDir, "a.md")

	if err := os.WriteFile(filePath, []byte(`hello`), 0644); err != nil {
		t.Fatalf("os.WriteFile() error=%v", err)
	}
	if err := os.WriteFile(queriesPath, []byte(`[{"query":"hello","expected_paths":["a.md"]}]`), 0644); err != nil {
		t.Fatalf("os.WriteFile() error=%v", err)
	}

	store, storeErr := sqlite.Open(ctx, dataDir)
	if storeErr != nil {
		t.Fatalf("sqlite.Open() error=%v, want no error", storeErr)
	}
	conn, connErr := filesystem.New(sourceDir, filesystem.DefaultMaxBytes)
	if connErr != nil {
		t.Fatalf("filesystem.New() error=%v, want no error", connErr)
	}

	indexed, indexErr := ingest.Run(ctx, store, conn)
	if indexErr != nil {
		t.Fatalf("ingest.Run() error=%v, want no error", indexErr)
	}

	sourceID := indexed.Source.ID

	if err := store.Close(); err != nil {
		t.Fatalf("store.Close() error=%v, want no error", err)
	}

	args := []string{"--queries", queriesPath, "--source", sourceID, "--data-dir", dataDir, "--json"}

	err := runEvaluate(ctx, args, &out, &stderr)
	if err != nil {
		t.Fatalf("runEvaluate() error=%v, want no error", err)
	}

	var report evaluation.Report

	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v; output=%q", err, out.String())
	}

	if len(report.Results) != 1 {
		t.Fatalf("len(report.Results)=%d, want 1", len(report.Results))
	}
	if report.Total != 1 {
		t.Errorf("total=%d, want 1", report.Total)
	}
	if report.Passed != 1 {
		t.Errorf("passed=%d, want 1", report.Passed)
	}
	if report.Failed != 0 {
		t.Errorf("failed=%d, want 0", report.Failed)
	}

	result := report.Results[0]
	wantPaths := []string{"a.md"}

	if !reflect.DeepEqual(result.ExpectedPaths, wantPaths) {
		t.Errorf("result.ExpectedPaths=%#v, want %#v", result.ExpectedPaths, wantPaths)
	}

	if !reflect.DeepEqual(result.ActualPaths, wantPaths) {
		t.Errorf("result.ActualPaths=%#v, want %#v", result.ActualPaths, wantPaths)
	}
}

func TestRunEvaluateMismatchText(t *testing.T) {
	var out bytes.Buffer
	var stderr bytes.Buffer

	ctx := context.Background()

	dataDir := t.TempDir()
	sourceDir := t.TempDir()

	queriesPath := filepath.Join(dataDir, "queries.json")
	filePath := filepath.Join(sourceDir, "a.md")

	if err := os.WriteFile(filePath, []byte(`hello`), 0644); err != nil {
		t.Fatalf("os.WriteFile() error=%v", err)
	}
	if err := os.WriteFile(queriesPath, []byte(`[{"query":"hello","expected_paths":["b.md"]}]`), 0644); err != nil {
		t.Fatalf("os.WriteFile() error=%v", err)
	}

	store, storeErr := sqlite.Open(ctx, dataDir)
	if storeErr != nil {
		t.Fatalf("sqlite.Open() error=%v, want no error", storeErr)
	}
	storeClosed := false
	defer func() {
		if !storeClosed {
			_ = store.Close()
		}
	}()
	conn, connErr := filesystem.New(sourceDir, filesystem.DefaultMaxBytes)
	if connErr != nil {
		t.Fatalf("filesystem.New() error=%v, want no error", connErr)
	}

	indexed, indexErr := ingest.Run(ctx, store, conn)
	if indexErr != nil {
		t.Fatalf("ingest.Run() error=%v, want no error", indexErr)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close() error=%v, want no error", err)
	}
	storeClosed = true

	args := []string{"--queries", queriesPath, "--source", indexed.Source.ID, "--data-dir", dataDir}
	err := runEvaluate(ctx, args, &out, &stderr)
	if !errors.Is(err, ErrMismatchedResults) {
		t.Fatalf("runEvaluate() error=%v, want ErrMismatchedResults", err)
	}

	for _, want := range []string{
		"1 cases: 0 passed, 1 failed",
		`query: "hello"`,
		`missing_paths=["b.md"]`,
		`unexpected_paths=["a.md"]`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output %q does not contain %q", out.String(), want)
		}
	}
}

func TestRunEvaluateMismatchJSON(t *testing.T) {
	var out bytes.Buffer
	var stderr bytes.Buffer

	ctx := context.Background()

	dataDir := t.TempDir()
	sourceDir := t.TempDir()

	queriesPath := filepath.Join(dataDir, "queries.json")
	filePath := filepath.Join(sourceDir, "a.md")

	if err := os.WriteFile(filePath, []byte(`hello`), 0644); err != nil {
		t.Fatalf("os.WriteFile() error=%v", err)
	}
	if err := os.WriteFile(queriesPath, []byte(`[{"query":"hello","expected_paths":["b.md"]}]`), 0644); err != nil {
		t.Fatalf("os.WriteFile() error=%v", err)
	}

	store, storeErr := sqlite.Open(ctx, dataDir)
	if storeErr != nil {
		t.Fatalf("sqlite.Open() error=%v, want no error", storeErr)
	}
	storeClosed := false
	defer func() {
		if !storeClosed {
			_ = store.Close()
		}
	}()
	conn, connErr := filesystem.New(sourceDir, filesystem.DefaultMaxBytes)
	if connErr != nil {
		t.Fatalf("filesystem.New() error=%v, want no error", connErr)
	}

	indexed, indexErr := ingest.Run(ctx, store, conn)
	if indexErr != nil {
		t.Fatalf("ingest.Run() error=%v, want no error", indexErr)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close() error=%v, want no error", err)
	}
	storeClosed = true

	args := []string{"--queries", queriesPath, "--source", indexed.Source.ID, "--data-dir", dataDir, "--json"}
	err := runEvaluate(ctx, args, &out, &stderr)
	if !errors.Is(err, ErrMismatchedResults) {
		t.Fatalf("runEvaluate() error=%v, want ErrMismatchedResults", err)
	}

	var report evaluation.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v; output=%q", err, out.String())
	}
	if report.SourceID != indexed.Source.ID {
		t.Errorf("report.SourceID=%q, want %q", report.SourceID, indexed.Source.ID)
	}
	if report.Total != 1 {
		t.Errorf("report.Total=%d, want 1", report.Total)
	}
	if report.Passed != 0 {
		t.Errorf("report.Passed=%d, want 0", report.Passed)
	}
	if report.Failed != 1 {
		t.Errorf("report.Failed=%d, want 1", report.Failed)
	}
	if report.ExactMatchRate != 0 {
		t.Errorf("report.ExactMatchRate=%v, want 0", report.ExactMatchRate)
	}
	if len(report.Results) != 1 {
		t.Fatalf("len(report.Results)=%d, want 1", len(report.Results))
	}
	result := report.Results[0]
	if !reflect.DeepEqual(result.MissingPaths, []string{"b.md"}) {
		t.Errorf("result.MissingPaths=%#v, want %#v", result.MissingPaths, []string{"b.md"})
	}
	if !reflect.DeepEqual(result.UnexpectedPaths, []string{"a.md"}) {
		t.Errorf("result.UnexpectedPaths=%#v, want %#v", result.UnexpectedPaths, []string{"a.md"})
	}
}
