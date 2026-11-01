package filesystem_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
)

func TestIgnorePolicyChangeDuringRefreshRollsBackWholeInventory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	data := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "generated"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.md"), []byte("stable keep evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "generated", "old.md"), []byte("generated evidence retained"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	conn, err := filesystem.New(root, 1<<20, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Run(ctx, store, conn); err != nil {
		t.Fatal(err)
	}
	baseline, err := store.SourceReport(ctx, conn.Source().ID, false)
	if err != nil {
		t.Fatal(err)
	}
	baselineID := baseline["report"].(sqlite.ReportSummary).ReportID

	entered := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	refreshDone := make(chan error, 1)
	go func() {
		_, refreshErr := ingest.RefreshWithProgress(ctx, store, conn, func(processed, skipped int, complete, flush bool) {
			if processed > 0 {
				once.Do(func() {
					close(entered)
					<-resume
				})
			}
		})
		refreshDone <- refreshErr
	}()
	<-entered
	policy := filepath.Join(root, ".findrailignore")
	tempPolicy := filepath.Join(root, ".policy-save.tmp")
	if err := os.WriteFile(tempPolicy, []byte("generated/\n"), 0600); err != nil {
		close(resume)
		t.Fatal(err)
	}
	if err := os.Rename(tempPolicy, policy); err != nil {
		close(resume)
		t.Fatal(err)
	}
	close(resume)
	if err := <-refreshDone; err == nil {
		t.Fatal("refresh committed inventory scanned against a changed ignore policy")
	}

	for _, query := range []string{"stable", "generated"} {
		result, searchErr := store.Search(ctx, search.Request{Query: query, Limit: 10})
		if searchErr != nil || result.Total != 1 {
			t.Fatalf("partial refresh replaced prior %q snapshot: %+v %v", query, result, searchErr)
		}
	}
	unchanged, err := store.SourceReport(ctx, conn.Source().ID, false)
	if err != nil || unchanged["report"].(sqlite.ReportSummary).ReportID != baselineID {
		t.Fatalf("failed policy refresh replaced successful report: %+v %v", unchanged, err)
	}
	if _, err := ingest.Refresh(ctx, store, conn); err != nil {
		t.Fatalf("retry did not use the new policy: %v", err)
	}
	removed, err := store.Search(ctx, search.Request{Query: "generated", Limit: 10})
	if err != nil || removed.Total != 0 {
		t.Fatalf("retry did not reconcile with the changed policy: %+v %v", removed, err)
	}
}

func TestCanceledRefreshDuringEnumerationKeepsCommittedSnapshot(t *testing.T) {
	ctx := context.Background()
	root, data := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("committed cancellation baseline"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	conn, err := filesystem.New(root, 1<<20, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Run(ctx, store, conn); err != nil {
		t.Fatal(err)
	}
	baseline, err := store.SourceReport(ctx, conn.Source().ID, false)
	if err != nil {
		t.Fatal(err)
	}
	baselineID := baseline["report"].(sqlite.ReportSummary).ReportID
	refreshCtx, cancel := context.WithCancel(ctx)
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	result := make(chan error, 1)
	go func() {
		_, refreshErr := ingest.RefreshWithProgress(refreshCtx, store, conn, func(processed, _ int, _, _ bool) {
			if processed > 0 {
				once.Do(func() { close(entered); <-resume })
			}
		})
		result <- refreshErr
	}()
	<-entered
	cancel()
	close(resume)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled scan result = %v, want context.Canceled", err)
	}
	searchable, err := store.Search(ctx, search.Request{Query: "cancellation", Limit: 10})
	if err != nil || searchable.Total != 1 {
		t.Fatalf("canceled scan disturbed committed search snapshot: %+v %v", searchable, err)
	}
	current, err := store.SourceReport(ctx, conn.Source().ID, false)
	if err != nil || current["report"].(sqlite.ReportSummary).ReportID != baselineID {
		t.Fatalf("canceled scan replaced successful report: %+v %v", current, err)
	}
}
