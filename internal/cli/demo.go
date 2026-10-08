package cli

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	transport "github.com/Lephiziel/findrail/internal/transport/http"
)

//go:embed demo-files/*
var demoFiles embed.FS

type startCommand func(context.Context, []string, io.Writer, io.Writer, func(string) error) error

// runDemo owns a new temporary workspace for each invocation. It never resolves
// the user's normal data directory, and removes its files after start shuts down.
func runDemo(ctx context.Context, args []string, out, stderr io.Writer, open func(string) error, start startCommand) (err error) {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: findrail demo [OPTIONS]\nTry three fictional documents in a temporary workspace. Ctrl+C stops the demo.\nThe demo does not use your normal index or read personal folders.")
		fs.PrintDefaults()
	}
	addr := fs.String("addr", "127.0.0.1:7766", "loopback address for the local UI")
	noOpen := fs.Bool("no-open", false, "print the UI URL without opening a browser")
	interval := fs.Duration("sync-interval", 5*time.Minute, "periodic full refresh, at least 1s")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("demo does not accept a directory; use findrail start DIRECTORY for your own files")
	}
	if *interval < time.Second {
		return fmt.Errorf("sync interval must be at least 1s")
	}
	if err := transport.ValidateAddress(*addr); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	workspace, err := os.MkdirTemp("", "findrail-demo-")
	if err != nil {
		return fmt.Errorf("create demo workspace: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(workspace); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove demo workspace %q: %w", workspace, cleanupErr))
		}
	}()
	documents := filepath.Join(workspace, "documents")
	if err := os.Mkdir(documents, 0700); err != nil {
		return fmt.Errorf("create demo documents: %w", err)
	}
	entries, err := demoFiles.ReadDir("demo-files")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		content, err := demoFiles.ReadFile("demo-files/" + entry.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(documents, entry.Name()), content, 0600); err != nil {
			return fmt.Errorf("write demo document: %w", err)
		}
	}
	if _, err := fmt.Fprintf(out, "Demo documents: %s\nSearch for idempotency, then preview webhook-runbook.pdf on page 2.\nTo try live refresh, edit retry-notes.md in this folder: replace amber with cobalt, save, then search for cobalt.\nThe demo workspace is removed when Findrail stops normally.\n", documents); err != nil {
		return err
	}
	startArgs := []string{"--data-dir", filepath.Join(workspace, "index"), "--addr", *addr, "--sync-interval", interval.String()}
	if *noOpen {
		startArgs = append(startArgs, "--no-open")
	}
	startArgs = append(startArgs, documents)
	return start(ctx, startArgs, out, stderr, open)
}
