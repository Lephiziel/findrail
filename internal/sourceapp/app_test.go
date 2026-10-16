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
	"github.com/Lephiziel/findrail/internal/snapshot"
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

func TestArchiveSourceCannotBeConfiguredOrRefreshed(t *testing.T) {
	a, store, _ := openApp(t)
	origin := snapshot.Origin{ID: "frozen-origin", Kind: "filesystem", Name: "Folder", Location: "/tmp/frozen", IndexedAt: "2026-10-09T12:00:00Z"}
	doc := snapshot.Document{ID: "doc", Path: "note.md", Title: "Note", URI: "file:///tmp/frozen/note.md", MediaType: "text/plain", Text: "frozen", ContentHash: strings.Repeat("a", 64), SizeBytes: 6, ModifiedAt: "2026-10-09T11:00:00Z"}
	data, err := snapshot.Encode(snapshot.Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: origin}, []snapshot.Document{doc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := snapshot.Inspect(data)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.ImportSnapshot(context.Background(), "Frozen", archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Refresh(id); err == nil || !strings.Contains(err.Error(), "archive_snapshot_frozen") {
		t.Fatalf("archive refresh error: %v", err)
	}
	if _, err = a.Configure(id, 1<<20); err == nil || !strings.Contains(err.Error(), "archive_snapshot_frozen") {
		t.Fatalf("archive configure error: %v", err)
	}
	if jobs := a.Jobs(); len(jobs) != 0 {
		t.Fatalf("archive created operation jobs: %+v", jobs)
	}
}

func TestSnapshotInspectClaimImportIsProcessBoundAndReplayProtected(t *testing.T) {
	a, store, _ := openApp(t)
	origin := snapshot.Origin{ID: "lease-origin", Kind: "filesystem", Name: "Synthetic", Location: "/synthetic", IndexedAt: "2026-10-09T12:00:00Z"}
	doc := snapshot.Document{ID: "lease-doc", Path: "note.md", Title: "Note", URI: "file:///synthetic/note.md", MediaType: "text/plain", Text: "portable lease fixture", ContentHash: strings.Repeat("a", 64), SizeBytes: 22, ModifiedAt: "2026-10-09T11:00:00Z"}
	data, err := snapshot.Encode(snapshot.Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: origin}, []snapshot.Document{doc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.zip")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	inspect, err := a.InspectSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := await(t, a, inspect.ID); got.Status != "succeeded" || got.Result != "" {
		t.Fatalf("inspect generic job leaked result: %+v", got)
	}
	preview := a.SnapshotInspection(inspect.ID)
	if preview.Status != "succeeded" || preview.Claim == "" || preview.Documents != 1 || preview.Fingerprint == "" {
		t.Fatalf("preview: %+v", preview)
	}
	importJob, err := a.ImportSnapshot(preview.Claim, "Imported synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if got := await(t, a, importJob.ID); got.Status != "succeeded" {
		t.Fatalf("import job: %+v", got)
	}
	if _, err = a.ImportSnapshot(preview.Claim, "Replay"); err == nil {
		t.Fatal("accepted replayed claim")
	}
	if result := a.SnapshotInspection(inspect.ID); result.Claim != "" {
		t.Fatal("consumed preview claim was still returned")
	}
	sources, err := store.Sources(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
}

func TestSnapshotImportRevalidatesChangedFileBeforeMutation(t *testing.T) {
	a, store, _ := openApp(t)
	makeFile := func(text string) []byte {
		origin := snapshot.Origin{ID: "changed-origin", Kind: "filesystem", Name: "Synthetic", Location: "/synthetic", IndexedAt: "2026-10-09T12:00:00Z"}
		doc := snapshot.Document{ID: "changed-doc", Path: "note.md", Title: "Note", URI: "file:///synthetic/note.md", MediaType: "text/plain", Text: text, ContentHash: strings.Repeat("a", 64), SizeBytes: int64(len(text)), ModifiedAt: "2026-10-09T11:00:00Z"}
		data, err := snapshot.Encode(snapshot.Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: origin}, []snapshot.Document{doc}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	path := filepath.Join(t.TempDir(), "replace.zip")
	if err := os.WriteFile(path, makeFile("claimed content"), 0600); err != nil {
		t.Fatal(err)
	}
	inspect, err := a.InspectSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := await(t, a, inspect.ID); got.Status != "succeeded" {
		t.Fatalf("inspect: %+v", got)
	}
	claim := a.SnapshotInspection(inspect.ID).Claim
	if claim == "" {
		t.Fatal("missing claim")
	}
	if err = os.WriteFile(path, makeFile("changed content"), 0600); err != nil {
		t.Fatal(err)
	}
	job, err := a.ImportSnapshot(claim, "Changed after inspect")
	if err != nil {
		t.Fatal(err)
	}
	if got := await(t, a, job.ID); got.Status != "failed" || got.ErrorCode != "snapshot_changed" || !strings.Contains(got.Error, "no source was imported") {
		t.Fatalf("changed-file import: %+v", got)
	}
	sources, err := store.Sources(context.Background())
	if err != nil || len(sources) != 0 {
		t.Fatalf("changed archive mutated storage: %+v %v", sources, err)
	}
}

func TestSnapshotClaimsExpireAndDoNotSurviveAppRestart(t *testing.T) {
	_, store, dir := openApp(t)
	origin := snapshot.Origin{ID: "claim-lifetime", Kind: "filesystem", Name: "Synthetic", Location: "/synthetic", IndexedAt: "2026-10-09T12:00:00Z"}
	doc := snapshot.Document{ID: "claim-doc", Path: "note.md", Title: "Note", URI: "file:///synthetic/note.md", MediaType: "text/plain", Text: "claim lifetime", ContentHash: strings.Repeat("a", 64), SizeBytes: 15, ModifiedAt: "2026-10-09T11:00:00Z"}
	data, err := snapshot.Encode(snapshot.Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: origin}, []snapshot.Document{doc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "claim.zip")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	first := New(context.Background(), store, dir, "/unused")
	inspect, err := first.InspectSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	await(t, first, inspect.ID)
	claim := first.SnapshotInspection(inspect.ID).Claim
	if claim == "" {
		t.Fatal("missing claim")
	}
	first.mu.Lock()
	lease := first.snapshotLeases[claim]
	lease.expires = time.Now().Add(-time.Second)
	first.snapshotLeases[claim] = lease
	first.mu.Unlock()
	if got := first.SnapshotInspection(inspect.ID); got.Status != "expired" {
		t.Fatalf("expired lease result: %+v", got)
	}
	if _, err = first.ImportSnapshot(claim, "Expired"); err == nil {
		t.Fatal("expired claim accepted")
	}
	c, cancel := context.WithTimeout(context.Background(), time.Second)
	if err = first.Close(c); err != nil {
		t.Fatal(err)
	}
	cancel()
	// A fresh process-local App has no lease state from its predecessor.
	second := New(context.Background(), store, dir, "/unused")
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = second.Close(c)
	}()
	if _, err = second.ImportSnapshot(claim, "After restart"); err == nil {
		t.Fatal("claim survived app restart")
	}
}

func TestSnapshotJobsRejectConcurrentInspectAndCancelWhileWaitingForDecodeSlot(t *testing.T) {
	a, _, _ := openApp(t)
	origin := snapshot.Origin{ID: "busy-origin", Kind: "filesystem", Name: "Synthetic", Location: "/synthetic", IndexedAt: "2026-10-09T12:00:00Z"}
	data, err := snapshot.Encode(snapshot.Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: origin}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "busy.zip")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	a.snapshotGate <- struct{}{}
	first, err := a.InspectSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.InspectSnapshot(path); err == nil || err.Error() != "busy" {
		t.Fatalf("concurrent inspect was not rejected busy: %v", err)
	}
	if err = a.Cancel(first.ID); err != nil {
		t.Fatal(err)
	}
	<-a.snapshotGate
	if done := await(t, a, first.ID); done.Status != "canceled" {
		t.Fatalf("canceled inspect: %+v", done)
	}
}

func TestPortableSnapshotManagementExportInspectImportRestartAndForget(t *testing.T) {
	ctx := context.Background()
	indexA, indexB := t.TempDir(), t.TempDir()
	storeA, err := sqlite.Open(ctx, indexA)
	if err != nil {
		t.Fatal(err)
	}
	appA := New(ctx, storeA, indexA, "/unused")
	defer func() {
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_ = appA.Close(c)
		_ = storeA.Close()
	}()
	origin := snapshot.Origin{ID: "lifecycle-origin", Kind: "filesystem", Name: "Synthetic PDF source", Location: "/synthetic/source", IndexedAt: "2026-10-09T12:00:00Z"}
	makeArchive := func(text string) snapshot.Archive {
		doc := snapshot.Document{ID: "original-pdf", Path: "report.pdf", Title: "Report", URI: "file:///synthetic/source/report.pdf", MediaType: "application/pdf", Text: "first page evidence\n" + text + "\n", ContentHash: strings.Repeat("b", 64), SizeBytes: 100, ModifiedAt: "2026-10-09T11:00:00Z", PageCount: 2}
		pages := []snapshot.Page{{Path: "report.pdf", Number: 1, Text: "first page evidence"}, {Path: "report.pdf", Number: 2, Text: text}}
		data, e := snapshot.Encode(snapshot.Manifest{Producer: "synthetic test", ExportedAt: "2026-10-09T12:00:00Z", Origin: origin}, []snapshot.Document{doc}, pages)
		if e != nil {
			t.Fatal(e)
		}
		decoded, e := snapshot.Inspect(data)
		if e != nil {
			t.Fatal(e)
		}
		return decoded
	}
	originalID, err := storeA.ImportSnapshot(ctx, "Synthetic PDF", makeArchive("secondpageprobe retained page two"))
	if err != nil {
		t.Fatal(err)
	}
	archiveDir := t.TempDir()
	export := func(app *App, id, name string) string {
		path := filepath.Join(archiveDir, name)
		job, e := app.ExportSnapshot(id, path)
		if e != nil {
			t.Fatal(e)
		}
		if done := await(t, app, job.ID); done.Status != "succeeded" {
			t.Fatalf("export: %+v", done)
		}
		info, e := os.Stat(path)
		if e != nil {
			t.Fatal(e)
		}
		if os.PathSeparator != '\\' && info.Mode().Perm() != 0600 {
			t.Fatalf("export mode=%o", info.Mode().Perm())
		}
		return path
	}
	pdfPath := export(appA, originalID, "pdf.zip")
	// The management path uses the shared v1 codec but limits the source build's
	// file/profile and validates before exposing its opaque preview lease.
	storeB, err := sqlite.Open(ctx, indexB)
	if err != nil {
		t.Fatal(err)
	}
	appB := New(ctx, storeB, indexB, "/unused")
	closeB := func() {
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if e := appB.Close(c); e != nil {
			t.Error(e)
		}
		if e := storeB.Close(); e != nil {
			t.Error(e)
		}
	}
	pdfInspect, err := appB.InspectSnapshot(pdfPath)
	if err != nil {
		t.Fatal(err)
	}
	if done := await(t, appB, pdfInspect.ID); done.Status != "succeeded" || done.Result != "" {
		t.Fatalf("inspect job: %+v", done)
	}
	pdfPreview := appB.SnapshotInspection(pdfInspect.ID)
	if pdfPreview.Status != "succeeded" || pdfPreview.Pages != 2 || pdfPreview.Claim == "" {
		t.Fatalf("PDF preview: %+v", pdfPreview)
	}
	pdfImport, err := appB.ImportSnapshot(pdfPreview.Claim, "Imported PDF")
	if err != nil {
		t.Fatal(err)
	}
	done := await(t, appB, pdfImport.ID)
	if done.Status != "succeeded" || !done.Progress.Committed || !strings.HasPrefix(done.Result, "imported:") {
		t.Fatalf("PDF import: %+v", done)
	}
	importedID := strings.TrimPrefix(done.Result, "imported:")
	response, err := storeB.Search(ctx, search.Request{Query: "secondpageprobe", Limit: 10})
	if err != nil || len(response.Results) != 1 || response.Results[0].Page != 2 {
		t.Fatalf("page 2 search: %+v %v", response, err)
	}
	evidence, err := storeB.Evidence(ctx, response.Results[0].ID, 2)
	if err != nil || !strings.Contains(evidence.Text, "secondpageprobe") {
		t.Fatalf("PDF page 2 evidence: %+v %v", evidence, err)
	}
	// Identical content returns the pre-existing ID and preserves its name.
	dupInspect, err := appB.InspectSnapshot(pdfPath)
	if err != nil {
		t.Fatal(err)
	}
	await(t, appB, dupInspect.ID)
	dupJob, err := appB.ImportSnapshot(appB.SnapshotInspection(dupInspect.ID).Claim, "Must not rename")
	if err != nil {
		t.Fatal(err)
	}
	if done := await(t, appB, dupJob.ID); done.Status != "succeeded" || done.Result != "already_imported:"+importedID {
		t.Fatalf("duplicate import: %+v", done)
	}
	// A changed fingerprint coexists; it does not replace the imported snapshot.
	changedBytes, err := snapshot.Encode(snapshot.Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: origin}, makeArchive("changedpageprobe distinct page two").Documents, makeArchive("changedpageprobe distinct page two").Pages)
	if err != nil {
		t.Fatal(err)
	}
	changedPath := filepath.Join(archiveDir, "changed.zip")
	if err = os.WriteFile(changedPath, changedBytes, 0600); err != nil {
		t.Fatal(err)
	}
	changedInspect, err := appB.InspectSnapshot(changedPath)
	if err != nil {
		t.Fatal(err)
	}
	await(t, appB, changedInspect.ID)
	changedJob, err := appB.ImportSnapshot(appB.SnapshotInspection(changedInspect.ID).Claim, "Changed snapshot")
	if err != nil {
		t.Fatal(err)
	}
	changedDone := await(t, appB, changedJob.ID)
	if changedDone.Status != "succeeded" {
		t.Fatalf("changed import: %+v", changedDone)
	}
	changedID := strings.TrimPrefix(changedDone.Result, "imported:")
	if changedID == importedID || !strings.HasPrefix(changedDone.Result, "imported:") {
		t.Fatalf("changed snapshot did not coexist: %+v", changedDone)
	}
	collection, err := storeB.CreateCollection(ctx, "Keep unrelated collection")
	if err != nil {
		t.Fatal(err)
	}
	// A separate text source exercises the filesystem-source export path too.
	root := filepath.Join(t.TempDir(), "synthetic-root")
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "note.md"), []byte("filesystemsnapshotprobe"), 0600); err != nil {
		t.Fatal(err)
	}
	folderJob, err := appA.AddFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	if done := await(t, appA, folderJob.ID); done.Status != "succeeded" {
		t.Fatalf("folder indexing: %+v", done)
	}
	sources, err := storeA.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var folderID string
	for _, source := range sources {
		if source.Kind == "filesystem" {
			folderID = source.ID
		}
	}
	if folderID == "" {
		t.Fatal("filesystem source not registered")
	}
	folderPath := export(appA, folderID, "folder.zip")
	folderInspect, err := appB.InspectSnapshot(folderPath)
	if err != nil {
		t.Fatal(err)
	}
	await(t, appB, folderInspect.ID)
	folderImport, err := appB.ImportSnapshot(appB.SnapshotInspection(folderInspect.ID).Claim, "Imported folder")
	if err != nil {
		t.Fatal(err)
	}
	if done := await(t, appB, folderImport.ID); done.Status != "succeeded" {
		t.Fatalf("folder import: %+v", done)
	}
	// Existing files, output symlinks and protected roots must not be published.
	for _, target := range []string{filepath.Join(archiveDir, "pdf.zip"), filepath.Join(indexA, "forbidden.zip"), filepath.Join(root, "forbidden.zip")} {
		job, e := appA.ExportSnapshot(originalID, target)
		if e != nil {
			t.Fatal(e)
		}
		if done := await(t, appA, job.ID); done.Status != "failed" {
			t.Fatalf("unsafe export target %q: %+v", target, done)
		}
	}
	link := filepath.Join(archiveDir, "target-link.zip")
	if os.PathSeparator != '\\' {
		if err = os.Symlink(filepath.Join(archiveDir, "not-present"), link); err != nil {
			t.Fatal(err)
		}
		job, e := appA.ExportSnapshot(originalID, link)
		if e != nil {
			t.Fatal(e)
		}
		if done := await(t, appA, job.ID); done.Status != "failed" {
			t.Fatalf("symlink export: %+v", done)
		}
	}
	closeB()
	storeB, err = sqlite.Open(ctx, indexB)
	if err != nil {
		t.Fatal(err)
	}
	appB = New(ctx, storeB, indexB, "/unused")
	response, err = storeB.Search(ctx, search.Request{Query: "secondpageprobe", Limit: 10})
	if err != nil || len(response.Results) != 1 || response.Results[0].Page != 2 {
		t.Fatalf("restart lost PDF snapshot: %+v %v", response, err)
	}
	if _, err = storeB.LoadCollection(ctx, collection.ID); err != nil {
		t.Fatalf("restart lost unrelated collection: %v", err)
	}
	remove, err := appB.Remove(importedID)
	if err != nil {
		t.Fatal(err)
	}
	if done := await(t, appB, remove.ID); done.Status != "succeeded" {
		t.Fatalf("Forget imported source: %+v", done)
	}
	response, err = storeB.Search(ctx, search.Request{Query: "secondpageprobe", Limit: 10})
	if err != nil || len(response.Results) != 0 {
		t.Fatalf("Forget left searchable PDF: %+v %v", response, err)
	}
	response, err = storeB.Search(ctx, search.Request{Query: "changedpageprobe", Limit: 10})
	if err != nil || len(response.Results) != 1 {
		t.Fatalf("Forget removed coexisting source: %+v %v", response, err)
	}
	closeB()
}

