package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/originals"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/snapshot"
	"github.com/Lephiziel/findrail/internal/sourceapp"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	syncer "github.com/Lephiziel/findrail/internal/sync"
	transport "github.com/Lephiziel/findrail/internal/transport/http"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func TestLocalSearchAPIAndBrowserBoundary(t *testing.T) {
	s, err := sqlite.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.md"), []byte("webhook <script>alert(1)</script>"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := filesystem.New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Run(context.Background(), s, c); err != nil {
		t.Fatal(err)
	}
	handler := transport.Handler(s)
	found, err := s.Search(context.Background(), search.Request{Query: "webhook", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	id := found.Results[0].ID
	cases := []struct {
		path, host, origin string
		code               int
		contains           string
	}{
		{"/healthz", "127.0.0.1:7766", "", 200, `"status":"ok"`},
		{"/api/v1/search?q=webhook", "127.0.0.1:7766", "", 200, `"total":1`},
		{"/api/v1/search?q=webhook&limit=0", "127.0.0.1:7766", "", 400, "limit"},
		{"/api/v1/search?q=%22", "127.0.0.1:7766", "", 400, "query"},
		{"/api/v1/sources", "127.0.0.1:7766", "", 200, "filesystem"},
		{"/api/v1/documents/" + id, "127.0.0.1:7766", "", 200, "webhook"},
		{"/api/v1/documents/" + id + "?page=1", "127.0.0.1:7766", "", 400, "page"},
		{"/api/v1/documents/" + id + "?page=0", "127.0.0.1:7766", "", 400, "page"},
		{"/api/v1/documents/missing", "127.0.0.1:7766", "", 404, "not found"},
		{"/api/v1/sync", "127.0.0.1:7766", "", 200, "\"enabled\":false"},
		{"/assets/citation.mjs", "127.0.0.1:7766", "", 200, "export function formatCitation"},
		{"/assets/keyboard_navigation.mjs", "127.0.0.1:7766", "", 200, "export function installSearchShortcut"},
		{"/assets/search_ui.mjs", "127.0.0.1:7766", "", 200, "initializeSearchUI"},
		{"/assets/preview_ui.mjs", "127.0.0.1:7766", "", 200, "initializePreviewUI"},
		{"/assets/collections_ui.mjs", "127.0.0.1:7766", "", 200, "initializeCollectionsUI"},
		{"/assets/freshness.mjs", "127.0.0.1:7766", "", 200, "sourceFreshness"},
		{"/assets/keyboard_navigation.mjs", "attacker.example:7766", "", 403, "local host"},
		{"/assets/keyboard_navigation.mjs", "127.0.0.1:7766", "https://attacker.example", 403, "same origin"},
		{"/assets/citation.mjs", "127.0.0.1:7766", "http://127.0.0.1:7766", 200, "export function formatCitation"},
		{"/assets/citation.mjs", "attacker.example:7766", "", 403, "local host"},
		{"/assets/citation.mjs", "127.0.0.1:7766", "https://attacker.example", 403, "same origin"},
		{"/assets/unknown.mjs", "127.0.0.1:7766", "", 404, ""},
		{"/assets/citation_test.mjs", "127.0.0.1:7766", "", 404, ""},
		{"/assets/search_ui.mjs/extra", "127.0.0.1:7766", "", 404, ""},
		{"/assets/%2e%2e/server.go", "127.0.0.1:7766", "", 404, ""},
		{"/api/v1/documents/" + id, "attacker.example:7766", "", 403, "local host"},
		{"/", "127.0.0.1:7766", "", 200, "Findrail"},
		{"/api/v1/search?q=webhook", "attacker.example:7766", "", 403, "local host"},
		{"/api/v1/search?q=webhook", "127.0.0.1:7766", "https://attacker.example", 403, "same origin"},
		{"/api/v1/search?q=webhook", "127.0.0.1:7766", "http://127.0.0.1:7766", 200, `"total":1`},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766"+tc.path, nil)
		r.Host = tc.host
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.contains) {
			t.Errorf("%s host %s: %d %s", tc.path, tc.host, w.Code, w.Body.String())
		}
		if strings.HasPrefix(tc.path, "/api/") && strings.Contains(w.Body.String(), "<script>") {
			t.Error("unescaped HTML in JSON")
		}
		if strings.HasPrefix(tc.path, "/assets/") && tc.code == http.StatusOK {
			if got := w.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
				t.Errorf("embedded module content type = %q", got)
			}
			for _, header := range []string{"X-Content-Type-Options", "Cache-Control", "Content-Security-Policy"} {
				if w.Header().Get(header) == "" {
					t.Errorf("embedded module missing %s header", header)
				}
			}
		}
	}
}

