package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	githubconnector "github.com/Lephiziel/findrail/internal/connectors/github"
	"github.com/Lephiziel/findrail/internal/diagnostics"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
)

type githubTestTransport func(*http.Request) (*http.Response, error)

func (f githubTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func githubTestTar(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "repo-root/readme.md", Mode: 0644, Size: 6}); err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(tw, "needle")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestGitHubFailureAttemptHidesPathsUnlessExplicit(t *testing.T) {
	prep := &githubconnector.PreparationFailure{Cause: errors.New("private raw parser error"), Diagnostics: diagnostics.Payload{ObservedFiles: 3, ObservedFilesKnown: true, Reasons: []diagnostics.Reason{{Code: "unsupported_format", Unit: "file", Count: 2}}, Examples: []diagnostics.Example{{Path: "docs/private.png", Unit: "file", Reason: "unsupported_format"}}, FailurePath: "docs/broken.md", Coverage: "selected_github_candidates"}}
	if got := failureCode(prep); got != "archive_preparation_failed" {
		t.Fatalf("failure code=%q", got)
	}
	without, _ := json.Marshal(githubAttempt("refresh-github", "archive_preparation_failed", prep, false))
	if strings.Contains(string(without), "private") || strings.Contains(string(without), "broken.md") {
		t.Fatalf("default failure leaked path/error: %s", without)
	}
	with, _ := json.Marshal(githubAttempt("refresh-github", "archive_preparation_failed", prep, true))
	if !strings.Contains(string(with), "docs/broken.md") || !strings.Contains(string(with), "docs/private.png") {
		t.Fatalf("explicit failure details missing: %s", with)
	}
}

func githubTestResponse(body []byte) *http.Response {
	return &http.Response{StatusCode: 200, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}
}

func githubTestFixture(sha string, archive []byte, onArchive func(*http.Request) error) githubTestTransport {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == "/repos/Example/Demo":
			return githubTestResponse([]byte(`{"id":42,"name":"Demo","full_name":"Example/Demo","html_url":"https://github.com/Example/Demo","default_branch":"main","private":false,"owner":{"login":"Example"}}`)), nil
		case strings.Contains(r.URL.Path, "/commits/"):
			return githubTestResponse([]byte(`{"sha":"` + sha + `","commit":{"committer":{"date":"2026-01-02T03:04:05Z"}}}`)), nil
		case strings.Contains(r.URL.Path, "/tarball/"):
			if onArchive != nil {
				if err := onArchive(r); err != nil {
					return nil, err
				}
			}
			return githubTestResponse(archive), nil
		default:
			return nil, errors.New("unexpected fixture URL")
		}
	}
}

func githubTestSnapshot(t *testing.T, sha string, archive []byte) *githubconnector.Snapshot {
	t.Helper()
	sel, err := githubconnector.SelectionFrom("Example", "Demo", "", "", 1024)
	if err != nil {
		t.Fatal(err)
	}
	c := githubconnector.TestClient(&http.Client{Transport: githubTestFixture(sha, archive, nil)}, "https://api.github.com")
	snapshot, err := c.Prepare(context.Background(), sel)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestGitHubIndexGuardsRemoteOperation(t *testing.T) {
	for _, phase := range []string{"commits", "tarball"} {
		t.Run(phase, func(t *testing.T) {
			for _, operation := range []string{"forget", "newer_publish", "re_register"} {
				t.Run(operation, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					dir := t.TempDir()
					store, err := sqlite.Open(ctx, dir)
					if err != nil {
						t.Fatal(err)
					}
					defer store.Close()
					archive := githubTestTar(t)
					a := githubTestSnapshot(t, strings.Repeat("a", 40), archive)
					_, previous, err := store.PublishGitHub(ctx, a, nil)
					if err != nil {
						t.Fatal(err)
					}
					c := githubTestSnapshot(t, strings.Repeat("c", 40), archive)
					started, release := make(chan struct{}), make(chan struct{})
					var once sync.Once
					unblock := func() { once.Do(func() { close(release) }) }
					defer unblock()
					oldTransport := http.DefaultTransport
					fixture := githubTestFixture(strings.Repeat("b", 40), archive, nil)
					http.DefaultTransport = githubTestTransport(func(r *http.Request) (*http.Response, error) {
						if !strings.Contains(r.URL.Path, "/"+phase+"/") {
							return fixture(r)
						}
						close(started)
						select {
						case <-release:
							return fixture(r)
						case <-r.Context().Done():
							return nil, r.Context().Err()
						}
					})
					defer func() { http.DefaultTransport = oldTransport }()
					finished := make(chan error, 1)
					go func() {
						finished <- runGitHub(ctx, "index-github", []string{"--data-dir", dir, "--json", "Example/Demo"}, io.Discard, io.Discard)
					}()
					select {
					case <-started:
					case <-ctx.Done():
						t.Fatal("archive request did not start")
					}
					if operation == "forget" {
						if err := store.ForgetSource(ctx, a.Source().ID); err != nil {
							t.Fatal(err)
						}
					} else if operation == "re_register" {
						if err := store.ForgetSource(ctx, a.Source().ID); err != nil {
							t.Fatal(err)
						}
						if _, _, err := store.PublishGitHub(ctx, c, nil); err != nil {
							t.Fatal(err)
						}
					} else {
						if _, _, err := store.PublishGitHub(ctx, c, &previous); err != nil {
							t.Fatal(err)
						}
					}
					unblock()
					select {
					case err := <-finished:
						if err == nil {
							t.Error("repeated index published despite source changing during download")
						}
					case <-ctx.Done():
						t.Fatal("repeated index did not finish")
					}
					current, err := store.GitHubSource(ctx, a.Source().ID)
					if operation == "forget" {
						if !errors.Is(err, ingest.ErrSourceGone) {
							t.Errorf("forgotten source resurrected: SHA=%s, error=%v", current.SHA, err)
						}
					} else if err != nil || current.SHA != strings.Repeat("c", 40) {
						t.Errorf("newer snapshot overwritten: SHA=%s, error=%v", current.SHA, err)
					}
				})
			}
		})
	}
}
