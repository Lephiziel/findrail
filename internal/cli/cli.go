package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Lephiziel/findrail/internal/config"
	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	pdfextract "github.com/Lephiziel/findrail/internal/extract/pdf"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	syncer "github.com/Lephiziel/findrail/internal/sync"
	transport "github.com/Lephiziel/findrail/internal/transport/http"
	"github.com/Lephiziel/findrail/pkg/connector"
)

const help = `Findrail — find your knowledge, keep your sources.

Usage:
  findrail index [--data-dir DIR] [--max-bytes N] [--max-pdf-bytes N] [--max-docx-bytes N] [--json] DIRECTORY
  findrail index-github [--data-dir DIR] [--ref REF] [--path PATH] [--max-bytes N] [--timeout 2m] [--json] OWNER/REPO
  findrail refresh-github [--data-dir DIR] [--timeout 2m] [--json] SOURCE_ID
  findrail start [--data-dir DIR] [--addr 127.0.0.1:7766] [--no-open] [DIRECTORY]
  findrail demo [--addr 127.0.0.1:7766] [--no-open]
  findrail search [--data-dir DIR] [--limit N] [--source ID] [--mode MODE] [--format FORMAT] [--path-prefix PATH] [--title-contains TEXT] [--json] QUERY
  findrail sources [--data-dir DIR] [--json]
  findrail source-report --data-dir DIR --source SOURCE_ID [--json] [--show-paths]
  findrail export-source --data-dir DIR --source ID --output FILE [--timeout 2m] [--json]
  findrail inspect-export [--show-paths] [--json] FILE
  findrail import-source --data-dir DIR --name NAME [--timeout 2m] [--json] FILE
  findrail forget [--data-dir DIR] SOURCE_ID
  findrail serve [--data-dir DIR] [--addr 127.0.0.1:7766] [--no-sync]
  findrail watch [--data-dir DIR] [--sync-interval 5m]
  findrail mcp --data-dir DIR --source ID [--source ID ...]
  findrail version
  findrail doctor [--data-dir DIR] [--json] [--show-paths]

Options must precede positional arguments. Run COMMAND --help for details.
Local alpha: text, Markdown, source code, text PDFs, preview and automatic refresh.
start includes local source management in source builds; serve and demo are read-only.
Public GitHub snapshots are manually refreshed. Source builds also include a read-only stdio MCP server.
`

