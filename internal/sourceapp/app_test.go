package sourceapp

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gh "github.com/Lephiziel/findrail/internal/connectors/github"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/sourcecoord"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
)

func openApp(t *testing.T) (*App, *sqlite.Store, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app := New(ctx, store, dir, "/does-not-need-to-exist-without-pdfs")
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := app.Close(c); err != nil {
			t.Error(err)
		}
	})
	return app, store, dir
}

type fakeRoundTripper func(*http.Request) (*http.Response, error)

func (f fakeRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func ghResponse(status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: nil}
}
func fakeArchive(t *testing.T, root string, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, text := range files {
		body := []byte(text)
		if err := tw.WriteHeader(&tar.Header{Name: root + "/" + name, Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestGitHubAddRefreshOfflinePreservesSnapshotAndStableIdentity(t *testing.T) {
	a, store, _ := openApp(t)
	sha1 := "1111111111111111111111111111111111111111"
	sha2 := "2222222222222222222222222222222222222222"
	sha3 := "3333333333333333333333333333333333333333"
	current := sha1
	offline := false
	pauseArchive := false
	archiveEntered, allowArchive := make(chan struct{}), make(chan struct{})
	pauseThird := false
	thirdArchiveEntered, allowThirdArchive := make(chan struct{}), make(chan struct{})
	calls := 0
	oldArchive := fakeArchive(t, "demo-"+sha1, map[string]string{"old.md": "oldmarker cached snapshot"})
	newArchive := fakeArchive(t, "demo-"+sha2, map[string]string{"new.md": "newmarker revised snapshot"})
	thirdArchive := fakeArchive(t, "demo-"+sha3, map[string]string{"third.md": "thirdmarker canceled snapshot"})
	roundTrip := fakeRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if offline {
			return nil, errors.New("offline")
		}
		switch {
		case strings.Contains(r.URL.Path, "/repos/octo/demo/commits/"):
			return ghResponse(200, []byte(`{"sha":"`+current+`","commit":{"committer":{"date":"2026-01-02T03:04:05Z"}}}`)), nil
		case r.URL.Path == "/repos/octo/demo":
			return ghResponse(200, []byte(`{"id":77,"name":"demo","full_name":"octo/demo","html_url":"https://github.com/octo/demo","default_branch":"main","private":false,"owner":{"login":"octo"}}`)), nil
		case strings.Contains(r.URL.Path, "/tarball/"):
			if current == sha1 {
				return ghResponse(200, oldArchive), nil
			}
			if current == sha3 && pauseThird {
				close(thirdArchiveEntered)
				select {
				case <-allowThirdArchive:
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
				return ghResponse(200, thirdArchive), nil
			}
			if pauseArchive {
				close(archiveEntered)
				<-allowArchive
			}
			return ghResponse(200, newArchive), nil
		default:
			return ghResponse(404, []byte("not found")), nil
		}
	})
	a.newGitHubClient = func() *gh.Client { c := gh.NewClient(); c.HTTP = &http.Client{Transport: roundTrip}; return c }
	added, err := a.AddGitHub("octo/demo", "", "")
	if err != nil {
		t.Fatal(err)
	}
	done := await(t, a, added.ID)
	if done.Status != "succeeded" {
		t.Fatalf("add failed: %+v", done)
	}
	sources, err := store.Sources(context.Background())
	if err != nil || len(sources) != 1 || sources[0].MaxDOCXBytes != 0 {
		t.Fatalf("sources: %+v %v", sources, err)
	}
	id := sources[0].ID
	result, err := store.Search(context.Background(), search.Request{Query: "oldmarker", Limit: 10})
	if err != nil || result.Total != 1 {
		t.Fatalf("initial search: %+v %v", result, err)
	}
	cached, err := store.Evidence(context.Background(), result.Results[0].ID, 0)
	if err != nil || !strings.Contains(cached.Text, "cached snapshot") {
		t.Fatalf("cached evidence: %+v %v", cached, err)
	}
	before := calls
	_, _ = store.Search(context.Background(), search.Request{Query: "oldmarker", Limit: 10})
	_, _ = store.Sources(context.Background())
	if calls != before {
		t.Fatal("ordinary local reads contacted GitHub")
	}
	current = sha2
	pauseArchive = true
	refreshed, err := a.Refresh(id)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-archiveEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("GitHub preparation did not reach bounded archive fetch")
	}
	whilePreparing, err := store.Search(context.Background(), search.Request{Query: "oldmarker", Limit: 10})
	if err != nil || whilePreparing.Total != 1 {
		t.Fatalf("committed search unavailable during GitHub preparation: %+v %v", whilePreparing, err)
	}
	close(allowArchive)
	done = await(t, a, refreshed.ID)
	if done.Status != "succeeded" {
		t.Fatalf("refresh failed: %+v", done)
	}
	sources, err = store.Sources(context.Background())
	if err != nil || len(sources) != 1 || sources[0].ID != id || sources[0].GitHub.SHA != sha2 {
		t.Fatalf("identity/revision changed unexpectedly: %+v %v", sources, err)
	}
	result, err = store.Search(context.Background(), search.Request{Query: "oldmarker", Limit: 10})
	if err != nil || result.Total != 0 {
		t.Fatalf("stale file remained: %+v %v", result, err)
	}
	result, err = store.Search(context.Background(), search.Request{Query: "newmarker", Limit: 10})
	if err != nil || result.Total != 1 {
		t.Fatalf("new file missing: %+v %v", result, err)
	}
	duplicate, err := a.AddGitHub("https://github.com/octo/demo", "", "")
	if err != nil {
		t.Fatal(err)
	}
	done = await(t, a, duplicate.ID)
	if done.Status != "failed" || done.ErrorCode != "already_added" {
		t.Fatalf("stable-identity duplicate: %+v", done)
	}
	sources, err = store.Sources(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("duplicate created a second source: %+v %v", sources, err)
	}
	offline = true
	failed, err := a.Refresh(id)
	if err != nil {
		t.Fatal(err)
	}
	done = await(t, a, failed.ID)
	if done.Status != "failed" {
		t.Fatalf("offline refresh: %+v", done)
	}
	result, err = store.Search(context.Background(), search.Request{Query: "newmarker", Limit: 10})
	if err != nil || result.Total != 1 {
		t.Fatalf("offline refresh lost snapshot: %+v %v", result, err)
	}
	after, err := store.Evidence(context.Background(), result.Results[0].ID, 0)
	if err != nil || !strings.Contains(after.Text, "revised snapshot") {
		t.Fatalf("offline preview unavailable: %+v %v", after, err)
	}
	offline = false
	current = sha3
	pauseThird = true
	canceled, err := a.Refresh(id)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-thirdArchiveEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel test did not reach archive preparation")
	}
	preserved, err := store.Search(context.Background(), search.Request{Query: "newmarker", Limit: 10})
	if err != nil || preserved.Total != 1 {
		t.Fatalf("search unavailable during cancellable preparation: %+v %v", preserved, err)
	}
	if err := a.Cancel(canceled.ID); err != nil {
		t.Fatal(err)
	}
	done = await(t, a, canceled.ID)
	if done.Status != "canceled" {
		t.Fatalf("cancel terminal state: %+v", done)
	}
	preserved, err = store.Search(context.Background(), search.Request{Query: "newmarker", Limit: 10})
	if err != nil || preserved.Total != 1 {
		t.Fatalf("cancellation lost committed snapshot: %+v %v", preserved, err)
	}
	duplicates, err := store.Sources(context.Background())
	if err != nil || len(duplicates) != 1 {
		t.Fatalf("duplicate source record: %+v %v", duplicates, err)
	}
}
func await(t *testing.T, a *App, id string) Job {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		changed := a.WaitChannel()
		j, ok := a.Job(id)
		if !ok {
			t.Fatalf("job %s missing", id)
		}
		if terminal(j.Status) {
			return j
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("job did not finish: %+v", j)
		}
	}
}

