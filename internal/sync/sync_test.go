package sync_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/sourcecoord"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	syncer "github.com/Lephiziel/findrail/internal/sync"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("source did not reconcile before deadline")
}
func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticLifecycle(t *testing.T) {
	for _, pollOnly := range []bool{false, true} {
		name := "events"
		if pollOnly {
			name = "polling"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			root, data := t.TempDir(), t.TempDir()
			s, err := sqlite.Open(ctx, data)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			factory := func(source connector.Source) (*filesystem.Connector, error) {
				return filesystem.NewWithOptions(source.Root, filesystem.Options{MaxTextBytes: source.MaxTextBytes, MaxPDFBytes: source.MaxPDFBytes, MaxDOCXBytes: source.MaxDOCXBytes, RegistrationToken: source.RegistrationToken, RegistrationRevision: source.RegistrationRevision}, data)
			}
			conn, err := filesystem.New(root, 1<<20, data)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "notes.md")
			put(t, path, "initial evidence")
			if _, err := ingest.Run(ctx, s, conn); err != nil {
				t.Fatal(err)
			}
			interval := time.Hour
			if pollOnly {
				interval = 80 * time.Millisecond
			}
			m := syncer.New(s, syncer.Config{Factory: factory, Interval: interval, DiscoveryInterval: 20 * time.Millisecond, Debounce: 20 * time.Millisecond, PollOnly: pollOnly})
			done := make(chan struct{})
			go func() { defer close(done); m.Run(ctx) }()
			defer func() { cancel(); <-done }()
			eventually(t, func() bool { status := m.Status(); return len(status) == 1 && status[0].State == "idle" })
			initialProgress := m.Status()[0].Progress
			if !initialProgress.Committed || initialProgress.ReportID == "" || initialProgress.Phase != "done" || initialProgress.Sequence < 3 {
				t.Fatalf("watcher did not publish committed progress: %+v", initialProgress)
			}
			has := func(query string, count int) bool {
				r, e := s.Search(ctx, search.Request{Query: query, Limit: 20})
				return e == nil && r.Total == count
			}
			put(t, path, "updated evidence")
			eventually(t, func() bool { return has("updated", 1) && has("initial", 0) })
			eventually(t, func() bool {
				status := m.Status()
				return len(status) == 1 && status[0].Progress.Committed && status[0].Progress.ProcessedDocuments >= 1 && status[0].Progress.ReportID != initialProgress.ReportID
			})
			if p := m.Status()[0].Progress; !p.Committed || p.ProcessedDocuments < 1 || p.ReportID == initialProgress.ReportID {
				t.Fatalf("successful watcher refresh progress/report not updated: %+v", p)
			}
			nested := filepath.Join(root, "nested")
			if err := os.Mkdir(nested, 0700); err != nil {
				t.Fatal(err)
			}
			other := filepath.Join(nested, "other.md")
			put(t, other, "nested fresh")
			eventually(t, func() bool { return has("nested", 1) })
			put(t, other, "nested changed")
			eventually(t, func() bool { return has("changed", 1) && has("fresh", 0) })
			if !pollOnly {
				tmp := filepath.Join(nested, "atomic-save.tmp")
				put(t, tmp, "atomic replacement marker")
				if err := os.Rename(tmp, other); err != nil {
					t.Fatal(err)
				}
				eventually(t, func() bool {
					r, e := s.Search(ctx, search.Request{Query: "atomic", Limit: 20})
					return e == nil && r.Total == 1 && has("changed", 0)
				})
			}
			if err := os.Rename(path, filepath.Join(root, "renamed.md")); err != nil {
				t.Fatal(err)
			}
			eventually(t, func() bool {
				r, e := s.Search(ctx, search.Request{Query: "updated", Limit: 20})
				return e == nil && r.Total == 1 && r.Results[0].Path == "renamed.md"
			})
			if err := os.Remove(other); err != nil {
				t.Fatal(err)
			}
			eventually(t, func() bool { return has("nested", 0) })
			if err := s.ForgetSource(ctx, conn.Source().ID); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(root, "renamed.md"), "must not resurrect")
			eventually(t, func() bool { return len(m.Status()) == 0 })
			if !has("resurrect", 0) {
				t.Fatal("forgotten source returned")
			}
		})
	}
}

