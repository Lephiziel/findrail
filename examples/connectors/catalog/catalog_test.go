package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Lephiziel/findrail/pkg/connector"
	"github.com/Lephiziel/findrail/pkg/connector/conformance"
)

func TestConformance(t *testing.T) {
	pages := [][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("non-GET request")
		}
		if r.URL.Path != "/v1/collections/demo/resources" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("cursor") == "" {
			w.Write(pages[0])
		} else {
			w.Write(pages[1])
		}
	}))
	defer server.Close()
	encode := func(p wirePage) []byte { b, _ := json.Marshal(p); return b }
	pages = [][]byte{encode(wirePage{Collection: "demo", Snapshot: "s1", Next: "next-1", Items: []wireItem{{ID: "a", Title: "Alpha", Path: "a.txt", URI: "https://catalog.example/a", Body: "searchable alpha", Modified: "2026-01-01T00:00:00Z"}}}), encode(wirePage{Collection: "demo", Snapshot: "s1", Complete: true, Items: []wireItem{{ID: "b", Title: "Beta", Path: "b.txt", URI: "https://catalog.example/b", Body: "searchable beta", Modified: "2026-01-01T00:00:00Z"}}})}
	c, err := New(Config{Origin: server.URL, Collection: "demo", AllowLoopbackHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	conformance.Run(t, func() connector.Connector { return c }, conformance.DefaultOptions(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Scan(ctx, func(_ connector.Document) error { return nil }); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatal("expected cancellation")
	}
}
