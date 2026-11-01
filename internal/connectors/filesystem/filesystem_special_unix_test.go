//go:build linux || darwin

package filesystem_test

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func TestIgnoreNegationCannotOverrideSpecialEntry(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "pipe.md"), 0600); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".findrailignore"), []byte("!pipe.md\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := filesystem.New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	var emitted int
	legacy, report, err := c.ScanWithDiagnostics(context.Background(), func(connector.Document) error { emitted++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if emitted != 0 || legacy.Seen != 0 {
		t.Fatalf("indexed special entry: emitted=%d %+v", emitted, legacy)
	}
	found := false
	for _, reason := range report.Reasons {
		if reason.Code == "special_entry" && reason.Unit == "entry" {
			found = true
		}
	}
	if !found {
		t.Fatalf("special-entry hard decision missing: %+v", report.Reasons)
	}
}

func TestAbsentIgnorePreservesUnixPaths(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"colon:name.md", `back\slash.md`} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("ordinary indexed text"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := filesystem.New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Scan(context.Background(), func(connector.Document) error { return nil })
	if err != nil || r.Seen != 2 {
		t.Fatalf("Scan() without policy: seen=%d err=%v, want 2 documents", r.Seen, err)
	}
}
