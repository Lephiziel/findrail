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

//go:embed migrations/002_local_alpha.sql
var alphaSchema string

type Store struct{ db, readers *sql.DB }

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
	q.Add("_pragma", "query_only(1)")
	u.RawQuery = q.Encode()
	s.readers, err = sql.Open("sqlite", u.String())
	if err != nil {
		db.Close()
		return nil, err
	}
	s.readers.SetMaxOpenConns(4)
	if err := s.readers.PingContext(ctx); err != nil {
		s.Close()
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
		fallthrough
	case 1:
		if _, err := tx.ExecContext(ctx, alphaSchema); err != nil {
			return fmt.Errorf("migrate index: %w", err)
		}
	case 2:
	default:
		return fmt.Errorf("unsupported index schema %d; use a compatible Findrail version", version)
	}
	return tx.Commit()
}

func (s *Store) Close() error { return errors.Join(s.readers.Close(), s.db.Close()) }

type scan struct {
	tx       *sql.Tx
	sourceID string
	token    string
}

func (s *Store) BeginScan(ctx context.Context, source connector.Source) (ingest.Scan, error) {
	return s.begin(ctx, source, true)
}

func (s *Store) BeginRefresh(ctx context.Context, source connector.Source) (ingest.Scan, error) {
	return s.begin(ctx, source, false)
}

