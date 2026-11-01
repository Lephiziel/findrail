package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
)

// Model failures after QueryContext succeeds: a successful query start is not
// proof that scanning the complete SQLite result succeeded.
func init() { sql.Register("findrail-integrity-errors", integrityErrorDriver{}) }

type integrityErrorDriver struct{}
type integrityErrorConn struct{ mode string }
type integrityErrorRows struct {
	columns []string
	values  [][]driver.Value
	endErr  error
	pos     int
}

func (integrityErrorDriver) Open(mode string) (driver.Conn, error) {
	return integrityErrorConn{mode}, nil
}
func (integrityErrorConn) Close() error                        { return nil }
func (integrityErrorConn) Prepare(string) (driver.Stmt, error) { return nil, errors.ErrUnsupported }
func (integrityErrorConn) Begin() (driver.Tx, error)           { return nil, errors.ErrUnsupported }
func (c integrityErrorConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "quick_check") {
		r := &integrityErrorRows{columns: []string{"quick_check"}, values: [][]driver.Value{{"ok"}}}
		switch c.mode {
		case "quick_rows_error":
			r.endErr = errors.New("private database details")
		case "quick_scan_error":
			r.values = [][]driver.Value{{nil}}
		case "quick_empty":
			r.values = nil
		}
		return r, nil
	}
	r := &integrityErrorRows{columns: []string{"table", "rowid", "parent", "fkid"}, values: [][]driver.Value{{"documents", int64(1), "sources", int64(0)}}}
	if c.mode == "fk_rows_error" {
		r.endErr = errors.New("private database details")
	}
	if c.mode == "fk_scan_error" {
		r.values[0][1] = "private invalid row ID"
	}
	return r, nil
}
func (r *integrityErrorRows) Columns() []string { return r.columns }
func (r *integrityErrorRows) Close() error      { return nil }
func (r *integrityErrorRows) Next(out []driver.Value) error {
	if r.pos == len(r.values) {
		if r.endErr != nil {
			return r.endErr
		}
		return io.EOF
	}
	copy(out, r.values[r.pos])
	r.pos++
	return nil
}

func TestIntegrityResultReadFailuresRemainIncomplete(t *testing.T) {
	for _, mode := range []string{"quick_rows_error", "quick_scan_error", "quick_empty", "fk_rows_error", "fk_scan_error"} {
		t.Run(mode, func(t *testing.T) {
			db, err := sql.Open("findrail-integrity-errors", mode)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			r := (&Store{readers: db}).IntegrityCheck(context.Background())
			phase := r.QuickCheck
			if strings.HasPrefix(mode, "fk_") {
				phase = r.ForeignKey
			}
			if phase.Complete || phase.Healthy {
				t.Fatalf("failed result read claimed completion: %+v", phase)
			}
			for _, finding := range phase.Findings {
				if strings.Contains(finding, "private") {
					t.Fatalf("SQLite details leaked: %q", finding)
				}
			}
		})
	}
}

func TestIntegrityFindingLimitDoesNotClaimFullScan(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec("PRAGMA foreign_keys=OFF; INSERT INTO evidence_collections(id,title,revision,created_at,updated_at) VALUES('c','c',1,'now','now')"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := s.db.Exec("INSERT INTO evidence_collection_entries(collection_id,entry_id,ordinal,source_id,document_id,snapshot_json) VALUES('c',?,?,'missing','missing','{}')", string(rune('a'+i)), i); err != nil {
			t.Fatal(err)
		}
	}
	r := s.IntegrityCheck(context.Background())
	if !r.QuickCheck.Complete || !r.QuickCheck.Healthy {
		t.Fatalf("structural phase: %+v", r.QuickCheck)
	}
	if r.ForeignKey.Complete || r.ForeignKey.Healthy || len(r.ForeignKey.Findings) != integrityFindingLimit {
		t.Fatalf("finding limit claimed full scan: %+v", r.ForeignKey)
	}
}
