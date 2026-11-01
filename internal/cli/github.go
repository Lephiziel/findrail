package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Lephiziel/findrail/internal/config"
	githubconnector "github.com/Lephiziel/findrail/internal/connectors/github"
	"github.com/Lephiziel/findrail/internal/diagnostics"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
)

type githubCommandResult struct {
	Source      any                        `json:"source"`
	Seen        int                        `json:"seen"`
	Updated     int                        `json:"updated"`
	Unchanged   int                        `json:"unchanged"`
	Removed     int                        `json:"removed"`
	Skipped     int                        `json:"skipped"`
	GitHub      sqlite.GitHubSource        `json:"github"`
	SkipReasons githubconnector.SkipCounts `json:"skip_reasons"`
	Report      any                        `json:"report,omitempty"`
}

func runGitHub(ctx context.Context, command string, args []string, out, stderr io.Writer) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", "", "directory for Findrail's private index")
	timeout := fs.Duration("timeout", 2*time.Minute, "overall operation deadline, 1s–5m")
	jsonOutput := fs.Bool("json", false, "output one JSON object")
	reportOutput := fs.Bool("report", false, "include committed indexing diagnostics")
	showPaths := fs.Bool("show-paths", false, "include bounded relative-path examples with --report")
	ref, pathValue := "", ""
	maxBytes := githubconnector.DefaultMaxBytes
	if command == "index-github" {
		fs.StringVar(&ref, "ref", "", "branch, tag, heads/NAME, tags/NAME, or full commit SHA")
		fs.StringVar(&pathValue, "path", "", "relative repository directory")
		fs.Int64Var(&maxBytes, "max-bytes", maxBytes, "maximum bytes per text file, 1–8388608")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("%s requires exactly one argument", command)
	}
	if *timeout < time.Second || *timeout > 5*time.Minute {
		return errors.New("timeout must be between 1s and 5m")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var sel githubconnector.Selection
	var expected *sqlite.GitHubSource
	var sourceID string
	dir, err := config.DataDir(*dataDir)
	if err != nil {
		return err
	}
	if command == "index-github" {
		owner, repo, err := githubconnector.ParseRepository(fs.Arg(0))
		if err != nil {
			return err
		}
		sel, err = githubconnector.SelectionFrom(owner, repo, ref, pathValue, maxBytes)
		if err != nil {
			return err
		}
	} else if err := githubconnector.ValidateSourceID(fs.Arg(0)); err != nil {
		return err
	}
	child, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	fail := func(code string, cause error) error {
		if *jsonOutput {
			_ = json.NewEncoder(out).Encode(map[string]any{"committed": false, "complete": false, "attempt": githubAttempt(command, code, cause, *showPaths)})
		}
		return &ingest.ScanFailure{Code: code, Cause: cause}
	}
	if command == "refresh-github" {
		operationStarted := time.Now().UTC()
		store, err := sqlite.Open(child, dir)
		if err != nil {
			return fail("index_unavailable", err)
		}
		defer store.Close()
		g, err := store.GitHubSource(child, fs.Arg(0))
		if err != nil {
			return fail("source_unavailable", err)
		}
		expected = &g
		sourceID = g.SourceID
		sel = githubconnector.Selection{Owner: g.Owner, Repo: g.Repo, RefMode: g.RefMode, RefValue: g.RefValue, Path: g.SelectedPath, MaxBytes: g.MaxBytes}
		snapshot, err := githubconnector.NewClient().Prepare(child, sel)
		if err != nil {
			store.RecordGitHubError(context.Background(), g, failureCode(err))
			return fail(failureCode(err), err)
		}
		if snapshot.Source().ID != sourceID || snapshot.Metadata().RepositoryID != g.RepositoryID {
			store.RecordGitHubError(context.Background(), g, "repository identity changed")
			return fail("stale_source", errors.New("repository identity changed"))
		}
		snapshot.SetStartedAt(operationStarted)
		return publishGitHub(child, store, snapshot, expected, *jsonOutput, *reportOutput, *showPaths, out)
	}
	client := githubconnector.NewClient()
	operationStarted := time.Now().UTC()
	// Resolve repository identity before opening storage or resolving the
	// selected commit. A slow ref request must not permit a forgotten source
	// or a newer published revision to be overwritten.
	repo, err := client.ResolveRepository(child, sel)
	if err != nil {
		return fail(failureCode(err), err)
	}
	store, err := sqlite.Open(child, dir)
	if err != nil {
		return fail("index_unavailable", err)
	}
	defer store.Close()
	identity := githubconnector.SourceID(repo.ID, sel)
	if g, e := store.GitHubSource(child, identity); e == nil {
		expected = &g
	} else if !errors.Is(e, ingest.ErrSourceGone) {
		return fail("index_unavailable", e)
	}
	meta, err := client.ResolveCommit(child, sel, repo)
	if err != nil {
		return fail(failureCode(err), err)
	}
	snapshot, err := client.PrepareResolved(child, sel, meta)
	if err != nil {
		return fail(failureCode(err), err)
	}
	snapshot.SetStartedAt(operationStarted)
	return publishGitHub(child, store, snapshot, expected, *jsonOutput, *reportOutput, *showPaths, out)
}