func TestSyncStatusExposesOptionalSafeWarningCode(t *testing.T) {
	store, err := sqlite.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := transport.Handler(store, transport.WithSyncStatus(true, func() []syncer.Status {
		return []syncer.Status{{
			SourceID: "source", State: "idle", Mode: "polling",
			WatchWarning:     "File change notifications are unavailable; periodic refresh remains active.",
			WatchWarningCode: "watch_unavailable",
		}}
	}))
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766/api/v1/sync", nil)
	req.Host = "127.0.0.1:7766"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("sync status returned %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Enabled bool            `json:"enabled"`
		Sources []syncer.Status `json:"sources"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Enabled || len(payload.Sources) != 1 || payload.Sources[0].WatchWarningCode != "watch_unavailable" {
		t.Fatalf("optional warning code missing from API response: %+v", payload)
	}
	if strings.Contains(response.Body.String(), "/private/") || strings.Contains(response.Body.String(), "permission denied") {
		t.Fatalf("sync response leaked raw watcher diagnostics: %s", response.Body.String())
	}
}

func TestDiagnosticsEndpointReportsOnlyWhitelistedFactsAndCapability(t *testing.T) {
	store, err := sqlite.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	h := transport.Handler(store, transport.WithDiagnostics("synthetic-version"))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics", nil)
	r.Host = "127.0.0.1:7766"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("diagnostics status %d: %s", w.Code, w.Body.String())
	}
	var report map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report["report_version"] != float64(1) || report["index_schema"] != float64(sqlite.CurrentWriteSchema()) {
		t.Fatalf("report facts: %+v", report)
	}
	if len(w.Body.Bytes()) > 16<<10 {
		t.Fatalf("report exceeds bound: %d", len(w.Body.Bytes()))
	}
	for _, forbidden := range []string{"data_directory", "source_id", "document_id", "query", "nonce", "token", "argv", "logs", "root"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatalf("report disclosed %q: %s", forbidden, w.Body.String())
		}
	}
	capReq := httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil)
	capReq.Host = r.Host
	capW := httptest.NewRecorder()
	h.ServeHTTP(capW, capReq)
	if !strings.Contains(capW.Body.String(), `"diagnostics":true`) {
		t.Fatalf("missing capability: %s", capW.Body.String())
	}
	if !strings.Contains(capW.Body.String(), `"evidence_bundle":false`) {
		t.Fatalf("read-only bundle capability enabled: %s", capW.Body.String())
	}
	routeW := httptest.NewRecorder()
	routeReq := httptest.NewRequest(http.MethodGet, "/api/v1/collections/missing/bundle", nil)
	routeReq.Host = r.Host
	h.ServeHTTP(routeW, routeReq)
	if routeW.Code != http.StatusNotFound {
		t.Fatalf("read-only bundle route registered: %d", routeW.Code)
	}
	without := httptest.NewRecorder()
	transport.Handler(store).ServeHTTP(without, r)
	if without.Code != http.StatusNotFound {
		t.Fatalf("unavailable backend capability exposed endpoint: %d", without.Code)
	}
}

func TestSourceReportHTTPAvailabilityAndPathOptIn(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "report-private-name.png"), []byte("synthetic"), 0600)
	conn, err := filesystem.New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ingest.Run(ctx, store, conn)
	if err != nil {
		t.Fatal(err)
	}
	h := transport.Handler(store)
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Host = "127.0.0.1:7766"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	defaultResponse := request("/api/v1/sources/" + result.Source.ID + "/report")
	if defaultResponse.Code != 200 || strings.Contains(defaultResponse.Body.String(), "report-private-name.png") {
		t.Fatalf("default report response leaked path: %d %s", defaultResponse.Code, defaultResponse.Body.String())
	}
	details := request("/api/v1/sources/" + result.Source.ID + "/report?include_paths=true")
	if details.Code != 200 || !strings.Contains(details.Body.String(), "report-private-name.png") {
		t.Fatalf("details report response: %d %s", details.Code, details.Body.String())
	}
	if got := request("/api/v1/sources/" + result.Source.ID + "/report?include_paths=true&include_paths=false"); got.Code != 400 {
		t.Fatalf("duplicate include_paths status=%d", got.Code)
	}
	if got := request("/api/v1/sources/missing/report"); got.Code != 404 {
		t.Fatalf("missing source status=%d", got.Code)
	}
}

func TestManagementRequiresSameOriginCapabilityAndStrictJSON(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	store, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bundleRoot := t.TempDir()
	if err = os.WriteFile(filepath.Join(bundleRoot, "bundle.md"), []byte("exact bundle citation"), 0600); err != nil {
		t.Fatal(err)
	}
	bundleConn, err := filesystem.New(bundleRoot, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, store, bundleConn); err != nil {
		t.Fatal(err)
	}
	foundBundle, err := store.Search(ctx, search.Request{Query: "citation", Limit: 1})
	if err != nil || len(foundBundle.Results) != 1 {
		t.Fatalf("bundle fixture search: %+v %v", foundBundle, err)
	}
	savedBundle, err := store.CreateCollection(ctx, "HTTP bundle")
	if err != nil {
		t.Fatal(err)
	}
	savedBundle, err = store.PinCollectionEntry(ctx, savedBundle.ID, savedBundle.Revision, foundBundle.Results[0].ID, 0, "bundle citation", false, foundBundle.Results[0].OriginalIdentity)
	if err != nil {
		t.Fatal(err)
	}
	app := sourceapp.New(ctx, store, dir, "/unused")
	defer func() {
		c, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := app.Close(c); err != nil {
			t.Error(err)
		}
	}()
	ready := make(chan string, 1)
	served := make(chan error, 1)
	go func() {
		served <- transport.Serve(ctx, "127.0.0.1:0", store, transport.WithManagement(app), transport.WithReady(func(u string) error { ready <- u; return nil }))
	}()
	base := <-ready
	u, _ := url.Parse(base)
	origin := "http://" + u.Host
	defer func() {
		cancel()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		// Serve uses a five-second graceful-shutdown deadline before forcing close.
		case <-time.After(7 * time.Second):
			t.Error("server did not stop")
		}
	}()
	client := &http.Client{Timeout: 2 * time.Second}
	session, err := client.Get(base + "/api/v1/session")
	if err != nil {
		t.Fatal(err)
	}
	var capability struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(session.Body).Decode(&capability); err != nil {
		t.Fatal(err)
	}
	session.Body.Close()
	if len(capability.Token) < 32 {
		t.Fatal("missing process token")
	}
	if session.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("session was cacheable: %q", session.Header.Get("Cache-Control"))
	}
	capsResp, err := client.Get(base + "/api/v1/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	var caps map[string]any
	if err = json.NewDecoder(capsResp.Body).Decode(&caps); err != nil {
		t.Fatal(err)
	}
	capsResp.Body.Close()
	if caps["evidence_bundle"] != true {
		t.Fatalf("management bundle capability=%v", caps["evidence_bundle"])
	}
	if caps["portable_snapshots"] != true {
		t.Fatalf("management snapshot capability=%v", caps["portable_snapshots"])
	}
	// Bundle export is a management-gated read, and imports use the same exact
	// origin/host/session boundary as other mutations.
	bundleCall := func(method, path, host, originValue, token, site string, body []byte) (int, []byte, http.Header) {
		r, _ := http.NewRequest(method, base+path, bytes.NewReader(body))
		if host != "" {
			r.Host = host
		}
		if originValue != "" {
			r.Header.Set("Origin", originValue)
		}
		if token != "" {
			r.Header.Set("X-Findrail-Token", token)
		}
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		if method == http.MethodPost {
			r.Header.Set("Content-Type", "application/json")
		}
		resp, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b, resp.Header
	}
	bundleReq := func(method, path, host, originValue, token, site string, body []byte) int {
		status, _, _ := bundleCall(method, path, host, originValue, token, site, body)
		return status
	}
	archiveBytes, err := snapshot.Encode(snapshot.Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: snapshot.Origin{ID: "http-origin", Kind: "filesystem", Name: "Private source name", Location: "/private/path", IndexedAt: "2026-10-09T11:00:00Z"}}, []snapshot.Document{{ID: "http-doc", Path: "note.md", Title: "Note", URI: "file:///private/path/note.md", MediaType: "text/plain", Text: "private archive text", ContentHash: strings.Repeat("a", 64), SizeBytes: 20, ModifiedAt: "2026-10-09T10:00:00Z"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "private.zip")
	if err = os.WriteFile(archivePath, archiveBytes, 0600); err != nil {
		t.Fatal(err)
	}
	inspectBody, _ := json.Marshal(map[string]string{"path": archivePath})
	status, jobRaw, _ := bundleCall(http.MethodPost, "/api/v1/snapshots/inspect", "", origin, capability.Token, "same-origin", inspectBody)
	if status != 202 || bytes.Contains(jobRaw, []byte(archivePath)) || bytes.Contains(jobRaw, []byte("private archive text")) {
		t.Fatalf("inspect job status=%d body=%s", status, jobRaw)
	}
	var inspectResult struct {
		Job sourceapp.Job `json:"job"`
	}
	if err = json.Unmarshal(jobRaw, &inspectResult); err != nil || inspectResult.Job.ID == "" {
		t.Fatalf("inspect job: %+v %v", inspectResult, err)
	}
	if _, body, _ := bundleCall(http.MethodGet, "/api/v1/snapshots/inspections/"+inspectResult.Job.ID, "", origin, "", "same-origin", nil); !bytes.Contains(body, []byte("session token required")) {
		t.Fatalf("unauthenticated inspection result: %s", body)
	}
	var snapshotPreviewRaw []byte
	for i := 0; i < 100; i++ {
		_, snapshotPreviewRaw, _ = bundleCall(http.MethodGet, "/api/v1/snapshots/inspections/"+inspectResult.Job.ID, "", origin, capability.Token, "same-origin", nil)
		if bytes.Contains(snapshotPreviewRaw, []byte(`"status":"succeeded"`)) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if bytes.Contains(snapshotPreviewRaw, []byte("Private source name")) || bytes.Contains(snapshotPreviewRaw, []byte("/private/path")) || bytes.Contains(snapshotPreviewRaw, []byte("private archive text")) || !bytes.Contains(snapshotPreviewRaw, []byte(`"claim":`)) {
		t.Fatalf("unsafe or incomplete preview: %s", snapshotPreviewRaw)
	}
	var snapshotResult sourceapp.SnapshotSummary
	if err = json.Unmarshal(snapshotPreviewRaw, &snapshotResult); err != nil || snapshotResult.Claim == "" {
		t.Fatalf("snapshot inspection result: %+v %v", snapshotResult, err)
	}
	pollJob := func(id string) sourceapp.Job {
		t.Helper()
		for i := 0; i < 100; i++ {
			_, raw, _ := bundleCall(http.MethodGet, "/api/v1/jobs/"+id, "", origin, "", "same-origin", nil)
			var job sourceapp.Job
			if json.Unmarshal(raw, &job) == nil && (job.Status == "succeeded" || job.Status == "failed" || job.Status == "canceled") {
				return job
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("job %s did not finish", id)
		return sourceapp.Job{}
	}
	snapshotImportBody, _ := json.Marshal(map[string]string{"claim": snapshotResult.Claim, "name": "HTTP imported synthetic"})
	status, importRaw, _ := bundleCall(http.MethodPost, "/api/v1/snapshots/import", "", origin, capability.Token, "same-origin", snapshotImportBody)
	var importEnvelope struct {
		Job sourceapp.Job `json:"job"`
	}
	if status != http.StatusAccepted || json.Unmarshal(importRaw, &importEnvelope) != nil || importEnvelope.Job.ID == "" || bytes.Contains(importRaw, []byte(snapshotResult.Claim)) {
		t.Fatalf("snapshot import job status=%d body=%s", status, importRaw)
	}
	if got := pollJob(importEnvelope.Job.ID); got.Status != "succeeded" || !strings.HasPrefix(got.Result, "imported:") || !got.Progress.Committed {
		t.Fatalf("snapshot import job result: %+v", got)
	}
	outputPath := filepath.Join(t.TempDir(), "http-export.zip")
	snapshotExportBody, _ := json.Marshal(map[string]string{"output_path": outputPath})
	status, exportRaw, _ := bundleCall(http.MethodPost, "/api/v1/sources/"+foundBundle.Results[0].SourceID+"/snapshot-export", "", origin, capability.Token, "same-origin", snapshotExportBody)
	var exportEnvelope struct {
		Job sourceapp.Job `json:"job"`
	}
	if status != http.StatusAccepted || json.Unmarshal(exportRaw, &exportEnvelope) != nil || bytes.Contains(exportRaw, []byte(outputPath)) {
		t.Fatalf("snapshot export job status=%d body=%s", status, exportRaw)
	}
	if got := pollJob(exportEnvelope.Job.ID); got.Status != "succeeded" || got.Result != "exported" || !got.Progress.Committed {
		t.Fatalf("snapshot export job result: %+v", got)
	}
	if info, statErr := os.Stat(outputPath); statErr != nil || !info.Mode().IsRegular() {
		t.Fatalf("HTTP snapshot output missing or invalid: %v", statErr)
	}
	if got := bundleReq(http.MethodPost, "/api/v1/snapshots/inspect", "", origin, capability.Token, "same-origin", []byte(`{"path":"x","path":"y"}`)); got != 400 {
		t.Fatalf("duplicate path request status=%d", got)
	}
	if got := bundleReq(http.MethodGet, "/api/v1/collections/missing/bundle", "", origin, "", "", nil); got != 403 {
		t.Fatalf("tokenless bundle export status=%d", got)
	}
	if got := bundleReq(http.MethodGet, "/api/v1/collections/missing/bundle", "attacker.example:7766", origin, capability.Token, "same-origin", nil); got != 403 {
		t.Fatalf("bad-host bundle export status=%d", got)
	}
	if got := bundleReq(http.MethodGet, "/api/v1/collections/missing/bundle", "", origin, capability.Token, "cross-site", nil); got != 403 {
		t.Fatalf("cross-site bundle export status=%d", got)
	}
	if got := bundleReq(http.MethodPost, "/api/v1/collections/import/preview", "", origin, capability.Token, "same-origin", []byte(`{"bundle":{},"mappings":{},"mappings":{}}`)); got != 400 {
		t.Fatalf("duplicate wrapper key status=%d", got)
	}
	for _, bad := range [][]byte{[]byte(`{"bundle":{},"mappings":{},"unexpected":true}`), []byte(`{"bundle":{"format":"findrail-evidence-bundle","format":"findrail-evidence-bundle"},"mappings":{}}`), {0xff, 0xfe}} {
		if got := bundleReq(http.MethodPost, "/api/v1/collections/import/preview", "", origin, capability.Token, "same-origin", bad); got != 400 {
			t.Errorf("invalid strict wrapper status=%d for %q", got, bad)
		}
	}
	if got := bundleReq(http.MethodPost, "/api/v1/collections/import/preview", "", origin, capability.Token, "same-origin", bytes.Repeat([]byte("x"), 5<<20+1)); got != 413 {
		t.Fatalf("oversized bundle wrapper status=%d", got)
	}
	status, bundleRaw, headers := bundleCall(http.MethodGet, "/api/v1/collections/"+savedBundle.ID+"/bundle", "", origin, capability.Token, "same-origin", nil)
	if status != 200 || headers.Get("Content-Disposition") != "attachment; filename=\"findrail-evidence-bundle.json\"" {
		t.Fatalf("bundle export status=%d header=%q body=%s", status, headers.Get("Content-Disposition"), bundleRaw)
	}
	previewBody, _ := json.Marshal(struct {
		Bundle   json.RawMessage   `json:"bundle"`
		Mappings map[string]string `json:"mappings"`
	}{bundleRaw, map[string]string{"s1": foundBundle.Results[0].SourceID}})
	status, previewRaw, _ := bundleCall(http.MethodPost, "/api/v1/collections/import/preview", "", origin, capability.Token, "same-origin", previewBody)
	if status != 200 {
		t.Fatalf("bundle preview status=%d body=%s", status, previewRaw)
	}
	var preview struct {
		Claim   string                     `json:"claim"`
		Entries []sqlite.BundleEntryStatus `json:"entries"`
	}
	if err = json.Unmarshal(previewRaw, &preview); err != nil || preview.Claim == "" || len(preview.Entries) != 1 || preview.Entries[0].Status != "match" {
		t.Fatalf("preview=%+v err=%v raw=%s", preview, err, previewRaw)
	}
	importBody, _ := json.Marshal(struct {
		Bundle json.RawMessage `json:"bundle"`
		Claim  string          `json:"claim"`
		Title  string          `json:"title"`
	}{bundleRaw, preview.Claim, "HTTP imported"})
	tampered := append([]byte(nil), importBody...)
	var tamperedRequest struct {
		Bundle json.RawMessage `json:"bundle"`
		Claim  string          `json:"claim"`
		Title  string          `json:"title"`
	}
	if err = json.Unmarshal(tampered, &tamperedRequest); err != nil {
		t.Fatal(err)
	}
	tamperedRequest.Claim = preview.Claim + "x"
	tampered, _ = json.Marshal(tamperedRequest)
	if status, body, _ := bundleCall(http.MethodPost, "/api/v1/collections/import", "", origin, capability.Token, "same-origin", tampered); status != 409 {
		t.Fatalf("tampered preview claim status=%d body=%s", status, body)
	}
	crossBundle := bytes.Replace(bundleRaw, []byte("HTTP bundle"), []byte("Other bundle"), 1)
	crossBody, _ := json.Marshal(struct {
		Bundle json.RawMessage `json:"bundle"`
		Claim  string          `json:"claim"`
		Title  string          `json:"title"`
	}{crossBundle, preview.Claim, "Cross bundle"})
	if status, body, _ := bundleCall(http.MethodPost, "/api/v1/collections/import", "", origin, capability.Token, "same-origin", crossBody); status != 409 {
		t.Fatalf("cross-bundle claim status=%d body=%s", status, body)
	}
	status, importedRaw, _ := bundleCall(http.MethodPost, "/api/v1/collections/import", "", origin, capability.Token, "same-origin", importBody)
	if status != 201 {
		t.Fatalf("bundle import status=%d body=%s", status, importedRaw)
	}
	var imported sqlite.Collection
	if err = json.Unmarshal(importedRaw, &imported); err != nil || len(imported.Entries) != 1 || imported.Entries[0].Excerpt != "bundle citation" {
		t.Fatalf("imported=%+v err=%v", imported, err)
	}
	root := filepath.Join(t.TempDir(), "must-not-open")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(struct {
		Type string `json:"type"`
		Path string `json:"path"`
	}{Type: "folder", Path: root})
	if err != nil {
		t.Fatal(err)
	}
	request := func(originValue, token, site, content string, payload []byte) int {
		r, _ := http.NewRequest("POST", base+"/api/v1/sources", bytes.NewReader(payload))
		if originValue != "<absent>" {
			r.Header.Set("Origin", originValue)
		}
		if token != "" {
			r.Header.Set("X-Findrail-Token", token)
		}
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		if content != "" {
			r.Header.Set("Content-Type", content)
		}
		resp, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	jobsBeforeRejectedRequests := len(app.Jobs())
	for _, tc := range []struct {
		name, origin, token, site, content string
		body                               []byte
		want                               int
	}{
		{"foreign origin", "http://evil.invalid", capability.Token, "same-origin", "application/json", body, 403},
		{"wrong port origin", origin + "9", capability.Token, "same-origin", "application/json", body, 403},
		{"null origin", "null", capability.Token, "same-origin", "application/json", body, 403},
		{"missing origin", "<absent>", capability.Token, "same-origin", "application/json", body, 403},
		{"bad token", origin, "bad", "same-origin", "application/json", body, 403},
		{"cross site", origin, capability.Token, "cross-site", "application/json", body, 403},
		{"text plain", origin, capability.Token, "same-origin", "text/plain", body, 400},
		{"unknown field", origin, capability.Token, "same-origin", "application/json", []byte(`{"type":"folder","path":"x","unknown":true}`), 400},
		{"trailing json", origin, capability.Token, "same-origin", "application/json", append(append([]byte{}, body...), []byte(` {}`)...), 400},
		{"oversized", origin, capability.Token, "same-origin", "application/json", bytes.Repeat([]byte("x"), 17<<10), 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := request(tc.origin, tc.token, tc.site, tc.content, tc.body); got != tc.want {
				t.Fatalf("status %d, want %d", got, tc.want)
			}
		})
	}
	if len(app.Jobs()) != jobsBeforeRejectedRequests {
		t.Fatalf("rejected request scheduled work: %+v", app.Jobs())
	}
	badHost, _ := http.NewRequest("POST", base+"/api/v1/sources", bytes.NewReader(body))
	badHost.Host = "localhost:1"
	badHost.Header.Set("Origin", origin)
	badHost.Header.Set("Content-Type", "application/json")
	badHost.Header.Set("X-Findrail-Token", capability.Token)
	badHost.Header.Set("Sec-Fetch-Site", "same-origin")
	hostResponse, err := client.Do(badHost)
	if err != nil {
		t.Fatal(err)
	}
	hostResponse.Body.Close()
	if hostResponse.StatusCode != 403 {
		t.Fatalf("spoofed Host status %d", hostResponse.StatusCode)
	}
	if len(app.Jobs()) != jobsBeforeRejectedRequests {
		t.Fatal("spoofed Host scheduled work")
	}
	postCtx, cancelPost := context.WithCancel(context.Background())
	validRequest, _ := http.NewRequestWithContext(postCtx, http.MethodPost, base+"/api/v1/sources", bytes.NewReader(body))
	validRequest.Header.Set("Origin", origin)
	validRequest.Header.Set("Content-Type", "application/json")
	validRequest.Header.Set("X-Findrail-Token", capability.Token)
	validRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	validResponse, err := client.Do(validRequest)
	if err != nil {
		cancelPost()
		t.Fatal(err)
	}
	var accepted struct {
		Job sourceapp.Job `json:"job"`
	}
	if err := json.NewDecoder(validResponse.Body).Decode(&accepted); err != nil {
		validResponse.Body.Close()
		cancelPost()
		t.Fatal(err)
	}
	validResponse.Body.Close()
	cancelPost()
	if validResponse.StatusCode != 202 {
		t.Fatalf("valid request status %d", validResponse.StatusCode)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if job, ok := app.Job(accepted.Job.ID); ok && job.Status == "succeeded" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("accepted folder job did not finish: %+v", app.Jobs())
}

func TestNativeOriginalRequiresTokenAndResolvesRegisteredDocument(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	root := t.TempDir()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "notes.txt")
	if err = os.WriteFile(file, []byte("safe original content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "manual.pdf"), []byte("synthetic pdf input"), 0600); err != nil {
		t.Fatal(err)
	}
	pdfExtractor := func(context.Context, io.Reader, int64) ([]connector.Page, error) {
		return []connector.Page{{Number: 1, Text: "evidence on page one"}}, nil
	}
	conn, err := filesystem.NewWithOptions(root, filesystem.Options{MaxTextBytes: 1024, MaxPDFBytes: 1024, ExtractPDF: pdfExtractor})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, store, conn); err != nil {
		t.Fatal(err)
	}
	found, err := store.Search(ctx, search.Request{Query: "original", Limit: 5})
	if err != nil || len(found.Results) != 1 {
		t.Fatalf("search: %+v %v", found, err)
	}
	var launches int
	var launched string
	app := sourceapp.New(ctx, store, t.TempDir(), "/unused")
	defer func() {
		c, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if e := app.Close(c); e != nil {
			t.Error(e)
		}
	}()
	ready := make(chan string, 1)
	served := make(chan error, 1)
	go func() {
		served <- transport.Serve(ctx, "127.0.0.1:0", store, transport.WithManagement(app), transport.WithOriginals(store, func(_ originals.Action, path string) error { launches++; launched = path; return nil }), transport.WithReady(func(u string) error { ready <- u; return nil }))
	}()
	base := <-ready
	defer func() {
		cancel()
		select {
		case e := <-served:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(7 * time.Second):
			t.Error("server did not stop")
		}
	}()
	u, _ := url.Parse(base)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(base + "/api/v1/session")
	if err != nil {
		t.Fatal(err)
	}
	var session struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&session)
	resp.Body.Close()
	if resp.StatusCode != 200 || session.Token == "" {
		t.Fatalf("session status %d", resp.StatusCode)
	}
	passageURL := base + "/api/v1/documents/" + found.Results[0].ID + "/passage?q=original&mode=literal&original_identity=" + url.QueryEscape(found.Results[0].OriginalIdentity)
	passageResponse, err := client.Get(passageURL)
	if err != nil {
		t.Fatal(err)
	}
	var passage search.Evidence
	if err = json.NewDecoder(passageResponse.Body).Decode(&passage); err != nil {
		t.Fatal(err)
	}
	passageResponse.Body.Close()
	if passageResponse.StatusCode != 200 || passage.MatchKind != "literal_body" || !strings.Contains(passage.Text, "original") {
		t.Fatalf("passage status=%d evidence=%+v", passageResponse.StatusCode, passage)
	}
	badResponse, err := client.Get(base + "/api/v1/documents/" + found.Results[0].ID + "/passage?start_char=0&start_char=1&original_identity=" + url.QueryEscape(found.Results[0].OriginalIdentity))
	if err != nil {
		t.Fatal(err)
	}
	badResponse.Body.Close()
	if badResponse.StatusCode != 400 {
		t.Fatalf("duplicate offset status=%d", badResponse.StatusCode)
	}
	for _, path := range []string{"/api/v1/search?q=original", "/api/v1/documents/" + found.Results[0].ID} {
		get, e := client.Get(base + path)
		if e != nil {
			t.Fatal(e)
		}
		get.Body.Close()
		if launches != 0 {
			t.Fatalf("GET %s invoked native launcher", path)
		}
	}
	body, _ := json.Marshal(map[string]any{"document_id": found.Results[0].ID, "original_identity": found.Results[0].OriginalIdentity, "action": "file"})
	post := func(origin, token string, payload []byte) int {
		r, _ := http.NewRequest("POST", base+"/api/v1/originals/open", bytes.NewReader(payload))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		if token != "" {
			r.Header.Add("X-Findrail-Token", token)
		}
		q, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer q.Body.Close()
		return q.StatusCode
	}
	if got := post("http://"+u.Host, "", body); got != 403 || launches != 0 {
		t.Fatalf("missing token status=%d launches=%d", got, launches)
	}
	if got := post("http://evil.invalid", session.Token, body); got != 403 || launches != 0 {
		t.Fatalf("cross-origin status=%d launches=%d", got, launches)
	}
	duplicate, _ := http.NewRequest("POST", base+"/api/v1/originals/open", bytes.NewReader(body))
	duplicate.Header.Set("Origin", "http://"+u.Host)
	duplicate.Header.Set("Content-Type", "application/json")
	duplicate.Header.Set("Sec-Fetch-Site", "same-origin")
	duplicate.Header.Add("X-Findrail-Token", session.Token)
	duplicate.Header.Add("X-Findrail-Token", session.Token)
	dupResp, err := client.Do(duplicate)
	if err != nil {
		t.Fatal(err)
	}
	dupResp.Body.Close()
	if dupResp.StatusCode != 403 || launches != 0 {
		t.Fatalf("duplicate token status=%d launches=%d", dupResp.StatusCode, launches)
	}
	spoof, _ := http.NewRequest("POST", base+"/api/v1/originals/open", bytes.NewReader(body))
	spoof.Host = "localhost:1"
	spoof.Header.Set("Origin", "http://"+u.Host)
	spoof.Header.Set("Content-Type", "application/json")
	spoof.Header.Set("Sec-Fetch-Site", "same-origin")
	spoof.Header.Set("X-Findrail-Token", session.Token)
	spoofResp, err := client.Do(spoof)
	if err != nil {
		t.Fatal(err)
	}
	spoofResp.Body.Close()
	if spoofResp.StatusCode != 403 || launches != 0 {
		t.Fatalf("spoofed Host status=%d launches=%d", spoofResp.StatusCode, launches)
	}
	if got := post("http://"+u.Host, session.Token, []byte(`{"document_id":"fake","original_identity":"fake","action":"file"}`)); got != 404 || launches != 0 {
		t.Fatalf("fake document status=%d launches=%d", got, launches)
	}
	if got := post("http://"+u.Host, session.Token, body); got != 202 || launches != 1 || launched != file {
		t.Fatalf("status=%d launches=%d path=%q", got, launches, launched)
	}
	pageBody, _ := json.Marshal(map[string]any{"document_id": found.Results[0].ID, "original_identity": found.Results[0].OriginalIdentity, "action": "file", "page": 1})
	if got := post("http://"+u.Host, session.Token, pageBody); got != 400 || launches != 1 {
		t.Fatalf("text page status=%d launches=%d", got, launches)
	}
	pdfSearch, err := store.Search(ctx, search.Request{Query: "evidence", Limit: 5})
	if err != nil || len(pdfSearch.Results) != 1 || pdfSearch.Results[0].Page != 1 {
		t.Fatalf("PDF search: %+v %v", pdfSearch, err)
	}
	pdfBody, _ := json.Marshal(map[string]any{"document_id": pdfSearch.Results[0].ID, "original_identity": pdfSearch.Results[0].OriginalIdentity, "action": "file", "page": 1})
	if got := post("http://"+u.Host, session.Token, pdfBody); got != 202 || launches != 2 || launched != filepath.Join(root, "manual.pdf") {
		t.Fatalf("PDF page action status=%d launches=%d path=%q", got, launches, launched)
	}
	badPage, _ := json.Marshal(map[string]any{"document_id": pdfSearch.Results[0].ID, "original_identity": pdfSearch.Results[0].OriginalIdentity, "action": "file", "page": 2})
	if got := post("http://"+u.Host, session.Token, badPage); got != 400 || launches != 2 {
		t.Fatalf("invalid PDF page status=%d launches=%d", got, launches)
	}
	titleOnly, err := store.Search(ctx, search.Request{Query: "manual", Limit: 5})
	if err != nil || len(titleOnly.Results) != 1 || titleOnly.Results[0].Page != 0 {
		t.Fatalf("title-only PDF result: %+v %v", titleOnly, err)
	}
	titleBody, _ := json.Marshal(map[string]any{"document_id": titleOnly.Results[0].ID, "original_identity": titleOnly.Results[0].OriginalIdentity, "action": "file"})
	if got := post("http://"+u.Host, session.Token, titleBody); got != 202 || launches != 3 || launched != filepath.Join(root, "manual.pdf") {
		t.Fatalf("title-only PDF open status=%d launches=%d path=%q", got, launches, launched)
	}
	folderBody, _ := json.Marshal(map[string]any{"document_id": found.Results[0].ID, "original_identity": found.Results[0].OriginalIdentity, "action": "folder"})
	if got := post("http://"+u.Host, session.Token, folderBody); got != 202 || launches != 4 || launched != root {
		t.Fatalf("folder status=%d launches=%d path=%q", got, launches, launched)
	}
	conn, err = filesystem.NewWithOptions(root, filesystem.Options{MaxTextBytes: 1024, MaxPDFBytes: 1024, ExtractPDF: pdfExtractor})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, store, conn); err != nil {
		t.Fatal(err)
	}
	if got := post("http://"+u.Host, session.Token, body); got != 409 || launches != 4 {
		t.Fatalf("stale registration status=%d launches=%d", got, launches)
	}
	capResp, err := client.Get(base + "/api/v1/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer capResp.Body.Close()
	var caps map[string]any
	_ = json.NewDecoder(capResp.Body).Decode(&caps)
	if caps["open_originals"] != true {
		t.Fatalf("capability missing: %#v", caps)
	}
}

func TestReadOnlyHandlerDoesNotExposeMutationController(t *testing.T) {
	s, err := sqlite.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	handler := transport.Handler(s)
	capabilityRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766/api/v1/capabilities", nil)
	capabilityResponse := httptest.NewRecorder()
	handler.ServeHTTP(capabilityResponse, capabilityRequest)
	if capabilityResponse.Code != 200 || !strings.Contains(capabilityResponse.Body.String(), `"open_originals":false`) || !strings.Contains(capabilityResponse.Body.String(), `"portable_snapshots":false`) {
		t.Fatalf("read-only opener capability: %d %s", capabilityResponse.Code, capabilityResponse.Body.String())
	}
	openRequest := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7766/api/v1/originals/open", strings.NewReader(`{}`))
	openRequest.Header.Set("Origin", "http://127.0.0.1:7766")
	openRequest.Header.Set("Content-Type", "application/json")
	openResponse := httptest.NewRecorder()
	handler.ServeHTTP(openResponse, openRequest)
	if openResponse.Code != http.StatusNotFound {
		t.Fatalf("read-only opener route status %d", openResponse.Code)
	}
	for _, path := range []string{"/api/v1/sources", "/api/v1/jobs/anything/cancel", "/api/v1/snapshots/inspect", "/api/v1/snapshots/import", "/api/v1/sources/example/snapshot-export"} {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7766"+path, strings.NewReader(`{"type":"folder"}`))
		r.Header.Set("Origin", "http://127.0.0.1:7766")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code == http.StatusAccepted {
			t.Fatalf("read-only handler accepted %s", path)
		}
	}
}

func TestServerRejectsNetworkExposure(t *testing.T) {
	if err := transport.Serve(context.Background(), "0.0.0.0:7766", nil); err == nil {
		t.Fatal("expected loopback restriction")
	}
}

func TestAddressValidation(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:7766", "localhost:0", "[::1]:7766"} {
		if err := transport.ValidateAddress(addr); err != nil {
			t.Errorf("rejected %q: %v", addr, err)
		}
	}
	for _, addr := range []string{"0.0.0.0:7766", "[::]:7766", "example.com:7766", "127.0.0.1", "127.0.0.1:-1", "127.0.0.1:65536", "localhost:http"} {
		if err := transport.ValidateAddress(addr); err == nil {
			t.Errorf("accepted %q", addr)
		}
	}
}

func TestReadyFailureReleasesBoundListener(t *testing.T) {
	sentinel := errors.New("cannot print URL")
	var address string
	err := transport.Serve(context.Background(), "127.0.0.1:0", nil, transport.WithReady(func(base string) error {
		u, err := url.Parse(base)
		if err != nil {
			t.Fatal(err)
		}
		address = u.Host
		return sentinel
	}))
	if !errors.Is(err, sentinel) || address == "" || strings.HasSuffix(address, ":0") {
		t.Fatalf("bad ready failure: address=%s, err=%v", address, err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listener leaked after callback failure: %v", err)
	}
	listener.Close()
}

type drainingBackend struct {
	transport.Backend
	started, release chan struct{}
}

func (b drainingBackend) Search(ctx context.Context, request search.Request) (search.Response, error) {
	close(b.started)
	select {
	case <-b.release:
		return search.Response{Query: request.Query, Results: []search.Result{}}, nil
	case <-ctx.Done():
		return search.Response{}, ctx.Err()
	}
}

func TestShutdownDrainsActiveSearchBeforeReturning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := drainingBackend{started: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(backend.release) })
	defer release()
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- transport.Serve(ctx, "127.0.0.1:0", backend, transport.WithReady(func(base string) error {
			ready <- base
			return nil
		}))
	}()
	var base string
	select {
	case base = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not become ready")
	}
	requestDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		r, err := client.Get(base + "/api/v1/search?q=drain")
		if err == nil {
			r.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-backend.started:
	case <-time.After(5 * time.Second):
		t.Fatal("search did not start")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("server returned before active search finished: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	release()
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("active search did not complete")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not finish shutdown")
	}
}

func TestCitationAssetDoesNotNeedBackend(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766/assets/citation.mjs", nil)
	req.Host = "127.0.0.1:7766"
	recorder := httptest.NewRecorder()
	transport.Handler(nil).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "export function formatCitation") {
		t.Fatalf("citation asset response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestCapabilitiesAdvertiseReadOnlyClientContract(t *testing.T) {
	s, err := sqlite.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766/api/v1/capabilities", nil)
	r.Host = "127.0.0.1:7766"
	w := httptest.NewRecorder()
	transport.Handler(s).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["client_api_version"] != float64(1) || got["pdf_title_only_evidence"] != true {
		t.Fatalf("capabilities=%v", got)
	}
	if got["management"] != false {
		t.Fatalf("management=%v", got["management"])
	}
	if got["collection_persistence"] != false {
		t.Fatalf("collection persistence=%v", got["collection_persistence"])
	}
	collection := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766/api/v1/collections", nil)
	request.Host = "127.0.0.1:7766"
	transport.Handler(s).ServeHTTP(collection, request)
	if collection.Code != http.StatusNotFound {
		t.Fatalf("read-only collection route status=%d", collection.Code)
	}
	for key, want := range map[string][]string{"search_modes": {"literal", "advanced"}, "search_filters": {"source", "format", "path_prefix", "title_contains"}} {
		values, ok := got[key].([]any)
		if !ok || len(values) != len(want) {
			t.Fatalf("%s=%v", key, got[key])
		}
		for i := range want {
			if values[i] != want[i] {
				t.Fatalf("%s=%v", key, values)
			}
		}
	}
}

func TestSearchPagesHTTPWalkAndOptionalCapability(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	if err := os.Mkdir(docs, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 237; i++ {
		name := filepath.Join(docs, fmt.Sprintf("doc-%03d.md", i))
		if err := os.WriteFile(name, []byte("sharedpage marker unicode-λ <&>"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	conn, err := filesystem.New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := ingest.Run(ctx, store, conn)
	if err != nil {
		t.Fatal(err)
	}
	handler := transport.Handler(store)
	request := func(target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766"+target, nil)
		r.Host = "127.0.0.1:7766"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, limit := range []int{1, 20, 100} {
		t.Run(fmt.Sprintf("limit-%d", limit), func(t *testing.T) {
			seen := map[string]bool{}
			cursor := ""
			for {
				target := "/api/v1/search/pages?q=sharedpage&mode=literal&source=" + url.QueryEscape(indexed.Source.ID) + "&format=text&path_prefix=docs&title_contains=doc&limit=" + fmt.Sprint(limit)
				if cursor != "" {
					target += "&cursor=" + url.QueryEscape(cursor)
				}
				w := request(target)
				if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || len(w.Body.Bytes()) > 524288 || !strings.HasSuffix(w.Body.String(), "\n") {
					t.Fatalf("page response status=%d bytes=%d headers=%v body=%s", w.Code, w.Body.Len(), w.Header(), w.Body.String())
				}
				var page search.PageResponse
				if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				if page.Total != 237 || page.Limit != limit || page.Offset != len(seen) || page.WindowLimit != 1000 || page.Returned != len(page.Results) {
					t.Fatalf("page envelope: %+v", page)
				}
				for _, result := range page.Results {
					if seen[result.ID] {
						t.Fatalf("duplicate result %s", result.ID)
					}
					seen[result.ID] = true
					if result.OriginalIdentity == "" || result.SnippetTruncated != false {
						t.Fatalf("result metadata: %+v", result)
					}
				}
				if page.NextCursor == nil {
					if page.EndReason == nil || *page.EndReason != "complete" {
						t.Fatalf("unexpected terminal page: %+v", page)
					}
					break
				}
				cursor = *page.NextCursor
			}
			if len(seen) != 237 {
				t.Fatalf("walk returned %d results", len(seen))
			}
		})
	}
	for _, tc := range []struct{ target, code string }{
		{"/api/v1/search/pages?q=sharedpage&limit=1&limit=2", "invalid_search_page_request"},
		{"/api/v1/search/pages?q=sharedpage&offset=1", "invalid_search_page_request"},
		{"/api/v1/search/pages?q=sharedpage&cursor=" + strings.Repeat("x", 5000), "cursor_invalid"},
	} {
		w := request(tc.target)
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != http.StatusBadRequest || body["code"] != tc.code || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: status=%d body=%v", tc.target, w.Code, body)
		}
	}
	cap := request("/api/v1/capabilities")
	var caps map[string]any
	_ = json.Unmarshal(cap.Body.Bytes(), &caps)
	if caps["search_pagination"] != true || caps["client_api_version"] != float64(1) {
		t.Fatalf("capabilities: %v", caps)
	}
	withoutPages := transport.Handler(pageUnavailableBackend{Backend: store})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766/api/v1/search/pages?q=x", nil)
	withoutPages.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"code":"search_page_unavailable"`) || strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("optional route response: %d %s", w.Code, w.Body.String())
	}
	capW := httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766/api/v1/capabilities", nil)
	withoutPages.ServeHTTP(capW, r)
	if !strings.Contains(capW.Body.String(), `"search_pagination":false`) {
		t.Fatalf("missing false capability: %s", capW.Body.String())
	}
}