func (s *Store) begin(ctx context.Context, source connector.Source, create bool) (ingest.Scan, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if create {
		maxText := source.MaxTextBytes
		if maxText == 0 {
			maxText = 1 << 20
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sources(id,kind,name,root,max_text_bytes,max_pdf_bytes) VALUES(?,?,?,?,?,?)
        ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,name=excluded.name,root=excluded.root,max_text_bytes=excluded.max_text_bytes,max_pdf_bytes=excluded.max_pdf_bytes`, source.ID, source.Kind, source.Name, source.Root, maxText, source.MaxPDFBytes); err != nil {
			tx.Rollback()
			return nil, err
		}
	} else {
		r, err := tx.ExecContext(ctx, `UPDATE sources SET name=name WHERE id=? AND kind=? AND root=? AND max_text_bytes=? AND max_pdf_bytes=?`, source.ID, source.Kind, source.Root, source.MaxTextBytes, source.MaxPDFBytes)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		n, err := r.RowsAffected()
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		if n != 1 {
			tx.Rollback()
			return nil, ingest.ErrSourceGone
		}
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
	if doc.MediaType == "" {
		doc.MediaType = "text/plain"
	}
	var previous, title, uri, path, sourceID, mediaType string
	err := s.tx.QueryRowContext(ctx, "SELECT content_hash,title,uri,path,source_id,media_type FROM documents WHERE id=?", doc.ID).Scan(&previous, &title, &uri, &path, &sourceID, &mediaType)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && sourceID != s.sourceID {
		return false, fmt.Errorf("document ID already belongs to a different source")
	}
	if err == nil && previous == doc.Hash && title == doc.Title && uri == doc.URI && path == doc.Path && mediaType == doc.MediaType {
		_, err := s.tx.ExecContext(ctx, "UPDATE documents SET modified_at=?,size_bytes=?,scan_token=? WHERE id=?", doc.ModifiedAt.Format(time.RFC3339Nano), doc.SizeBytes, s.token, doc.ID)
		return false, err
	}
	_, err = s.tx.ExecContext(ctx, `INSERT INTO documents(id,source_id,title,uri,path,content,content_hash,size_bytes,modified_at,scan_token,media_type,page_count)
        VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
        title=excluded.title,uri=excluded.uri,path=excluded.path,content=excluded.content,
        content_hash=excluded.content_hash,size_bytes=excluded.size_bytes,modified_at=excluded.modified_at,scan_token=excluded.scan_token,media_type=excluded.media_type,page_count=excluded.page_count`,
		doc.ID, doc.SourceID, doc.Title, doc.URI, doc.Path, doc.Content, doc.Hash, doc.SizeBytes, doc.ModifiedAt.Format(time.RFC3339Nano), s.token, doc.MediaType, len(doc.Pages))
	if err != nil {
		return false, err
	}
	if _, err := s.tx.ExecContext(ctx, "DELETE FROM document_pages WHERE document_id=?", doc.ID); err != nil {
		return false, err
	}
	for i, page := range doc.Pages {
		if page.Number != i+1 {
			return false, fmt.Errorf("invalid PDF page sequence")
		}
		if _, err := s.tx.ExecContext(ctx, "INSERT INTO document_pages(document_id,page_number,content) VALUES(?,?,?)", doc.ID, page.Number, page.Text); err != nil {
			return false, err
		}
	}
	return true, nil
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
	tx, err := s.readers.BeginTx(ctx, nil)
	if err != nil {
		return response, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents_fts JOIN documents d ON d.rowid=documents_fts.rowid WHERE `+where, args...).Scan(&response.Total); err != nil {
		return response, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.id,d.title,d.uri,d.path,d.source_id,s.name,s.kind,
        snippet(documents_fts,1,'[',']','…',32),-bm25(documents_fts,4.0,1.0)
        ,d.media_type,d.page_count
        FROM documents_fts JOIN documents d ON d.rowid=documents_fts.rowid JOIN sources s ON s.id=d.source_id
        WHERE `+where+` ORDER BY bm25(documents_fts,4.0,1.0),d.id LIMIT ?`, append(args, request.Limit)...)
	if err != nil {
		return response, err
	}
	defer rows.Close()
	for rows.Next() {
		var result search.Result
		if err := rows.Scan(&result.ID, &result.Title, &result.URI, &result.Path, &result.SourceID, &result.SourceName, &result.SourceKind, &result.Snippet, &result.Score, &result.MediaType, &result.PageCount); err != nil {
			return response, err
		}
		response.Results = append(response.Results, result)
	}
	if err := rows.Err(); err != nil {
		return response, err
	}
	if err := rows.Close(); err != nil {
		return response, err
	}
	pageExpression := strings.ReplaceAll(expression, " AND ", " OR ")
	for i := range response.Results {
		r := &response.Results[i]
		if r.PageCount == 0 {
			continue
		}
		err := tx.QueryRowContext(ctx, `SELECT p.page_number,snippet(pages_fts,0,'[',']','…',32)
            FROM pages_fts JOIN document_pages p ON p.rowid=pages_fts.rowid
            WHERE pages_fts MATCH ? AND p.document_id=? ORDER BY bm25(pages_fts),p.page_number LIMIT 1`, pageExpression, r.ID).Scan(&r.Page, &r.Snippet)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return response, err
		}
		if r.Page > 0 {
			r.URI += fmt.Sprintf("#page=%d", r.Page)
		}
	}
	return response, nil
}

type SourceStatus struct {
	connector.Source
	Documents     int    `json:"documents"`
	LastIndexedAt string `json:"last_indexed_at"`
}

func (s *Store) Sources(ctx context.Context) ([]SourceStatus, error) {
	rows, err := s.readers.QueryContext(ctx, `SELECT s.id,s.kind,s.name,s.root,s.last_indexed_at,COUNT(d.id),s.max_text_bytes,s.max_pdf_bytes
        FROM sources s LEFT JOIN documents d ON d.source_id=s.id GROUP BY s.id ORDER BY s.name,s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SourceStatus{}
	for rows.Next() {
		var source SourceStatus
		if err := rows.Scan(&source.ID, &source.Kind, &source.Name, &source.Root, &source.LastIndexedAt, &source.Documents, &source.MaxTextBytes, &source.MaxPDFBytes); err != nil {
			return nil, err
		}
		result = append(result, source)
	}
	return result, rows.Err()
}

// Evidence reads only the indexed snapshot and returns at most 64 Ki characters.
func (s *Store) Evidence(ctx context.Context, id string, page int) (search.Evidence, error) {
	var e search.Evidence
	if page < 0 {
		return e, search.ErrPage
	}
	tx, err := s.readers.BeginTx(ctx, nil)
	if err != nil {
		return e, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT d.id,d.title,d.uri,d.path,d.source_id,s.name,d.media_type,d.content_hash,d.modified_at,d.page_count,
        substr(d.content,1,65536),length(d.content)>65536 FROM documents d JOIN sources s ON s.id=d.source_id WHERE d.id=?`, id).Scan(&e.ID, &e.Title, &e.URI, &e.Path, &e.SourceID, &e.SourceName, &e.MediaType, &e.ContentHash, &e.ModifiedAt, &e.PageCount, &e.Text, &e.Truncated)
	if errors.Is(err, sql.ErrNoRows) {
		return e, search.ErrNotFound
	}
	if err != nil {
		return e, err
	}
	if e.PageCount > 0 {
		if page == 0 {
			page = 1
		}
		if page > e.PageCount {
			return e, search.ErrPage
		}
		if err := tx.QueryRowContext(ctx, "SELECT substr(content,1,65536),length(content)>65536 FROM document_pages WHERE document_id=? AND page_number=?", id, page).Scan(&e.Text, &e.Truncated); err != nil {
			return e, err
		}
		e.Page = page
		e.URI += fmt.Sprintf("#page=%d", page)
	} else if page != 0 {
		return e, search.ErrPage
	}
	return e, nil
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
