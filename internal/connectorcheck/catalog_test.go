//go:build connector_kit

package connectorcheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	catalog "example.com/findrail-catalog"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
)

func TestCatalogSQLiteLifecycle(t *testing.T) {
	state := []map[string]any{{"id": "r1", "title": "First", "path": "first.txt", "uri": "https://catalog.example/r1", "body": "oldtoken", "modified": "2026-01-01T00:00:00Z"}}
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "unavailable", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"collection": "one", "snapshot": "s1", "complete": true, "items": state})
	}))
	defer srv.Close()
	newConn := func() *catalog.Connector {
		c, e := catalog.New(catalog.Config{Origin: srv.URL, Collection: "one", AllowLoopbackHTTP: true})
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	db, e := sqlite.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	c := newConn()
	if _, e = ingest.Run(ctx, db, c); e != nil {
		t.Fatal(e)
	}
	query := func(q string) int {
		r, e := db.Search(ctx, search.Request{Query: q, Limit: 10})
		if e != nil {
			t.Fatal(e)
		}
		return r.Total
	}
	if query("oldtoken") != 1 {
		t.Fatal("initial inventory missing")
	}
	state[0]["body"] = "newtoken"
	if _, e = ingest.Refresh(ctx, db, c); e != nil {
		t.Fatal(e)
	}
	if query("oldtoken") != 0 || query("newtoken") != 1 {
		t.Fatal("updated inventory not published")
	}
	state = nil
	fail = true
	if _, e = ingest.Refresh(ctx, db, c); e == nil {
		t.Fatal("expected late failure")
	}
	if query("newtoken") != 1 {
		t.Fatal("failed inventory changed committed state")
	}
	fail = false
	if _, e = ingest.Refresh(ctx, db, c); e != nil {
		t.Fatal(e)
	}
	if query("newtoken") != 0 {
		t.Fatal("empty successful inventory did not prune")
	}
}
