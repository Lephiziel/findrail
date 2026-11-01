package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/Lephiziel/findrail/pkg/connector"
)

type blockedMCPWriter struct {
	started chan struct{}
	closed  chan struct{}
	start   sync.Once
	close   sync.Once
}

func (w *blockedMCPWriter) Write([]byte) (int, error) {
	w.start.Do(func() { close(w.started) })
	<-w.closed
	return 0, io.ErrClosedPipe
}

func (w *blockedMCPWriter) Close() error {
	w.close.Do(func() { close(w.closed) })
	return nil
}

func mcpTestIndex(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	store, err := sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scan, err := store.BeginScan(context.Background(), connector.Source{ID: "allowed", Name: "notes", Kind: "filesystem"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scan.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMCPShutdownClosesBlockedOutput(t *testing.T) {
	for _, cause := range []string{"cancel", "EOF"} {
		t.Run(cause, func(t *testing.T) { testMCPBlockedOutput(t, cause) })
	}
}

func testMCPBlockedOutput(t *testing.T, cause string) {
	dir := mcpTestIndex(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, input := io.Pipe()
	defer input.Close()
	out := &blockedMCPWriter{started: make(chan struct{}), closed: make(chan struct{})}
	defer out.Close()
	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runMCPWithIO(ctx, []string{"--data-dir", dir, "--source", "allowed"}, out, &stderr, "test", reader)
	}()
	if _, err := io.WriteString(input, "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"server/discover\"}\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-out.started:
	case <-time.After(2 * time.Second):
		t.Fatal("server never attempted to write a discovery response")
	}
	if cause == "EOF" {
		input.Close()
	} else {
		cancel()
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		out.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("MCP failed to stop even after output was unblocked")
		}
		t.Fatalf("%s left MCP stuck writing to a client that stopped reading", cause)
	}
}

func TestMCPStartupValidationAndHelp(t *testing.T) {
	dir := mcpTestIndex(t)
	for _, tc := range []struct {
		name string
		args []string
		help bool
	}{
		{"help", []string{"--help"}, true},
		{"missing source", nil, false},
		{"duplicate source", []string{"--source", "allowed", "--source", "allowed"}, false},
		{"unknown source", []string{"--source", "unknown"}, false},
		{"empty source", []string{"--source", ""}, false},
		{"whitespace source", []string{"--source", "bad id"}, false},
		{"control source", []string{"--source", "bad\x01id"}, false},
		{"long source", []string{"--source", strings.Repeat("文", 129)}, false},
		{"unknown flag", []string{"--source", "allowed", "--allow-all"}, false},
		{"extra arg", []string{"--source", "allowed", "/some/path"}, false},
		{"zero results", []string{"--source", "allowed", "--max-results", "0"}, false},
		{"high results", []string{"--source", "allowed", "--max-results", "21"}, false},
		{"fractional results", []string{"--source", "allowed", "--max-results", "1.5"}, false},
		{"low text", []string{"--source", "allowed", "--max-text-chars", "255"}, false},
		{"high text", []string{"--source", "allowed", "--max-text-chars", "32769"}, false},
		{"low timeout", []string{"--source", "allowed", "--request-timeout", "500ms"}, false},
		{"high timeout", []string{"--source", "allowed", "--request-timeout", "31s"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			args := append([]string{"--data-dir", dir}, tc.args...)
			err := runMCPWithIO(context.Background(), args, &out, &stderr, "test", io.NopCloser(strings.NewReader("")))
			if (err == nil) != tc.help || out.Len() != 0 {
				t.Fatalf("startup error=%v stdout=%q", err, out.String())
			}
			if tc.help && !strings.Contains(stderr.String(), "Usage: findrail mcp") {
				t.Fatal("help did not reach stderr")
			}
		})
	}
	tooMany := []string{"--data-dir", dir}
	for i := range 17 {
		tooMany = append(tooMany, "--source", fmt.Sprintf("id%d", i))
	}
	if err := runMCPWithIO(context.Background(), tooMany, io.Discard, io.Discard, "test", io.NopCloser(strings.NewReader(""))); err == nil {
		t.Fatal("accepted 17 sources")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	for _, args := range [][]string{{"--help"}, {"--source", "allowed"}} {
		var out bytes.Buffer
		err := runMCPWithIO(context.Background(), append([]string{"--data-dir", missing}, args...), &out, io.Discard, "test", io.NopCloser(strings.NewReader("")))
		if (err == nil) != (args[0] == "--help") || out.Len() != 0 {
			t.Fatalf("missing index: %v", err)
		}
		if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("startup created index: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runMCPWithIO(ctx, []string{"--data-dir", missing, "--source", "allowed"}, io.Discard, io.Discard, "test", io.NopCloser(strings.NewReader(""))); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled startup: %v", err)
	}
}

func TestMCPRejectsOversizedFrame(t *testing.T) {
	dir := mcpTestIndex(t)
	reader, input := io.Pipe()
	defer input.Close()
	var out, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runMCPWithIO(context.Background(), []string{"--data-dir", dir, "--source", "allowed"}, &out, &stderr, "test", reader)
	}()
	go func() { _, _ = io.WriteString(input, strings.Repeat("x", (64<<10)+1)+"\n") }()
	select {
	case err := <-done:
		if err == nil || out.Len() != 0 {
			t.Fatalf("oversized frame: error=%v stdout=%q", err, out.String())
		}
	case <-time.After(2 * time.Second):
		reader.Close()
		t.Fatal("oversized frame did not close the transport")
	}
}
