package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/pkg/connector"
)

func runWire(t *testing.T, status int, wire string, callback func(connector.Document) error) (int, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); _, _ = w.Write([]byte(wire)) }))
	defer srv.Close()
	c, err := New(Config{Origin: srv.URL, Collection: "c", AllowLoopbackHTTP: true, RequestTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Scan(context.Background(), callback)
	return r.Seen, err
}

func TestRejectsHostileResponses(t *testing.T) {
	good := `{"collection":"c","snapshot":"s","complete":true,"items":[{"ID":"x","Title":"X","Path":"x.txt","URI":"https://catalog.example/x","Body":"text","Modified":"2026-01-01T00:00:00Z"}]}`
	tests := []struct {
		name   string
		status int
		wire   string
	}{{"collection mismatch", 200, strings.Replace(good, `"collection":"c"`, `"collection":"other"`, 1)}, {"missing revision", 200, strings.Replace(good, `"snapshot":"s",`, "", 1)}, {"trailing json", 200, good + ` {}`}, {"malformed json", 200, "{"}, {"invalid utf8", 200, string([]byte{'{', 0xff, '}'})}, {"401", 401, good}, {"403", 403, good}, {"oversize response", 200, strings.Repeat(" ", pageLimit+1)}, {"hostile metadata", 200, strings.Replace(good, `"Title":"X"`, `"Title":"`+strings.Repeat("x", 16<<10)+`"`, 1)}, {"bad URI", 200, strings.Replace(good, "https://catalog.example/x", "file:///etc/passwd", 1)}, {"traversal", 200, strings.Replace(good, "x.txt", "a/../x.txt", 1)}, {"invalid page envelope", 200, `{"collection":"c","snapshot":"s","complete":false,"items":[]}`}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := runWire(t, tt.status, tt.wire, func(connector.Document) error { return nil }); err == nil {
				t.Fatal("accepted invalid response")
			}
		})
	}
	for _, status := range []int{401, 403} {
		t.Run(fmt.Sprint("typed unavailable ", status), func(t *testing.T) {
			_, err := runWire(t, status, good, func(connector.Document) error { return nil })
			var unavailable *UnavailableError
			if !errors.As(err, &unavailable) || unavailable.Status != status || strings.Contains(err.Error(), "example") {
				t.Fatalf("unsafe/untyped access failure: %v", err)
			}
		})
	}
}

func TestRequiredWireFields(t *testing.T) {
	for name, wire := range map[string]string{"missing items": `{"collection":"c","snapshot":"s","complete":true}`, "null items": `{"collection":"c","snapshot":"s","complete":true,"items":null}`, "missing resource field": `{"collection":"c","snapshot":"s","complete":true,"items":[{"ID":"x","Title":"X","Path":"x.txt","URI":"https://catalog.example/x","Modified":"2026-01-01T00:00:00Z"}]}`} {
		t.Run(name, func(t *testing.T) {
			if _, err := runWire(t, 200, wire, func(connector.Document) error { return nil }); err == nil {
				t.Fatal("accepted response with missing required field")
			}
		})
	}
}

func TestCallbackAndCancellationArePreserved(t *testing.T) {
	sentinel := errors.New("consumer sentinel")
	_, err := runWire(t, 200, `{"collection":"c","snapshot":"s","complete":true,"items":[{"ID":"x","Title":"X","Path":"x.txt","URI":"https://catalog.example/x","Body":"text","Modified":"2026-01-01T00:00:00Z"}]}`, func(connector.Document) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatal("callback error was not preserved")
	}
	started := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { once.Do(func() { close(started) }); <-r.Context().Done() }))
	defer srv.Close()
	c, e := New(Config{Origin: srv.URL, Collection: "c", AllowLoopbackHTTP: true, RequestTimeout: 5 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, scanErr := c.Scan(ctx, func(connector.Document) error { return nil }); done <- scanErr }()
	select {
	case <-started:
		cancel()
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("request never started")
	}
	select {
	case err = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled scan did not return")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation while waiting for page was not returned")
	}
}

