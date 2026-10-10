package ingest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Lephiziel/findrail/internal/diagnostics"
	"github.com/Lephiziel/findrail/pkg/connector"
)

// Scan is atomic: Commit publishes a complete source inventory; Rollback leaves
// the previously indexed source intact.
type Scan interface {
	Upsert(context.Context, connector.Document) (bool, error)
	Commit(context.Context) (int, error)
	Rollback() error
}

type Store interface {
	BeginScan(context.Context, connector.Source) (Scan, error)
}

type RefreshStore interface {
	BeginRefresh(context.Context, connector.Source) (Scan, error)
}

type ConfigureStore interface {
	BeginConfigure(context.Context, connector.Source, string, int64, int64) (Scan, error)
}

var ErrSourceGone = errors.New("source is no longer registered")

type Result struct {
	Source      connector.Source     `json:"source"`
	Seen        int                  `json:"seen"`
	Updated     int                  `json:"updated"`
	Unchanged   int                  `json:"unchanged"`
	Removed     int                  `json:"removed"`
	Skipped     int                  `json:"skipped"`
	SkippedPDF  int                  `json:"skipped_pdf,omitempty"`
	SkippedDOCX int                  `json:"skipped_docx,omitempty"`
	Attempt     *diagnostics.Attempt `json:"attempt,omitempty"`
	Report      *diagnostics.Report  `json:"report,omitempty"`
}

type ScanFailure struct {
	Code  string
	Cause error
}

func (e *ScanFailure) Error() string {
	return e.Code + ": scan did not publish a complete snapshot; any previous committed snapshot is unchanged"
}
func (e *ScanFailure) Unwrap() error { return e.Cause }

type ProgressFunc func(processedDocuments, skippedEntries int, scanComplete, flush bool)

func Run(ctx context.Context, store Store, source connector.Connector) (Result, error) {
	return run(ctx, store.BeginScan, source, "initial_index")
}

func RunWithProgress(ctx context.Context, store Store, source connector.Connector, progress ProgressFunc) (Result, error) {
	return runProgress(ctx, store.BeginScan, source, "initial_index", progress)
}

// Refresh never registers a missing source; forgetting a source stops future refreshes.
func Refresh(ctx context.Context, store RefreshStore, source connector.Connector) (Result, error) {
	return run(ctx, store.BeginRefresh, source, "refresh")
}
func RefreshWithProgress(ctx context.Context, store RefreshStore, source connector.Connector, progress ProgressFunc) (Result, error) {
	return runProgress(ctx, store.BeginRefresh, source, "refresh", progress)
}

// Configure atomically changes source policy and replaces its snapshot. The
// storage guard binds publication to the source registration observed by the UI.
func Configure(ctx context.Context, store ConfigureStore, source connector.Connector, token string, revision, previousDOCXLimit int64) (Result, error) {
	return ConfigureWithProgress(ctx, store, source, token, revision, previousDOCXLimit, nil)
}
func ConfigureWithProgress(ctx context.Context, store ConfigureStore, source connector.Connector, token string, revision, previousDOCXLimit int64, progress ProgressFunc) (Result, error) {
	return runProgress(ctx, func(ctx context.Context, s connector.Source) (Scan, error) {
		return store.BeginConfigure(ctx, s, token, revision, previousDOCXLimit)
	}, source, "configure", progress)
}

