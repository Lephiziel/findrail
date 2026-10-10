// Package sync refreshes complete source snapshots. Native notifications are
// hints; startup scans and periodic reconciliation remain authoritative.
package sync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/sourcecoord"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/Lephiziel/findrail/pkg/connector"
	"github.com/fsnotify/fsnotify"
)

type Status struct {
	SourceID      string   `json:"source_id"`
	State         string   `json:"state"`
	Mode          string   `json:"mode"`
	LastStartedAt string   `json:"last_started_at,omitempty"`
	LastSuccessAt string   `json:"last_success_at,omitempty"`
	LastError     string   `json:"last_error,omitempty"`
	WatchWarning  string   `json:"watch_warning,omitempty"`
	Skipped       int      `json:"skipped"`
	SkippedPDF    int      `json:"skipped_pdf,omitempty"`
	SkippedDOCX   int      `json:"skipped_docx,omitempty"`
	Progress      Progress `json:"progress"`
}
type Progress struct {
	AttemptID          string `json:"attempt_id"`
	Sequence           uint64 `json:"sequence"`
	Phase              string `json:"phase"`
	ProcessedDocuments int    `json:"processed_documents"`
	SkippedEntries     int    `json:"skipped_entries"`
	ElapsedMillis      int64  `json:"elapsed_millis"`
	Partial            bool   `json:"partial"`
	Committed          bool   `json:"committed"`
	ReportID           string `json:"report_id,omitempty"`
	FailureCode        string `json:"failure_code,omitempty"`
	updatedAt          time.Time
}
type Config struct {
	Interval          time.Duration
	DiscoveryInterval time.Duration
	Debounce          time.Duration
	PollOnly          bool
	Factory           func(connector.Source) (*filesystem.Connector, error)
	Coordinator       *sourcecoord.Locks
}
type Manager struct {
	store  *sqlite.Store
	config Config
	mu     sync.RWMutex
	status map[string]Status
}

func New(store *sqlite.Store, config Config) *Manager {
	if config.Coordinator == nil {
		config.Coordinator = sourcecoord.New()
	}
	if config.Interval <= 0 {
		config.Interval = 5 * time.Minute
	}
	if config.DiscoveryInterval <= 0 {
		config.DiscoveryInterval = 2 * time.Second
	}
	if config.Debounce <= 0 {
		config.Debounce = 350 * time.Millisecond
	}
	return &Manager{store: store, config: config, status: make(map[string]Status)}
}
func (m *Manager) Status() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Status, 0, len(m.status))
	for _, s := range m.status {
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SourceID < result[j].SourceID })
	return result
}
func (m *Manager) update(ctx context.Context, id string, change func(*Status)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	s := m.status[id]
	s.SourceID = id
	change(&s)
	m.status[id] = s
}

func (m *Manager) progress(ctx context.Context, id, attempt string, start time.Time, processed, skipped int, flush bool, phase string, committed bool, reportID, failureCode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	s, ok := m.status[id]
	if !ok || s.Progress.AttemptID != attempt {
		return
	}
	now := time.Now().UTC()
	if !flush && !s.Progress.updatedAt.IsZero() && now.Sub(s.Progress.updatedAt) < 200*time.Millisecond {
		return
	}
	if processed < s.Progress.ProcessedDocuments {
		processed = s.Progress.ProcessedDocuments
	}
	if skipped < s.Progress.SkippedEntries {
		skipped = s.Progress.SkippedEntries
	}
	s.Progress.Sequence++
	s.Progress.Phase = phase
	s.Progress.ProcessedDocuments = processed
	s.Progress.SkippedEntries = skipped
	s.Progress.ElapsedMillis = now.Sub(start).Milliseconds()
	s.Progress.Partial = !committed
	s.Progress.Committed = committed
	s.Progress.ReportID = reportID
	s.Progress.FailureCode = failureCode
	s.Progress.updatedAt = now
	m.status[id] = s
}

type worker struct {
	source connector.Source
	cancel context.CancelFunc
}

