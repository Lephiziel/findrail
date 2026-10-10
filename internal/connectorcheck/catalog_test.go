//go:build connector_kit

package connectorcheck

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	catalog "example.com/findrail-catalog"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/Lephiziel/findrail/pkg/connector"
)

type upsertFailureStore struct {
	db            *sqlite.Store
	failAt, calls int
}

func (s *upsertFailureStore) BeginScan(ctx context.Context, src connector.Source) (ingest.Scan, error) {
	sc, e := s.db.BeginScan(ctx, src)
	if e != nil {
		return nil, e
	}
	return &upsertFailureScan{Scan: sc, owner: s}, nil
}
func (s *upsertFailureStore) BeginRefresh(ctx context.Context, src connector.Source) (ingest.Scan, error) {
	sc, e := s.db.BeginRefresh(ctx, src)
	if e != nil {
		return nil, e
	}
	return &upsertFailureScan{Scan: sc, owner: s}, nil
}

type upsertFailureScan struct {
	ingest.Scan
	owner *upsertFailureStore
}

func (s *upsertFailureScan) Upsert(ctx context.Context, d connector.Document) (bool, error) {
	s.owner.calls++
	if s.owner.calls == s.owner.failAt {
		return false, errors.New("injected storage callback failure")
	}
	return s.Scan.Upsert(ctx, d)
}