func TestMissingRootPreservesSnapshotAndRecovers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	parent := t.TempDir()
	root := filepath.Join(parent, "notes")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	put(t, filepath.Join(root, "note.md"), "retained snapshot")
	conn, err := filesystem.New(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Run(ctx, s, conn); err != nil {
		t.Fatal(err)
	}
	locks := sourcecoord.New()
	m := syncer.New(s, syncer.Config{Coordinator: locks, Factory: func(source connector.Source) (*filesystem.Connector, error) {
		return filesystem.NewWithOptions(source.Root, filesystem.Options{MaxTextBytes: source.MaxTextBytes, MaxPDFBytes: source.MaxPDFBytes, MaxDOCXBytes: source.MaxDOCXBytes, RegistrationToken: source.RegistrationToken, RegistrationRevision: source.RegistrationRevision})
	}, Interval: 50 * time.Millisecond, PollOnly: false})
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	defer func() { cancel(); <-done }()
	eventually(t, func() bool { return len(m.Status()) == 1 && m.Status()[0].State == "idle" })
	lastSuccessAt := m.Status()[0].LastSuccessAt
	// Exclude successful polls between the baseline report and moving the root.
	unlock, err := locks.Acquire(ctx, conn.Source().ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	prior, err := s.SourceReport(ctx, conn.Source().ID, false)
	if err != nil {
		t.Fatal(err)
	}
	priorID := prior["report"].(sqlite.ReportSummary).ReportID
	moved := filepath.Join(parent, "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	unlock()
	eventually(t, func() bool { return m.Status()[0].State == "error" })
	failedStatus := m.Status()[0]
	if failedStatus.LastErrorCode != "scan_failed" || strings.Contains(failedStatus.LastError, root) || strings.Contains(failedStatus.LastError, moved) {
		t.Fatalf("refresh error leaked a private path or lacked a stable code: %+v", failedStatus)
	}
	if failedStatus.LastSuccessAt != lastSuccessAt {
		t.Fatalf("failed refresh replaced last successful commit time: before=%s after=%s", lastSuccessAt, failedStatus.LastSuccessAt)
	}
	failed := m.Status()[0].Progress
	if failed.Committed || !failed.Partial || failed.FailureCode != "scan_failed" || failed.Phase != "done" {
		t.Fatalf("failed watcher attempt progress: %+v", failed)
	}
	beforeReport, err := s.SourceReport(ctx, conn.Source().ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if beforeReport["report"].(sqlite.ReportSummary).ReportID != priorID {
		t.Fatal("failed refresh overwrote previous durable report")
	}
	r, err := s.Search(ctx, search.Request{Query: "retained", Limit: 20})
	if err != nil || r.Total != 1 {
		t.Fatalf("missing root pruned snapshot: %+v %v", r, err)
	}
	if err := os.Rename(moved, root); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "note.md"), "recovered root")
	eventually(t, func() bool {
		r, e := s.Search(ctx, search.Request{Query: "recovered", Limit: 20})
		return e == nil && r.Total == 1
	})
	eventually(t, func() bool { return m.Status()[0].Mode == "events" && m.Status()[0].WatchWarningCode == "" })
	after, err := s.SourceReport(ctx, conn.Source().ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if after["report"].(sqlite.ReportSummary).ReportID == priorID {
		t.Fatal("successful watcher recovery did not publish a new report")
	}
}

func TestRootIgnorePolicyWatcherAtomicRecoveryAndPrunedDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root, data := t.TempDir(), t.TempDir()
	generated := filepath.Join(root, "generated", "nested")
	if err := os.MkdirAll(generated, 0700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(generated, "old.md"), "old generated marker")
	put(t, filepath.Join(root, "keep.md"), "permanent keep marker")
	s, err := sqlite.Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	factory := func(source connector.Source) (*filesystem.Connector, error) {
		return filesystem.NewWithOptions(source.Root, filesystem.Options{MaxTextBytes: source.MaxTextBytes, MaxPDFBytes: source.MaxPDFBytes, MaxDOCXBytes: source.MaxDOCXBytes, RegistrationToken: source.RegistrationToken, RegistrationRevision: source.RegistrationRevision}, data)
	}
	conn, err := filesystem.New(root, 1<<20, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, s, conn); err != nil {
		t.Fatal(err)
	}
	locks := sourcecoord.New()
	m := syncer.New(s, syncer.Config{Coordinator: locks, Factory: factory, Interval: time.Hour, DiscoveryInterval: 20 * time.Millisecond, Debounce: 20 * time.Millisecond})
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	defer func() { cancel(); <-done }()
	eventually(t, func() bool { return len(m.Status()) == 1 && m.Status()[0].State == "idle" })
	has := func(q string, n int) bool {
		r, e := s.Search(ctx, search.Request{Query: q, Limit: 10})
		return e == nil && r.Total == n
	}
	policy := filepath.Join(root, ".findrailignore")
	atomicPolicy := func(content string) {
		t.Helper()
		tmp := filepath.Join(root, ".policy-save.tmp")
		put(t, tmp, content)
		if e := os.Rename(tmp, policy); e != nil {
			t.Fatal(e)
		}
	}
	atomicPolicy("generated/\n")
	eventually(t, func() bool { return has("old", 0) && m.Status()[0].State == "idle" })
	prior, err := s.SourceReport(ctx, conn.Source().ID, false)
	if err != nil {
		t.Fatal(err)
	}
	priorID := prior["report"].(sqlite.ReportSummary).ReportID
	var reconciled bool
	for _, reason := range prior["report"].(sqlite.ReportSummary).Reasons {
		if reason.Code == "excluded_path" && reason.Unit == "directory" && reason.Count == 1 {
			reconciled = true
		}
	}
	if !reconciled {
		t.Fatalf("ignore decision missing from durable diagnostics: %+v", prior["report"])
	}
	put(t, filepath.Join(generated, "new.md"), "newly discovered marker")
	if has("newly", 1) {
		t.Fatal("ignored subtree was indexed before policy changed")
	}
	atomicPolicy("!generated/\n")
	eventually(t, func() bool { return has("newly", 1) })
	// A second successful event refresh may still be pending. Keep its commit
	// out of the baseline-to-malformed-policy interval, as in the missing-root test.
	unlock, err := locks.Acquire(ctx, conn.Source().ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	validReport, err := s.SourceReport(ctx, conn.Source().ID, false)
	if err != nil {
		t.Fatal(err)
	}
	validReportID := validReport["report"].(sqlite.ReportSummary).ReportID
	atomicPolicy("[malformed\n")
	unlock()
	eventually(t, func() bool { return m.Status()[0].State == "error" })
	if status := m.Status()[0]; status.Progress.FailureCode != "scan_failed" || status.Progress.Committed {
		t.Fatalf("policy failure progress leaked or committed: %+v", status.Progress)
	}
	if !has("newly", 1) {
		t.Fatal("malformed policy replaced the previous searchable snapshot")
	}
	failedReport, err := s.SourceReport(ctx, conn.Source().ID, false)
	if err != nil {
		t.Fatal(err)
	}
	failedReportID := failedReport["report"].(sqlite.ReportSummary).ReportID
	if failedReportID != validReportID || failedReportID == priorID {
		t.Fatal("malformed policy replaced the prior successful report")
	}
	atomicPolicy("generated/\n")
	eventually(t, func() bool { return has("newly", 0) && m.Status()[0].State == "idle" })
	if err := os.Remove(policy); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return has("newly", 1) })
	if err := s.ForgetSource(ctx, conn.Source().ID); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "another.md"), "forgotten source stays forgotten")
	eventually(t, func() bool { return len(m.Status()) == 0 })
	if has("forgotten", 1) {
		t.Fatal("forget allowed refresh re-registration")
	}
}

