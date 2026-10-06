package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func demoTempRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// os.TempDir uses TMPDIR on Unix and TMP/TEMP on Windows.
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, dir)
	}
	return dir
}

func TestDemoUsesPrivateWorkspaceAndCleansUpAfterStart(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal-stop", true: "startup-error"}[fail], func(t *testing.T) {
			parent := demoTempRoot(t)
			sentinel := filepath.Join(parent, "existing-index.db")
			if err := os.WriteFile(sentinel, []byte("leave existing data alone"), 0600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			failure := errors.New("listener unavailable")
			var workspace string
			err := runDemo(context.Background(), []string{"--no-open", "--addr", "127.0.0.1:0", "--sync-interval", "1s"}, &output, io.Discard, nil,
				func(_ context.Context, args []string, _, _ io.Writer, _ func(string) error) error {
					if len(args) != 8 || args[0] != "--data-dir" || args[2] != "--addr" || args[3] != "127.0.0.1:0" || args[4] != "--sync-interval" || args[5] != "1s" || args[6] != "--no-open" {
						t.Fatalf("start options: %v", args)
					}
					documents, index := args[7], args[1]
					workspace = filepath.Dir(documents)
					if filepath.Dir(workspace) != parent || index != filepath.Join(workspace, "index") || documents != filepath.Join(workspace, "documents") {
						t.Fatalf("workspace is not isolated: %v", args)
					}
					entries, err := os.ReadDir(documents)
					if err != nil || len(entries) != 3 {
						t.Fatalf("demo documents: %v, %v", entries, err)
					}
					for _, name := range []string{"retry-notes.md", "retry.go", "webhook-runbook.pdf"} {
						if info, err := os.Stat(filepath.Join(documents, name)); err != nil || info.Size() == 0 {
							t.Fatalf("missing fixture %s: %v", name, err)
						}
					}
					if err := os.Mkdir(index, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(index, "findrail.db"), []byte("temporary"), 0600); err != nil {
						t.Fatal(err)
					}
					if fail {
						return failure
					}
					return nil
				})
			if fail && !errors.Is(err, failure) || !fail && err != nil {
				t.Fatalf("start result: %v", err)
			}
			if workspace == "" {
				t.Fatal("start was not called")
			}
			if _, err := os.Stat(workspace); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("workspace remains: %v", err)
			}
			if data, err := os.ReadFile(sentinel); err != nil || string(data) != "leave existing data alone" {
				t.Fatalf("existing data changed: %q, %v", data, err)
			}
			if !strings.Contains(output.String(), filepath.Join(workspace, "documents")) || !strings.Contains(output.String(), "idempotency") {
				t.Fatalf("missing demo instructions: %s", output.String())
			}
		})
	}
}

func TestDemoRejectsInputBeforeCreatingWorkspace(t *testing.T) {
	parent := demoTempRoot(t)
	for _, args := range [][]string{
		{"--addr", "0.0.0.0:7766"}, {"--addr", "127.0.0.1:65536"},
		{"--sync-interval", "0s"}, {"/personal/folder"}, {"--data-dir", "index"},
	} {
		if err := runDemo(context.Background(), args, io.Discard, io.Discard, nil,
			func(context.Context, []string, io.Writer, io.Writer, func(string) error) error {
				t.Fatal("invalid arguments reached start")
				return nil
			}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runDemo(ctx, nil, io.Discard, io.Discard, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: %v", err)
	}
	if err := Run(context.Background(), []string{"demo", "--help"}, io.Discard, io.Discard, "test"); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatalf("invalid invocation created files: %v, %v", entries, err)
	}
}

type demoBrokenWriter struct{}

func (demoBrokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestDemoCleansUpWhenInstructionsCannotBeWritten(t *testing.T) {
	parent := demoTempRoot(t)
	if err := runDemo(context.Background(), nil, demoBrokenWriter{}, io.Discard, nil, nil); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("output error: %v", err)
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatalf("workspace remains: %v, %v", entries, err)
	}
}
