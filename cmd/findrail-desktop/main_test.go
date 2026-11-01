package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pdfextract "github.com/Lephiziel/findrail/internal/extract/pdf"
)

func TestDesktopExecutablePreservesVersionAndPDFWorker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	name := "findrail-desktop"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	build := exec.CommandContext(ctx, "go", "build", "-ldflags", "-X main.version=desktop-test", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build launcher: %v\n%s", err, out)
	}
	if out, err := exec.CommandContext(ctx, binary, "version").CombinedOutput(); err != nil || !strings.Contains(string(out), "desktop-test") {
		t.Fatalf("launcher version: %v %s", err, out)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "demo", "webhook-runbook.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	pages, err := pdfextract.Extractor(binary)(ctx, bytes.NewReader(data), int64(len(data)))
	if err != nil || len(pages) != 2 || !strings.Contains(strings.ToLower(pages[1].Text), "idempotency") {
		t.Fatalf("desktop PDF child: pages=%+v err=%v", pages, err)
	}
}
