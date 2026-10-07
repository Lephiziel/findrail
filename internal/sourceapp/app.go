// Package sourceapp coordinates explicitly requested source changes and their
// process-scoped jobs. It is composed only by the writable `start` command.
package sourceapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	gh "github.com/Lephiziel/findrail/internal/connectors/github"
	pdfextract "github.com/Lephiziel/findrail/internal/extract/pdf"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/sourcecoord"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/Lephiziel/findrail/pkg/connector"
)

const (
	maxRunning  = 2
	maxQueued   = 16
	maxHistory  = 100
	jobDeadline = 5 * time.Minute
)

type Store interface {
	ingest.Store
	ingest.RefreshStore
	Sources(context.Context) ([]sqlite.SourceStatus, error)
	ForgetSource(context.Context, string) error
	GitHubSource(context.Context, string) (sqlite.GitHubSource, error)
	PublishGitHub(context.Context, *gh.Snapshot, *sqlite.GitHubSource) (ingest.Result, sqlite.GitHubSource, error)
	RecordGitHubError(context.Context, sqlite.GitHubSource, string)
}

type Job struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Target    string    `json:"target,omitempty"`
	SourceID  string    `json:"source_id,omitempty"`
	Status    string    `json:"status"`
	Phase     string    `json:"phase"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Result    string    `json:"result,omitempty"`
	ErrorCode string    `json:"error_code,omitempty"`
	Error     string    `json:"error,omitempty"`
}
type work struct {
	job      *Job
	run      func(context.Context, *Job) (string, error)
	ctx      context.Context
	cancel   context.CancelFunc
	finished chan struct{}
	after    <-chan struct{}
}
type App struct {
	store           Store
	dataDir         string
	executable      string
	root            context.Context
	stop            context.CancelFunc
	mu              sync.Mutex
	queue           []*work
	jobs            []*Job
	byID            map[string]*work
	active          map[string]string
	changed         chan struct{}
	wake            chan struct{}
	closed          bool
	wg              sync.WaitGroup
	newGitHubClient func() *gh.Client
	coordinator     *sourcecoord.Locks
}

func New(ctx context.Context, store Store, dataDir, executable string) *App {
	return NewWithCoordinator(ctx, store, dataDir, executable, sourcecoord.New())
}
func NewWithCoordinator(ctx context.Context, store Store, dataDir, executable string, coordinator *sourcecoord.Locks) *App {
	if coordinator == nil {
		coordinator = sourcecoord.New()
	}
	root, stop := context.WithCancel(ctx)
	a := &App{store: store, dataDir: dataDir, executable: executable, root: root, stop: stop, byID: map[string]*work{}, active: map[string]string{}, changed: make(chan struct{}), wake: make(chan struct{}, 1), newGitHubClient: gh.NewClient, coordinator: coordinator}
	for i := 0; i < maxRunning; i++ {
		a.wg.Add(1)
		go a.worker()
	}
	return a
}
func (a *App) signalLocked() { close(a.changed); a.changed = make(chan struct{}) }
func (a *App) submit(kind, target, sourceID string, run func(context.Context, *Job) (string, error)) (Job, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return Job{}, errors.New("shutdown")
	}
	if id := a.active[target]; id != "" {
		return *a.byID[id].job, errors.New("busy")
	}
	if len(a.queue) >= maxQueued {
		return Job{}, errors.New("queue_full")
	}
	job, w, err := a.newWorkLocked(kind, target, sourceID, run)
	if err != nil {
		return Job{}, err
	}
	a.enqueueLocked(w)
	return *job, nil
}
func (a *App) newWorkLocked(kind, target, sourceID string, run func(context.Context, *Job) (string, error)) (*Job, *work, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	job := &Job{ID: hex.EncodeToString(b[:]), Type: kind, Target: target, SourceID: sourceID, Status: "queued", Phase: "validating", CreatedAt: now, UpdatedAt: now}
	ctx, cancel := context.WithTimeout(a.root, jobDeadline)
	w := &work{job: job, run: run, ctx: ctx, cancel: cancel, finished: make(chan struct{})}
	a.jobs = append(a.jobs, job)
	a.byID[job.ID] = w
	return job, w, nil
}
func (a *App) enqueueLocked(w *work) {
	a.active[w.job.Target] = w.job.ID
	a.queue = append(a.queue, w)
	a.pruneLocked()
	a.signalLocked()
	select {
	case a.wake <- struct{}{}:
	default:
	}
}
func (a *App) pruneLocked() {
	done := 0
	for i := len(a.jobs) - 1; i >= 0; i-- {
		if terminal(a.jobs[i].Status) {
			done++
			if done > maxHistory {
				delete(a.byID, a.jobs[i].ID)
				a.jobs = append(a.jobs[:i], a.jobs[i+1:]...)
			}
		}
	}
}
func terminal(s string) bool { return s == "succeeded" || s == "failed" || s == "canceled" }
func (a *App) phase(j *Job, phase string) {
	a.mu.Lock()
	j.Phase = phase
	j.UpdatedAt = time.Now().UTC()
	a.signalLocked()
	a.mu.Unlock()
}
func (a *App) setSourceTarget(j *Job, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if existing := a.active[id]; existing != "" && existing != j.ID {
		return errors.New("busy")
	}
	if a.active[j.Target] == j.ID {
		delete(a.active, j.Target)
	}
	j.Target = id
	j.SourceID = id
	a.active[id] = j.ID
	j.UpdatedAt = time.Now().UTC()
	a.signalLocked()
	return nil
}
func (a *App) githubDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 2*time.Minute)
}
func bounded(s string, n int) string {
	for i, r := range s {
		if r < 32 && r != '\t' {
			s = s[:i] + " " + s[i+1:]
		}
	}
	if len(s) > n {
		s = s[:n]
	}
	return s
}
func (a *App) worker() {
	defer a.wg.Done()
	for {
		select {
		case <-a.root.Done():
			return
		case <-a.wake:
		}
		for {
			a.mu.Lock()
			if len(a.queue) == 0 || a.closed {
				a.mu.Unlock()
				break
			}
			w := a.queue[0]
			a.queue = a.queue[1:]
			if w.ctx.Err() != nil {
				if errors.Is(w.ctx.Err(), context.DeadlineExceeded) {
					w.job.Status = "failed"
					w.job.ErrorCode = "deadline_exceeded"
					w.job.Error = "Operation exceeded its time limit before it could start."
				} else {
					w.job.Status = "canceled"
					w.job.ErrorCode = "canceled"
					w.job.Error = "Operation canceled before it started."
				}
				w.job.Phase = "done"
				w.job.UpdatedAt = time.Now().UTC()
				if a.active[w.job.Target] == w.job.ID {
					delete(a.active, w.job.Target)
				}
				close(w.finished)
				a.signalLocked()
				a.mu.Unlock()
				continue
			}
			w.job.Status = "running"
			w.job.Phase = "validating"
			w.job.UpdatedAt = time.Now().UTC()
			a.signalLocked()
			if len(a.queue) > 0 {
				select {
				case a.wake <- struct{}{}:
				default:
				}
			}
			a.mu.Unlock()
			ctx, cancel := context.WithTimeout(w.ctx, jobDeadline)
			var result string
			var err error
			if w.after != nil {
				a.phase(w.job, "removing")
				select {
				case <-w.after:
				case <-ctx.Done():
					err = ctx.Err()
				}
			}
			if err == nil {
				result, err = w.run(ctx, w.job)
			}
			cancel()
			a.mu.Lock()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					w.job.Status = "failed"
					w.job.ErrorCode = "deadline_exceeded"
					w.job.Error = "Operation exceeded its time limit; the previous snapshot remains available."
				} else if errors.Is(err, context.Canceled) {
					w.job.Status = "canceled"
					w.job.ErrorCode = "canceled"
					w.job.Error = "Operation canceled or timed out."
				} else {
					w.job.Status = "failed"
					w.job.ErrorCode, w.job.Error = friendlyError(err)
				}
			} else {
				w.job.Status = "succeeded"
				w.job.Result = bounded(result, 300)
			}
			w.job.Phase = "done"
			w.job.UpdatedAt = time.Now().UTC()
			if a.active[w.job.Target] == w.job.ID {
				delete(a.active, w.job.Target)
			}
			close(w.finished)
			a.signalLocked()
			a.pruneLocked()
			a.mu.Unlock()
		}
	}
}
func friendlyError(err error) (string, string) {
	s := err.Error()
	lower := strings.ToLower(s)
	switch {
	case s == "already_added":
		return "already_added", "This source is already indexed. Use Refresh in Sources."
	case strings.Contains(lower, "source_changed") || errors.Is(err, ingest.ErrSourceGone):
		return "stale_source", "The source changed or was removed before this update could be published. The previous snapshot remains available."
	case strings.Contains(lower, "rate limit"):
		return "rate_limited", "GitHub rate limit reached. Try again later; the previous snapshot remains available."
	case strings.Contains(lower, "returned 404"):
		return "not_found", "The public repository or selected ref was not found."
	case strings.Contains(lower, "github request failed") || strings.Contains(lower, "no such host") || strings.Contains(lower, "connection refused"):
		return "network_unavailable", "Could not reach GitHub. Check the connection and retry; the previous snapshot remains available."
	case strings.Contains(lower, "limit exceeded") || strings.Contains(lower, "exceeds "):
		return "limits_exceeded", "The source exceeds a supported indexing limit; the previous snapshot remains available."
	default:
		return "operation_failed", bounded(s, 300)
	}
}
func (a *App) Jobs() []Job {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Job, 0, len(a.jobs))
	for _, j := range a.jobs {
		out = append(out, *j)
	}
	return out
}
func (a *App) Job(id string) (Job, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w := a.byID[id]
	if w == nil {
		return Job{}, false
	}
	return *w.job, true
}
func (a *App) WaitChannel() <-chan struct{} { a.mu.Lock(); defer a.mu.Unlock(); return a.changed }
func (a *App) Cancel(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	w := a.byID[id]
	if w == nil {
		return errors.New("not_found")
	}
	if terminal(w.job.Status) {
		return nil
	}
	w.cancel()
	if w.job.Status == "queued" {
		for i, x := range a.queue {
			if x == w {
				a.queue = append(a.queue[:i], a.queue[i+1:]...)
				break
			}
		}
		w.job.Status = "canceled"
		w.job.Phase = "done"
		w.job.UpdatedAt = time.Now().UTC()
		if a.active[w.job.Target] == w.job.ID {
			delete(a.active, w.job.Target)
		}
		close(w.finished)
		a.signalLocked()
	}
	return nil
}
func (a *App) Close(ctx context.Context) error {
	a.mu.Lock()
	a.closed = true
	queued := map[*work]bool{}
	for _, w := range a.queue {
		queued[w] = true
	}
	a.queue = nil
	for _, w := range a.byID {
		if !terminal(w.job.Status) {
			w.cancel()
			if queued[w] {
				w.job.Status = "canceled"
				w.job.Phase = "done"
				w.job.UpdatedAt = time.Now().UTC()
				if a.active[w.job.Target] == w.job.ID {
					delete(a.active, w.job.Target)
				}
				close(w.finished)
			}
		}
	}
	a.signalLocked()
	a.mu.Unlock()
	a.stop()
	done := make(chan struct{})
	go func() { a.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) AddFolder(path string) (Job, error) {
	return a.AddFolderWithDOCX(path, 8<<20)
}

func (a *App) AddFolderWithDOCX(path string, maxDOCXBytes int64) (Job, error) {
	if len(path) > 4096 {
		return Job{}, errors.New("invalid_path")
	}
	if maxDOCXBytes < 0 || maxDOCXBytes > 16<<20 {
		return Job{}, errors.New("max_docx_bytes must be 0–16777216")
	}
	if !filepathIsAbs(path) {
		return Job{}, errors.New("folder path must be absolute")
	}
	conn, err := a.folder(connector.Source{Root: path, MaxTextBytes: filesystem.DefaultMaxBytes, MaxPDFBytes: 16 << 20, MaxDOCXBytes: maxDOCXBytes})
	if err != nil {
		return Job{}, fmt.Errorf("invalid folder: %w", err)
	}
	source := conn.Source()
	sources, err := a.store.Sources(a.root)
	if err != nil {
		return Job{}, err
	}
	for _, s := range sources {
		if s.ID == source.ID {
			return Job{}, errors.New("already_added")
		}
	}
	return a.submit("add_folder", source.ID, source.ID, func(ctx context.Context, j *Job) (string, error) {
		a.phase(j, "indexing")
		unlock, e := a.coordinator.Acquire(ctx, source.ID)
		if e != nil {
			return "", e
		}
		defer unlock()
		c, e := a.folder(source)
		if e != nil {
			return "", e
		}
		r, e := ingest.Run(ctx, a.store, c)
		if e != nil {
			return "", e
		}
		return fmt.Sprintf("Indexed %d documents", r.Seen), nil
	})
}
func filepathIsAbs(p string) bool { return filepath.IsAbs(p) }
func (a *App) folder(s connector.Source) (*filesystem.Connector, error) {
	return filesystem.NewWithOptions(s.Root, filesystem.Options{MaxTextBytes: s.MaxTextBytes, MaxPDFBytes: s.MaxPDFBytes, MaxDOCXBytes: s.MaxDOCXBytes, ExtractPDF: pdfextract.Extractor(a.executable)}, a.dataDir)
}

func (a *App) AddGitHub(repo, ref, subdir string) (Job, error) {
	if len(repo) > 300 || len(ref) > 1024 || len(subdir) > 1024 {
		return Job{}, errors.New("input_too_long")
	}
	owner, name, err := gh.ParseRepository(repo)
	if err != nil {
		return Job{}, err
	}
	sel, err := gh.SelectionFrom(owner, name, ref, subdir, gh.DefaultMaxBytes)
	if err != nil {
		return Job{}, err
	}
	key := "github:" + owner + "/" + name + ":" + sel.RefMode + ":" + sel.RefValue + ":" + sel.Path
	return a.submit("add_github", key, "", func(ctx context.Context, j *Job) (string, error) {
		ctx, cancel := a.githubDeadline(ctx)
		defer cancel()
		client := a.newGitHubClient()
		a.phase(j, "resolving")
		repository, e := client.ResolveRepository(ctx, sel)
		if e != nil {
			return "", e
		}
		id := gh.SourceID(repository.ID, sel)
		sources, e := a.store.Sources(ctx)
		if e != nil {
			return "", e
		}
		for _, s := range sources {
			if s.ID == id {
				return "", errors.New("already_added")
			}
		}
		if e = a.setSourceTarget(j, id); e != nil {
			return "", e
		}
		a.phase(j, "downloading")
		meta, e := client.ResolveCommit(ctx, sel, repository)
		if e != nil {
			return "", e
		}
		snapshot, e := client.PrepareResolved(ctx, sel, meta)
		if e != nil {
			return "", e
		}
		a.phase(j, "publishing")
		_, _, e = a.store.PublishGitHub(ctx, snapshot, nil)
		if e != nil {
			return "", e
		}
		return "Published snapshot " + meta.SHA[:12], nil
	})
}
func (a *App) Refresh(id string) (Job, error) {
	sources, err := a.store.Sources(a.root)
	if err != nil {
		return Job{}, err
	}
	var found *sqlite.SourceStatus
	for i := range sources {
		if sources[i].ID == id {
			found = &sources[i]
			break
		}
	}
	if found == nil {
		return Job{}, errors.New("not_found")
	}
	return a.submit("refresh", id, id, func(ctx context.Context, j *Job) (string, error) {
		if found.Kind == "github" {
			ctx, cancel := a.githubDeadline(ctx)
			defer cancel()
			a.phase(j, "resolving")
			g, e := a.store.GitHubSource(ctx, id)
			if e != nil {
				return "", e
			}
			sel := gh.Selection{Owner: g.Owner, Repo: g.Repo, RefMode: g.RefMode, RefValue: g.RefValue, Path: g.SelectedPath, MaxBytes: g.MaxBytes}
			client := a.newGitHubClient()
			repo, e := client.ResolveRepository(ctx, sel)
			if e != nil {
				a.store.RecordGitHubError(context.Background(), g, e.Error())
				return "", e
			}
			if repo.ID != g.RepositoryID {
				e = errors.New("repository identity changed")
				a.store.RecordGitHubError(context.Background(), g, e.Error())
				return "", e
			}
			meta, e := client.ResolveCommit(ctx, sel, repo)
			if e != nil {
				a.store.RecordGitHubError(context.Background(), g, e.Error())
				return "", e
			}
			a.phase(j, "downloading")
			snapshot, e := client.PrepareResolved(ctx, sel, meta)
			if e != nil {
				a.store.RecordGitHubError(context.Background(), g, e.Error())
				return "", e
			}
			a.phase(j, "publishing")
			_, _, e = a.store.PublishGitHub(ctx, snapshot, &g)
			if e != nil {
				return "", e
			}
			return "Published snapshot " + meta.SHA[:12], nil
		}
		a.phase(j, "indexing")
		unlock, e := a.coordinator.Acquire(ctx, id)
		if e != nil {
			return "", e
		}
		defer unlock()
		c, e := a.folder(found.Source)
		if e != nil {
			return "", e
		}
		r, e := ingest.Refresh(ctx, a.store, c)
		if e != nil {
			return "", e
		}
		return fmt.Sprintf("Updated %d documents", r.Seen), nil
	})
}
func (a *App) Remove(id string) (Job, error) {
	sources, err := a.store.Sources(a.root)
	if err != nil {
		return Job{}, err
	}
	exists := false
	for _, s := range sources {
		if s.ID == id {
			exists = true
			break
		}
	}
	if !exists {
		return Job{}, errors.New("not_found")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return Job{}, errors.New("shutdown")
	}
	var previous *work
	if activeID := a.active[id]; activeID != "" {
		previous = a.byID[activeID]
		if previous == nil || previous.job.Type == "remove" {
			return Job{}, errors.New("busy")
		}
	}
	queueSize := len(a.queue)
	if previous != nil && previous.job.Status == "queued" {
		queueSize--
	}
	if queueSize >= maxQueued {
		return Job{}, errors.New("queue_full")
	}
	var after <-chan struct{}
	if previous != nil && !terminal(previous.job.Status) {
		previous.cancel()
		if previous.job.Status == "queued" {
			for i, w := range a.queue {
				if w == previous {
					a.queue = append(a.queue[:i], a.queue[i+1:]...)
					break
				}
			}
			previous.job.Status = "canceled"
			previous.job.Phase = "done"
			previous.job.UpdatedAt = time.Now().UTC()
			close(previous.finished)
			a.signalLocked()
		} else {
			after = previous.finished
		}
	}
	job, w, err := a.newWorkLocked("remove", id, id, func(ctx context.Context, j *Job) (string, error) {
		a.phase(j, "removing")
		unlock, e := a.coordinator.Acquire(ctx, id)
		if e != nil {
			return "", e
		}
		defer unlock()
		if err := a.store.ForgetSource(ctx, id); err != nil {
			return "", err
		}
		return "Source removed from index; originals are unchanged", nil
	})
	if err != nil {
		return Job{}, err
	}
	w.after = after
	job.Phase = "removing"
	a.enqueueLocked(w)
	return *job, nil
}