func TestFilesystemPolicyRefreshConfigureAndRollback(t *testing.T) {
	a, store, _ := openApp(t)
	root := filepath.Join(t.TempDir(), "folder with spaces")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "generated.md")
	if err := os.WriteFile(file, []byte("sourceapp policy marker"), 0600); err != nil {
		t.Fatal(err)
	}
	added, err := a.AddFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	done := await(t, a, added.ID)
	if done.Status != "succeeded" {
		t.Fatalf("add folder: %+v", done)
	}
	sources, err := store.Sources(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources: %+v %v", sources, err)
	}
	id := sources[0].ID
	searchCount := func(q string) int {
		r, e := store.Search(context.Background(), search.Request{Query: q, Limit: 10})
		if e != nil {
			t.Fatal(e)
		}
		return r.Total
	}
	if searchCount("sourceapp") != 1 {
		t.Fatal("initial document missing")
	}
	policy := filepath.Join(root, ".findrailignore")
	if err := os.WriteFile(policy, []byte("generated.md\n"), 0600); err != nil {
		t.Fatal(err)
	}
	job, err := a.Refresh(id)
	if err != nil {
		t.Fatal(err)
	}
	done = await(t, a, job.ID)
	if done.Status != "succeeded" || searchCount("sourceapp") != 0 {
		t.Fatalf("policy refresh failed: %+v", done)
	}
	if err := os.WriteFile(policy, []byte("generated.md\n!generated.md\n"), 0600); err != nil {
		t.Fatal(err)
	}
	job, err = a.Refresh(id)
	if err != nil {
		t.Fatal(err)
	}
	done = await(t, a, job.ID)
	if done.Status != "succeeded" || searchCount("sourceapp") != 1 {
		t.Fatalf("sourceapp policy reinclusion: %+v", done)
	}
	if err := os.WriteFile(policy, []byte("[invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	job, err = a.Refresh(id)
	if err != nil {
		t.Fatal(err)
	}
	done = await(t, a, job.ID)
	if done.Status != "failed" {
		t.Fatalf("malformed refresh: %+v", done)
	}
	if searchCount("sourceapp") != 1 {
		t.Fatal("failed sourceapp refresh removed previous snapshot")
	}
	if err := os.WriteFile(policy, []byte("generated.md\n"), 0600); err != nil {
		t.Fatal(err)
	}
	job, err = a.Configure(id, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	done = await(t, a, job.ID)
	if done.Status != "succeeded" || searchCount("sourceapp") != 0 {
		t.Fatalf("Configure ignored root policy: %+v", done)
	}
	if err := os.Remove(policy); err != nil {
		t.Fatal(err)
	}
	job, err = a.Refresh(id)
	if err != nil {
		t.Fatal(err)
	}
	done = await(t, a, job.ID)
	if done.Status != "succeeded" || searchCount("sourceapp") != 1 {
		t.Fatalf("Configure did not rescan using current filesystem policy: %+v", done)
	}
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
	// Exercise the management job lifecycle for a GitHub registration as well as
	// filesystem and archive sources: export preserves frozen GitHub provenance.
	githubArchivePath := filepath.Join(t.TempDir(), "github-source.zip")
	githubExport, err := a.ExportSnapshot(id, githubArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	if done := await(t, a, githubExport.ID); done.Status != "succeeded" || !done.Progress.Committed {
		t.Fatalf("GitHub export: %+v", done)
	}
	githubInspect, err := a.InspectSnapshot(githubArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	if done := await(t, a, githubInspect.ID); done.Status != "succeeded" {
		t.Fatalf("GitHub inspect: %+v", done)
	}
	githubPreview := a.SnapshotInspection(githubInspect.ID)
	if githubPreview.Kind != "github" || githubPreview.Claim == "" {
		t.Fatalf("GitHub provenance preview: %+v", githubPreview)
	}
	githubImport, err := a.ImportSnapshot(githubPreview.Claim, "Frozen GitHub fixture")
	if err != nil {
		t.Fatal(err)
	}
	githubImportDone := await(t, a, githubImport.ID)
	if githubImportDone.Status != "succeeded" || !githubImportDone.Progress.Committed {
		t.Fatalf("GitHub archive import: %+v", githubImportDone)
	}
	githubArchiveID := strings.TrimPrefix(githubImportDone.Result, "imported:")
	if !strings.HasPrefix(githubImportDone.Result, "imported:") {
		t.Fatalf("GitHub import result: %+v", githubImportDone)
	}
	archiveSources, err := store.Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var githubArchiveFound bool
	for _, source := range archiveSources {
		if source.ID == githubArchiveID && source.Archive != nil && source.Archive.Origin.Kind == "github" {
			githubArchiveFound = true
		}
	}
	if !githubArchiveFound {
		t.Fatalf("GitHub source provenance not frozen in imported archive: %+v", archiveSources)
	}
	removeGitHubArchive, err := a.Remove(githubArchiveID)
	if err != nil {
		t.Fatal(err)
	}
	if done := await(t, a, removeGitHubArchive.ID); done.Status != "succeeded" {
		t.Fatalf("remove temporary GitHub archive fixture: %+v", done)
	}
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
	cleanupStarted := make(chan struct{})
	allowCleanup := make(chan struct{})
	cleaned := make(chan struct{})
	job, err := a.submit("test", "cancel", "", func(ctx context.Context, _ *Job) (string, error) {
		close(entered)
		<-ctx.Done()
		close(cleanupStarted)
		<-allowCleanup
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
	<-cleanupStarted
	got, _ := a.Job(job.ID)
	close(allowCleanup)
	if got.Status != "running" {
		t.Fatalf("marked canceled before cleanup: %+v", got)
	}
	select {
	case <-cleaned:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not run")
	}
	if got := await(t, a, job.ID); got.Status != "canceled" {
		t.Fatalf("terminal cancel: %+v", got)
	} else if got.Progress.Committed || !got.Progress.Partial || got.Progress.FailureCode != "canceled" || got.Progress.Phase != "done" {
		t.Fatalf("cancel progress falsely claims publication: %+v", got.Progress)
	}
}

func TestFailedRefreshProgressKeepsDurableReportAndHidesFatalPath(t *testing.T) {
	a, store, _ := openApp(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("committed before fatal extraction"), 0600); err != nil {
		t.Fatal(err)
	}
	added, err := a.AddFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	if job := await(t, a, added.ID); job.Status != "succeeded" {
		t.Fatal(job)
	}
	before, err := store.SourceReport(context.Background(), added.SourceID, false)
	if err != nil {
		t.Fatal(err)
	}
	beforeID := before["report"].(sqlite.ReportSummary).ReportID
	if err = os.WriteFile(filepath.Join(root, "z-broken.pdf"), []byte("%PDF-invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	refresh, err := a.Refresh(added.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	failed := await(t, a, refresh.ID)
	if failed.Status != "failed" || failed.ErrorCode != "scan_failed" || failed.Progress.Committed || !failed.Progress.Partial || failed.Progress.FailureCode != "scan_failed" || failed.Progress.ProcessedDocuments != 1 || strings.Contains(failed.Error, "z-broken.pdf") {
		t.Fatalf("failed attempt diagnostics: %+v", failed)
	}
	after, err := store.SourceReport(context.Background(), added.SourceID, false)
	if err != nil {
		t.Fatal(err)
	}
	if after["report"].(sqlite.ReportSummary).ReportID != beforeID {
		t.Fatal("failed refresh replaced previous committed report")
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
	} else if got.Progress.AttemptID != added.ID || got.Progress.ProcessedDocuments != 1 || got.Progress.Sequence == 0 || got.Progress.Partial || !got.Progress.Committed || got.Progress.ReportID == "" {
		t.Fatalf("final committed progress is not accurate: %+v", got.Progress)
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
	deadline := time.After(5 * time.Second)
	for {
		changed := app.WaitChannel()
		job, _ := app.Job(remove.ID)
		if job.Status == "running" && job.Phase == "removing" {
			break
		}
		select {
		case <-changed:
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

func TestRemoveCancelsCoordinatedConfigureWithoutChangingPolicy(t *testing.T) {
	app, store, _ := openApp(t)
	root := t.TempDir()
	note := filepath.Join(root, "note.md")
	if err := os.WriteFile(note, []byte("configure remove race"), 0600); err != nil {
		t.Fatal(err)
	}
	added, err := app.AddFolder(root)
	if err != nil {
		t.Fatal(err)
	}
	if job := await(t, app, added.ID); job.Status != "succeeded" {
		t.Fatal(job)
	}
	unlock, err := app.coordinator.Acquire(context.Background(), added.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := app.Configure(added.SourceID, 0)
	if err != nil {
		unlock()
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		changed := app.WaitChannel()
		job, _ := app.Job(configured.ID)
		if job.Status == "running" && job.Phase == "indexing" {
			break
		}
		select {
		case <-changed:
		case <-deadline:
			unlock()
			t.Fatal("Configure did not wait for shared source coordinator")
		}
	}
	removed, err := app.Remove(added.SourceID)
	if err != nil {
		unlock()
		t.Fatal(err)
	}
	unlock()
	if job := await(t, app, configured.ID); job.Status != "canceled" {
		t.Fatalf("Configure status: %+v", job)
	}
	if job := await(t, app, removed.ID); job.Status != "succeeded" {
		t.Fatalf("Remove status: %+v", job)
	}
	if sources := mustSources(t, store); len(sources) != 0 {
		t.Fatalf("source survived Remove: %+v", sources)
	}
	if _, err := os.Stat(note); err != nil {
		t.Fatalf("original changed or removed: %v", err)
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