func TestSecondPageFailuresAreProvisional(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			fmt.Fprint(w, `{"collection":"c","snapshot":"s","complete":false,"next":"n","items":[{"ID":"x","Title":"X","Path":"x.txt","URI":"https://catalog.example/x","Body":"text","Modified":"2026-01-01T00:00:00Z"}]}`)
			return
		}
		fmt.Fprint(w, `{"collection":"c","snapshot":"changed","complete":true,"items":[]}`)
	}))
	defer server.Close()
	c, e := New(Config{Origin: server.URL, Collection: "c", AllowLoopbackHTTP: true, RequestTimeout: 5 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	emitted := 0
	report, err := c.Scan(context.Background(), func(connector.Document) error { emitted++; return nil })
	if err == nil || emitted != 1 || report.Seen != 1 {
		t.Fatalf("late failure not observed: emitted=%d report=%d err=%v", emitted, report.Seen, err)
	}
}

func TestCollectionIdentityIsBoundOnEveryPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			fmt.Fprint(w, `{"collection":"c","snapshot":"s","complete":false,"next":"n","items":[]}`)
			return
		}
		fmt.Fprint(w, `{"collection":"other","snapshot":"s","complete":true,"items":[]}`)
	}))
	defer srv.Close()
	c, e := New(Config{Origin: srv.URL, Collection: "c", AllowLoopbackHTTP: true, RequestTimeout: 5 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Scan(context.Background(), func(connector.Document) error { return nil }); e == nil {
		t.Fatal("later page from another collection accepted")
	}
}

func TestRedirectAndTransientStatusAreNotRetried(t *testing.T) {
	var requests atomic.Int32
	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/v1/collections/c/resources" {
			http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
			return
		}
		fmt.Fprint(w, `{"collection":"c","snapshot":"s","complete":true,"items":[]}`)
	}))
	defer redirectServer.Close()
	c, e := New(Config{Origin: redirectServer.URL, Collection: "c", AllowLoopbackHTTP: true, RequestTimeout: 5 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.Scan(context.Background(), func(connector.Document) error { return nil })
	if e == nil || requests.Load() != 1 {
		t.Fatalf("redirect was followed or accepted: count=%d err=%v", requests.Load(), e)
	}
	requests.Store(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "private fixture detail", 503)
	}))
	defer srv.Close()
	c, e = New(Config{Origin: srv.URL, Collection: "c", AllowLoopbackHTTP: true, RequestTimeout: 5 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.Scan(context.Background(), func(connector.Document) error { return nil })
	if e == nil || requests.Load() != 1 {
		t.Fatalf("transient status was retried: count=%d err=%v", requests.Load(), e)
	}
}

func TestConstructorDoesNotPerformNetworkAndRejectsUnsafeOrigin(t *testing.T) {
	var requests atomic.Int32
	probe := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer probe.Close()
	for _, origin := range []string{"http://example.test", "https://user@example.test", "https://example.test/?x=1", "https://example.test/#x", "https://example.test/path"} {
		if _, err := New(Config{Origin: origin, Collection: "c"}); err == nil {
			t.Fatalf("accepted unsafe origin %q", origin)
		}
	}
	if _, err := New(Config{Origin: "http://127.0.0.1:1", Collection: "c"}); err == nil {
		t.Fatal("loopback HTTP accepted without explicit option")
	}
	if _, err := New(Config{Origin: "https://catalog.example", Collection: "c"}); err == nil {
		t.Fatal("missing explicit request timeout accepted")
	}
	if _, err := New(Config{Origin: probe.URL, Collection: "c", AllowLoopbackHTTP: true, RequestTimeout: 5 * time.Second}); err != nil {
		t.Fatal(err)
	}
	adapter, err := New(Config{Origin: probe.URL, Collection: "c", AllowLoopbackHTTP: true, RequestTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_ = adapter.Source()
	if requests.Load() != 0 {
		t.Fatal("constructor or Source performed network I/O")
	}
}

func TestCursorLoopAndDuplicateResourceFail(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprintf("duplicate_%v", duplicate), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				first := r.URL.Query().Get("cursor") == ""
				if first {
					fmt.Fprint(w, `{"collection":"c","snapshot":"s","complete":false,"next":"opaque","items":[{"ID":"x","Title":"X","Path":"x.txt","URI":"https://catalog.example/x","Body":"one","Modified":"2026-01-01T00:00:00Z"}]}`)
					return
				}
				if duplicate {
					fmt.Fprint(w, `{"collection":"c","snapshot":"s","complete":true,"items":[{"ID":"x","Title":"X","Path":"x.txt","URI":"https://catalog.example/x","Body":"one","Modified":"2026-01-01T00:00:00Z"}]}`)
				} else {
					fmt.Fprint(w, `{"collection":"c","snapshot":"s","complete":false,"next":"opaque","items":[]}`)
				}
			}))
			defer srv.Close()
			c, e := New(Config{Origin: srv.URL, Collection: "c", AllowLoopbackHTTP: true, RequestTimeout: 5 * time.Second})
			if e != nil {
				t.Fatal(e)
			}
			_, e = c.Scan(context.Background(), func(connector.Document) error { return nil })
			if e == nil {
				t.Fatal("cursor loop or duplicate ID accepted")
			}
		})
	}
}

