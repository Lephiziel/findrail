package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Lephiziel/findrail/internal/config"
	"github.com/Lephiziel/findrail/internal/evaluation"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
)

var ErrMismatchedResults = errors.New("evaluation results do not match expected paths")

func runEvaluate(ctx context.Context, args []string, out, stderr io.Writer) error {
	fs := flag.NewFlagSet("evaluate", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var queriesFile string
	fs.StringVar(&queriesFile, "queries", "", "Path to evaluation queries JSON")

	var dataDir string
	fs.StringVar(&dataDir, "data-dir", "", "directory for Findrail's private index")

	var sourceID string
	fs.StringVar(&sourceID, "source", "", "Source ID to evaluate")

	var jsonOutput bool
	fs.BoolVar(&jsonOutput, "json", false, "output JSON")

	err := fs.Parse(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("positional args are not allowed")
	}

	if queriesFile == "" {
		return errors.New("--queries is required")
	}
	if sourceID == "" {
		return errors.New("--source is required")
	}

	file, fileErr := os.Open(queriesFile)
	if fileErr != nil {
		return fmt.Errorf("open queries: %w", fileErr)
	}
	defer file.Close()

	cases, casesErr := evaluation.LoadCases(file)
	if casesErr != nil {
		return fmt.Errorf("load queries: %w", casesErr)
	}

	dataDirPath, pathErr := config.DataDir(dataDir)
	if pathErr != nil {
		return fmt.Errorf("resolve data directory: %w", pathErr)
	}

	path := filepath.Join(dataDirPath, "findrail.db")

	info, fileErr := os.Stat(path)
	if fileErr != nil {
		return fmt.Errorf("stat index %q: %w", path, fileErr)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("index %q is not a regular file", path)
	}

	store, err := sqlite.OpenReadOnly(ctx, dataDirPath)
	if err != nil {
		return fmt.Errorf("open index: %w", err)
	}
	defer store.Close()

	sources, sourcesErr := store.Sources(ctx)
	if sourcesErr != nil {
		return fmt.Errorf("list sources: %w", sourcesErr)
	}

	found := false

	for _, source := range sources {
		if source.ID == sourceID {
			found = true
			break
		}

	}
	if !found {
		return fmt.Errorf("source %q not found", sourceID)
	}

	report, reportErr := evaluation.Run(ctx, store, cases, sourceID)
	if reportErr != nil {
		return fmt.Errorf("evaluate: %w", reportErr)
	}

	if jsonOutput {
		encoder := json.NewEncoder(out)
		err := encoder.Encode(report)
		if err != nil {
			return fmt.Errorf("write JSON report: %w", err)
		}
	} else {
		_, err := fmt.Fprintf(out, "%d cases: %d passed, %d failed\n", report.Total, report.Passed, report.Failed)
		if err != nil {
			return fmt.Errorf("write report: %w", err)
		}

		for _, result := range report.Results {
			if result.Passed {
				continue
			}
			safeMissingPaths := make([]string, len(result.MissingPaths))
			for i, p := range result.MissingPaths {
				safeMissingPaths[i] = safe(p)
			}

			safeUnexpectedPaths := make([]string, len(result.UnexpectedPaths))
			for i, p := range result.UnexpectedPaths {
				safeUnexpectedPaths[i] = safe(p)
			}

			_, err := fmt.Fprintf(
				out,
				"query: %q, missing_paths=%q, unexpected_paths=%q\n",
				safe(result.Query), safeMissingPaths, safeUnexpectedPaths,
			)
			if err != nil {
				return fmt.Errorf("write report: %w", err)
			}
		}
	}
	if report.Failed > 0 {
		return ErrMismatchedResults
	}
	return nil
}