func Run(ctx context.Context, args []string, out, stderr io.Writer, version string) error {
	if len(args) == 1 && args[0] == "__extract-pdf" {
		return pdfextract.Worker(ctx, os.Stdin, out)
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, err := io.WriteString(out, help)
		return err
	}
	if args[0] == "version" {
		_, err := fmt.Fprintln(out, "findrail", version)
		return err
	}
	if args[0] == "doctor" {
		return runDoctor(ctx, args[1:], out, stderr, version)
	}
	if args[0] == "source-report" {
		return runSourceReport(ctx, args[1:], out, stderr)
	}
	if args[0] == "export-source" || args[0] == "inspect-export" || args[0] == "import-source" {
		return runSnapshotCommand(ctx, args[0], args[1:], out, version)
	}
	if args[0] == "start" {
		return runStart(ctx, args[1:], out, stderr, openBrowser)
	}
	if args[0] == "mcp" {
		return runMCP(ctx, args[1:], out, stderr, version)
	}
	if args[0] == "demo" {
		return runDemo(ctx, args[1:], out, stderr, openBrowser, runStartReadOnly)
	}
	if args[0] == "index-github" || args[0] == "refresh-github" {
		return runGitHub(ctx, args[0], args[1:], out, stderr)
	}
	if args[0] != "index" && args[0] != "search" && args[0] != "sources" && args[0] != "serve" && args[0] != "watch" && args[0] != "forget" {
		return fmt.Errorf("unknown command %q; run findrail --help", args[0])
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", "", "directory for Findrail's private index")
	var jsonOutput bool
	maxBytes, limit, sourceID, addr := filesystem.DefaultMaxBytes, 20, "", "127.0.0.1:7766"
	mode, format, pathPrefix, titleContains := "literal", "all", "", ""
	maxPDFBytes := int64(16 << 20)
	maxDOCXBytes := int64(8 << 20)
	noSync := false
	syncInterval := 5 * time.Minute
	switch args[0] {
	case "index":
		fs.Int64Var(&maxBytes, "max-bytes", maxBytes, "maximum bytes per text document")
		fs.Int64Var(&maxPDFBytes, "max-pdf-bytes", maxPDFBytes, "maximum bytes per PDF; 0 disables PDFs, maximum 33554432")
		fs.Int64Var(&maxDOCXBytes, "max-docx-bytes", maxDOCXBytes, "maximum bytes per DOCX; 0 disables DOCX, maximum 16777216")
		fs.BoolVar(&jsonOutput, "json", false, "output JSON")
	case "search":
		fs.IntVar(&limit, "limit", limit, "maximum search results, 1–100")
		fs.StringVar(&sourceID, "source", "", "filter results by source ID")
		fs.StringVar(&mode, "mode", "literal", "query mode: literal or advanced")
		fs.StringVar(&format, "format", "all", "format: all, text, pdf, docx")
		fs.StringVar(&pathPrefix, "path-prefix", "", "relative indexed path prefix")
		fs.StringVar(&titleContains, "title-contains", "", "literal title substring")
		fs.BoolVar(&jsonOutput, "json", false, "output JSON")
	case "sources":
		fs.BoolVar(&jsonOutput, "json", false, "output JSON")
	case "serve":
		fs.StringVar(&addr, "addr", addr, "loopback address for the local UI")
		fs.BoolVar(&noSync, "no-sync", false, "serve the existing snapshot without automatic refresh")
		fs.DurationVar(&syncInterval, "sync-interval", syncInterval, "periodic full refresh, at least 1s")
	case "watch":
		fs.DurationVar(&syncInterval, "sync-interval", syncInterval, "periodic full refresh, at least 1s")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if args[0] == "index" && fs.NArg() != 1 {
		return fmt.Errorf("index requires one directory")
	}
	if args[0] == "forget" && fs.NArg() != 1 {
		return fmt.Errorf("forget requires one source ID")
	}
	if args[0] == "search" && fs.NArg() == 0 {
		return fmt.Errorf("search requires a query")
	}
	searchRequest := search.Request{}
	if args[0] == "search" {
		searchRequest = search.Request{Query: strings.Join(fs.Args(), " "), SourceID: sourceID, Limit: limit, Mode: mode, Format: format, PathPrefix: pathPrefix, TitleContains: titleContains}
		if limit < 1 || limit > 100 {
			return search.ErrLimit
		}
		if err := search.ValidateRequest(searchRequest); err != nil {
			return err
		}
	}
	if (args[0] == "sources" || args[0] == "serve" || args[0] == "watch") && fs.NArg() != 0 {
		return fmt.Errorf("%s does not accept positional arguments", args[0])
	}
	if syncInterval < time.Second {
		return fmt.Errorf("sync interval must be at least 1s")
	}
	dir, err := config.DataDir(*dataDir)
	if err != nil {
		return err
	}
	store, err := sqlite.Open(ctx, dir)
	if err != nil {
		return fmt.Errorf("open index: %w", err)
	}
	defer store.Close()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	newConnector := func(source connector.Source) (*filesystem.Connector, error) {
		return filesystem.NewWithOptions(source.Root, filesystem.Options{MaxTextBytes: source.MaxTextBytes, MaxPDFBytes: source.MaxPDFBytes, MaxDOCXBytes: source.MaxDOCXBytes, RegistrationToken: source.RegistrationToken, RegistrationRevision: source.RegistrationRevision, ExtractPDF: pdfextract.Extractor(executable)}, dir)
	}
	switch args[0] {
	case "index":
		if maxDOCXBytes < 0 || maxDOCXBytes > 16<<20 {
			return fmt.Errorf("max-docx-bytes must be 0–16777216")
		}
		conn, err := newConnector(connector.Source{Root: fs.Arg(0), MaxTextBytes: maxBytes, MaxPDFBytes: maxPDFBytes, MaxDOCXBytes: maxDOCXBytes})
		if err != nil {
			return err
		}
		result, err := ingest.Run(ctx, store, conn)
		if err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(out).Encode(result)
		}
		_, err = fmt.Fprintf(out, "Indexed %s: %d documents, %d updated, %d unchanged, %d removed, %d skipped.\nSource: %s\n", result.Source.Name, result.Seen, result.Updated, result.Unchanged, result.Removed, result.Skipped, result.Source.ID)
		if err == nil && result.SkippedPDF > 0 {
			_, err = fmt.Fprintf(out, "Skipped %d PDFs: no usable text or extraction limits. OCR is not included.\n", result.SkippedPDF)
		}
		if err == nil && result.SkippedDOCX > 0 {
			_, err = fmt.Fprintf(out, "Skipped %d DOCX files: disabled, unsupported, no body text, or extraction limit.\n", result.SkippedDOCX)
		}
		return err
	case "search":
		response, err := store.Search(ctx, searchRequest)
		if err != nil {
			var validation *search.ValidationError
			if errors.As(err, &validation) || errors.Is(err, search.ErrQuery) || errors.Is(err, search.ErrLimit) {
				return err
			}
			return fmt.Errorf("search unavailable")
		}
		if jsonOutput {
			return json.NewEncoder(out).Encode(response)
		}
		if _, err := fmt.Fprintf(out, "%d matches for %q\n", response.Total, response.Query); err != nil {
			return err
		}
		for _, result := range response.Results {
			if _, err := fmt.Fprintf(out, "\n%s · %s\n%s\n%s\n", safe(result.Title), safe(result.SourceName), safe(result.Snippet), safe(result.URI)); err != nil {
				return err
			}
		}
	case "sources":
		sources, err := store.Sources(ctx)
		if err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(out).Encode(map[string]any{"sources": sources})
		}
		for _, source := range sources {
			if _, err := fmt.Fprintf(out, "%s  %s  %d documents\n", source.ID, safe(source.Name), source.Documents); err != nil {
				return err
			}
		}
	case "serve", "watch":
		manager := syncer.New(store, syncer.Config{Interval: syncInterval, Factory: newConnector})
		if !noSync {
			child, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() { defer close(done); manager.Run(child) }()
			defer func() { cancel(); <-done }()
		}
		if args[0] == "watch" {
			fmt.Fprintln(out, "Refreshing registered sources. Press Ctrl+C to stop.")
			<-ctx.Done()
			return nil
		}
		if _, err := fmt.Fprintf(out, "Findrail local UI: http://%s\n", addr); err != nil {
			return err
		}
		return transport.Serve(ctx, addr, store, transport.WithSyncStatus(!noSync, manager.Status))
	case "forget":
		if err := store.ForgetSource(ctx, fs.Arg(0)); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "Source removed from the index. Original files are unchanged.")
		return err
	}
	return nil
}

// safe prevents indexed text and filenames from injecting terminal controls.
func safe(text string) string {
	return strings.Map(func(r rune) rune {
		if (r < 32 && r != '\n' && r != '\t') || (r >= 127 && r <= 159) {
			return -1
		}
		return r
	}, text)
}
