package http_test

import (
	"context"
	"errors"
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
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	transport "github.com/Lephiziel/findrail/internal/transport/http"
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
		{"/assets/citation.mjs", "127.0.0.1:7766", "http://127.0.0.1:7766", 200, "export function formatCitation"},
		{"/assets/citation.mjs", "attacker.example:7766", "", 403, "local host"},
		{"/assets/citation.mjs", "127.0.0.1:7766", "https://attacker.example", 403, "same origin"},
		{"/assets/unknown.mjs", "127.0.0.1:7766", "", 404, ""},
		{"/assets/citation_test.mjs", "127.0.0.1:7766", "", 404, ""},
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
		if tc.path == "/assets/citation.mjs" && tc.code == http.StatusOK {
			if got := w.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
				t.Errorf("citation module content type = %q", got)
			}
			for _, header := range []string{"X-Content-Type-Options", "Cache-Control", "Content-Security-Policy"} {
				if w.Header().Get(header) == "" {
					t.Errorf("citation module missing %s header", header)
				}
			}
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
