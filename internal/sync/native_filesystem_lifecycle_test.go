package sync_test

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/diagnostics"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	syncer "github.com/Lephiziel/findrail/internal/sync"
	"github.com/Lephiziel/findrail/pkg/connector"
	"github.com/fsnotify/fsnotify"
)

func openNativeStore(t *testing.T, ctx context.Context, directory string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func sourceReportID(t *testing.T, store *sqlite.Store, sourceID string) string {
	t.Helper()
	result, err := store.SourceReport(context.Background(), sourceID, false)
	if err != nil {
		t.Fatal(err)
	}
	report, ok := result["report"].(sqlite.ReportSummary)
	if !ok || !report.Available || report.ReportID == "" {
		t.Fatalf("committed source report unavailable: %+v", result)
	}
	return report.ReportID
}

func nativeSearch(t *testing.T, store *sqlite.Store, query string) search.Response {
	t.Helper()
	result, err := store.Search(context.Background(), search.Request{Query: query, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func verifyNativeEvidence(t *testing.T, store *sqlite.Store, query, sourceID, relativePath, fullPath, content string) string {
	t.Helper()
	result := nativeSearch(t, store, query)
	if result.Total != 1 || len(result.Results) != 1 {
		t.Fatalf("search %q returned %d results: %+v", query, result.Total, result.Results)
	}
	hit := result.Results[0]
	wantURI := nativeFileURI(fullPath)
	if hit.SourceID != sourceID || hit.Path != relativePath || (hit.URI != wantURI && !nativeURIReferencesPath(hit.URI, fullPath)) {
		t.Fatalf("search provenance differs from native path: got=%+v want source=%q path=%q uri=%q", hit, sourceID, relativePath, wantURI)
	}
	evidence, err := store.Evidence(context.Background(), hit.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.ID != hit.ID || evidence.SourceID != sourceID || evidence.Path != relativePath || evidence.URI != wantURI || evidence.Text != content {
		t.Fatalf("preview did not preserve indexed snapshot provenance/content: %+v", evidence)
	}
	return hit.ID
}

func nativeFileURI(path string) string {
	path = filepath.ToSlash(filepath.Clean(path))
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func nativeURIReferencesPath(uri, path string) bool {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	uriPath := parsed.Path
	if len(uriPath) >= 3 && uriPath[0] == '/' && uriPath[2] == ':' {
		uriPath = uriPath[1:]
	}
	uriInfo, err := os.Stat(filepath.FromSlash(uriPath))
	if err != nil {
		return false
	}
	pathInfo, err := os.Stat(path)
	return err == nil && os.SameFile(uriInfo, pathInfo)
}

func replaceNativeFile(t *testing.T, path, content string) {
	t.Helper()
	temporary := filepath.Join(filepath.Dir(path), ".editor-save.tmp")
	if err := os.WriteFile(temporary, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
}

func TestUnicodeNativePathsKeepSearchPreviewAndIdentityAcrossRefreshAndRestart(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "notes space-日本語")
	parent := filepath.Join(root, "parent folder-Καλημέρα")
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	name := "résumé 🧭.md"
	path := filepath.Join(parent, name)
	content := "unicodeprovenance complete synthetic snapshot"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	store := openNativeStore(t, ctx, data)
	conn, err := filesystem.New(root, 1<<20, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Run(ctx, store, conn); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if conn.Source().Root != canonicalRoot {
		t.Fatalf("source root lost native path: got %q want %q", conn.Source().Root, canonicalRoot)
	}
	relative := filepath.ToSlash(filepath.Join(filepath.Base(parent), name))
	fullPath := filepath.Join(canonicalRoot, filepath.Base(parent), name)
	id := verifyNativeEvidence(t, store, "unicodeprovenance", conn.Source().ID, relative, fullPath, content)
	firstReport := sourceReportID(t, store, conn.Source().ID)

	refreshed, err := ingest.Refresh(ctx, store, conn)
	if err != nil || refreshed.Unchanged != 1 || refreshed.Removed != 0 {
		t.Fatalf("repeat refresh changed Unicode inventory: result=%+v err=%v", refreshed, err)
	}
	if got := verifyNativeEvidence(t, store, "unicodeprovenance", conn.Source().ID, relative, fullPath, content); got != id {
		t.Fatalf("refresh changed document identity: before=%q after=%q", id, got)
	}
	secondReport := sourceReportID(t, store, conn.Source().ID)
	if secondReport == firstReport {
		t.Fatal("successful refresh did not publish a new source report")
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	conn, err = filesystem.New(root, 1<<20, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Refresh(ctx, store, conn); err != nil {
		t.Fatal(err)
	}
	if got := verifyNativeEvidence(t, store, "unicodeprovenance", conn.Source().ID, relative, fullPath, content); got != id {
		t.Fatalf("restart/refresh changed document identity: before=%q after=%q", id, got)
	}
	if got := nativeSearch(t, store, "unicodeprovenance").Total; got != 1 {
		t.Fatalf("restart/refresh created duplicate documents: %d", got)
	}
}

func TestNativeAtomicEditorReplacementPublishesCompleteSnapshots(t *testing.T) {
	ctx := context.Background()
	root, data := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "atomic note.md")
	initial := "atomicinitialtoken complete baseline"
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	store := openNativeStore(t, ctx, data)
	conn, err := filesystem.New(root, 1<<20, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Run(ctx, store, conn); err != nil {
		t.Fatal(err)
	}
	beforeReport := sourceReportID(t, store, conn.Source().ID)

	candidate := "atomiccandidatetoken complete editor version"
	final := "atomicfinaltoken complete replacement version"
	if err := os.WriteFile(path, []byte(candidate), 0600); err != nil {
		t.Fatal(err)
	}
	mutation := emittedMutationConnector{inner: conn, afterFirst: func() error {
		temporary := filepath.Join(root, "atomic replacement.tmp")
		if err := os.WriteFile(temporary, []byte(final), 0600); err != nil {
			return err
		}
		return os.Rename(temporary, path)
	}}
	if _, err := ingest.Refresh(ctx, store, mutation); err != nil {
		t.Fatal(err)
	}
	if got := nativeSearch(t, store, "atomicinitialtoken").Total; got != 0 {
		t.Fatalf("pre-refresh snapshot remained after successful replacement refresh: %d", got)
	}
	if got := nativeSearch(t, store, "atomicfinaltoken").Total; got != 0 {
		t.Fatalf("refresh observed replacement before completing its enumerated snapshot: %d", got)
	}
	relative := filepath.Base(path)
	verifyNativeEvidence(t, store, "atomiccandidatetoken", conn.Source().ID, relative, path, candidate)
	if got := sourceReportID(t, store, conn.Source().ID); got == beforeReport {
		t.Fatal("successful replacement refresh did not publish a new report")
	}

	if _, err := ingest.Refresh(ctx, store, conn); err != nil {
		t.Fatal(err)
	}
	if got := nativeSearch(t, store, "atomiccandidatetoken").Total; got != 0 {
		t.Fatalf("old complete editor version survived retry: %d", got)
	}
	verifyNativeEvidence(t, store, "atomicfinaltoken", conn.Source().ID, relative, path, final)
}

type emittedMutationConnector struct {
	inner      *filesystem.Connector
	afterFirst func() error
}

func (c emittedMutationConnector) Source() connector.Source { return c.inner.Source() }

func (c emittedMutationConnector) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	report, _, err := c.ScanWithDiagnostics(ctx, emit)
	return report, err
}

func (c emittedMutationConnector) ScanWithDiagnostics(ctx context.Context, emit func(connector.Document) error) (connector.Report, diagnostics.Payload, error) {
	var once sync.Once
	var mutationErr error
	return c.inner.ScanWithDiagnostics(ctx, func(document connector.Document) error {
		if err := emit(document); err != nil {
			return err
		}
		once.Do(func() { mutationErr = c.afterFirst() })
		return mutationErr
	})
}

func TestNativeCaseOnlyRenameReplacesIdentityAndPrunesOldDocument(t *testing.T) {
	ctx := context.Background()
	root, data := t.TempDir(), t.TempDir()
	probe := filepath.Join(root, "CaseSensitivityProbe.md")
	if err := os.WriteFile(probe, []byte("probe"), 0600); err != nil {
		t.Fatal(err)
	}
	_, caseProbeErr := os.Stat(filepath.Join(root, "casesensitivityprobe.md"))
	caseSensitive := errors.Is(caseProbeErr, os.ErrNotExist)
	if caseProbeErr != nil && !caseSensitive {
		t.Fatalf("case-sensitivity probe failed: %v", caseProbeErr)
	}
	if err := os.Remove(probe); err != nil {
		t.Fatal(err)
	}
	t.Logf("native filesystem case sensitivity observed: sensitive=%t", caseSensitive)

	oldName, newName := "CaseOnly Note.md", "caseonly note.md"
	oldPath, newPath := filepath.Join(root, oldName), filepath.Join(root, newName)
	content := "caseidentitytoken complete native rename"
	if err := os.WriteFile(oldPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	store := openNativeStore(t, ctx, data)
	conn, err := filesystem.New(root, 1<<20, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Run(ctx, store, conn); err != nil {
		t.Fatal(err)
	}
	oldID := verifyNativeEvidence(t, store, "caseidentitytoken", conn.Source().ID, oldName, oldPath, content)
	beforeReport := sourceReportID(t, store, conn.Source().ID)

	// A temporary intermediate makes the directory entry's new spelling
	// observable on case-insensitive filesystems as well as case-sensitive ones.
	intermediate := filepath.Join(root, ".case-rename-in-progress")
	if err := os.Rename(oldPath, intermediate); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(intermediate, newPath); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Refresh(ctx, store, conn); err != nil {
		t.Fatal(err)
	}
	newID := verifyNativeEvidence(t, store, "caseidentitytoken", conn.Source().ID, newName, newPath, content)
	if oldID == newID {
		t.Fatalf("rename retained the old document identity %q", oldID)
	}
	if _, err := store.Evidence(ctx, oldID, 0); !errors.Is(err, search.ErrNotFound) {
		t.Fatalf("old path identity survived complete rename refresh: %v", err)
	}
	if got := nativeSearch(t, store, "caseidentitytoken").Total; got != 1 {
		t.Fatalf("case-only rename left duplicate search documents: %d", got)
	}
	if got := sourceReportID(t, store, conn.Source().ID); got == beforeReport {
		t.Fatal("successful case-only rename refresh did not publish a new report")
	}
}

func TestNativeReadFailureKeepsLastSnapshotAndRetryPublishesReplacement(t *testing.T) {
	ctx := context.Background()
	root, data := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "temporarily locked.md")
	initial := "nativefailuretoken committed baseline"
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	store := openNativeStore(t, ctx, data)
	conn, err := filesystem.New(root, 1<<20, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Run(ctx, store, conn); err != nil {
		t.Fatal(err)
	}
	beforeReport := sourceReportID(t, store, conn.Source().ID)

	restoreRead := blockNativeFileRead(t, path)
	t.Cleanup(restoreRead)
	refreshResult := make(chan error, 1)
	go func() {
		_, refreshErr := ingest.Refresh(ctx, store, conn)
		refreshResult <- refreshErr
	}()
	select {
	case err := <-refreshResult:
		if err == nil {
			t.Fatal("native read refusal was committed as a successful inventory")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native read refusal did not terminate the scan")
	}
	if got := nativeSearch(t, store, "nativefailuretoken").Total; got != 1 {
		t.Fatalf("failed native read lost the committed snapshot: %d", got)
	}
	if got := sourceReportID(t, store, conn.Source().ID); got != beforeReport {
		t.Fatal("failed native read replaced the last successful source report")
	}

	restoreRead()
	updated := "nativerecoverytoken complete replacement"
	replaceNativeFile(t, path, updated)
	if _, err := ingest.Refresh(ctx, store, conn); err != nil {
		t.Fatalf("refresh after releasing native read fault failed: %v", err)
	}
	if got := nativeSearch(t, store, "nativefailuretoken").Total; got != 0 {
		t.Fatalf("retry retained the old content after replacement: %d", got)
	}
	verifyNativeEvidence(t, store, "nativerecoverytoken", conn.Source().ID, filepath.Base(path), path, updated)
	if got := sourceReportID(t, store, conn.Source().ID); got == beforeReport {
		t.Fatal("successful retry did not publish a new source report")
	}
}

func TestForgetDuringControlledNativeEnumerationCancelsWithoutLateCommit(t *testing.T) {
	ctx := context.Background()
	root, data := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "baseline.md"), []byte("forgetbaseline committed snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	var blockPDF atomic.Bool
	var enterOnce sync.Once
	extractPDF := func(ctx context.Context, _ io.Reader, _ int64) ([]connector.Page, error) {
		if !blockPDF.Load() {
			return []connector.Page{{Number: 1, Text: "controlledpdf uncommitted content"}}, nil
		}
		enterOnce.Do(func() { close(entered) })
		<-ctx.Done()
		return nil, ctx.Err()
	}
	newConnector := func(source connector.Source) (*filesystem.Connector, error) {
		return filesystem.NewWithOptions(source.Root, filesystem.Options{
			MaxTextBytes: source.MaxTextBytes, MaxPDFBytes: source.MaxPDFBytes,
			MaxDOCXBytes: source.MaxDOCXBytes, ExtractPDF: extractPDF,
			RegistrationToken: source.RegistrationToken, RegistrationRevision: source.RegistrationRevision,
		}, data)
	}
	store := openNativeStore(t, ctx, data)
	initial, err := filesystem.NewWithOptions(root, filesystem.Options{MaxTextBytes: 1 << 20, MaxPDFBytes: 1 << 20, ExtractPDF: extractPDF}, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Run(ctx, store, initial); err != nil {
		t.Fatal(err)
	}
	sourceID := initial.Source().ID

	managerCtx, cancelManager := context.WithCancel(ctx)
	watcher := newTestWatcher()
	manager := syncer.New(store, syncer.Config{
		Factory: newConnector, Interval: time.Hour, DiscoveryInterval: 10 * time.Millisecond,
		Debounce:       10 * time.Millisecond,
		WatcherFactory: func() (syncer.Watcher, error) { return watcher, nil },
	})
	managerDone := make(chan struct{})
	go func() {
		defer close(managerDone)
		manager.Run(managerCtx)
	}()
	defer func() {
		cancelManager()
		select {
		case <-managerDone:
		case <-time.After(3 * time.Second):
			t.Error("manager shutdown exceeded the bounded cleanup deadline")
		}
	}()
	eventually(t, func() bool {
		status := manager.Status()
		return len(status) == 1 && status[0].State == "idle"
	})
	baselineReport := sourceReportID(t, store, sourceID)
	blockPDF.Store(true)
	if err := os.WriteFile(filepath.Join(root, "controlled.pdf"), []byte("synthetic local pdf bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	watcher.events <- fsnotify.Event{Name: filepath.Join(initial.Source().Root, "controlled.pdf"), Op: fsnotify.Create}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("native filesystem enumeration did not reach the controlled PDF read")
	}
	status := manager.Status()
	if len(status) != 1 || status[0].State != "indexing" || status[0].Progress.AttemptID == "" {
		t.Fatalf("controlled scan did not expose its active attempt: %+v", status)
	}
	activeAttempt := status[0].Progress.AttemptID
	if got := nativeSearch(t, store, "forgetbaseline").Total; got != 1 {
		t.Fatalf("in-flight enumeration hid the committed snapshot: %d", got)
	}
	if got := sourceReportID(t, store, sourceID); got != baselineReport {
		t.Fatal("in-flight enumeration changed the committed source report")
	}

	forgetStarted := make(chan struct{})
	forgetDone := make(chan error, 1)
	go func() {
		close(forgetStarted)
		forgetDone <- store.ForgetSource(ctx, sourceID)
	}()
	<-forgetStarted
	cancelManager()
	select {
	case <-managerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("manager did not stop after cancellation of controlled enumeration")
	}
	select {
	case err := <-forgetDone:
		if err != nil {
			t.Fatalf("Forget did not complete after the scan rolled back: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Forget remained blocked after controlled enumeration was canceled")
	}

	sources, err := store.Sources(ctx)
	if err != nil || len(sources) != 0 {
		t.Fatalf("forgotten source was resurrected after cancellation: %+v %v", sources, err)
	}
	if _, err := store.SourceReport(ctx, sourceID, false); !errors.Is(err, sqlite.ErrSourceNotFound) {
		t.Fatalf("forgotten source report reappeared: %v", err)
	}
	for _, query := range []string{"forgetbaseline", "controlledpdf", "uncommitted"} {
		if got := nativeSearch(t, store, query).Total; got != 0 {
			t.Fatalf("forgotten/canceled enumeration published %q: %d results", query, got)
		}
	}
	status = manager.Status()
	if len(status) != 1 || status[0].Progress.AttemptID != activeAttempt || status[0].Progress.Committed || status[0].Progress.ReportID != "" {
		t.Fatalf("canceled old generation published late progress: %+v", status)
	}
}