func TestFolderAddDuplicateRemovePreservesOriginalAndSearch(t *testing.T) {
	a, store, _ := openApp(t)
	root := filepath.Join(t.TempDir(), "Notes Ω")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "note.md")
	original := []byte("searchable marker sourceapp")
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	job, err := a.AddFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	done := await(t, a, job.ID)
	if done.Status != "succeeded" {
		t.Fatalf("add job: %+v", done)
	}
	resp, err := store.Search(context.Background(), search.Request{Query: "marker", Limit: 10})
	if err != nil || resp.Total != 1 {
		t.Fatalf("search result: %+v %v", resp, err)
	}
	if _, err = a.AddFolder(root); err == nil || err.Error() != "already_added" {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err = a.AddFolder(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing folder accepted")
	}
	remove, err := a.Remove(job.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	if done = await(t, a, remove.ID); done.Status != "succeeded" {
		t.Fatalf("remove job: %+v", done)
	}
	resp, err = store.Search(context.Background(), search.Request{Query: "marker", Limit: 10})
	if err != nil || resp.Total != 0 {
		t.Fatalf("removed results: %+v %v", resp, err)
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != string(original) {
		t.Fatalf("source modified: %q %v", got, err)
	}
}

func TestQueueBoundsDuplicateCancellationAndShutdown(t *testing.T) {
	a, _, _ := openApp(t)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	block := func(ctx context.Context, _ *Job) (string, error) {
		started <- struct{}{}
		select {
		case <-release:
			return "ok", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	first, err := a.submit("test", "running-a", "", block)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.submit("test", "running-b", "", block)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("worker did not start")
		}
	}
	queued := make([]Job, 0, maxQueued)
	for i := 0; i < maxQueued; i++ {
		j, e := a.submit("test", string(rune('a'+i)), "", func(context.Context, *Job) (string, error) { return "done", nil })
		if e != nil {
			t.Fatalf("queue item %d: %v", i, e)
		}
		queued = append(queued, j)
	}
	if _, err := a.submit("test", "overflow", "", block); err == nil || err.Error() != "queue_full" {
		t.Fatalf("queue limit: %v", err)
	}
	if _, err := a.submit("test", "running-a", "", block); err == nil || err.Error() != "busy" {
		t.Fatalf("duplicate target: %v", err)
	}
	if err := a.Cancel(queued[0].ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.Job(queued[0].ID); got.Status != "canceled" {
		t.Fatalf("queued cancel status: %+v", got)
	}
	release <- struct{}{}
	release <- struct{}{}
	if got := await(t, a, first.ID); got.Status != "succeeded" {
		t.Fatalf("first: %+v", got)
	}
	if got := await(t, a, second.ID); got.Status != "succeeded" {
		t.Fatalf("second: %+v", got)
	}
	for _, j := range queued[1:] {
		if got := await(t, a, j.ID); got.Status != "succeeded" {
			t.Fatalf("queued: %+v", got)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.submit("test", "after-close", "", block); err == nil || err.Error() != "shutdown" {
		t.Fatalf("accepted after shutdown: %v", err)
	}
}

func TestRunningCancelWaitsForCleanup(t *testing.T) {
	a, _, _ := openApp(t)
	entered := make(chan struct{})
	cleaned := make(chan struct{})
	job, err := a.submit("test", "cancel", "", func(ctx context.Context, _ *Job) (string, error) {
		close(entered)
		<-ctx.Done()
		close(cleaned)
		return "", ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := a.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.Job(job.ID); got.Status != "running" {
		t.Fatalf("marked canceled before cleanup: %+v", got)
	}
	select {
	case <-cleaned:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not run")
	}
	if got := await(t, a, job.ID); got.Status != "canceled" {
		t.Fatalf("terminal cancel: %+v", got)
	}
}

func TestRemoveCancelsAndWaitsForExistingOperation(t *testing.T) {
	a, store, _ := openApp(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("race marker"), 0600); err != nil {
		t.Fatal(err)
	}
	added, err := a.AddFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := await(t, a, added.ID); got.Status != "succeeded" {
		t.Fatal(got)
	}
	entered, cleanup, allowCleanup := make(chan struct{}), make(chan struct{}), make(chan struct{})
	refresh, err := a.submit("refresh", added.SourceID, added.SourceID, func(ctx context.Context, _ *Job) (string, error) {
		close(entered)
		<-ctx.Done()
		close(cleanup)
		<-allowCleanup
		return "", ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	removal, err := a.Remove(added.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	if removal.Phase != "removing" {
		t.Fatalf("removal phase: %+v", removal)
	}
	<-cleanup
	if _, err := store.Sources(context.Background()); err != nil {
		t.Fatal(err)
	}
	stillThere := false
	for _, s := range mustSources(t, store) {
		if s.ID == added.SourceID {
			stillThere = true
		}
	}
	if !stillThere {
		t.Fatal("source was removed before previous work cleaned up")
	}
	close(allowCleanup)
	if got := await(t, a, refresh.ID); got.Status != "canceled" {
		t.Fatalf("refresh status: %+v", got)
	}
	if got := await(t, a, removal.ID); got.Status != "succeeded" {
		t.Fatalf("remove status: %+v", got)
	}
	if len(mustSources(t, store)) != 0 {
		t.Fatal("remove did not delete source")
	}
}

func TestRemoveWaitsForSharedWatcherCoordination(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	coord := sourcecoord.New()
	app := NewWithCoordinator(ctx, store, dir, "/unused", coord)
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := app.Close(closeCtx); err != nil {
			t.Error(err)
		}
	}()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("coordinated source"), 0600); err != nil {
		t.Fatal(err)
	}
	added, err := app.AddFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := await(t, app, added.ID); got.Status != "succeeded" {
		t.Fatal(got)
	}
	if sources := mustSources(t, store); len(sources) != 1 || sources[0].MaxDOCXBytes != 8<<20 {
		t.Fatalf("new folder DOCX default not saved: %+v", sources)
	}
	watcherUnlock, err := coord.Acquire(ctx, added.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	remove, err := app.Remove(added.SourceID)
	if err != nil {
		watcherUnlock()
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		job, _ := app.Job(remove.ID)
		if job.Status == "running" && job.Phase == "removing" {
			break
		}
		select {
		case <-app.WaitChannel():
		case <-deadline:
			watcherUnlock()
			t.Fatal("remove did not enter coordinated phase")
		}
	}
	if sources := mustSources(t, store); len(sources) != 1 {
		watcherUnlock()
		t.Fatalf("removed source while watcher owned coordination: %+v", sources)
	}
	watcherUnlock()
	if got := await(t, app, remove.ID); got.Status != "succeeded" {
		t.Fatalf("coordinated removal: %+v", got)
	}
	if len(mustSources(t, store)) != 0 {
		t.Fatal("coordinated remove did not finish")
	}
}

func TestCancelAfterCommitRemainsSucceeded(t *testing.T) {
	a, _, _ := openApp(t)
	committed, finish := make(chan struct{}), make(chan struct{})
	job, err := a.submit("test", "commit-cancel", "", func(context.Context, *Job) (string, error) { close(committed); <-finish; return "committed", nil })
	if err != nil {
		t.Fatal(err)
	}
	<-committed
	if err := a.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	close(finish)
	if got := await(t, a, job.ID); got.Status != "succeeded" || got.Result != "committed" {
		t.Fatalf("cancel claimed to roll back committed work: %+v", got)
	}
}

func TestShutdownCancelsAndWaitsForCleanup(t *testing.T) {
	a, _, _ := openApp(t)
	entered, cleaning, allow := make(chan struct{}), make(chan struct{}), make(chan struct{})
	job, err := a.submit("test", "shutdown", "", func(ctx context.Context, _ *Job) (string, error) {
		close(entered)
		<-ctx.Done()
		close(cleaning)
		<-allow
		return "", ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	closed := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		closed <- a.Close(ctx)
	}()
	<-cleaning
	select {
	case err := <-closed:
		t.Fatalf("shutdown returned before cleanup: %v", err)
	default:
	}
	close(allow)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if got, _ := a.Job(job.ID); got.Status != "canceled" {
		t.Fatalf("shutdown job state: %+v", got)
	}
}
func mustSources(t *testing.T, s *sqlite.Store) []sqlite.SourceStatus {
	t.Helper()
	got, err := s.Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestHistoryBoundDoesNotDropActiveJobs(t *testing.T) {
	a, _, _ := openApp(t)
	started := make(chan struct{})
	active, err := a.submit("held", "keep-active", "", func(ctx context.Context, _ *Job) (string, error) { close(started); <-ctx.Done(); return "", ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	<-started
	for i := 0; i < maxHistory+5; i++ {
		j, err := a.submit("instant", fmt.Sprintf("%d", i), "", func(context.Context, *Job) (string, error) { return "ok", nil })
		if err != nil {
			t.Fatal(err)
		}
		if got := await(t, a, j.ID); got.Status != "succeeded" {
			t.Fatal(got.Status)
		}
	}
	if _, ok := a.Job(active.ID); !ok {
		t.Fatal("history pruning evicted an active job")
	}
	a.mu.Lock()
	activeWork := a.byID[active.ID]
	a.mu.Unlock()
	if err := a.Cancel(active.ID); err != nil {
		t.Fatal(err)
	}
	<-activeWork.finished
	if activeWork.job.Status != "canceled" {
		t.Fatal(activeWork.job.Status)
	}
	jobs := a.Jobs()
	if len(jobs) > maxHistory {
		t.Fatalf("history has %d jobs", len(jobs))
	}
	for _, j := range jobs {
		if !terminal(j.Status) {
			t.Errorf("unexpected active retained job: %+v", j)
		}
	}
}
