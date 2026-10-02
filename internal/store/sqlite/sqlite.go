package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/pkg/connector"
	_ "modernc.org/sqlite"
)

//go:embed migrations/001_init.sql
var initialSchema string

type Store struct{ db *sql.DB }

func Open(ctx context.Context, dataDir string) (*Store, error) {
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "findrail.db")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = file.Chmod(0600); err != nil {
		file.Close()
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := u.Query()
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.initialize(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) initialize(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	switch version {
	case 0:
		if _, err := tx.ExecContext(ctx, initialSchema); err != nil {
			return fmt.Errorf("initialize schema: %w", err)
		}
	case 1:
	default:
		return fmt.Errorf("unsupported index schema %d; use a compatible Findrail version", version)
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

type scan struct {
	tx       *sql.Tx
	sourceID string
	token    string
}

func (s *Store) BeginScan(ctx context.Context, source connector.Source) (ingest.Scan, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sources(id,kind,name,root) VALUES(?,?,?,?)
        ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,name=excluded.name,root=excluded.root`, source.ID, source.Kind, source.Name, source.Root); err != nil {
		tx.Rollback()
		return nil, err
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		tx.Rollback()
		return nil, err
	}
	return &scan{tx: tx, sourceID: source.ID, token: fmt.Sprintf("%x", b)}, nil
}

func (s *scan) Upsert(ctx context.Context, doc connector.Document) (bool, error) {
	if doc.SourceID != s.sourceID {
		return false, fmt.Errorf("document belongs to a different source")
	}
	var previous, title, uri, path, sourceID string
	err := s.tx.QueryRowContext(ctx, "SELECT content_hash,title,uri,path,source_id FROM documents WHERE id=?", doc.ID).Scan(&previous, &title, &uri, &path, &sourceID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && sourceID != s.sourceID {
		return false, fmt.Errorf("document ID already belongs to a different source")
	}
	if err == nil && previous == doc.Hash && title == doc.Title && uri == doc.URI && path == doc.Path {
		_, err := s.tx.ExecContext(ctx, "UPDATE documents SET modified_at=?,size_bytes=?,scan_token=? WHERE id=?", doc.ModifiedAt.Format(time.RFC3339Nano), doc.SizeBytes, s.token, doc.ID)
		return false, err
	}
	_, err = s.tx.ExecContext(ctx, `INSERT INTO documents(id,source_id,title,uri,path,content,content_hash,size_bytes,modified_at,scan_token)
        VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
        title=excluded.title,uri=excluded.uri,path=excluded.path,content=excluded.content,
        content_hash=excluded.content_hash,size_bytes=excluded.size_bytes,modified_at=excluded.modified_at,scan_token=excluded.scan_token`,
		doc.ID, doc.SourceID, doc.Title, doc.URI, doc.Path, doc.Content, doc.Hash, doc.SizeBytes, doc.ModifiedAt.Format(time.RFC3339Nano), s.token)
	return true, err
}

func (s *scan) Commit(ctx context.Context) (int, error) {
	r, err := s.tx.ExecContext(ctx, "DELETE FROM documents WHERE source_id=? AND scan_token<>?", s.sourceID, s.token)
	if err != nil {
		return 0, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := s.tx.ExecContext(ctx, "UPDATE sources SET last_indexed_at=? WHERE id=?", time.Now().UTC().Format(time.RFC3339Nano), s.sourceID); err != nil {
		return 0, err
	}
	if err := s.tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}

func (s *scan) Rollback() error { return s.tx.Rollback() }

func (s *Store) Search(ctx context.Context, request search.Request) (search.Response, error) {
	response := search.Response{Query: request.Query, Results: []search.Result{}}
	expression, err := search.Expression(request.Query)
	if err != nil {
		return response, err
	}
	if request.Limit < 1 || request.Limit > 100 {
		return response, search.ErrLimit
	}
	where := "documents_fts MATCH ?"
	args := []any{expression}
	if request.SourceID != "" {
		where += " AND d.source_id=?"
		args = append(args, request.SourceID)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents_fts JOIN documents d ON d.rowid=documents_fts.rowid WHERE `+where, args...).Scan(&response.Total); err != nil {
		return response, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,d.title,d.uri,d.path,d.source_id,s.name,s.kind,
        snippet(documents_fts,1,'[',']','…',32),-bm25(documents_fts,4.0,1.0)
        FROM documents_fts JOIN documents d ON d.rowid=documents_fts.rowid JOIN sources s ON s.id=d.source_id
        WHERE `+where+` ORDER BY bm25(documents_fts,4.0,1.0),d.id LIMIT ?`, append(args, request.Limit)...)
	if err != nil {
		return response, err
	}
	defer rows.Close()
	for rows.Next() {
		var result search.Result
		if err := rows.Scan(&result.ID, &result.Title, &result.URI, &result.Path, &result.SourceID, &result.SourceName, &result.SourceKind, &result.Snippet, &result.Score); err != nil {
			return response, err
		}
		response.Results = append(response.Results, result)
	}
	return response, rows.Err()
}

type SourceStatus struct {
	connector.Source
	Documents     int    `json:"documents"`
	LastIndexedAt string `json:"last_indexed_at"`
}

func (s *Store) Sources(ctx context.Context) ([]SourceStatus, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.kind,s.name,s.root,s.last_indexed_at,COUNT(d.id)
        FROM sources s LEFT JOIN documents d ON d.source_id=s.id GROUP BY s.id ORDER BY s.name,s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SourceStatus{}
	for rows.Next() {
		var source SourceStatus
		if err := rows.Scan(&source.ID, &source.Kind, &source.Name, &source.Root, &source.LastIndexedAt, &source.Documents); err != nil {
			return nil, err
		}
		result = append(result, source)
	}
	return result, rows.Err()
}

// ForgetSource removes a source and its searchable documents. Originals are
// untouched. This is a logical deletion, not forensic secure erasure.
func (s *Store) ForgetSource(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM sources WHERE id=?", id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("source not found")
	}
	return nil
}