func TestCatalogSQLiteLifecycle(t *testing.T) {
	state := []map[string]any{{"id": "r1", "title": "First", "path": "first.txt", "uri": "https://catalog.example/r1", "body": "oldtoken", "modified": "2026-01-01T00:00:00Z"}, {"id": "r2", "title": "Second", "path": "second.txt", "uri": "https://catalog.example/r2", "body": "secondtoken", "modified": "2026-01-01T00:00:00Z"}}
	lateFail := false
	blockSecond := false
	secondRequested := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if lateFail || blockSecond {
			if r.URL.Query().Get("cursor") != "" {
				if blockSecond {
					select {
					case secondRequested <- struct{}{}:
					default:
					}
					<-r.Context().Done()
					return
				}
				http.Error(w, "late page failure", 503)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"collection": "one", "snapshot": "s1", "complete": false, "next": "later", "items": state})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"collection": "one", "snapshot": "s1", "complete": true, "items": state})
	}))
	defer srv.Close()
	newConn := func() *catalog.Connector {
		c, e := catalog.New(catalog.Config{Origin: srv.URL, Collection: "one", AllowLoopbackHTTP: true, RequestTimeout: 5 * time.Second})
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	dir := t.TempDir()
	db, e := sqlite.Open(context.Background(), dir)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	c := newConn()
	initial, e := ingest.Run(ctx, db, c)
	if e != nil {
		t.Fatal(e)
	}
	if initial.Updated != 2 || initial.Seen != 2 {
		t.Fatalf("unexpected initial result: %+v", initial)
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
	initialSearch, e := db.Search(ctx, search.Request{Query: "oldtoken", Limit: 10})
	if e != nil || len(initialSearch.Results) != 1 {
		t.Fatalf("initial evidence search failed: %v", e)
	}
	initialEvidence, e := db.EvidenceForSource(ctx, c.Source().ID, initialSearch.Results[0].ID, 0)
	if e != nil || initialEvidence.Text != "oldtoken" || initialEvidence.SourceKind != "educational-catalog" {
		t.Fatalf("initial evidence missing: %+v %v", initialEvidence, e)
	}
	state[0]["body"] = "storage-provisional"
	failing := &upsertFailureStore{db: db, failAt: 2}
	if _, e = ingest.Refresh(ctx, failing, c); e == nil {
		t.Fatal("expected injected storage error")
	}
	if query("oldtoken") != 1 || query("storage-provisional") != 0 {
		t.Fatal("storage callback error published provisional state")
	}
	state[0]["body"] = "oldtoken"
	unchanged, e := ingest.Refresh(ctx, db, c)
	if e != nil || unchanged.Unchanged != 2 {
		t.Fatalf("unchanged scan not recognized: %+v %v", unchanged, e)
	}
	state[0]["body"] = "newtoken"
	if _, e = ingest.Refresh(ctx, db, c); e != nil {
		t.Fatal(e)
	}
	if query("oldtoken") != 0 || query("newtoken") != 1 {
		t.Fatal("updated inventory not published")
	}
	res, e := db.Search(ctx, search.Request{Query: "newtoken", Limit: 10})
	if e != nil || len(res.Results) != 1 {
		t.Fatalf("missing result: %+v %v", res, e)
	}
	updatedEvidence, e := db.EvidenceForSource(ctx, c.Source().ID, res.Results[0].ID, 0)
	if e != nil || updatedEvidence.Text != "newtoken" || updatedEvidence.ContentHash == initialEvidence.ContentHash {
		t.Fatalf("body evidence/hash did not update: %+v %v", updatedEvidence, e)
	}
	before, e := db.EvidenceForSource(ctx, c.Source().ID, res.Results[0].ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	state[0]["title"] = "Renamed First"
	state[0]["path"] = "renamed/first.txt"
	state[0]["uri"] = "https://catalog.example/renamed"
	if _, e = ingest.Refresh(ctx, db, c); e != nil {
		t.Fatal(e)
	}
	res, e = db.Search(ctx, search.Request{Query: "newtoken", Limit: 10})
	if e != nil || len(res.Results) != 1 || res.Results[0].Title != "Renamed First" {
		t.Fatalf("metadata update missing: %+v %v", res, e)
	}
	after, e := db.EvidenceForSource(ctx, c.Source().ID, res.Results[0].ID, 0)
	if e != nil {
		t.Fatal(e)
	}
	if before.ContentHash != after.ContentHash || before.URI == after.URI || before.Path == after.Path {
		t.Fatal("metadata-only update did not preserve hash and update evidence")
	}
	state[0]["body"] = "provisionaltoken"
	lateFail = true
	if _, e = ingest.Refresh(ctx, db, c); e == nil {
		t.Fatal("expected late failure")
	}
	if query("newtoken") != 1 || query("provisionaltoken") != 0 {
		t.Fatal("failed inventory changed committed state")
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = sqlite.Open(ctx, dir)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if query("newtoken") != 1 || query("provisionaltoken") != 0 {
		t.Fatal("failed provisional inventory survived restart")
	}
	lateFail = false
	if _, e = ingest.Refresh(ctx, db, c); e != nil {
		t.Fatalf("recovery failed: %v", e)
	}
	if query("provisionaltoken") != 1 {
		t.Fatal("recovery did not publish complete scan")
	}
	state = state[1:]
	removed, e := ingest.Refresh(ctx, db, c)
	if e != nil || removed.Removed != 1 {
		t.Fatalf("single deletion was not pruned: %+v %v", removed, e)
	}
	if query("secondtoken") != 1 || query("provisionaltoken") != 0 {
		t.Fatal("single deletion affected the wrong documents")
	}
	state = []map[string]any{}
	if _, e = ingest.Refresh(ctx, db, c); e != nil {
		t.Fatal(e)
	}
	if query("provisionaltoken") != 0 {
		t.Fatal("empty successful inventory did not prune")
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = sqlite.Open(ctx, dir)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if query("provisionaltoken") != 0 {
		t.Fatal("successful empty inventory did not persist after reopen")
	}
	// Cancellation after the first provisional callback, while a later page is
	// blocked, must retain the already committed empty snapshot.
	state = []map[string]any{{"id": "r1", "title": "First", "path": "first.txt", "uri": "https://catalog.example/r1", "body": "stabletoken", "modified": "2026-01-01T00:00:00Z"}}
	if _, e = ingest.Refresh(ctx, db, c); e != nil {
		t.Fatal(e)
	}
	if query("stabletoken") != 1 {
		t.Fatal("could not establish cancellation rollback baseline")
	}
	state[0]["body"] = "cancel-provisional"
	blockSecond = true
	cancelCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, err := ingest.Refresh(cancelCtx, db, c); done <- err }()
	select {
	case <-secondRequested:
		cancel()
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("scan did not reach blocked later page")
	}
	if err := <-done; err == nil {
		t.Fatal("canceled scan succeeded")
	}
	blockSecond = false
	if query("stabletoken") != 1 || query("cancel-provisional") != 0 {
		t.Fatal("canceled partial inventory was published")
	}
	if _, e = ingest.Refresh(ctx, db, c); e != nil {
		t.Fatalf("refresh after cancellation failed: %v", e)
	}
	if query("cancel-provisional") != 1 {
		t.Fatal("successful scan after cancellation did not recover")
	}
}

func TestMalformedLaterPageNeverPublishes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"collection":"broken","snapshot":"s","complete":false,"next":"next","items":[{"ID":"x","Title":"X","Path":"x.txt","URI":"https://catalog.example/x","Body":"never searchable","Modified":"2026-01-01T00:00:00Z"}]}`))
			return
		}
		_, _ = w.Write([]byte("{"))
	}))
	defer srv.Close()
	c, e := catalog.New(catalog.Config{Origin: srv.URL, Collection: "broken", AllowLoopbackHTTP: true, RequestTimeout: 5 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	db, e := sqlite.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = ingest.Run(context.Background(), db, c); e == nil {
		t.Fatal("malformed late page succeeded")
	}
	r, e := db.Search(context.Background(), search.Request{Query: "searchable", Limit: 10})
	if e != nil {
		t.Fatal(e)
	}
	if r.Total != 0 {
		t.Fatal("malformed inventory partially published")
	}
}

func TestCatalogSourceIsolationAndForgetRefresh(t *testing.T) {
	failOne := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := "one"
		if r.URL.Path == "/v1/collections/two/resources" {
			name = "two"
		}
		if name == "one" && failOne {
			http.Error(w, "unavailable", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"collection": name, "snapshot": "s1", "complete": true, "items": []any{map[string]any{"id": "same-raw-id", "title": name, "path": "doc.txt", "uri": "https://catalog.example/" + name, "body": name + "token", "modified": "2026-01-01T00:00:00Z"}}})
	}))
	defer server.Close()
	makeC := func(collection string) *catalog.Connector {
		c, e := catalog.New(catalog.Config{Origin: server.URL, Collection: collection, AllowLoopbackHTTP: true, RequestTimeout: 5 * time.Second})
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
	one, two := makeC("one"), makeC("two")
	if _, e = ingest.Run(ctx, db, one); e != nil {
		t.Fatal(e)
	}
	if _, e = ingest.Run(ctx, db, two); e != nil {
		t.Fatal(e)
	}
	searchCount := func(q, source string) int {
		r, e := db.Search(ctx, search.Request{Query: q, SourceID: source, Limit: 10})
		if e != nil {
			t.Fatal(e)
		}
		return r.Total
	}
	if searchCount("onetoken", one.Source().ID) != 1 || searchCount("twotoken", two.Source().ID) != 1 {
		t.Fatal("same raw resource ID was not isolated by source")
	}
	failOne = true
	if _, e = ingest.Refresh(ctx, db, one); e == nil {
		t.Fatal("expected first-source refresh failure")
	}
	failOne = false
	if searchCount("onetoken", one.Source().ID) != 1 || searchCount("twotoken", two.Source().ID) != 1 {
		t.Fatal("failed refresh crossed source boundary")
	}
	if _, e = ingest.Refresh(ctx, db, two); e != nil {
		t.Fatal(e)
	}
	if e = db.ForgetSource(ctx, one.Source().ID); e != nil {
		t.Fatal(e)
	}
	if _, e = ingest.Refresh(ctx, db, one); !errors.Is(e, ingest.ErrSourceGone) {
		t.Fatalf("refresh silently re-registered forgotten source: %v", e)
	}
	if searchCount("twotoken", two.Source().ID) != 1 || searchCount("onetoken", one.Source().ID) != 0 {
		t.Fatal("forget/removal crossed source boundary")
	}
}
