package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
	"unicode"

	"github.com/Lephiziel/findrail/internal/config"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	mcptransport "github.com/Lephiziel/findrail/internal/transport/mcp"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type sourceFlags []string

func (f *sourceFlags) String() string { return fmt.Sprint([]string(*f)) }

func (f *sourceFlags) Set(value string) error {
	if err := validateMCPID(value); err != nil {
		return err
	}
	for _, current := range *f {
		if current == value {
			return fmt.Errorf("duplicate source %q", value)
		}
	}
	if len(*f) >= mcptransport.MaxAllowedSources {
		return fmt.Errorf("at most %d sources are allowed", mcptransport.MaxAllowedSources)
	}
	*f = append(*f, value)
	return nil
}

func runMCP(ctx context.Context, args []string, out, stderr io.Writer, version string) error {
	return runMCPWithIO(ctx, args, out, stderr, version, os.Stdin)
}

func runMCPWithIO(ctx context.Context, args []string, out, stderr io.Writer, version string, reader io.ReadCloser) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: findrail mcp --data-dir DIR --source ID [--source ID ...] [--max-results N] [--max-text-chars N] [--request-timeout DURATION]")
		fmt.Fprintln(stderr, "Run the local read-only MCP server over stdio. The index must already exist.")
	}
	dataDir := fs.String("data-dir", "", "existing Findrail index directory")
	var sources sourceFlags
	fs.Var(&sources, "source", "allowed source ID; repeat at least once")
	maxResults := fs.Int("max-results", mcptransport.DefaultMaxResults, "maximum search results, 1–20")
	maxTextChars := fs.Int("max-text-chars", mcptransport.DefaultMaxTextChars, "maximum evidence text characters, 256–32768")
	requestTimeout := fs.Duration("request-timeout", 5*time.Second, "backend request timeout, 1s–30s")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("mcp does not accept positional arguments")
	}
	if len(sources) == 0 {
		return errors.New("mcp requires at least one --source ID")
	}
	if *maxResults < 1 || *maxResults > mcptransport.MaxSearchResults {
		return fmt.Errorf("max-results must be between 1 and %d", mcptransport.MaxSearchResults)
	}
	if *maxTextChars < 256 || *maxTextChars > mcptransport.MaxEvidenceTextChars {
		return fmt.Errorf("max-text-chars must be between 256 and %d", mcptransport.MaxEvidenceTextChars)
	}
	if *requestTimeout < time.Second || *requestTimeout > 30*time.Second {
		return errors.New("request-timeout must be between 1s and 30s")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := config.DataDir(*dataDir)
	if err != nil {
		return err
	}
	store, err := sqlite.OpenReadOnly(ctx, dir)
	if err != nil {
		return fmt.Errorf("open read-only index: %w", err)
	}
	defer store.Close()
	registered, err := store.Sources(ctx)
	if err != nil {
		return fmt.Errorf("read sources: %w", err)
	}
	known := make(map[string]struct{}, len(registered))
	for _, source := range registered {
		known[source.ID] = struct{}{}
	}
	for _, id := range sources {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("source %q is not registered in the index", id)
		}
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	server, err := mcptransport.New(store, mcptransport.Config{
		AllowedSources: sources, MaxResults: *maxResults, MaxTextChars: *maxTextChars,
		RequestTimeout: *requestTimeout, Version: version, Logger: logger,
	})
	if err != nil {
		return err
	}
	writer, ok := out.(io.WriteCloser)
	if !ok {
		writer = nopWriteCloser{out}
	}
	if file, ok := reader.(*os.File); ok {
		stream, cleanup, err := prepareMCPFile(file)
		if err != nil {
			return fmt.Errorf("prepare MCP input: %w", err)
		}
		defer cleanup()
		reader = stream
	}
	if file, ok := writer.(*os.File); ok {
		stream, cleanup, err := prepareMCPFile(file)
		if err != nil {
			return fmt.Errorf("prepare MCP output: %w", err)
		}
		defer cleanup()
		writer = stream
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// SDK session shutdown drains active responses before closing the transport.
	// Close both pipes on cancellation so a stalled client cannot block that drain.
	stopClose := context.AfterFunc(runCtx, func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	defer stopClose()
	err = server.Run(runCtx, &sdkmcp.IOTransport{Reader: cancelOnEOFReader{reader, cancel}, Writer: writer, MaxLineLength: mcptransport.MaxFrameBytes})
	if runCtx.Err() != nil {
		return nil
	}
	return err
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

type cancelOnEOFReader struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r cancelOnEOFReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if errors.Is(err, io.EOF) {
		r.cancel()
	}
	return n, err
}

func validateMCPID(value string) error {
	if value == "" {
		return errors.New("source ID must not be empty")
	}
	if len([]rune(value)) > 128 {
		return errors.New("source ID must be at most 128 Unicode characters")
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("source ID must not contain whitespace or control characters")
		}
	}
	return nil
}
