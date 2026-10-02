package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Lephiziel/findrail/internal/config"
	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	transport "github.com/Lephiziel/findrail/internal/transport/http"
)

const help = `Findrail — find your knowledge, keep your sources.

Usage:
  findrail index [--data-dir DIR] [--max-bytes N] [--json] DIRECTORY
  findrail search [--data-dir DIR] [--limit N] [--source ID] [--json] QUERY
  findrail sources [--data-dir DIR] [--json]
  findrail forget [--data-dir DIR] SOURCE_ID
  findrail serve [--data-dir DIR] [--addr 127.0.0.1:7766]
  findrail version

Options must precede positional arguments. Run COMMAND --help for details.
This is an early development foundation. PDF, cloud connectors, file watching,
semantic search, and an MCP server are planned; they are not implemented yet.
`

func Run(ctx context.Context, args []string, out, stderr io.Writer, version string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, err := io.WriteString(out, help)
		return err
	}
	if args[0] == "version" {
		_, err := fmt.Fprintln(out, "findrail", version)
		return err
	}
	if args[0] != "index" && args[0] != "search" && args[0] != "sources" && args[0] != "serve" && args[0] != "forget" {
		return fmt.Errorf("unknown command %q; run findrail --help", args[0])
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", "", "directory for Findrail's private index")
	var jsonOutput bool
	maxBytes, limit, sourceID, addr := filesystem.DefaultMaxBytes, 20, "", "127.0.0.1:7766"
	switch args[0] {
	case "index":
		fs.Int64Var(&maxBytes, "max-bytes", maxBytes, "maximum bytes per text document")
		fs.BoolVar(&jsonOutput, "json", false, "output JSON")
	case "search":
		fs.IntVar(&limit, "limit", limit, "maximum search results, 1–100")
		fs.StringVar(&sourceID, "source", "", "filter results by source ID")
		fs.BoolVar(&jsonOutput, "json", false, "output JSON")
	case "sources":
		fs.BoolVar(&jsonOutput, "json", false, "output JSON")
	case "serve":
		fs.StringVar(&addr, "addr", addr, "loopback address for the local UI")
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
	if (args[0] == "sources" || args[0] == "serve") && fs.NArg() != 0 {
		return fmt.Errorf("%s does not accept positional arguments", args[0])
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
	switch args[0] {
	case "index":
		conn, err := filesystem.New(fs.Arg(0), maxBytes, dir)
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
		return err
	case "search":
		response, err := store.Search(ctx, search.Request{Query: strings.Join(fs.Args(), " "), SourceID: sourceID, Limit: limit})
		if err != nil {
			return err
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
	case "serve":
		if _, err := fmt.Fprintf(out, "Findrail local UI: http://%s\n", addr); err != nil {
			return err
		}
		return transport.Serve(ctx, addr, store)
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
