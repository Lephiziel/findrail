package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Lephiziel/findrail/internal/config"
	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	pdfextract "github.com/Lephiziel/findrail/internal/extract/pdf"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/sourceapp"
	"github.com/Lephiziel/findrail/internal/sourcecoord"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	syncer "github.com/Lephiziel/findrail/internal/sync"
	transport "github.com/Lephiziel/findrail/internal/transport/http"
	"github.com/Lephiziel/findrail/pkg/connector"
)

// runStart composes the existing index and server workflows. Browser launching
// is injected so tests and headless clients never launch desktop applications.
func runStart(ctx context.Context, args []string, out, stderr io.Writer, open func(string) error) error {
	return runStartWithManagement(ctx, args, out, stderr, open, true)
}
func runStartReadOnly(ctx context.Context, args []string, out, stderr io.Writer, open func(string) error) error {
	return runStartWithManagement(ctx, args, out, stderr, open, false)
}
func runStartWithManagement(ctx context.Context, args []string, out, stderr io.Writer, open func(string) error, management bool) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: findrail start [OPTIONS] [DIRECTORY]\nIndex a folder, manage sources, serve local search, and open the browser.\nWithout DIRECTORY, resume registered sources or start with an empty index. Options precede DIRECTORY.")
		fs.PrintDefaults()
	}
	dataDir := fs.String("data-dir", "", "directory for Findrail's private index")
	addr := fs.String("addr", "127.0.0.1:7766", "loopback address for the local UI")
	noOpen := fs.Bool("no-open", false, "print the UI URL without opening a browser")
	maxBytes := fs.Int64("max-bytes", filesystem.DefaultMaxBytes, "maximum bytes per text document in DIRECTORY")
	maxPDFBytes := fs.Int64("max-pdf-bytes", 16<<20, "maximum bytes per PDF in DIRECTORY; 0 disables PDFs")
	interval := fs.Duration("sync-interval", 5*time.Minute, "periodic full refresh, at least 1s")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("start accepts at most one directory")
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
	if fs.NArg() == 0 {
		var limitSet bool
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "max-bytes" || f.Name == "max-pdf-bytes" {
				limitSet = true
			}
		})
		if limitSet {
			return fmt.Errorf("document size limits require a directory; stored folders keep their existing limits")
		}
	}
	dir, err := config.DataDir(*dataDir)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	factory := func(source connector.Source) (*filesystem.Connector, error) {
		return filesystem.NewWithOptions(source.Root, filesystem.Options{
			MaxTextBytes: source.MaxTextBytes, MaxPDFBytes: source.MaxPDFBytes,
			ExtractPDF: pdfextract.Extractor(executable),
		}, dir)
	}
	var selected *filesystem.Connector
	if fs.NArg() == 1 {
		selected, err = factory(connector.Source{Root: fs.Arg(0), MaxTextBytes: *maxBytes, MaxPDFBytes: *maxPDFBytes})
		if err != nil {
			return fmt.Errorf("choose folder: %w", err)
		}
	} else {
		if _, err := os.Stat(filepath.Join(dir, "findrail.db")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("open index: %w", err)
		}
	}
	store, err := sqlite.Open(ctx, dir)
	if err != nil {
		return fmt.Errorf("open index: %w", err)
	}
	defer store.Close()
	if selected != nil {
		if _, err := fmt.Fprintf(out, "Indexing %q...\n", selected.Source().Root); err != nil {
			return err
		}
		result, err := ingest.Run(ctx, store, selected)
		if err != nil {
			return fmt.Errorf("index folder: %w", err)
		}
		if _, err := fmt.Fprintf(out, "Indexed %d documents: %d updated, %d unchanged, %d removed, %d skipped.\n", result.Seen, result.Updated, result.Unchanged, result.Removed, result.Skipped); err != nil {
			return err
		}
		if result.SkippedPDF > 0 {
			if _, err := fmt.Fprintf(out, "Skipped %d PDFs: no usable text or extraction limits. OCR is not included.\n", result.SkippedPDF); err != nil {
				return err
			}
		}
	}
	child, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	coordinator := sourcecoord.New()
	manager := syncer.New(store, syncer.Config{Interval: *interval, Factory: factory, Coordinator: coordinator})
	go func() { defer close(done); manager.Run(child) }()
	defer func() { cancel(); <-done }()
	var opts []transport.Option
	opts = append(opts, transport.WithSyncStatus(true, manager.Status))
	var app *sourceapp.App
	if management {
		app = sourceapp.NewWithCoordinator(child, store, dir, executable, coordinator)
		opts = append(opts, transport.WithManagement(app))
	}
	serveErr := transport.Serve(ctx, *addr, store, append(opts,
		transport.WithReady(func(url string) error {
			if ctx.Err() != nil {
				return nil
			}
			if _, err := fmt.Fprintf(out, "Findrail local UI: %s\nPress Ctrl+C to stop.\n", url); err != nil {
				return err
			}
			if !*noOpen {
				if err := open(url); err != nil {
					fmt.Fprintf(stderr, "Could not start the browser opener: %v. Open %s manually.\n", err, url)
				}
			}
			return nil
		}),
	)...)
	if app == nil {
		return serveErr
	}
	shutdown, stopShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopShutdown()
	if err := app.Close(shutdown); err != nil {
		// The app has canceled every task. Do not close the store while a bounded
		// extraction/rollback is still touching it, even if graceful wait elapsed.
		if waitErr := app.Close(context.Background()); waitErr != nil {
			return errors.Join(fmt.Errorf("stop source jobs: %w", err), waitErr)
		}
		return fmt.Errorf("source jobs exceeded the graceful shutdown window and were drained: %w", err)
	}
	return serveErr
}
