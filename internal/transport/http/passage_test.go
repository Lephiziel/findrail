package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	transport "github.com/Lephiziel/findrail/internal/transport/http"
	"github.com/Lephiziel/findrail/pkg/connector"
)

type oversizedPassage struct{ *sqlite.Store }

func (b oversizedPassage) Passage(context.Context, string, string, string, int) (search.Evidence, error) {
	return search.Evidence{Text: "bounded body", Title: strings.Repeat("x", 512<<10)}, nil
}

func TestPassageStrictParametersAndResponseBound(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	scan, err := s.BeginScan(ctx, connector.Source{ID: "source", Kind: "filesystem", Name: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scan.Upsert(ctx, connector.Document{ID: "doc", SourceID: "source", Title: "note", Path: "note.md", URI: "file:///synthetic/note.md", MediaType: "text/plain", Content: "needle text", Hash: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := scan.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	ev, err := s.Evidence(ctx, "doc", 0)
	if err != nil {
		t.Fatal(err)
	}
	identity := "original_identity=" + url.QueryEscape(ev.OriginalIdentity)
	h := transport.Handler(s)
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"q=needle&" + identity, 200},
		{"q=needle&mode=literal&" + identity, 200},
		{"q=needle&mode=&" + identity, 400},
		{"q=needle&bad=%zz&" + identity, 400},
		{"q=needle&ignored;parameter&" + identity, 400},
		{"q=needle&mode=advanced&" + identity, 400},
		{"q=needle&start_char=0&" + identity, 400},
		{"start_char=0&mode=literal&" + identity, 400},
		{"start_char=0&start_char=1&" + identity, 400},
		{"start_char=-1&" + identity, 400},
		{"start_char=999999999999999999999&" + identity, 400},
		{"start_char=12&" + identity, 400},
		{"q=needle&page=1&" + identity, 400},
		{"q=needle", 400},
		{"q=needle&original_identity=stale", 409},
	} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/documents/doc/passage?"+tc.query, nil)
		r.Host = "127.0.0.1:7766"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s: got %d want %d: %s", tc.query, w.Code, tc.want, w.Body.String())
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/documents/doc/passage?q=needle&"+identity, nil)
	r.Host = "127.0.0.1:7766"
	w := httptest.NewRecorder()
	transport.Handler(oversizedPassage{s}).ServeHTTP(w, r)
	if w.Code != 500 || w.Body.Len() > 512<<10 {
		t.Fatalf("oversized passage response: status %d bytes %d", w.Code, w.Body.Len())
	}
}
