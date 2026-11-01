package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func TestMCPConfigProfilesAndValidation(t *testing.T) {
	dir := mcpTestIndex(t)
	name := `findrail "測試"\ cli`
	if runtime.GOOS == "windows" {
		name = "findrail 測試 cli.exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(binary, []byte("synthetic executable"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, client := range []string{"vscode", "claude-desktop"} {
		var out, stderr bytes.Buffer
		args := []string{"--client", client, "--data-dir", dir, "--source", "allowed", "--binary", binary}
		if err := runMCPConfig(context.Background(), args, &out, &stderr); err != nil {
			t.Fatal(err)
		}
		var got generatedMCPConfig
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		server := got.MCPServers["findrail"]
		wantArgs := []string{"mcp", "--data-dir", dir, "--source", "allowed"}
		if server.Command != binary || !equalStrings(server.Args, wantArgs) {
			t.Fatalf("unexpected config: %#v", got)
		}
		if stderr.Len() != 0 || !strings.HasPrefix(out.String(), "{") {
			t.Fatalf("stdout/stderr: %q / %q", out.String(), stderr.String())
		}
	}
	// The UTF-8 source is valid in the index but is deliberately not in the
	// generated scope: configuration must retain only explicitly selected IDs.
	store, err := sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := store.BeginScan(context.Background(), connector.Source{ID: "源-β", Name: "unselected", Kind: "filesystem"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scan.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	var scoped bytes.Buffer
	if err := runMCPConfig(context.Background(), []string{"--client", "vscode", "--data-dir", dir, "--source", "allowed", "--binary", binary}, &scoped, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(scoped.String(), "源-β") {
		t.Fatalf("config included unselected source: %s", scoped.String())
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown client", []string{"--client", "other", "--data-dir", dir, "--source", "allowed"}},
		{"duplicate source", []string{"--client", "vscode", "--data-dir", dir, "--source", "allowed", "--source", "allowed"}},
		{"unknown source", []string{"--client", "vscode", "--data-dir", dir, "--source", "missing"}},
		{"relative binary", []string{"--client", "vscode", "--data-dir", dir, "--source", "allowed", "--binary", "findrail"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := runMCPConfig(context.Background(), tc.args, &out, &bytes.Buffer{}); err == nil || out.Len() != 0 {
				t.Fatalf("error=%v partial=%q", err, out.String())
			}
		})
	}
	missing := filepath.Join(t.TempDir(), "missing")
	var out bytes.Buffer
	if err := runMCPConfig(context.Background(), []string{"--client", "vscode", "--data-dir", missing, "--source", "allowed"}, &out, &bytes.Buffer{}); err == nil || out.Len() != 0 {
		t.Fatalf("missing index error=%v output=%q", err, out.String())
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created missing index: %v", err)
	}
	if err := runMCPConfig(context.Background(), []string{"--client", "vscode", "--data-dir", dir, "--source", "allowed"}, failingMCPWriter{}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected stdout write failure")
	}
}

type failingMCPWriter struct{}

func (failingMCPWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