func run(ctx context.Context, begin func(context.Context, connector.Source) (Scan, error), source connector.Connector, operation string) (Result, error) {
	return runProgress(ctx, begin, source, operation, nil)
}
func runProgress(ctx context.Context, begin func(context.Context, connector.Source) (Scan, error), source connector.Connector, operation string, progress ProgressFunc) (Result, error) {
	result := Result{Source: source.Source()}
	started := time.Now().UTC()
	scan, err := begin(ctx, result.Source)
	if err != nil {
		return result, err
	}
	defer scan.Rollback()
	emit := func(doc connector.Document) error {
		if doc.SourceID != result.Source.ID || doc.ID == "" {
			return fmt.Errorf("connector emitted an invalid document identity")
		}
		changed, err := scan.Upsert(ctx, doc)
		if err == nil {
			if changed {
				result.Updated++
			} else {
				result.Unchanged++
			}
		}
		if err == nil && progress != nil {
			progress(result.Updated+result.Unchanged, 0, false, false)
		}
		return err
	}
	var report connector.Report
	var payload diagnostics.Payload
	var hasDiagnostics bool
	if scanner, ok := source.(interface {
		ScanWithDiagnostics(context.Context, func(connector.Document) error) (connector.Report, diagnostics.Payload, error)
	}); ok {
		report, payload, err = scanner.ScanWithDiagnostics(ctx, emit)
		hasDiagnostics = true
	} else {
		report, err = source.Scan(ctx, emit)
	}
	result.Seen, result.Skipped = report.Seen, report.Skipped
	result.SkippedPDF = report.SkippedPDF
	result.SkippedDOCX = report.SkippedDOCX
	if progress != nil {
		progress(result.Updated+result.Unchanged, report.Skipped, err == nil, true)
	}
	if err != nil {
		result.Attempt = attemptSummary(result, operation, started, payload, hasDiagnostics, err)
		return result, &ScanFailure{Code: failureCode(err), Cause: err}
	}
	if hasDiagnostics {
		var idBytes [16]byte
		if _, err := rand.Read(idBytes[:]); err != nil {
			return result, err
		}
		r := &diagnostics.Report{FormatVersion: diagnostics.FormatVersion, ID: hex.EncodeToString(idBytes[:]), SourceID: result.Source.ID, SourceKind: result.Source.Kind, SnapshotID: hex.EncodeToString(idBytes[:]), Operation: operation, StartedAt: started, Committed: true, Complete: true, IndexedDocuments: int64(result.Updated + result.Unchanged), UpdatedDocuments: int64(result.Updated), UnchangedDocuments: int64(result.Unchanged), ObservedFiles: payload.ObservedFiles, ObservedEntries: payload.ObservedEntries, ObservedEntriesKnown: payload.ObservedEntriesKnown, ObservedDirectories: payload.ObservedDirectories, ObservedFilesKnown: payload.ObservedFilesKnown, ObservedDirectoriesKnown: payload.ObservedDirectoriesKnown, Reasons: payload.Reasons, Examples: payload.Examples, ExamplesOmitted: payload.ExamplesOmitted, RedactedSamples: payload.RedactedSamples, Coverage: payload.Coverage}
		for _, reason := range payload.Reasons {
			if reason.Unit == "file" {
				r.SkippedFiles += reason.Count
			}
			if reason.Unit == "directory" {
				r.PrunedDirectories += reason.Count
			}
			if reason.Unit == "entry" {
				r.SkippedEntries += reason.Count
			}
		}
		if setter, ok := scan.(interface{ SetReport(*diagnostics.Report) }); ok {
			setter.SetReport(r)
			result.Report = r
		}
	}
	result.Removed, err = scan.Commit(ctx)
	if err != nil {
		result.Attempt = attemptSummary(result, operation, started, payload, hasDiagnostics, err)
		result.Attempt.FailureCode = "publication_failed"
		result.Report = nil
		return result, &ScanFailure{Code: "publication_failed", Cause: err}
	}
	return result, err
}

func failureCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	return "scan_failed"
}
func attemptSummary(result Result, operation string, started time.Time, p diagnostics.Payload, hasDiagnostics bool, cause error) *diagnostics.Attempt {
	finished := time.Now().UTC()
	attempt := &diagnostics.Attempt{SourceID: result.Source.ID, SourceKind: result.Source.Kind, Operation: operation, StartedAt: started, FinishedAt: finished, DurationMillis: finished.Sub(started).Milliseconds(), ProcessedDocuments: int64(result.Updated + result.Unchanged), UpdatedDocuments: int64(result.Updated), UnchangedDocuments: int64(result.Unchanged), Committed: false, Complete: false, FailureCode: failureCode(cause), Coverage: "unsupported"}
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		attempt.AttemptID = hex.EncodeToString(b[:])
	}
	if hasDiagnostics {
		attempt.Overflow = p.Overflow
		attempt.Coverage = p.Coverage
		attempt.ObservedFiles = p.ObservedFiles
		attempt.ObservedEntries = p.ObservedEntries
		attempt.ObservedEntriesKnown = p.ObservedEntriesKnown
		attempt.ObservedFilesKnown = p.ObservedFilesKnown
		attempt.ObservedDirectories = p.ObservedDirectories
		attempt.ObservedDirectoriesKnown = p.ObservedDirectoriesKnown
		attempt.Reasons = p.Reasons
		attempt.Examples = p.Examples
		attempt.ExamplesOmitted = p.ExamplesOmitted
		attempt.RedactedSamples = p.RedactedSamples
		attempt.FailurePath = p.FailurePath
		for _, r := range p.Reasons {
			if r.Unit == "file" {
				attempt.SkippedFiles += r.Count
			}
			if r.Unit == "directory" {
				attempt.PrunedDirectories += r.Count
			}
			if r.Unit == "entry" {
				attempt.SkippedEntries += r.Count
			}
		}
	}
	return attempt
}