func TestDocumentAndAggregateBudgets(t *testing.T) {
	makeItem := func(id string) wireItem {
		return wireItem{ID: id, Title: "Title", Path: "docs/" + id + ".txt", URI: "https://catalog.example/" + id, Body: "x", Modified: "2026-01-01T00:00:00Z"}
	}
	tooMany := make([]wireItem, 1001)
	for i := range tooMany {
		tooMany[i] = makeItem(strconv.Itoa(i))
	}
	encoded, _ := json.Marshal(wirePage{Collection: "c", Snapshot: "s", Complete: true, Items: tooMany})
	if _, err := runWire(t, 200, string(encoded), func(connector.Document) error { return nil }); err == nil {
		t.Fatal("document count cap not enforced")
	}
	big := makeItem("big")
	big.Body = strings.Repeat("x", (256<<10)+1)
	encoded, _ = json.Marshal(wirePage{Collection: "c", Snapshot: "s", Complete: true, Items: []wireItem{big}})
	if _, err := runWire(t, 200, string(encoded), func(connector.Document) error { return nil }); err == nil {
		t.Fatal("per-document content cap not enforced")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := 0
		if c := r.URL.Query().Get("cursor"); c != "" {
			n, _ = strconv.Atoi(strings.TrimPrefix(c, "p"))
		}
		items := make([]wireItem, 3)
		for i := range items {
			items[i] = makeItem(fmt.Sprintf("%d-%d", n, i))
			items[i].Body = strings.Repeat("z", 240<<10)
		}
		p := wirePage{Collection: "c", Snapshot: "s", Complete: n == 5, Items: items}
		if n < 5 {
			p.Next = fmt.Sprintf("p%d", n+1)
		}
		b, _ := json.Marshal(p)
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	c, e := New(Config{Origin: srv.URL, Collection: "c", AllowLoopbackHTTP: true, RequestTimeout: 5 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Scan(context.Background(), func(connector.Document) error { return nil }); e == nil {
		t.Fatal("aggregate scan budget not enforced")
	}
}

func TestPageCountBudget(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1)) - 1
		next := fmt.Sprintf("p%d", n+1)
		if n > 0 {
			next = fmt.Sprintf("p%d", n+1)
		}
		p := wirePage{Collection: "c", Snapshot: "s", Complete: false, Next: next, Items: []wireItem{}}
		b, _ := json.Marshal(p)
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	c, e := New(Config{Origin: srv.URL, Collection: "c", AllowLoopbackHTTP: true, RequestTimeout: 5 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Scan(context.Background(), func(connector.Document) error { return nil }); e == nil || calls.Load() != 100 {
		t.Fatalf("expected bounded page exhaustion: calls=%d err=%v", calls.Load(), e)
	}
}
