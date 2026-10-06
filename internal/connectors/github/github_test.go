package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/pkg/connector"
)

func TestValidationIdentityAndPermalink(t *testing.T) {
	inputs := []string{"Example/Demo", "https://github.com/Example/Demo", "https://github.com/Example/Demo.git/"}
	for _, in := range inputs {
		owner, repo, err := ParseRepository(in)
		if err != nil || owner != "Example" || repo != "Demo" {
			t.Fatalf("ParseRepository(%q) = %q/%q, %v", in, owner, repo, err)
		}
	}
	for _, in := range []string{"http://github.com/a/b", "github.com/a/b", "git@github.com:a/b", "https://github.com/a/b/tree/main", "https://user@github.com/a/b", "a/b/c"} {
		if _, _, err := ParseRepository(in); err == nil {
			t.Errorf("accepted %q", in)
		}
	}
	a, _ := SelectionFrom("Example", "Demo", "heads/main", "docs", 1024)
	b := a
	if SourceID(42, a) != SourceID(42, b) {
		t.Fatal("identity is not stable")
	}
	if _, err := NormalizePath("../docs"); err == nil {
		t.Fatal("accepted traversal")
	}
	uri, err := Permalink("Example", "Demo", strings.Repeat("a", 40), "docs/a #?%.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(uri, "/blob/"+strings.Repeat("a", 40)+"/docs/a%20%23%3F%25.md") {
		t.Fatalf("unexpected permalink %s", uri)
	}
}

func TestPrepareHTTPArchiveSnapshot(t *testing.T) {
	sha := strings.Repeat("a", 40)
	archive := tarball(t, map[string]string{"repo-root/docs/read me.md": "needle text", "repo-root/docs/empty": "", "repo-root/vendor/no.go": "ignored", "repo-root/docs/lfs.txt": "version https://git-lfs.github.com/spec/v1\noid sha256:x\nsize 1\n"})
	broken := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body string
		status := 200
		switch {
		case r.URL.Path == "/repos/Example/Demo":
			body = `{"id":42,"name":"Demo","full_name":"Example/Demo","html_url":"https://github.com/Example/Demo","default_branch":"main","private":false,"owner":{"login":"Example"}}`
		case strings.Contains(r.URL.Path, "/commits/"):
			body = `{"sha":"` + sha + `","commit":{"committer":{"date":"2026-01-02T03:04:05Z"}}}`
		case strings.Contains(r.URL.Path, "/tarball/"):
			data := archive
			if broken {
				data = archive[:len(archive)-4]
			}
			return response(status, data), nil
		default:
			status = 404
		}
		return response(status, []byte(body)), nil
	})}
	sel, err := SelectionFrom("Example", "Demo", "", "docs", 1024)
	if err != nil {
		t.Fatal(err)
	}
	s, err := TestClient(client, "https://api.test").Prepare(context.Background(), sel)
	if err != nil {
		t.Fatal(err)
	}
	if s.Source().Kind != "github" || s.Metadata().SHA != sha {
		t.Fatalf("bad snapshot: %#v %#v", s.Source(), s.Metadata())
	}
	var docs int
	report, err := s.Scan(context.Background(), func(d connector.Document) error {
		docs++
		if !strings.Contains(d.URI, "/blob/"+sha+"/") {
			t.Errorf("not pinned: %s", d.URI)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if docs != 1 || report.Seen != 1 || s.SkipCounts().LFS != 1 {
		t.Fatalf("docs=%d report=%+v skips=%+v", docs, report, s.SkipCounts())
	}
	broken = true
	if _, err := TestClient(client, "https://api.test").Prepare(context.Background(), sel); err == nil {
		t.Fatal("accepted truncated gzip")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}
}

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body)), ModTime: time.Unix(0, 0)}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
