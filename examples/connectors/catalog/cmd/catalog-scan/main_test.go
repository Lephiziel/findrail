package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScanCommandSyntheticSuccessAndProvisionalFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail_%v", fail), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/collections/demo/resources" {
					http.NotFound(w, r)
					return
				}
				if fail && r.URL.Query().Get("cursor") != "" {
					http.Error(w, "opaque fixture error", 503)
					return
				}
				if fail {
					fmt.Fprint(w, `{"collection":"demo","snapshot":"s","complete":false,"next":"next","items":[{"ID":"x","Title":"Synthetic","Path":"x.txt","URI":"https://catalog.example/x","Body":"safe synthetic body","Modified":"2026-01-01T00:00:00Z"}]}`)
					return
				}
				fmt.Fprint(w, `{"collection":"demo","snapshot":"s","complete":true,"items":[{"ID":"x","Title":"Synthetic","Path":"x.txt","URI":"https://catalog.example/x","Body":"safe synthetic body","Modified":"2026-01-01T00:00:00Z"}]}`)
			}))
			defer srv.Close()
			var stdout, stderr strings.Builder
			code := run([]string{"--origin", srv.URL, "--collection", "demo", "--allow-loopback-http"}, &stdout, &stderr)
			if fail {
				if code != 1 || strings.Contains(stdout.String(), `"type":"complete"`) || !strings.Contains(stderr.String(), "provisional") {
					t.Fatalf("failed stream result: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
				}
				if strings.Contains(stderr.String(), "opaque fixture error") {
					t.Fatal("raw remote error leaked")
				}
				return
			}
			if code != 0 {
				t.Fatalf("successful command returned %d: %s", code, stderr.String())
			}
			lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
			if len(lines) != 3 {
				t.Fatalf("expected source, one doc and completion records: %s", stdout.String())
			}
			var record map[string]any
			if err := json.Unmarshal([]byte(lines[2]), &record); err != nil || record["type"] != "complete" {
				t.Fatalf("missing completion record: %s", lines[2])
			}
		})
	}
}
