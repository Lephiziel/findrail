package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
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

func TestPrepareArchiveIgnoresGlobalPAXHeader(t *testing.T) {
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": strings.Repeat("a", 40)}}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "repo-root", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	body := "hello"
	if err := tw.WriteHeader(&tar.Header{Name: "repo-root/readme.md", Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := prepareArchive(context.Background(), bytes.NewReader(raw.Bytes()), Metadata{RepositoryID: 1, Owner: "Example", Repo: "Demo", SHA: strings.Repeat("b", 40), RefMode: "default", MaxBytes: 1024, CommitTime: time.Unix(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	var got int
	_, err = s.Scan(context.Background(), func(connector.Document) error { got++; return nil })
	if err != nil || got != 1 {
		t.Fatalf("scan got %d, %v", got, err)
	}
}

func TestNormalizePathRejectsAbsoluteBeforeTrimming(t *testing.T) {
	for _, value := range []string{"/", "//", "C:/repo", `\\server\\share`} {
		if _, err := NormalizePath(value); err == nil {
			t.Errorf("NormalizePath(%q) accepted unsafe path", value)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestExclusionReasonTaxonomy(t *testing.T) {
	for path, want := range map[string]string{".private/file.md": "hidden_entry", "vendor/file.go": "dependency_directory", "docs/secret-notes.md": "sensitive_name", "docs/.private/secret.md": "hidden_entry"} {
		if got := exclusionReason(path); got != want {
			t.Errorf("exclusionReason(%q)=%q, want %q", path, got, want)
		}
	}
}

func TestArchivePreparationFailureRetainsBoundedPartialDiagnostics(t *testing.T) {
	var raw bytes.Buffer
	gzipWriter := gzip.NewWriter(&raw)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "repo-root", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.WriteHeader(&tar.Header{Name: "repo-root/picture.png", Typeflag: tar.TypeReg, Mode: 0600, Size: 3}); err != nil {
		t.Fatal(err)
	}
	_, _ = tarWriter.Write([]byte("png"))
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	compressed := append(append([]byte(nil), raw.Bytes()...), 0xaa)
	var updates int
	_, err := prepareArchiveWithProgress(context.Background(), bytes.NewReader(compressed), Metadata{RepositoryID: 2, Owner: "Example", Repo: "Demo", SHA: strings.Repeat("c", 40), RefMode: "default", MaxBytes: 1024, CommitTime: time.Unix(0, 0)}, func(processed, skipped int, complete, flush bool) {
		updates++
		if complete {
			t.Fatal("failed archive reported complete progress")
		}
	})
	var failure *PreparationFailure
	if !errors.As(err, &failure) {
		t.Fatalf("expected structured archive failure: %v", err)
	}
	if failure.Diagnostics.ObservedFiles != 1 || !failure.Diagnostics.ObservedFilesKnown || len(failure.Diagnostics.Reasons) != 1 || failure.Diagnostics.Reasons[0].Code != "unsupported_format" || len(failure.Diagnostics.Examples) != 1 || failure.Diagnostics.Examples[0].Path != "picture.png" || updates < 2 {
		t.Fatalf("partial archive diagnostics lost: %+v updates=%d", failure.Diagnostics, updates)
	}
}
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
