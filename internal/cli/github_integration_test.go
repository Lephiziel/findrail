package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	web "github.com/Lephiziel/findrail/internal/transport/http"
	transport "github.com/Lephiziel/findrail/internal/transport/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Only the compiled test helper accepts an injected fixture origin. The release
// executable has neither this environment hook nor an alternate-origin flag.
func TestGitHubCLIProcess(t *testing.T) {
	if os.Getenv("FINDRAIL_GITHUB_TEST_PROCESS") != "1" {
		return
	}
	fixture, err := url.Parse(os.Getenv("FINDRAIL_GITHUB_TEST_ORIGIN"))
	if err != nil || fixture.Host == "" {
		os.Exit(2)
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	http.DefaultTransport = githubTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.github.com" || r.URL.Scheme != "https" {
			return nil, errors.New("unexpected outbound request in GitHub test process")
		}
		request := r.Clone(r.Context())
		request.URL.Scheme, request.URL.Host = fixture.Scheme, fixture.Host
		return base.RoundTrip(request)
	})
	var args []string
	if json.Unmarshal([]byte(os.Getenv("FINDRAIL_GITHUB_TEST_ARGS")), &args) != nil {
		os.Exit(2)
	}
	if err := Run(context.Background(), args, os.Stdout, os.Stderr, "github-integration-test"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func githubIntegrationArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "synthetic GitHub archive"}}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "repo-root/docs/", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: "repo-root/" + name, Mode: 0644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

