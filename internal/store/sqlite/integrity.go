package sqlite

import (
	"context"
	"database/sql"
	"time"
)

// IntegrityPhase contains only fixed, safe messages. SQLite-controlled values
// are never returned to diagnostic callers.
type IntegrityPhase struct {
	Complete bool
	Healthy  bool
	Findings []string
}

type IntegrityReport struct {
	QuickCheck IntegrityPhase
	ForeignKey IntegrityPhase
}

const integrityFindingLimit = 10

// IntegrityCheck performs bounded, read-only SQLite checks on an already-open
// read-only Store. It makes no claim about FTS/content consistency.
func (s *Store) IntegrityCheck(ctx context.Context) IntegrityReport {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result := IntegrityReport{}
	if s == nil || s.readers == nil {
		result.QuickCheck = IntegrityPhase{Findings: []string{"Read-only index access is unavailable."}}
		result.ForeignKey = IntegrityPhase{Findings: []string{"Read-only index access is unavailable."}}
		return result
	}
	result.QuickCheck = quickCheck(ctx, s.readers)
	if !result.QuickCheck.Complete || !result.QuickCheck.Healthy {
		result.ForeignKey = IntegrityPhase{Findings: []string{"Skipped because the structural check did not complete cleanly."}}
		return result
	}
	result.ForeignKey = foreignKeyCheck(ctx, s.readers)
	return result
}

func quickCheck(ctx context.Context, db *sql.DB) IntegrityPhase {
	phase := IntegrityPhase{Healthy: true, Findings: []string{}}
	rows, err := db.QueryContext(ctx, "PRAGMA quick_check(10)")
	if err != nil {
		return IntegrityPhase{Findings: []string{"Structural check could not be completed."}}
	}
	defer rows.Close()
	seen := false
	complete := true
	for rows.Next() {
		seen = true
		var message string
		if err := rows.Scan(&message); err != nil {
			complete = false
			phase.Healthy = false
			phase.Findings = append(phase.Findings, "Structural check could not be completed.")
			break
		}
		if message != "ok" {
			phase.Healthy = false
			if len(phase.Findings) < integrityFindingLimit {
				phase.Findings = append(phase.Findings, "SQLite reported a structural integrity issue.")
			}
		}
	}
	if err := rows.Err(); err != nil {
		complete = false
		phase.Healthy = false
		if len(phase.Findings) == 0 {
			phase.Findings = append(phase.Findings, "Structural check could not be completed.")
		}
	}
	if !seen && ctx.Err() == nil {
		complete = false
		phase.Healthy = false
		phase.Findings = append(phase.Findings, "Structural check returned no result.")
	}
	if len(phase.Findings) == integrityFindingLimit {
		complete = false
		phase.Findings[0] = "Structural check stopped at the finding limit; additional issues may remain."
	}
	phase.Complete = complete && ctx.Err() == nil
	if ctx.Err() != nil {
		phase.Healthy = false
		phase.Findings = []string{"Structural check was interrupted before completion."}
	}
	if phase.Healthy && phase.Complete {
		phase.Findings = []string{"SQLite structural quick check completed without reported issues."}
	}
	return phase
}

var knownIntegrityTables = map[string]bool{
	"sources": true, "documents": true, "pdf_pages": true,
	"scan_runs": true, "evidence_collections": true, "evidence_items": true,
	"evidence_collection_entries": true,
	"documents_fts":               true, "documents_fts_data": true,
	"documents_fts_idx": true, "documents_fts_docsize": true,
	"documents_fts_config": true,
}

func foreignKeyCheck(ctx context.Context, db *sql.DB) IntegrityPhase {
	phase := IntegrityPhase{Healthy: true, Findings: []string{}}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return IntegrityPhase{Findings: []string{"Foreign-key check could not be completed."}}
	}
	defer rows.Close()
	count := 0
	complete := true
	for rows.Next() {
		var table, parent string
		var rowid sql.NullInt64
		var constraint sql.NullInt64
		if err := rows.Scan(&table, &rowid, &parent, &constraint); err != nil {
			complete = false
			phase.Healthy = false
			phase.Findings = append(phase.Findings, "Foreign-key check could not be completed.")
			break
		}
		phase.Healthy = false
		count++
		if count <= integrityFindingLimit {
			if knownIntegrityTables[table] && knownIntegrityTables[parent] {
				phase.Findings = append(phase.Findings, "Foreign-key issue involves known tables "+table+" and "+parent+".")
			} else if knownIntegrityTables[table] {
				phase.Findings = append(phase.Findings, "Foreign-key issue involves known table "+table+".")
			} else {
				phase.Findings = append(phase.Findings, "A foreign-key issue was found in the index.")
			}
		}
		if count == integrityFindingLimit {
			complete = false
			phase.Findings[0] = "Foreign-key check stopped at the finding limit; additional issues may remain."
			break
		}
	}
	if err := rows.Err(); err != nil {
		complete = false
		phase.Healthy = false
		if len(phase.Findings) == 0 {
			phase.Findings = append(phase.Findings, "Foreign-key check could not be completed.")
		}
	}
	phase.Complete = complete && ctx.Err() == nil
	if ctx.Err() != nil {
		phase.Healthy = false
		phase.Findings = []string{"Foreign-key check was interrupted before completion."}
	}
	if phase.Healthy && phase.Complete {
		phase.Findings = []string{"Foreign-key check completed without reported issues."}
	}
	return phase
}
