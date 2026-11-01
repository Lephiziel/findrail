package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	githubconnector "github.com/Lephiziel/findrail/internal/connectors/github"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
)

type fixtureTransport struct{ archive []byte }

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body string
	switch {
	case r.URL.Path == "/repos/Example/Demo":
		body = `{"id":42,"name":"Demo","full_name":"Example/Demo","html_url":"https://github.com/Example/Demo","default_branch":"main","private":false,"owner":{"login":"Example"}}`
	case strings.Contains(r.URL.Path, "/commits/"):
		body = `{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","commit":{"committer":{"date":"2026-01-02T03:04:05Z"}}}`
	case strings.Contains(r.URL.Path, "/tarball/"):
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(f.archive)), Request: r}, nil
	default:
		return nil, fmt.Errorf("unexpected synthetic GitHub route")
	}
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func makeArchive() ([]byte, error) {
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	content := "githubsynthetic pinned fixture phrase"
	if err := tw.WriteHeader(&tar.Header{Name: "repo-root/docs/github-note.md", Mode: 0644, Size: int64(len(content))}); err != nil {
		return nil, err
	}
	if _, err := io.WriteString(tw, content); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "expected index directory")
		os.Exit(2)
	}
	archive, err := makeArchive()
	if err != nil {
		panic(err)
	}
	selection, err := githubconnector.SelectionFrom("Example", "Demo", "", "docs", 1<<20)
	if err != nil {
		panic(err)
	}
	client := githubconnector.TestClient(&http.Client{Transport: fixtureTransport{archive: archive}}, "https://api.github.com")
	snapshot, err := client.Prepare(context.Background(), selection)
	if err != nil {
		panic(err)
	}
	store, err := sqlite.Open(context.Background(), os.Args[1])
	if err != nil {
		panic(err)
	}
	defer store.Close()
	if _, _, err := store.PublishGitHub(context.Background(), snapshot, nil); err != nil {
		panic(err)
	}
}