// This journey executes real child processes for CLI ingestion, updates,
// retrieval and deletion, using real HTTP responses and a real SQLite index.
// HTTP and SDK MCP clients subsequently read the same index with GitHub offline.
func TestGitHubCLIIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	shaA, shaB := strings.Repeat("a", 40), strings.Repeat("b", 40)
	archiveA := githubIntegrationArchive(t, map[string]string{
		"docs/readme.md": "needle alpha", "docs/keep #?%.md": "needle unchanged", "docs/removed.md": "obsolete needle",
	})
	archiveB := githubIntegrationArchive(t, map[string]string{
		"docs/readme.md": "needle beta", "docs/keep #?%.md": "needle unchanged",
	})
	var mu sync.Mutex
	sha, archive, offline := shaA, archiveA, false
	var requests atomic.Int64
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		mu.Lock()
		defer mu.Unlock()
		if offline {
			http.Error(w, "fixture offline", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/repos/Example/Demo":
			io.WriteString(w, `{"id":42,"name":"Demo","full_name":"Example/Demo","default_branch":"main","private":false,"owner":{"login":"Example"}}`)
		case "/repos/Example/Demo/commits/main":
			fmt.Fprintf(w, `{"sha":%q,"commit":{"committer":{"date":"2026-01-02T03:04:05Z"}}}`, sha)
		case "/repos/Example/Demo/tarball/" + sha:
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer fixture.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := func(args ...string) ([]byte, error) {
		t.Helper()
		encoded, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestGitHubCLIProcess$")
		cmd.Env = append(os.Environ(), "FINDRAIL_GITHUB_TEST_PROCESS=1", "FINDRAIL_GITHUB_TEST_ORIGIN="+fixture.URL, "FINDRAIL_GITHUB_TEST_ARGS="+string(encoded))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		body, err := cmd.Output()
		if err != nil {
			return body, fmt.Errorf("%s: %w: %s", args[0], err, stderr.String())
		}
		return body, nil
	}
	run := func(target any, args ...string) {
		t.Helper()
		body, err := command(args...)
		if err != nil {
			t.Fatal(err)
		}
		if target != nil && json.Unmarshal(body, target) != nil {
			t.Fatalf("invalid command JSON: %s", body)
		}
	}
	data, local := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(local, "local.md"), []byte("needle local"), 0600); err != nil {
		t.Fatal(err)
	}
	run(nil, "index", "--data-dir", data, "--max-pdf-bytes", "0", local)
	var initial githubCommandResult
	run(&initial, "index-github", "--data-dir", data, "--path", "docs", "--json", "Example/Demo")
	source := initial.GitHub.SourceID
	if initial.Seen != 3 || initial.GitHub.SHA != shaA || source == "" {
		t.Fatalf("initial index: %+v", initial)
	}
	var mixed search.Response
	run(&mixed, "search", "--data-dir", data, "--json", "needle")
	if mixed.Total != 4 {
		t.Fatalf("mixed results: %+v", mixed)
	}
	var before search.Response
	run(&before, "search", "--data-dir", data, "--source", source, "--json", "unchanged")
	if before.Total != 1 || !strings.Contains(before.Results[0].URI, shaA+"/docs/keep%20%23%3F%25.md") {
		t.Fatalf("initial permalink: %+v", before)
	}
	mu.Lock()
	sha, archive = shaB, archiveB
	mu.Unlock()
	var updated githubCommandResult
	run(&updated, "refresh-github", "--data-dir", data, "--json", source)
	if updated.Seen != 2 || updated.Removed != 1 || updated.GitHub.SHA != shaB || updated.GitHub.Revision != 2 {
		t.Fatalf("refresh: %+v", updated)
	}
	mu.Lock()
	archive = archiveB[:len(archiveB)-4]
	mu.Unlock()
	if _, err := command("refresh-github", "--data-dir", data, source); err == nil {
		t.Fatal("truncated archive refresh succeeded")
	}
	mu.Lock()
	offline = true
	mu.Unlock()
	networkBefore := requests.Load()
	var found search.Response
	run(&found, "search", "--data-dir", data, "--source", source, "--json", "unchanged")
	if found.Total != 1 || found.Results[0].ID != before.Results[0].ID || !strings.Contains(found.Results[0].URI, shaB+"/") {
		t.Fatalf("unchanged text did not retain ID and update permalink: %+v", found)
	}
	var obsolete search.Response
	run(&obsolete, "search", "--data-dir", data, "--source", source, "--json", "obsolete")
	if obsolete.Total != 0 {
		t.Fatalf("removed file remains searchable: %+v", obsolete)
	}
	var sources struct {
		Sources []sqlite.SourceStatus `json:"sources"`
	}
	run(&sources, "sources", "--data-dir", data, "--json")
	if len(sources.Sources) != 2 {
		t.Fatalf("sources: %+v", sources)
	}
	for _, item := range sources.Sources {
		if item.ID == source && (item.Documents != 2 || item.GitHub == nil || item.GitHub.SHA != shaB || item.GitHub.LastError == "" || item.GitHub.RegistrationToken != "") {
			t.Fatalf("failed refresh changed snapshot or leaked registration token: %+v", item)
		}
	}
	store, err := sqlite.OpenReadOnly(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := web.Handler(store)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766/api/v1/documents/"+found.Results[0].ID, nil))
	var evidence search.Evidence
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &evidence) != nil || evidence.Text != "needle unchanged" || evidence.URI != found.Results[0].URI {
		t.Fatalf("offline HTTP evidence: %d %s", response.Code, response.Body.String())
	}
	server, err := transport.New(store, transport.Config{AllowedSources: []string{source}})
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	serverCtx, stopServer := context.WithCancel(ctx)
	defer stopServer()
	done := make(chan error, 1)
	go func() { done <- server.Run(serverCtx, serverTransport) }()
	t.Cleanup(func() {
		stopServer()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			t.Error("MCP server did not stop")
		}
	})
	client := sdk.NewClient(&sdk.Implementation{Name: "github-integration", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, args map[string]any) *sdk.CallToolResult {
		t.Helper()
		result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("%s: %+v %v", name, result, err)
		}
		return result
	}
	listed := call("findrail_list_sources", map[string]any{})
	if body, _ := json.Marshal(listed.StructuredContent); !bytes.Contains(body, []byte(`"kind":"github"`)) || bytes.Contains(body, []byte(`"kind":"filesystem"`)) {
		t.Fatalf("MCP source scope: %s", body)
	}
	call("findrail_search", map[string]any{"source_id": source, "query": "needle"})
	result := call("findrail_get_evidence", map[string]any{"source_id": source, "document_id": evidence.ID})
	if body, _ := json.Marshal(result.StructuredContent); !bytes.Contains(body, []byte("needle unchanged")) || !bytes.Contains(body, []byte(shaB)) {
		t.Fatalf("MCP evidence: %s", body)
	}
	run(nil, "forget", "--data-dir", data, source)
	if _, err := store.GitHubSource(ctx, source); err == nil {
		t.Fatal("forget left GitHub metadata")
	}
	if _, err := store.Evidence(ctx, evidence.ID, 0); !errors.Is(err, search.ErrNotFound) {
		t.Fatalf("forgotten evidence: %v", err)
	}
	result, err = session.CallTool(ctx, &sdk.CallToolParams{Name: "findrail_get_evidence", Arguments: map[string]any{"source_id": source, "document_id": evidence.ID}})
	if err != nil || !result.IsError {
		t.Fatalf("MCP disclosed forgotten evidence: %+v %v", result, err)
	}
	if requests.Load() != networkBefore {
		t.Fatalf("offline retrieval made %d GitHub requests", requests.Load()-networkBefore)
	}
	session.Close()
	stopServer()
}
