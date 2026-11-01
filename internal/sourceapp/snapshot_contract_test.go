package sourceapp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSnapshotPendingSummaryDoesNotPublishInvalidSuccessMetadata(t *testing.T) {
	raw, err := json.Marshal(SnapshotSummary{Status: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"format", "version", "kind", "fingerprint", "frozen", "claim"} {
		if _, ok := fields[key]; ok {
			t.Errorf("pending summary exposes invalid %s: %s", key, raw)
		}
	}
}

func TestForgetCancelsSnapshotExportWaitingForSourceAuthority(t *testing.T) {
	a, store, _ := openApp(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("synthetic snapshot authority"), 0600); err != nil {
		t.Fatal(err)
	}
	add, err := a.AddFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	if done := await(t, a, add.ID); done.Status != "succeeded" {
		t.Fatalf("add: %+v", done)
	}
	sources, err := store.Sources(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources: %+v %v", sources, err)
	}
	id := sources[0].ID
	unlock, err := a.coordinator.Acquire(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(unlock) }
	defer release()
	output := filepath.Join(t.TempDir(), "must-not-publish.zip")
	export, err := a.ExportSnapshot(id, output)
	if err != nil {
		t.Fatal(err)
	}
	remove, err := a.Remove(id)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	finished := a.byID[export.ID].finished
	a.mu.Unlock()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		release()
		t.Fatal("Forget did not cancel the export waiting on the source lock")
	}
	release()
	if done := await(t, a, export.ID); done.Status != "canceled" {
		t.Fatalf("export: %+v", done)
	}
	if done := await(t, a, remove.ID); done.Status != "succeeded" {
		t.Fatalf("Forget: %+v", done)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("canceled export published output: %v", err)
	}
}

func TestSnapshotExportRejectsReplacedSourceRegistration(t *testing.T) {
	a, store, dir := openApp(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("synthetic registration"), 0600); err != nil {
		t.Fatal(err)
	}
	add, err := a.AddFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	if done := await(t, a, add.ID); done.Status != "succeeded" {
		t.Fatalf("add: %+v", done)
	}
	sources, err := store.Sources(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources: %+v %v", sources, err)
	}
	id := sources[0].ID
	unlock, err := a.coordinator.Acquire(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(unlock) }
	defer release()
	target := filepath.Join(t.TempDir(), "replaced.zip")
	export, err := a.ExportSnapshot(id, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ForgetSource(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	other := New(context.Background(), store, dir, "/unused")
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := other.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	readd, err := other.AddFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	if done := await(t, other, readd.ID); done.Status != "succeeded" {
		t.Fatalf("re-add: %+v", done)
	}
	release()
	if done := await(t, a, export.ID); done.Status != "failed" || done.ErrorCode != "stale_source" {
		t.Fatalf("old authority exported replacement: %+v", done)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("stale export published output: %v", err)
	}
}