func TestForgetReaddWhileOldWorkerWaitsForSourceLock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root, data := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "note.md")
	put(t, path, "new registration survives old refresh")
	store, err := sqlite.Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	newConnector := func() *filesystem.Connector {
		t.Helper()
		c, e := filesystem.New(root, 1<<20, data)
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	if _, err := ingest.Run(ctx, store, newConnector()); err != nil {
		t.Fatal(err)
	}
	id := newConnector().Source().ID
	locks := sourcecoord.New()
	unlock, err := locks.Acquire(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	m := syncer.New(store, syncer.Config{
		Factory: func(source connector.Source) (*filesystem.Connector, error) {
			return filesystem.NewWithOptions(source.Root, filesystem.Options{
				MaxTextBytes: source.MaxTextBytes, MaxPDFBytes: source.MaxPDFBytes,
				MaxDOCXBytes: source.MaxDOCXBytes, RegistrationToken: source.RegistrationToken,
				RegistrationRevision: source.RegistrationRevision,
			}, data)
		},
		Coordinator: locks, Interval: time.Hour, DiscoveryInterval: 10 * time.Millisecond,
		PollOnly: true,
	})
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	defer func() { cancel(); unlock(); <-done }()
	eventually(t, func() bool { return len(m.Status()) == 1 && m.Status()[0].State == "indexing" })
	oldAttempt := m.Status()[0].Progress.AttemptID
	registered, err := store.Sources(ctx)
	if err != nil || len(registered) != 1 {
		t.Fatalf("read configured source: %+v %v", registered, err)
	}
	configured, err := filesystem.NewWithOptions(root, filesystem.Options{
		MaxTextBytes: registered[0].MaxTextBytes, MaxPDFBytes: registered[0].MaxPDFBytes,
		MaxDOCXBytes: 1 << 20, RegistrationToken: registered[0].RegistrationToken,
		RegistrationRevision: registered[0].RegistrationRevision,
	}, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Configure(ctx, store, configured, registered[0].RegistrationToken, registered[0].RegistrationRevision, registered[0].MaxDOCXBytes); err != nil {
		t.Fatalf("configure source while previous worker waits: %v", err)
	}
	eventually(t, func() bool {
		status := m.Status()
		return len(status) == 1 && status[0].State == "indexing" && status[0].Progress.AttemptID != oldAttempt
	})

	if err := store.ForgetSource(ctx, id); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return len(m.Status()) == 0 })
	if _, err := ingest.Run(ctx, store, newConnector()); err != nil {
		t.Fatalf("re-register same source ID: %v", err)
	}
	eventually(t, func() bool { return len(m.Status()) == 1 && m.Status()[0].State == "indexing" })
	newAttempt := m.Status()[0].Progress.AttemptID
	if newAttempt == "" {
		t.Fatal("new registration did not start its own refresh attempt")
	}
	unlock()
	eventually(t, func() bool {
		status := m.Status()
		return len(status) == 1 && status[0].State == "idle" && status[0].Progress.AttemptID == newAttempt && status[0].Progress.Committed
	})
	results, err := store.Search(ctx, search.Request{Query: "survives", Limit: 10})
	if err != nil || results.Total != 1 {
		t.Fatalf("re-added source did not retain committed searchable snapshot: %+v %v", results, err)
	}
}

