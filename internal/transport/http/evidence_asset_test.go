package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEvidenceCollectionAssetUsesExistingLocalBoundary(t *testing.T) {
	handler := Handler(nil)
	for _, tc := range []struct {
		host string
		want int
	}{
		{"127.0.0.1:7766", http.StatusOK},
		{"attacker.example:7766", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7766/assets/evidence_collection.mjs", nil)
		req.Host = tc.host
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("host %q: got status %d, want %d", tc.host, rec.Code, tc.want)
		}
		if tc.want == http.StatusOK {
			if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
				t.Errorf("content type = %q", got)
			}
			if !strings.Contains(rec.Body.String(), "export class EvidenceCollection") {
				t.Error("asset body does not contain collection module")
			}
			for _, name := range []string{"X-Content-Type-Options", "Cache-Control", "Content-Security-Policy"} {
				if rec.Header().Get(name) == "" {
					t.Errorf("missing %s middleware header", name)
				}
			}
		}
	}
}
