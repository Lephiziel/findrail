package sync_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
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
				return filesystem.New(source.Root, source.MaxTextBytes, data)
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
			has := func(query string, count int) bool {
				r, e := s.Search(ctx, search.Request{Query: query, Limit: 20})
				return e == nil && r.Total == count
			}
			put(t, path, "updated evidence")
			eventually(t, func() bool { return has("updated", 1) && has("initial", 0) })
			nested := filepath.Join(root, "nested")
			if err := os.Mkdir(nested, 0700); err != nil {
				t.Fatal(err)
			}
			other := filepath.Join(nested, "other.md")
			put(t, other, "nested fresh")
			eventually(t, func() bool { return has("nested", 1) })
			put(t, other, "nested changed")
			eventually(t, func() bool { return has("changed", 1) && has("fresh", 0) })
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
	m := syncer.New(s, syncer.Config{Factory: func(source connector.Source) (*filesystem.Connector, error) {
		return filesystem.New(source.Root, source.MaxTextBytes)
	}, Interval: 50 * time.Millisecond, PollOnly: true})
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	defer func() { cancel(); <-done }()
	eventually(t, func() bool { return len(m.Status()) == 1 && m.Status()[0].State == "idle" })
	moved := filepath.Join(parent, "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return m.Status()[0].State == "error" })
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
}
