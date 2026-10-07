package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/search"
)

type startOutput struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	urls chan string
}

func (w *startOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	for _, line := range strings.Split(string(p), "\n") {
		if rest, ok := strings.CutPrefix(line, "Findrail local UI: "); ok && w.urls != nil {
			select {
			case w.urls <- rest:
			default:
			}
		}
	}
	return n, err
}

func (w *startOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func startForTest(t *testing.T, args []string, open func(string) error) (string, func() error, *startOutput, *startOutput) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	out := &startOutput{urls: make(chan string, 1)}
	stderr := &startOutput{}
	done := make(chan error, 1)
	go func() { done <- runStart(ctx, args, out, stderr, open) }()
	var once sync.Once
	var result error
	stop := func() error {
		once.Do(func() {
			cancel()
			select {
			case result = <-done:
			case <-time.After(10 * time.Second):
				result = errors.New("start did not stop")
			}
		})
		return result
	}
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Error(err)
		}
	})
	select {
	case base := <-out.urls:
		return base, stop, out, stderr
	case err := <-done:
		once.Do(func() { cancel(); result = err })
		t.Fatalf("start exited before listening: %v; %s", err, stderr.String())
	case <-time.After(10 * time.Second):
		t.Fatal("start did not bind a listener")
	}
	return "", stop, out, stderr
}

func startSearch(base, query string) (search.Response, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	r, err := client.Get(base + "/api/v1/search?q=" + url.QueryEscape(query))
	if err != nil {
		return search.Response{}, err
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return search.Response{}, errors.New(r.Status)
	}
	var response search.Response
	err = json.NewDecoder(r.Body).Decode(&response)
	return response, err
}

func TestStartIndexesWatchesAndStops(t *testing.T) {
	root := filepath.Join(t.TempDir(), "notes with spaces")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "sample.md")
	if err := os.WriteFile(path, []byte("initialmarker"), 0600); err != nil {
		t.Fatal(err)
	}
	var opened atomic.Int32
	base, stop, out, _ := startForTest(t, []string{"--data-dir", t.TempDir(), "--addr", "127.0.0.1:0", "--sync-interval", "1s", "--no-open", root}, func(string) error {
		opened.Add(1)
		return nil
	})
	if strings.HasSuffix(base, ":0") {
		t.Fatal("printed the requested port rather than the bound port")
	}
	found, err := startSearch(base, "initialmarker")
	if err != nil || found.Total != 1 {
		t.Fatalf("initial search: %+v, %v", found, err)
	}
	if err := os.WriteFile(path, []byte("refreshedmarker"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(7 * time.Second)
	for {
		newResult, newErr := startSearch(base, "refreshedmarker")
		oldResult, oldErr := startSearch(base, "initialmarker")
		if newErr == nil && oldErr == nil && newResult.Total == 1 && oldResult.Total == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("refresh failed: new=%+v %v, old=%+v %v", newResult, newErr, oldResult, oldErr)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if opened.Load() != 0 {
		t.Fatal("--no-open launched a browser")
	}
	if !strings.Contains(out.String(), "Indexed 1 documents") {
		t.Fatalf("missing indexing progress: %s", out.String())
	}
	if _, err := startSearch(base, "refreshedmarker"); err == nil {
		t.Fatal("HTTP remained available after cancellation")
	}
}

func TestStartResumesStoredFoldersAndBrowserFailureIsNonfatal(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("resumemarker"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"index", "--data-dir", data, root}, io.Discard, io.Discard, "test"); err != nil {
		t.Fatal(err)
	}
	var opened atomic.Int32
	openedURL := make(chan string, 1)
	base, stop, _, stderr := startForTest(t, []string{"--data-dir", data, "--addr", "127.0.0.1:0"}, func(url string) error {
		opened.Add(1)
		openedURL <- url
		return errors.New("desktop unavailable")
	})
	found, err := startSearch(base, "resumemarker")
	if err != nil || found.Total != 1 {
		t.Fatalf("browser failure stopped search: %+v %v", found, err)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if opened.Load() != 1 || <-openedURL != base {
		t.Fatal("browser opener was not called once with the bound URL")
	}
	if !strings.Contains(stderr.String(), "desktop unavailable") || !strings.Contains(stderr.String(), base) {
		t.Fatalf("missing manual-open fallback: %s", stderr.String())
	}
}

func TestStartRejectsBadInputBeforeCreatingIndex(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"remote", []string{"--addr", "0.0.0.0:7766", root}},
		{"bad-port", []string{"--addr", "127.0.0.1:65536", root}},
		{"interval", []string{"--sync-interval", "0s", root}},
		{"two-folders", []string{root, root}},
		{"missing-folder", []string{filepath.Join(root, "absent")}},
		{"text-limit", []string{"--max-bytes", "0", root}},
		{"pdf-limit", []string{"--max-pdf-bytes", "-1", root}},
		{"limit-without-folder", []string{"--max-bytes", "100"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := filepath.Join(t.TempDir(), "new-index")
			args := append([]string{"--data-dir", data}, tc.args...)
			if err := runStart(context.Background(), args, io.Discard, io.Discard, func(string) error { t.Error("opened browser"); return nil }); err == nil {
				t.Fatal("accepted bad input")
			}
			if _, err := os.Stat(data); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("created index for bad input: %v", err)
			}
		})
	}
	data := filepath.Join(t.TempDir(), "new-index")
	if err := Run(context.Background(), []string{"start", "--data-dir", data, "--help"}, io.Discard, io.Discard, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(data); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("help created an index: %v", err)
	}
}

