package sync_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	syncer "github.com/Lephiziel/findrail/internal/sync"
	"github.com/Lephiziel/findrail/pkg/connector"
	"github.com/fsnotify/fsnotify"
)

type testWatcher struct {
	mu      sync.Mutex
	dirs    map[string]bool
	failAdd bool
	events  chan fsnotify.Event
	errors  chan error
	once    sync.Once
}

func newTestWatcher() *testWatcher {
	return &testWatcher{dirs: make(map[string]bool), events: make(chan fsnotify.Event, 4), errors: make(chan error, 4)}
}
func (w *testWatcher) Add(path string) error {
	w.mu.Lock()
	if w.failAdd {
		w.mu.Unlock()
		return errors.New("watch registration failed at /private/path")
	}
	w.dirs[path] = true
	w.mu.Unlock()
	return nil
}
func (w *testWatcher) Remove(path string) error {
	w.mu.Lock()
	delete(w.dirs, path)
	w.mu.Unlock()
	return nil
}
func (w *testWatcher) WatchList() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failAdd {
		return nil
	}
	result := make([]string, 0, len(w.dirs))
	for path := range w.dirs {
		result = append(result, path)
	}
	return result
}
func (w *testWatcher) Close() error {
	w.once.Do(func() { close(w.events); close(w.errors) })
	return nil
}
func (w *testWatcher) Events() <-chan fsnotify.Event { return w.events }
func (w *testWatcher) Errors() <-chan error          { return w.errors }

func TestWatcherSetupRetriesAndOverflowUsesSafeWarning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root, data := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("baseline"), 0600); err != nil {
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
	factory := func(source connector.Source) (*filesystem.Connector, error) {
		return filesystem.NewWithOptions(source.Root, filesystem.Options{
			MaxTextBytes: source.MaxTextBytes, MaxPDFBytes: source.MaxPDFBytes,
			MaxDOCXBytes: source.MaxDOCXBytes, RegistrationToken: source.RegistrationToken,
			RegistrationRevision: source.RegistrationRevision,
		}, data)
	}
	created := make(chan *testWatcher, 1)
	var calls int
	var callsMu sync.Mutex
	watcherFactory := func() (syncer.Watcher, error) {
		callsMu.Lock()
		calls++
		call := calls
		callsMu.Unlock()
		if call <= 2 {
			return nil, errors.New("private /tmp/path: resource unavailable")
		}
		w := newTestWatcher()
		created <- w
		return w, nil
	}
	m := syncer.New(store, syncer.Config{
		Factory: factory, WatcherFactory: watcherFactory,
		Interval: 45 * time.Millisecond, DiscoveryInterval: 10 * time.Millisecond,
	})
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	defer func() { cancel(); <-done }()

	eventually(t, func() bool {
		status := m.Status()
		return len(status) == 1 && status[0].Mode == "polling" && status[0].WatchWarningCode == "watch_unavailable"
	})
	if strings.Contains(m.Status()[0].WatchWarning, root) || strings.Contains(m.Status()[0].WatchWarning, "/tmp/path") {
		t.Fatalf("watch setup leaked OS error: %+v", m.Status()[0])
	}
	var watcher *testWatcher
	select {
	case watcher = <-created:
	case <-time.After(3 * time.Second):
		t.Fatal("periodic reconciliation did not retry watcher setup")
	}
	eventually(t, func() bool { return m.Status()[0].Mode == "events" && m.Status()[0].WatchWarningCode == "" })

	watcher.mu.Lock()
	watcher.failAdd = true
	watcher.mu.Unlock()
	watcher.errors <- errors.New("overflow at /private/user/path")
	eventually(t, func() bool {
		status := m.Status()
		return status[0].Mode == "polling" && status[0].WatchWarningCode == "watch_degraded"
	})
	status := m.Status()[0]
	if strings.Contains(status.WatchWarning, "/private/user/path") || strings.Contains(status.WatchWarning, root) {
		t.Fatalf("watch overflow leaked a private path: %+v", status)
	}
	watcher.mu.Lock()
	watcher.failAdd = false
	watcher.mu.Unlock()
	eventually(t, func() bool { return m.Status()[0].Mode == "events" && m.Status()[0].WatchWarningCode == "" })
}

func TestContinuousWatcherEventsCannotDeferRefreshForever(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root, data := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("unchanged source"), 0600); err != nil {
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
	created := make(chan *testWatcher, 1)
	m := syncer.New(store, syncer.Config{
		Factory: func(source connector.Source) (*filesystem.Connector, error) {
			return filesystem.NewWithOptions(source.Root, filesystem.Options{
				MaxTextBytes: source.MaxTextBytes, MaxPDFBytes: source.MaxPDFBytes,
				MaxDOCXBytes: source.MaxDOCXBytes, RegistrationToken: source.RegistrationToken,
				RegistrationRevision: source.RegistrationRevision,
			}, data)
		},
		WatcherFactory: func() (syncer.Watcher, error) {
			watcher := newTestWatcher()
			created <- watcher
			return watcher, nil
		},
		Interval: time.Hour, DiscoveryInterval: 10 * time.Millisecond, Debounce: 5 * time.Second,
	})
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	defer func() { cancel(); <-done }()
	var watcher *testWatcher
	select {
	case watcher = <-created:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher was not initialized")
	}
	eventually(t, func() bool {
		status := m.Status()
		return len(status) == 1 && status[0].State == "idle" && status[0].Mode == "events"
	})
	firstAttempt := m.Status()[0].Progress.AttemptID
	stop := make(chan struct{})
	floodDone := make(chan struct{})
	go func() {
		defer close(floodDone)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				select {
				case watcher.events <- fsnotify.Event{Name: filepath.Join(root, "note.md"), Op: fsnotify.Write}:
				default:
				}
			}
		}
	}()

	deadline := time.After(4 * time.Second)
	for {
		select {
		case <-deadline:
			close(stop)
			<-floodDone
			t.Fatal("continuous change notifications deferred reconciliation indefinitely")
		default:
			status := m.Status()
			if len(status) == 1 && status[0].Progress.AttemptID != firstAttempt && status[0].State == "idle" && status[0].Progress.Committed {
				close(stop)
				<-floodDone
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
}
