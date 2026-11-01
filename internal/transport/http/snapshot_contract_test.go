package http

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSnapshotRequestFieldsAreExactAndNonNull(t *testing.T) {
	for _, raw := range []string{`{"path":"/synthetic/a.zip","Path":"/synthetic/b.zip"}`, `{"path":null}`, `{"path":"/synthetic/a.zip","unknown":null}`} {
		t.Run(raw, func(t *testing.T) {
			var body struct {
				Path string `json:"path"`
			}
			response := httptest.NewRecorder()
			request := httptest.NewRequest("POST", "/api/v1/snapshots/inspect", strings.NewReader(raw))
			if decodeSnapshotRequest(response, request, &body, "path") || response.Code != 400 {
				t.Fatalf("accepted non-contract fields: status=%d path=%q", response.Code, body.Path)
			}
		})
	}
}
