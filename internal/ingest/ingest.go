package ingest

import (
	"context"
	"errors"
	"fmt"

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

var ErrSourceGone = errors.New("source is no longer registered")

type Result struct {
	Source      connector.Source `json:"source"`
	Seen        int              `json:"seen"`
	Updated     int              `json:"updated"`
	Unchanged   int              `json:"unchanged"`
	Removed     int              `json:"removed"`
	Skipped     int              `json:"skipped"`
	SkippedPDF  int              `json:"skipped_pdf,omitempty"`
	SkippedDOCX int              `json:"skipped_docx,omitempty"`
}

func Run(ctx context.Context, store Store, source connector.Connector) (Result, error) {
	return run(ctx, store.BeginScan, source)
}

// Refresh never registers a missing source; forgetting a source stops future refreshes.
func Refresh(ctx context.Context, store RefreshStore, source connector.Connector) (Result, error) {
	return run(ctx, store.BeginRefresh, source)
}

func run(ctx context.Context, begin func(context.Context, connector.Source) (Scan, error), source connector.Connector) (Result, error) {
	result := Result{Source: source.Source()}
	scan, err := begin(ctx, result.Source)
	if err != nil {
		return result, err
	}
	defer scan.Rollback()
	report, err := source.Scan(ctx, func(doc connector.Document) error {
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
		return err
	})
	result.Seen, result.Skipped = report.Seen, report.Skipped
	result.SkippedPDF = report.SkippedPDF
	result.SkippedDOCX = report.SkippedDOCX
	if err != nil {
		return result, fmt.Errorf("scan failed; previous index preserved: %w", err)
	}
	result.Removed, err = scan.Commit(ctx)
	return result, err
}
