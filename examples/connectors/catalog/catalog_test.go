package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/pkg/connector"
	"github.com/Lephiziel/findrail/pkg/connector/conformance"
)

func TestConformance(t *testing.T) {
	var mu sync.RWMutex
	pages := map[string][][]byte{}
	failures := map[string]bool{}
	encode := func(p wirePage) []byte {
		b, e := json.Marshal(p)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	set := func(col string, ps [][]byte, fail bool) {
		mu.Lock()
		defer mu.Unlock()
		pages[col] = ps
		failures[col] = fail
	}
	item := func(id, title, path, uri, body string) wireItem {
		return wireItem{ID: id, Title: title, Path: path, URI: uri, Body: body, Modified: "2026-01-01T00:00:00Z"}
	}
	basePages := func(col string) [][]byte {
		return [][]byte{encode(wirePage{Collection: col, Snapshot: "snap-1", Next: "next-1", Complete: false, Items: []wireItem{item("a", "Alpha", "a.txt", "https://catalog.example/a", "searchable alpha")}}), encode(wirePage{Collection: col, Snapshot: "snap-1", Complete: true, Items: []wireItem{item("b", "Beta", "b.txt", "https://catalog.example/b", "searchable beta")}})}
	}
	set("demo", basePages("demo"), false)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("non-GET request")
			http.Error(w, "method", 405)
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) != 5 || parts[1] != "v1" || parts[2] != "collections" || parts[4] != "resources" {
			http.NotFound(w, r)
			return
		}
		mu.RLock()
		col := parts[3]
		fail := failures[col]
		ps := append([][]byte(nil), pages[col]...)
		mu.RUnlock()
		if len(ps) == 0 {
			http.NotFound(w, r)
			return
		}
		cursor := r.URL.Query().Get("cursor")
		if cursor != "" && fail {
			http.Error(w, "late page failure", 503)
			return
		}
		if cursor == "" {
			_, _ = w.Write(ps[0])
		} else if len(ps) > 1 {
			_, _ = w.Write(ps[1])
		} else {
			_, _ = w.Write(ps[0])
		}
	}))
	defer server.Close()
	newConn := func(col string) connector.Connector {
		c, e := New(Config{Origin: server.URL, Collection: col, AllowLoopbackHTTP: true, RequestTimeout: 5 * time.Second})
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	set("demo", basePages("demo"), false)
	hooks := &conformance.Hooks{
		UpdateContent: func() connector.Connector {
			set("demo", [][]byte{encode(wirePage{Collection: "demo", Snapshot: "snap-1", Complete: true, Items: []wireItem{item("a", "Alpha", "a.txt", "https://catalog.example/a", "updated searchable alpha"), item("b", "Beta", "b.txt", "https://catalog.example/b", "searchable beta")}})}, false)
			return newConn("demo")
		},
		UpdateMetadata: func() connector.Connector {
			set("demo", [][]byte{encode(wirePage{Collection: "demo", Snapshot: "snap-1", Complete: true, Items: []wireItem{item("a", "Alpha renamed", "renamed/a.txt", "https://catalog.example/renamed-a", "searchable alpha"), item("b", "Beta", "b.txt", "https://catalog.example/b", "searchable beta")}})}, false)
			return newConn("demo")
		},
		DeleteOne: func() connector.Connector {
			set("demo", [][]byte{encode(wirePage{Collection: "demo", Snapshot: "snap-1", Complete: true, Items: []wireItem{item("b", "Beta", "b.txt", "https://catalog.example/b", "searchable beta")}})}, false)
			return newConn("demo")
		},
		Empty: func() connector.Connector {
			set("demo", [][]byte{encode(wirePage{Collection: "demo", Snapshot: "snap-1", Complete: true, Items: []wireItem{}})}, false)
			return newConn("demo")
		},
		FailLate: func() connector.Connector { set("demo", basePages("demo"), true); return newConn("demo") },
		Recover:  func() connector.Connector { set("demo", basePages("demo"), false); return newConn("demo") },
		SecondSource: func() connector.Connector {
			set("other", [][]byte{encode(wirePage{Collection: "other", Snapshot: "snap-1", Complete: true, Items: []wireItem{item("a", "Other", "a.txt", "https://catalog.example/other", "searchable other")}})}, false)
			return newConn("other")
		},
	}
	set("demo", basePages("demo"), false)
	conformance.Run(t, func() connector.Connector { set("demo", basePages("demo"), false); return newConn("demo") }, conformance.DefaultOptions(), hooks)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newConn("demo").Scan(ctx, func(connector.Document) error { return nil }); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled scan was not recognized")
	}
}