type pageUnavailableBackend struct{ transport.Backend }

type pageErrorBackend struct {
	transport.Backend
	err error
}

func (b pageErrorBackend) SearchPage(context.Context, search.PageRequest) (search.PageResponse, error) {
	return search.PageResponse{}, b.err
}

func TestSearchPageHTTPErrorMapping(t *testing.T) {
	store, err := sqlite.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{search.ErrCursorInvalid, 400, "cursor_invalid"},
		{search.ErrCursorMismatch, 400, "cursor_mismatch"},
		{search.ErrCursorExpired, 409, "cursor_expired"},
		{search.ErrCursorStale, 409, "cursor_stale"},
		{search.ErrCursorSessionChanged, 409, "cursor_session_changed"},
		{search.ErrPageBusy, 429, "search_page_busy"},
		{search.ErrPageClosed, 503, "search_page_unavailable"},
		{search.ErrPageUnavailable, 503, "search_page_unavailable"},
		{context.DeadlineExceeded, 504, "search_page_timeout"},
		{search.ErrPageResultTooLarge, 500, "page_result_too_large"},
		{errors.New("private SQL diagnostic"), 500, "search_page_unavailable"},
	}
	for _, tc := range cases {
		handler := transport.Handler(pageErrorBackend{Backend: store, err: tc.err})
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766/api/v1/search/pages?q=marker", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != tc.status || body["code"] != tc.code || body["error"] == "" || strings.Contains(w.Body.String(), "private SQL") || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%v mapped to status=%d body=%s", tc.err, w.Code, w.Body.String())
		}
	}
}