func publishGitHub(ctx context.Context, store *sqlite.Store, snapshot *githubconnector.Snapshot, expected *sqlite.GitHubSource, jsonOutput, reportOutput, showPaths bool, out io.Writer) error {
	r, g, err := store.PublishGitHub(ctx, snapshot, expected)
	if err != nil {
		if jsonOutput {
			_ = json.NewEncoder(out).Encode(map[string]any{"committed": false, "complete": false, "attempt": map[string]any{"operation": "github_publication", "committed": false, "failure_code": failureCode(err)}})
		}
		return &ingest.ScanFailure{Code: failureCode(err), Cause: err}
	}
	result := githubCommandResult{Source: r.Source, Seen: r.Seen, Updated: r.Updated, Unchanged: r.Unchanged, Removed: r.Removed, Skipped: r.Skipped, GitHub: g, SkipReasons: snapshot.SkipCounts()}
	if reportOutput {
		if value, e := store.SourceReport(ctx, r.Source.ID, showPaths); e == nil {
			result.Report = value["report"]
		}
	}
	if jsonOutput {
		return json.NewEncoder(out).Encode(result)
	}
	_, err = fmt.Fprintf(out, "Indexed %s at %.12s: %d documents, %d updated, %d unchanged, %d removed, %d skipped.\nSource: %s\n", r.Source.Name, g.SHA, r.Seen, r.Updated, r.Unchanged, r.Removed, r.Skipped, r.Source.ID)
	if err == nil && reportOutput && result.Report != nil {
		switch report := result.Report.(type) {
		case sqlite.ReportSummary:
			_, err = fmt.Fprintf(out, "Committed report %s: %d indexed, %d skipped files.\n", report.ReportID, report.IndexedDocuments, report.SkippedFiles)
		case diagnostics.Report:
			_, err = fmt.Fprintf(out, "Committed report %s: %d indexed, %d skipped files.\n", report.ID, report.IndexedDocuments, report.SkippedFiles)
		}
	}
	return err
}

func failureCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	var prep *githubconnector.PreparationFailure
	if errors.As(err, &prep) {
		return "archive_preparation_failed"
	}
	var scan *ingest.ScanFailure
	if errors.As(err, &scan) {
		return scan.Code
	}
	return "operation_failed"
}

func githubAttempt(operation, code string, cause error, showPaths bool) map[string]any {
	var idBytes [16]byte
	attemptID := ""
	if _, err := rand.Read(idBytes[:]); err == nil {
		attemptID = hex.EncodeToString(idBytes[:])
	}
	result := map[string]any{"attempt_id": attemptID, "operation": operation, "committed": false, "complete": false, "failure_code": code}
	var prep *githubconnector.PreparationFailure
	if errors.As(cause, &prep) {
		p := prep.Diagnostics
		result["processed_documents"] = p.ProcessedDocuments
		result["observed_files"] = p.ObservedFiles
		result["observed_files_known"] = p.ObservedFilesKnown
		result["observed_entries"] = p.ObservedEntries
		result["observed_entries_known"] = p.ObservedEntriesKnown
		result["observed_directories"] = p.ObservedDirectories
		result["observed_directories_known"] = p.ObservedDirectoriesKnown
		result["skipped_files"] = int64(0)
		for _, r := range p.Reasons {
			if r.Unit == "file" {
				result["skipped_files"] = result["skipped_files"].(int64) + r.Count
			}
		}
		result["reasons"] = p.Reasons
		result["skipped_entries"] = int64(0)
		for _, r := range p.Reasons {
			if r.Unit == "entry" {
				result["skipped_entries"] = result["skipped_entries"].(int64) + r.Count
			}
		}
		result["examples_omitted"] = p.ExamplesOmitted
		result["redacted_samples"] = p.RedactedSamples
		result["coverage"] = p.Coverage
		result["overflow"] = p.Overflow
		if showPaths {
			result["examples"] = p.Examples
			if p.FailurePath != "" {
				result["failure_path"] = p.FailurePath
			}
		}
	}
	return result
}