func TestShutdownCancelsWorkerWaitingForSourceLock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	root, data := t.TempDir(), t.TempDir()
	put(t, filepath.Join(root, "note.md"), "committed before shutdown")
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
	locks := sourcecoord.New()
	unlock, err := locks.Acquire(ctx, conn.Source().ID)
	if err != nil {
		t.Fatal(err)
	}
	m := syncer.New(store, syncer.Config{
		Factory: func(source connector.Source) (*filesystem.Connector, error) {
			return filesystem.NewWithOptions(source.Root, filesystem.Options{
				MaxTextBytes: source.MaxTextBytes, MaxPDFBytes: source.MaxPDFBytes,
				MaxDOCXBytes: source.MaxDOCXBytes, RegistrationToken: source.RegistrationToken,
				RegistrationRevision: source.RegistrationRevision,
			}, data)
		},
		Coordinator: locks, PollOnly: true, DiscoveryInterval: 10 * time.Millisecond,
	})
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	eventually(t, func() bool { return len(m.Status()) == 1 && m.Status()[0].State == "indexing" })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("manager shutdown leaked a worker waiting on the source lock")
	}
	unlock()
	results, err := store.Search(context.Background(), search.Request{Query: "committed", Limit: 10})
	if err != nil || results.Total != 1 {
		t.Fatalf("shutdown disturbed the committed snapshot: %+v %v", results, err)
	}
}
