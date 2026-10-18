//go:build linux

// Package recovery contains process-level crash tests. SIGKILL models abrupt
// process death on Linux; it is not a power-loss or filesystem-failure test.
package recovery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/diagnostics"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/Lephiziel/findrail/pkg/connector"
	modernsqlite "modernc.org/sqlite"
)

const sourceID = "recovery-synthetic-source"

var recoveryFunction sync.Once

type contentConnector struct {
	root, content string
	barrier       string
	maxDOCX       int64
	alternate     bool
	second        bool
}

func (c contentConnector) Source() connector.Source {
	return connector.Source{ID: sourceID, Kind: "filesystem", Name: "Synthetic", Root: c.root, MaxTextBytes: 1 << 20, MaxDOCXBytes: c.maxDOCX}
}
func (c contentConnector) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	seen := 0
	if c.content != "" {
		digest := sha256.Sum256([]byte(c.content))
		id, path, uri, title := "synthetic-document", "synthetic.txt", "file:///synthetic.txt", "Synthetic"
		if c.alternate {
			id, path, uri, title = "replacement-document", "replacement.txt", "file:///replacement.txt", "Replacement"
		}
		doc := connector.Document{ID: id, SourceID: sourceID, Title: title, URI: uri, Path: path, Content: c.content, Hash: hex.EncodeToString(digest[:]), ModifiedAt: time.Unix(1, 0).UTC(), MediaType: "application/pdf", Pages: []connector.Page{{Number: 1, Text: c.content + " pageidentitytoken"}}}
		docs := []connector.Document{doc}
		if c.second {
			second := doc
			second.ID, second.Path, second.URI, second.Title = "synthetic-second-document", "second.txt", "file:///second.txt", "Second synthetic"
			docs = append(docs, second)
		}
		for _, document := range docs {
			if err := emit(document); err != nil {
				return connector.Report{}, err
			}
			seen++
		}
	}
	if c.barrier != "" {
		if err := os.WriteFile(c.barrier, []byte("ready"), 0600); err != nil {
			return connector.Report{}, err
		}
		<-ctx.Done()
		return connector.Report{}, ctx.Err()
	}
	return connector.Report{Seen: seen}, nil
}

func (c contentConnector) ScanWithDiagnostics(ctx context.Context, emit func(connector.Document) error) (connector.Report, diagnostics.Payload, error) {
	r, err := c.Scan(ctx, emit)
	return r, diagnostics.Payload{ProcessedDocuments: int64(r.Seen), ObservedEntries: int64(r.Seen), ObservedEntriesKnown: true, ObservedFiles: int64(r.Seen), ObservedFilesKnown: true, ObservedDirectoriesKnown: true, Coverage: "complete"}, err
}

