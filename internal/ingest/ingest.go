package ingest

import (
	"context"
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

type Result struct {
	Source    connector.Source `json:"source"`
	Seen      int              `json:"seen"`
	Updated   int              `json:"updated"`
	Unchanged int              `json:"unchanged"`
	Removed   int              `json:"removed"`
	Skipped   int              `json:"skipped"`
}

func Run(ctx context.Context, store Store, source connector.Connector) (Result, error) {
	result := Result{Source: source.Source()}
	scan, err := store.BeginScan(ctx, result.Source)
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
	if err != nil {
		return result, fmt.Errorf("scan failed; previous index preserved: %w", err)
	}
	result.Removed, err = scan.Commit(ctx)
	return result, err
}
