package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/Lephiziel/findrail/internal/config"
	"github.com/Lephiziel/findrail/internal/diagnostics"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
)

func runSourceReport(ctx context.Context, args []string, out, stderr io.Writer) error {
	fs := flag.NewFlagSet("source-report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("data-dir", "", "existing Findrail index directory")
	source := fs.String("source", "", "registered source ID")
	asJSON := fs.Bool("json", false, "output JSON")
	paths := fs.Bool("show-paths", false, "show retained safe relative examples")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("source-report does not accept positional arguments")
	}
	if *source == "" {
		return errors.New("source-report requires --source")
	}
	dataDir, err := config.DataDir(*dir)
	if err != nil {
		return err
	}
	store, err := sqlite.OpenReadOnlyForReport(ctx, dataDir)
	if err != nil {
		return fmt.Errorf("open existing index read-only: %w", err)
	}
	defer store.Close()
	result, err := store.SourceReport(ctx, *source, *paths)
	if errors.Is(err, sqlite.ErrSourceNotFound) {
		return fmt.Errorf("source %q not found", *source)
	}
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(out).Encode(result)
	}
	if available, _ := result["available"].(bool); !available {
		_, err = fmt.Fprintf(out, "Indexing report unavailable (%v).\n", result["availability"])
		return err
	}
	var operation, reportID string
	var indexed, skipped, pruned int64
	var reasons []diagnostics.Reason
	var examples []diagnostics.Example
	switch r := result["report"].(type) {
	case sqlite.ReportSummary:
		operation, reportID, indexed, skipped, pruned, reasons = r.Operation, r.ReportID, r.IndexedDocuments, r.SkippedFiles, r.PrunedDirectories, r.Reasons
	case diagnostics.Report:
		operation, reportID, indexed, skipped, pruned, reasons, examples = r.Operation, r.ID, r.IndexedDocuments, r.SkippedFiles, r.PrunedDirectories, r.Reasons, r.Examples
	default:
		return errors.New("indexing report has an unsupported response shape")
	}
	if _, err = fmt.Fprintf(out, "Committed %s report %s for %s: %d indexed, %d skipped files, %d pruned directories.\n", operation, reportID, *source, indexed, skipped, pruned); err != nil {
		return err
	}
	for _, reason := range reasons {
		if _, err = fmt.Fprintf(out, "  %s (%s): %d\n", reason.Code, reason.Unit, reason.Count); err != nil {
			return err
		}
	}
	for _, e := range examples {
		if _, err = fmt.Fprintf(out, "  %s: %s [%s]\n", safe(e.Path), e.Reason, e.Unit); err != nil {
			return err
		}
	}
	return nil
}