// TestCrashHelper is entered only by the child process started by the parent.
func TestCrashHelper(t *testing.T) {
	if os.Getenv("FINDRAIL_RECOVERY_HELPER") != "1" {
		t.Skip("helper process")
	}
	// Keep a live deadline while parked at a barrier. With Background's nil
	// Done channel the helper can be diagnosed as a deadlocked Go program and
	// exit on its own before the parent performs the controlled termination.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	barrier := os.Getenv("FINDRAIL_RECOVERY_BARRIER")
	if os.Getenv("FINDRAIL_RECOVERY_OPERATION") == "bundle-before-publish" || os.Getenv("FINDRAIL_RECOVERY_OPERATION") == "forget-before-publish" {
		registerCrashBarrier(barrier)
	}
	store, err := sqlite.Open(ctx, os.Getenv("FINDRAIL_RECOVERY_DATA"))
	if err != nil {
		os.Exit(21)
	}
	switch os.Getenv("FINDRAIL_RECOVERY_OPERATION") {
	case "refresh-before-publish":
		_, err = ingest.Refresh(ctx, store, contentConnector{root: "synthetic", content: "uncommittednewtoken", barrier: barrier})
	case "refresh-after-success-prune":
		_, err = ingest.Refresh(ctx, store, contentConnector{root: "synthetic", content: "replacementnewtoken", alternate: true})
		if err == nil {
			err = notifyAndBlock(barrier)
		}
	case "configure-before-publish":
		sources, e := store.Sources(ctx)
		if e != nil || len(sources) != 1 {
			os.Exit(23)
		}
		prior := sources[0]
		configured := prior.Source
		configured.MaxDOCXBytes = 64 << 10
		_, err = ingest.Configure(ctx, store, contentConnector{root: configured.Root, content: "configurednewtoken", barrier: barrier, maxDOCX: configured.MaxDOCXBytes}, prior.RegistrationToken, prior.RegistrationRevision, prior.MaxDOCXBytes)
	case "configure-after-success":
		sources, e := store.Sources(ctx)
		if e != nil || len(sources) != 1 {
			os.Exit(23)
		}
		prior := sources[0]
		configured := prior.Source
		configured.MaxDOCXBytes = 64 << 10
		_, err = ingest.Configure(ctx, store, contentConnector{root: configured.Root, content: "configurednewtoken", maxDOCX: configured.MaxDOCXBytes}, prior.RegistrationToken, prior.RegistrationRevision, prior.MaxDOCXBytes)
		if err == nil {
			err = notifyAndBlock(barrier)
		}
	case "forget-after-success":
		sources, e := store.Sources(ctx)
		if e != nil || len(sources) != 1 {
			os.Exit(23)
		}
		err = store.ForgetSourceRegistration(ctx, sourceID, sources[0].RegistrationToken)
		if err == nil {
			err = notifyAndBlock(barrier)
		}
	case "forget-before-publish":
		sources, e := store.Sources(ctx)
		if e != nil || len(sources) != 1 {
			os.Exit(23)
		}
		err = store.ForgetSourceRegistration(ctx, sourceID, sources[0].RegistrationToken)
	case "bundle-after-success":
		err = importLinkedBundle(ctx, store, "")
		if err == nil {
			err = notifyAndBlock(barrier)
		}
	case "bundle-before-publish":
		err = importLinkedBundle(ctx, store, "")
	default:
		os.Exit(25)
	}
	_ = store.Close()
	if err != nil {
		os.Exit(22)
	}
	os.Exit(0)
}

func importLinkedBundle(ctx context.Context, store *sqlite.Store, title string) error {
	collections, err := store.ListCollections(ctx)
	if err != nil {
		return err
	}
	var linked string
	for _, collection := range collections {
		if collection.Title == "Source-linked synthetic collection" {
			linked = collection.ID
			break
		}
	}
	if linked == "" {
		return errors.New("synthetic linked collection missing")
	}
	bundle, err := store.ExportEvidenceBundle(ctx, linked)
	if err != nil {
		return err
	}
	if title == "" {
		title = "Synthetic imported collection"
	}
	mappings := map[string]string{"s1": sourceID}
	preview, err := store.PreviewEvidenceBundle(ctx, bundle, mappings)
	if err != nil {
		return err
	}
	_, err = store.ImportEvidenceBundle(ctx, bundle, mappings, title, preview.Binding)
	return err
}

func registerCrashBarrier(path string) {
	recoveryFunction.Do(func() {
		if err := modernsqlite.RegisterScalarFunction("findrail_recovery_barrier", 0, func(*modernsqlite.FunctionContext, []driver.Value) (driver.Value, error) {
			if path == "" {
				return int64(1), nil
			}
			_ = os.WriteFile(path, []byte("inside_uncommitted_bundle_transaction"), 0600)
			select {}
		}); err != nil {
			panic(err)
		}
	})
}

func notifyAndBlock(barrier string) error {
	if err := os.WriteFile(barrier, []byte("acknowledged"), 0600); err != nil {
		return err
	}
	select {}
}

