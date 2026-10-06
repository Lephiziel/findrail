package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/Lephiziel/findrail/pkg/connector"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeBackend struct {
	mu       sync.Mutex
	searches int
	evidence int
	sources  []sqlite.SourceStatus
	search   search.Response
	e        search.Evidence
	err      error
}

func (f *fakeBackend) Search(context.Context, search.Request) (search.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches++
	return f.search, f.err
}
func (f *fakeBackend) Sources(context.Context) ([]sqlite.SourceStatus, error) {
	return f.sources, f.err
}
func (f *fakeBackend) EvidenceForSource(context.Context, string, string, int) (search.Evidence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.evidence++
	return f.e, f.err
}

func testBackend() *fakeBackend {
	return &fakeBackend{
		sources: []sqlite.SourceStatus{
			{Source: connector.Source{ID: "b", Name: "zeta", Kind: "filesystem"}, Documents: 2, LastIndexedAt: "2026-10-06T00:00:00Z"},
			{Source: connector.Source{ID: "a", Name: "alpha", Kind: "filesystem"}, Documents: 1, LastIndexedAt: "2026-10-06T00:00:00Z"},
		},
		search: search.Response{Total: 1, Results: []search.Result{{ID: "doc-a", Title: "notes.md", URI: "file:///notes.md", Path: "notes.md", SourceID: "a", SourceName: "alpha", SourceKind: "filesystem", Snippet: "hello"}}},
		e:      search.Evidence{ID: "doc-a", Title: "notes.md", URI: "file:///notes.md", Path: "notes.md", SourceID: "a", SourceName: "alpha", MediaType: "text/plain", ContentHash: "hash", ModifiedAt: "2026-10-06T00:00:00Z", Text: "hello"},
	}
}

func connectTestServer(t *testing.T, version string) (*Server, *sdkmcp.ServerSession, *sdkmcp.ClientSession) {
	t.Helper()
	backend := testBackend()
	server, err := New(backend, Config{AllowedSources: []string{"a"}, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := server.server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "test"}, &sdkmcp.ClientOptions{Capabilities: &sdkmcp.ClientCapabilities{}})
	clientSession, err := client.Connect(context.Background(), clientTransport, &sdkmcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		serverSession.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clientSession.Close()
		serverSession.Close()
	})
	return server, serverSession, clientSession
}

func TestToolsAndReadOnlyJourney(t *testing.T) {
	for _, version := range supportedProtocolVersions {
		t.Run(version, func(t *testing.T) {
			_, _, session := connectTestServer(t, version)
			list, err := session.ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(list.Tools) != 3 {
				t.Fatalf("tools = %d, want 3", len(list.Tools))
			}
			for _, tool := range list.Tools {
				if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint || tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
					t.Fatalf("unsafe tool annotations for %s: %+v", tool.Name, tool.Annotations)
				}
			}
			sources, err := session.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "findrail_list_sources", Arguments: map[string]any{}})
			if err != nil || sources.IsError {
				t.Fatalf("list sources: %v %+v", err, sources)
			}
			var listed listSourcesOutput
			decodeStructured(t, sources.StructuredContent, &listed)
			if len(listed.Sources) != 1 || listed.Sources[0].ID != "a" {
				t.Fatalf("sources = %+v", listed)
			}
			found, err := session.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "findrail_search", Arguments: map[string]any{"query": "hello", "source_id": "a"}})
			if err != nil || found.IsError {
				t.Fatalf("search: %v %+v", err, found)
			}
			var searchResult searchOutput
			decodeStructured(t, found.StructuredContent, &searchResult)
			if searchResult.Returned != 1 || searchResult.Results[0].SourceID != "a" {
				t.Fatalf("search result = %+v", searchResult)
			}
			evidence, err := session.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "findrail_get_evidence", Arguments: map[string]any{"document_id": "doc-a", "source_id": "a"}})
			if err != nil || evidence.IsError {
				t.Fatalf("evidence: %v %+v", err, evidence)
			}
			var gotEvidence evidenceOutput
			decodeStructured(t, evidence.StructuredContent, &gotEvidence)
			if gotEvidence.Text != "hello" || gotEvidence.TextChars != 5 || !gotEvidence.IndexedSnapshot {
				t.Fatalf("evidence result = %+v", gotEvidence)
			}
		})
	}
}

func TestSourceScopeIsCheckedBeforeBackend(t *testing.T) {
	backend := testBackend()
	server, err := New(backend, Config{AllowedSources: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := server.server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "client", Version: "test"}, &sdkmcp.ClientOptions{Capabilities: &sdkmcp.ClientCapabilities{}})
	session, err := client.Connect(context.Background(), clientTransport, &sdkmcp.ClientSessionOptions{ProtocolVersion: supportedProtocolVersions[0]})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	defer serverSession.Close()
	result, err := session.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "findrail_search", Arguments: map[string]any{"query": "secret", "source_id": "b"}})
	if err != nil || !result.IsError {
		t.Fatalf("source scope: %v %+v", err, result)
	}
	var payload map[string]map[string]string
	decodeStructured(t, result.StructuredContent, &payload)
	if payload["error"]["code"] != "source_unavailable" {
		t.Fatalf("error = %+v", payload)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.searches != 0 {
		t.Fatalf("backend search called %d times", backend.searches)
	}
}

func TestEvidenceLimitPreservesUnicode(t *testing.T) {
	backend := testBackend()
	backend.e.Text = "абвгд"
	server, err := New(backend, Config{AllowedSources: []string{"a"}, MaxTextChars: 256})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.evidence(context.Background(), nil, evidenceInput{DocumentID: "doc-a", SourceID: "a"}); err != nil {
		t.Fatal(err)
	}
}

func decodeStructured(t *testing.T, value any, target any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}
