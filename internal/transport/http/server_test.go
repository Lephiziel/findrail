package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/ingest"
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
	}
}

func TestServerRejectsNetworkExposure(t *testing.T) {
	if err := transport.Serve(context.Background(), "0.0.0.0:7766", nil); err == nil {
		t.Fatal("expected loopback restriction")
	}
}