// Run blocks until cancellation and waits for every writer to roll back or finish.
func (m *Manager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	active := map[string]worker{}
	defer func() {
		for _, w := range active {
			w.cancel()
		}
		wg.Wait()
	}()
	reconcile := func() {
		sources, err := m.store.Sources(ctx)
		if err != nil {
			return
		}
		wanted := map[string]bool{}
		for _, s := range sources {
			if s.Kind != "filesystem" {
				continue
			}
			wanted[s.ID] = true
			old, ok := active[s.ID]
			if ok && old.source == s.Source {
				continue
			}
			if ok {
				old.cancel()
			}
			child, cancel := context.WithCancel(ctx)
			active[s.ID] = worker{s.Source, cancel}
			wg.Add(1)
			go func(source connector.Source) { defer wg.Done(); m.runSource(child, source) }(s.Source)
		}
		for id, w := range active {
			if !wanted[id] {
				w.cancel()
				delete(active, id)
				m.mu.Lock()
				delete(m.status, id)
				m.mu.Unlock()
			}
		}
	}
	reconcile()
	ticker := time.NewTicker(m.config.DiscoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}

func (m *Manager) runSource(ctx context.Context, source connector.Source) {
	eventConnector, _ := m.config.Factory(source)
	events := make(chan struct{}, 1)
	signal := func() {
		select {
		case events <- struct{}{}:
		default:
		}
	}
	var watcher *fsnotify.Watcher
	if !m.config.PollOnly {
		var err error
		watcher, err = fsnotify.NewWatcher()
		if err != nil {
			m.update(ctx, source.ID, func(s *Status) { s.WatchWarning = err.Error() })
		}
	}
	mode := "polling"
	if watcher != nil {
		mode = "events"
	}
	m.update(ctx, source.ID, func(s *Status) { s.State = "starting"; s.Mode = mode })
	if watcher != nil {
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-ctx.Done():
					return
				case event, ok := <-watcher.Events:
					if !ok {
						return
					}
					if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 && (eventConnector == nil || eventConnector.RelevantPath(event.Name)) {
						signal()
					}
				case err, ok := <-watcher.Errors:
					if !ok {
						return
					}
					m.update(ctx, source.ID, func(s *Status) { s.WatchWarning = err.Error() })
					signal()
				}
			}
		}()
		defer func() { watcher.Close(); <-done }()
	}
	// The timer handles debounce and retries; polling never depends on notifications.
	timer := time.NewTimer(0)
	defer timer.Stop()
	poll := time.NewTicker(m.config.Interval)
	defer poll.Stop()
	retry := time.Second
	var firstEvent time.Time
	schedule := func(delay time.Duration) {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-events:
			if firstEvent.IsZero() {
				firstEvent = time.Now()
			}
			delay := m.config.Debounce
			if until := time.Until(firstEvent.Add(2 * time.Second)); until < delay {
				delay = until
			}
			if delay < 0 {
				delay = 0
			}
			schedule(delay)
		case <-poll.C:
			schedule(0)
		case <-timer.C:
			firstEvent = time.Time{}
			if ctx.Err() != nil {
				return
			}
			var attemptBytes [16]byte
			if _, err := rand.Read(attemptBytes[:]); err != nil {
				m.update(ctx, source.ID, func(s *Status) { s.State = "error"; s.LastError = "Could not start the refresh attempt." })
				schedule(retry)
				continue
			}
			attempt := hex.EncodeToString(attemptBytes[:])
			started := time.Now().UTC()
			m.update(ctx, source.ID, func(s *Status) {
				s.State = "indexing"
				s.LastStartedAt = started.Format(time.RFC3339Nano)
				s.Progress = Progress{AttemptID: attempt, Phase: "enumerating_extracting", Partial: true}
			})
			m.progress(ctx, source.ID, attempt, started, 0, 0, true, "enumerating_extracting", false, "", "")
			conn, err := m.config.Factory(source)
			if err == nil && conn.Source() != source {
				err = errors.New("source identity or extraction settings changed")
			}
			if err == nil && watcher != nil {
				dirs, watchErr := conn.WatchDirectories(ctx)
				if watchErr == nil {
					existing := map[string]bool{}
					for _, d := range watcher.WatchList() {
						existing[d] = true
					}
					desired := map[string]bool{}
					for _, d := range dirs {
						desired[d] = true
						if !existing[d] {
							if e := watcher.Add(d); e != nil {
								watchErr = e
								break
							}
						}
					}
					for d := range existing {
						if !desired[d] {
							_ = watcher.Remove(d)
						}
					}
				}
				m.update(ctx, source.ID, func(s *Status) {
					if watchErr != nil {
						s.Mode = "polling"
						s.WatchWarning = watchErr.Error()
					} else {
						s.Mode = "events"
						s.WatchWarning = ""
					}
				})
			}
			var result ingest.Result
			if err == nil {
				var unlock func()
				unlock, err = m.config.Coordinator.Acquire(ctx, source.ID)
				if err == nil {
					result, err = ingest.RefreshWithProgress(ctx, m.store, conn, func(processed, skipped int, complete, flush bool) {
						phase := "enumerating_extracting"
						if complete {
							phase = "publishing"
						}
						m.progress(ctx, source.ID, attempt, started, processed, skipped, flush, phase, false, "", "")
					})
					unlock()
				}
			}
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, ingest.ErrSourceGone) {
				return
			}
			if err != nil {
				failureCode := "scan_failed"
				if errors.Is(err, context.Canceled) {
					failureCode = "canceled"
				} else if errors.Is(err, context.DeadlineExceeded) {
					failureCode = "deadline_exceeded"
				} else if errors.Is(err, ingest.ErrSourceGone) {
					failureCode = "stale_source"
				}
				m.progress(ctx, source.ID, attempt, started, 0, 0, true, "done", false, "", failureCode)
				m.update(ctx, source.ID, func(s *Status) {
					s.State = "error"
					s.LastError = "Refresh failed; the previous committed snapshot and report remain available."
				})
				schedule(retry)
				retry *= 2
				if retry > time.Minute {
					retry = time.Minute
				}
			} else {
				reportID := ""
				if result.Report != nil {
					reportID = result.Report.ID
				}
				m.progress(ctx, source.ID, attempt, started, result.Updated+result.Unchanged, result.Skipped, true, "done", true, reportID, "")
				retry = time.Second
				m.update(ctx, source.ID, func(s *Status) {
					s.State = "idle"
					s.LastError = ""
					s.LastSuccessAt = time.Now().UTC().Format(time.RFC3339Nano)
					s.Skipped = result.Skipped
					s.SkippedPDF = result.SkippedPDF
					s.SkippedDOCX = result.SkippedDOCX
				})
			}
		}
	}
}