// TestSIGKILLDuringRefreshKeepsCommittedSnapshot has an explicit connector
// barrier after real production Upsert calls and before Commit. No sleeps are
// used to choose the crash boundary.
func TestSIGKILLRecoveryProcessCases(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SIGKILL process-group scenario is Linux-specific")
	}
	for _, operation := range []string{"refresh-before-publish", "refresh-after-success-prune", "configure-before-publish", "configure-after-success", "forget-before-publish", "forget-after-success", "bundle-before-publish", "bundle-after-success"} {
		t.Run(operation, func(t *testing.T) { runCrashCase(t, operation) })
	}
}

func runCrashCase(t *testing.T, operation string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if operation == "bundle-before-publish" || operation == "forget-before-publish" {
		registerCrashBarrier("")
	}
	root := t.TempDir()
	evidenceDir := os.Getenv("FINDRAIL_RECOVERY_REPORT_DIR")
	if evidenceDir == "" {
		evidenceDir = os.TempDir()
	}
	if err := os.MkdirAll(evidenceDir, 0700); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(evidenceDir, "findrail-recovery-"+operation+".json")
	report := caseReport{Case: operation, Status: "failed", Phase: "starting"}
	started := time.Now()
	defer func() {
		report.DurationMillis = time.Since(started).Milliseconds()
		if err := writeReport(reportPath, report); err != nil {
			t.Errorf("write recovery report: %v", err)
		}
	}()

	if err := writeReport(reportPath, report); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "data")
	store, err := sqlite.Open(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	content := "alpha committedoldtoken omega secondmarker pageidentitytoken"
	if _, err = ingest.Run(ctx, store, contentConnector{root: "synthetic", content: content + " pagecrashmarker", second: true}); err != nil {
		t.Fatal(err)
	}
	baseline, err := store.CreateCollection(ctx, "Unrelated synthetic collection")
	if err != nil {
		t.Fatal(err)
	}
	linked, err := store.CreateCollection(ctx, "Source-linked synthetic collection")
	if err != nil {
		t.Fatal(err)
	}
	initialSearch, err := store.Search(ctx, search.Request{Query: "committedoldtoken", Limit: 10})
	if err != nil || len(initialSearch.Results) != 2 {
		t.Fatalf("seed search: %+v %v", initialSearch, err)
	}
	var primary search.Result
	for _, result := range initialSearch.Results {
		if result.Path == "synthetic.txt" {
			primary = result
		}
	}
	if primary.ID == "" {
		t.Fatal("primary synthetic document missing")
	}
	linked, err = store.PinCollectionEntryAt(ctx, linked.ID, linked.Revision, primary.ID, 1, "committedoldtoken", false, primary.OriginalIdentity, 0)
	if err != nil {
		t.Fatal(err)
	}
	linked, err = store.PinCollectionEntryAt(ctx, linked.ID, linked.Revision, primary.ID, 1, "secondmarker", false, primary.OriginalIdentity, 0)
	if err != nil {
		t.Fatal(err)
	}
	linked, err = store.SaveCollection(ctx, linked.ID, linked.Revision, linked.Title, []sqlite.CollectionEdit{{Key: linked.Entries[0].Key, Note: "first note"}, {Key: linked.Entries[1].Key, Note: "second note"}})
	if err != nil {
		t.Fatal(err)
	}
	if operation == "bundle-before-publish" {
		databaseURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(dataDir, "findrail.db"))}).String()
		ddl, openErr := sql.Open("sqlite", databaseURL)
		if openErr != nil {
			t.Fatal(openErr)
		}
		_, err = ddl.ExecContext(ctx, `CREATE TRIGGER recovery_test_import_barrier AFTER INSERT ON evidence_collections BEGIN SELECT findrail_recovery_barrier(); END`)
		_ = ddl.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if operation == "forget-before-publish" {
		databaseURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(dataDir, "findrail.db"))}).String()
		ddl, openErr := sql.Open("sqlite", databaseURL)
		if openErr != nil {
			t.Fatal(openErr)
		}
		_, err = ddl.ExecContext(ctx, `CREATE TRIGGER recovery_test_forget_barrier AFTER DELETE ON sources BEGIN SELECT findrail_recovery_barrier(); END`)
		_ = ddl.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	seedSources, err := store.Sources(ctx)
	if err != nil || len(seedSources) != 1 {
		t.Fatalf("seed source registration: %+v %v", seedSources, err)
	}
	seedToken, seedRevision := seedSources[0].RegistrationToken, seedSources[0].RegistrationRevision
	beforeReportResult, err := store.SourceReport(ctx, sourceID, false)
	if err != nil {
		t.Fatal(err)
	}
	beforeReport := beforeReportResult["report"].(sqlite.ReportSummary)
	firstPage, err := store.SearchPage(ctx, search.PageRequest{Request: search.Request{Query: "pagecrashmarker", Limit: 1}})
	if err != nil || firstPage.NextCursor == nil {
		t.Fatalf("failed to seed process-local cursor: %+v %v", firstPage, err)
	}
	oldCursor := *firstPage.NextCursor
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	barrier := filepath.Join(root, "barrier")

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCrashHelper$")
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "FINDRAIL_RECOVERY_HELPER=1", "FINDRAIL_RECOVERY_DATA="+dataDir, "FINDRAIL_RECOVERY_BARRIER="+barrier, "FINDRAIL_RECOVERY_OPERATION="+operation)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		if _, err = os.Stat(barrier); err == nil {
			break
		}
		select {
		case err = <-done:
			t.Fatalf("helper exited before controlled boundary: %v", err)
		case <-ctx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			t.Fatalf("deadline waiting for controlled boundary")
		case <-time.After(10 * time.Millisecond):
		}
	}
	report.Phase = "upserted_uncommitted"
	if operation == "refresh-before-publish" || operation == "configure-before-publish" || operation == "forget-before-publish" || operation == "bundle-before-publish" {
		reader, e := sqlite.OpenReadOnly(ctx, dataDir)
		if e != nil {
			t.Fatalf("open concurrent read-only snapshot: %v", e)
		}
		old, e := reader.Search(ctx, search.Request{Query: "committedoldtoken", Limit: 10})
		if e != nil || old.Total != 2 {
			t.Fatalf("committed snapshot unavailable during writer transaction: %+v %v", old, e)
		}
		newText := "uncommittednewtoken"
		if operation == "configure-before-publish" {
			newText = "configurednewtoken"
		}
		provisional, e := reader.Search(ctx, search.Request{Query: newText, Limit: 10})
		_ = reader.Close()
		if e != nil || provisional.Total != 0 {
			t.Fatalf("uncommitted changes visible during writer transaction: %+v %v", provisional, e)
		}
	}
	if err = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("child process group did not terminate before deadline")
	}
	report.Phase = "reopened"

	store, err = sqlite.Open(ctx, dataDir)
	if err != nil {
		t.Fatalf("reopen after SIGKILL: %v", err)
	}
	defer store.Close()
	readonly, err := sqlite.OpenReadOnly(ctx, dataDir)
	if err != nil {
		t.Fatalf("read-only reopen: %v", err)
	}
	integrity := readonly.IntegrityCheck(ctx)
	_ = readonly.Close()
	if !integrity.QuickCheck.Complete || !integrity.QuickCheck.Healthy || !integrity.ForeignKey.Complete || !integrity.ForeignKey.Healthy {
		t.Fatalf("post-crash SQLite/FK integrity check: %+v", integrity)
	}
	queries := []struct {
		text string
		want int
	}{{"committedoldtoken", 2}, {"uncommittednewtoken", 0}, {"configurednewtoken", 0}}
	if operation == "configure-after-success" {
		queries = []struct {
			text string
			want int
		}{{"committedoldtoken", 0}, {"configurednewtoken", 1}}
	}
	if operation == "refresh-after-success-prune" {
		queries = []struct {
			text string
			want int
		}{{"committedoldtoken", 0}, {"replacementnewtoken", 1}}
	}
	if operation == "forget-after-success" {
		queries = []struct {
			text string
			want int
		}{{"committedoldtoken", 0}}
	}
	for _, query := range queries {
		got, e := store.Search(ctx, search.Request{Query: query.text, Limit: 10})
		if e != nil {
			t.Fatal(e)
		}
		if got.Total != query.want {
			t.Fatalf("query %q total=%d want=%d", query.text, got.Total, query.want)
		}
	}
	pageQuery := "pageidentitytoken"
	if operation == "refresh-after-success-prune" {
		pageQuery = "replacementnewtoken"
	}
	if operation == "configure-after-success" {
		pageQuery = "configurednewtoken"
	}
	pageResults, err := store.Search(ctx, search.Request{Query: pageQuery, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	wantPages := 2
	if operation == "forget-after-success" {
		wantPages = 0
	}
	if operation == "refresh-after-success-prune" || operation == "configure-after-success" {
		wantPages = 1
	}
	if pageResults.Total != wantPages {
		t.Fatalf("PDF page identity query total=%d want=%d", pageResults.Total, wantPages)
	}
	wantIDs := map[string]bool{"synthetic-document": true, "synthetic-second-document": true}
	if operation == "refresh-after-success-prune" {
		wantIDs = map[string]bool{"replacement-document": true}
	}
	if operation == "configure-after-success" {
		wantIDs = map[string]bool{"synthetic-document": true}
	}
	if operation == "forget-after-success" {
		wantIDs = map[string]bool{}
	}
	for _, result := range pageResults.Results {
		if result.Page != 1 || !strings.HasSuffix(result.URI, "#page=1") {
			t.Fatalf("page provenance lost after reopen: %+v", result)
		}
		if !wantIDs[result.ID] {
			t.Fatalf("unexpected document identity after reopen: %+v", result)
		}
		delete(wantIDs, result.ID)
	}
	if len(wantIDs) != 0 {
		t.Fatalf("expected document/page identities absent after reopen: %v", wantIDs)
	}
	if _, e := store.SearchPage(ctx, search.PageRequest{Request: search.Request{Query: "pagecrashmarker", Limit: 1}, Cursor: oldCursor}); !errors.Is(e, search.ErrCursorSessionChanged) {
		t.Fatalf("pre-crash cursor survived process restart: %v", e)
	}
	sources, err := store.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if operation == "forget-after-success" {
		if len(sources) != 0 {
			t.Fatalf("forgotten source survived: %+v", sources)
		}
	} else if len(sources) != 1 || sources[0].Documents != map[bool]int{true: 1, false: 2}[operation == "refresh-after-success-prune" || operation == "configure-after-success"] {
		t.Fatalf("reopened source state: %+v", sources)
	}
	if operation == "configure-before-publish" || operation == "configure-after-success" {
		want := int64(0)
		if operation == "configure-after-success" {
			want = 64 << 10
		}
		if len(sources) != 1 || sources[0].MaxDOCXBytes != want {
			t.Fatalf("configure policy after reopen: %+v", sources)
		}
		wantRevision := seedRevision
		if operation == "configure-after-success" {
			wantRevision++
		}
		if sources[0].RegistrationToken != seedToken || sources[0].RegistrationRevision != wantRevision {
			t.Fatalf("registration guard state after restart: %+v", sources[0])
		}
		if operation == "configure-after-success" {
			if _, e := store.BeginConfigure(ctx, sources[0].Source, seedToken, seedRevision, 0); !errors.Is(e, ingest.ErrSourceGone) {
				t.Fatalf("stale configure claim survived restart: %v", e)
			}
		}
	}
	if operation != "forget-after-success" {
		reportResult, e := store.SourceReport(ctx, sourceID, false)
		if e != nil {
			t.Fatal(e)
		}
		persisted, ok := reportResult["report"].(sqlite.ReportSummary)
		if !ok || !persisted.Available {
			t.Fatalf("missing committed report: %#v", reportResult)
		}
		if operation == "refresh-before-publish" || operation == "configure-before-publish" || operation == "forget-before-publish" {
			if persisted.ReportID != beforeReport.ReportID {
				t.Fatalf("uncommitted scan replaced report: before=%+v after=%+v", beforeReport, persisted)
			}
		}
		if operation == "configure-after-success" && persisted.Operation != "configure" {
			t.Fatalf("success report not committed: %+v", persisted)
		}
		if operation == "refresh-after-success-prune" {
			full, e := store.SourceReport(ctx, sourceID, true)
			if e != nil {
				t.Fatal(e)
			}
			committed := full["report"].(diagnostics.Report)
			if committed.Operation != "refresh" || committed.RemovedDocuments != 2 || committed.IndexedDocuments != 1 {
				t.Fatalf("prune report mismatch: %+v", committed)
			}
		}
	}
	collections, err := store.ListCollections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundBaseline, foundLinked, foundImport := false, false, false
	for _, collection := range collections {
		foundBaseline = foundBaseline || collection.ID == baseline.ID
		foundLinked = foundLinked || collection.ID == linked.ID
		foundImport = foundImport || collection.Title == "Synthetic imported collection"
	}
	if !foundBaseline || !foundLinked {
		t.Fatal("unrelated/source-linked collection records were not preserved")
	}
	for _, collection := range collections {
		if collection.ID == baseline.ID && collection.EntryCount != 0 {
			t.Fatalf("unrelated collection unexpectedly changed: %+v", collection)
		}
		if collection.ID == linked.ID {
			wantEntries := 2
			if operation == "forget-after-success" || operation == "refresh-after-success-prune" {
				wantEntries = 0
			}
			if collection.EntryCount != wantEntries {
				t.Fatalf("source-linked collection cascade state: %+v", collection)
			}
		}
	}
	if foundImport != (operation == "bundle-after-success") {
		t.Fatalf("bundle import presence=%t operation=%s", foundImport, operation)
	}
	if operation == "bundle-after-success" {
		for _, collection := range collections {
			if collection.Title != "Synthetic imported collection" {
				continue
			}
			loaded, e := store.LoadCollection(ctx, collection.ID)
			if e != nil {
				t.Fatal(e)
			}
			if loaded.Title != "Synthetic imported collection" || loaded.EntryCount != 2 || len(loaded.Entries) != 2 {
				t.Fatalf("incomplete imported collection: %+v", loaded)
			}
			wantText := []string{"committedoldtoken", "secondmarker"}
			wantNotes := []string{"first note", "second note"}
			wantOffsets := []int{strings.Index("alpha committedoldtoken omega secondmarker", wantText[0]), strings.Index("alpha committedoldtoken omega secondmarker", wantText[1])}
			for i, entry := range loaded.Entries {
				if entry.Excerpt != wantText[i] || entry.Note != wantNotes[i] || entry.Evidence.TextStartChar != wantOffsets[i] {
					t.Fatalf("imported entry order/note/offset mismatch: %+v", loaded.Entries)
				}
			}
		}
	}
	report.Status = "passed"
	report.Phase = "verified_reopened_state"
}

type caseReport struct {
	Case           string `json:"case"`
	Status         string `json:"status"`
	DurationMillis int64  `json:"duration_ms"`
	Phase          string `json:"phase"`
	BinarySHA      string `json:"binary_sha256"`
	Revision       string `json:"revision"`
	Schema         int    `json:"schema"`
	Platform       string `json:"platform"`
}

func writeReport(path string, report caseReport) error {
	if exe, err := os.Executable(); err == nil {
		if file, err := os.Open(exe); err == nil {
			h := sha256.New()
			_, copyErr := io.Copy(h, file)
			_ = file.Close()
			if copyErr == nil {
				report.BinarySHA = hex.EncodeToString(h.Sum(nil))
			}
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				report.Revision = setting.Value
			}
		}
	}
	report.Schema = sqlite.CurrentWriteSchema()
	report.Platform = runtime.GOOS + "/" + runtime.GOARCH
	b, err := json.Marshal(report)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".findrail-recovery-*.json")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if _, err = file.Write(b); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