func TestStartCreatesEmptyIndexAndServes(t *testing.T) {
	data := filepath.Join(t.TempDir(), "empty-index")
	base, stop, _, _ := startForTest(t, []string{"--data-dir", data, "--addr", "127.0.0.1:0", "--no-open"}, func(string) error {
		t.Fatal("--no-open launched a browser")
		return nil
	})
	resp, err := http.Get(base + "/api/v1/sources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sources endpoint: %s", resp.Status)
	}
	capability, err := http.Get(base + "/api/v1/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	var enabled map[string]bool
	if err := json.NewDecoder(capability.Body).Decode(&enabled); err != nil {
		t.Fatal(err)
	}
	capability.Body.Close()
	if !enabled["management"] {
		t.Fatal("start did not enable source management")
	}
	if _, err := os.Stat(filepath.Join(data, "findrail.db")); err != nil {
		t.Fatalf("empty index not created: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
}

func TestReadOnlyStartCompositionHasNoManagement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	out := &startOutput{urls: make(chan string, 1)}
	done := make(chan error, 1)
	data := t.TempDir()
	go func() {
		done <- runStartReadOnly(ctx, []string{"--data-dir", data, "--addr", "127.0.0.1:0", "--no-open"}, out, io.Discard, func(string) error { return nil })
	}()
	var base string
	select {
	case base = <-out.urls:
	case err := <-done:
		t.Fatalf("read-only startup failed: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("listener did not start")
	}
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("read-only server did not stop")
		}
	}()
	resp, err := http.Get(base + "/api/v1/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	var caps map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if caps["management"] {
		t.Fatal("read-only composition enabled management")
	}
	r, _ := http.NewRequest(http.MethodPost, base+"/api/v1/sources", strings.NewReader(`{"type":"folder","path":"/tmp"}`))
	r.Header.Set("Origin", base)
	r.Header.Set("Content-Type", "application/json")
	result, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	result.Body.Close()
	if result.StatusCode == http.StatusAccepted {
		t.Fatal("read-only composition accepted a source mutation")
	}
}

func TestStartDoesNotOpenBrowserWhenPortIsBusy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var out bytes.Buffer
	var opened atomic.Int32
	err = runStart(context.Background(), []string{"--data-dir", t.TempDir(), "--addr", listener.Addr().String(), t.TempDir()}, &out, io.Discard, func(string) error {
		opened.Add(1)
		return nil
	})
	if err == nil || opened.Load() != 0 || strings.Contains(out.String(), "Findrail local UI:") {
		t.Fatalf("misreported busy port: %v, %s", err, out.String())
	}
}

func TestBrowserCommandUsesDirectURLArgument(t *testing.T) {
	url := "http://[::1]:7766"
	for _, tc := range []struct {
		goos, name string
		args       []string
	}{
		{"linux", "xdg-open", []string{url}},
		{"darwin", "open", []string{url}},
		{"windows", "rundll32", []string{"url.dll,FileProtocolHandler", url}},
	} {
		name, args, err := browserCommand(tc.goos, url)
		if err != nil || name != tc.name || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("%s: %s %v %v", tc.goos, name, args, err)
		}
	}
	if _, _, err := browserCommand("plan9", url); err == nil {
		t.Fatal("unsupported browser opener accepted")
	}
}
