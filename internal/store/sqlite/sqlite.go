package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	githubconnector "github.com/Lephiziel/findrail/internal/connectors/github"
	"github.com/Lephiziel/findrail/internal/diagnostics"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/snapshot"
	"github.com/Lephiziel/findrail/pkg/connector"
	_ "modernc.org/sqlite"
)

//go:embed migrations/001_init.sql
var initialSchema string

//go:embed migrations/002_local_alpha.sql
var alphaSchema string

//go:embed migrations/003_github_snapshots.sql
var githubSchema string

//go:embed migrations/004_docx_policy.sql
var docxSchema string

//go:embed migrations/005_archive_sources.sql
var archiveSchema string

//go:embed migrations/006_indexing_reports.sql
var reportSchema string

type Store struct {
	db, readers   *sql.DB
	schemaVersion int
}

var ErrArchiveFrozen = errors.New("archive_snapshot_frozen: imported archive sources cannot be scanned or refreshed")

const currentSchemaVersion = 6

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
	u := sqliteFileURL(path)
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

// OpenReadOnly opens an existing Findrail index without creating or migrating
// anything. The returned store exposes the same read methods as Store, but all
// write paths fail safely because it has no writer connection.
func OpenReadOnly(ctx context.Context, dataDir string) (*Store, error) {
	return openReadOnly(ctx, dataDir, false)
}

// OpenReadOnlyForReport also accepts schema 5 so the report command can return
// an honest unavailable result without performing the schema-6 migration.
func OpenReadOnlyForReport(ctx context.Context, dataDir string) (*Store, error) {
	return openReadOnly(ctx, dataDir, true)
}

func openReadOnly(ctx context.Context, dataDir string, allowSchema5 bool) (*Store, error) {
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dataDir)
	if err != nil {
		return nil, fmt.Errorf("read-only index directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("read-only index path is not a directory")
	}
	path := filepath.Join(dataDir, "findrail.db")
	if info, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("read-only index database: %w", err)
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("read-only index database is not a regular file")
	}
	u := sqliteFileURL(path)
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "query_only(1)")
	u.RawQuery = q.Encode()
	readers, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	readers.SetMaxOpenConns(4)
	s := &Store{readers: readers}
	if err := readers.PingContext(ctx); err != nil {
		_ = readers.Close()
		return nil, err
	}
	var version int
	if err := readers.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		_ = readers.Close()
		return nil, err
	}
	if version != currentSchemaVersion && !(allowSchema5 && version == 5) {
		_ = readers.Close()
		return nil, fmt.Errorf("unsupported index schema %d; open or update this index with the regular Findrail command", version)
	}
	s.schemaVersion = version
	return s, nil
}

func sqliteFileURL(path string) url.URL {
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	return url.URL{Scheme: "file", Path: uriPath}
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
		fallthrough
	case 2:
		if _, err := tx.ExecContext(ctx, githubSchema); err != nil {
			return fmt.Errorf("migrate GitHub snapshots: %w", err)
		}
		fallthrough
	case 3:
		if _, err := tx.ExecContext(ctx, docxSchema); err != nil {
			return fmt.Errorf("migrate DOCX policy: %w", err)
		}
		fallthrough
	case 4:
		if _, err := tx.ExecContext(ctx, archiveSchema); err != nil {
			return fmt.Errorf("migrate archive sources: %w", err)
		}
		fallthrough
	case 5:
		if _, err := tx.ExecContext(ctx, reportSchema); err != nil {
			return fmt.Errorf("migrate indexing reports: %w", err)
		}
	case 6:
	default:
		return fmt.Errorf("unsupported index schema %d; use a compatible Findrail version", version)
	}
	return tx.Commit()
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var errs []error
	if s.readers != nil {
		errs = append(errs, s.readers.Close())
	}
	if s.db != nil {
		errs = append(errs, s.db.Close())
	}
	return errors.Join(errs...)
}

type scan struct {
	tx       *sql.Tx
	sourceID string
	token    string
	report   *diagnostics.Report
}

func (s *Store) BeginScan(ctx context.Context, source connector.Source) (ingest.Scan, error) {
	return s.begin(ctx, source, true)
}

func (s *Store) BeginRefresh(ctx context.Context, source connector.Source) (ingest.Scan, error) {
	return s.begin(ctx, source, false)
}

func (s *Store) begin(ctx context.Context, source connector.Source, create bool) (ingest.Scan, error) {
	if source.Kind == "archive" {
		return nil, ErrArchiveFrozen
	}
	if s.db == nil {
		return nil, errors.New("index is read-only")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if create {
		maxText := source.MaxTextBytes
		if maxText == 0 {
			maxText = 1 << 20
		}
		token, err := registrationToken()
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sources(id,kind,name,root,max_text_bytes,max_pdf_bytes,max_docx_bytes,registration_token,revision) VALUES(?,?,?,?,?,?,?,?,1)
        ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,name=excluded.name,root=excluded.root,max_text_bytes=excluded.max_text_bytes,max_pdf_bytes=excluded.max_pdf_bytes,max_docx_bytes=excluded.max_docx_bytes,registration_token=excluded.registration_token,revision=sources.revision+1`, source.ID, source.Kind, source.Name, source.Root, maxText, source.MaxPDFBytes, source.MaxDOCXBytes, token); err != nil {
			tx.Rollback()
			return nil, err
		}
	} else {
		query := `UPDATE sources SET name=name WHERE id=? AND kind=? AND root=? AND max_text_bytes=? AND max_pdf_bytes=? AND max_docx_bytes=?`
		args := []any{source.ID, source.Kind, source.Root, source.MaxTextBytes, source.MaxPDFBytes, source.MaxDOCXBytes}
		if source.RegistrationToken != "" {
			query += ` AND registration_token=? AND revision=?`
			args = append(args, source.RegistrationToken, source.RegistrationRevision)
		}
		r, err := tx.ExecContext(ctx, query, args...)
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

func registrationToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b[:]), nil
}

func (s *Store) BeginConfigure(ctx context.Context, source connector.Source, token string, revision, previousDOCXLimit int64) (ingest.Scan, error) {
	if s.db == nil {
		return nil, errors.New("index is read-only")
	}
	if source.MaxDOCXBytes < 0 || source.MaxDOCXBytes > 16<<20 || previousDOCXLimit < 0 || previousDOCXLimit > 16<<20 || token == "" || revision < 1 {
		return nil, errors.New("invalid source registration or DOCX policy")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	r, err := tx.ExecContext(ctx, `UPDATE sources SET max_docx_bytes=?,revision=revision+1 WHERE id=? AND kind='filesystem' AND root=? AND registration_token=? AND revision=? AND max_docx_bytes=?`, source.MaxDOCXBytes, source.ID, source.Root, token, revision, previousDOCXLimit)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if n != 1 {
		_ = tx.Rollback()
		return nil, ingest.ErrSourceGone
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return &scan{tx: tx, sourceID: source.ID, token: fmt.Sprintf("%x", b[:])}, nil
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
	if s.report != nil {
		s.report.RemovedDocuments = int64(n)
		s.report.FinishedAt = time.Now().UTC()
		s.report.DurationMillis = s.report.FinishedAt.Sub(s.report.StartedAt).Milliseconds()
		if err := s.report.Validate(); err != nil {
			return 0, err
		}
		payload, err := json.Marshal(s.report)
		if err != nil {
			return 0, err
		}
		reasons, err := json.Marshal(s.report.Reasons)
		if err != nil {
			return 0, err
		}
		if _, err = s.tx.ExecContext(ctx, `INSERT INTO indexing_reports(source_id,report_id,snapshot_id,report_json,committed_at,operation,finished_at,indexed_documents,skipped_files,pruned_directories,coverage,reasons_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source_id) DO UPDATE SET report_id=excluded.report_id,snapshot_id=excluded.snapshot_id,report_json=excluded.report_json,committed_at=excluded.committed_at,operation=excluded.operation,finished_at=excluded.finished_at,indexed_documents=excluded.indexed_documents,skipped_files=excluded.skipped_files,pruned_directories=excluded.pruned_directories,coverage=excluded.coverage,reasons_json=excluded.reasons_json`, s.sourceID, s.report.ID, s.report.SnapshotID, string(payload), s.report.FinishedAt.Format(time.RFC3339Nano), s.report.Operation, s.report.FinishedAt.Format(time.RFC3339Nano), s.report.IndexedDocuments, s.report.SkippedFiles, s.report.PrunedDirectories, s.report.Coverage, string(reasons)); err != nil {
			return 0, err
		}
	} else {
		// An uninstrumented adapter publishes a new snapshot without inheriting a
		// misleading report from an earlier scan.
		if _, err := s.tx.ExecContext(ctx, "DELETE FROM indexing_reports WHERE source_id=?", s.sourceID); err != nil {
			return 0, err
		}
	}
	if err := s.tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}

func (s *scan) SetReport(r *diagnostics.Report) { s.report = r }

func (s *scan) Rollback() error { return s.tx.Rollback() }

func (s *Store) Search(ctx context.Context, request search.Request) (search.Response, error) {
	response := search.Response{Query: request.Query, Results: []search.Result{}}
	if err := search.ValidateRequest(request); err != nil {
		return response, err
	}
	if request.Limit < 1 || request.Limit > 100 {
		return response, search.ErrLimit
	}
	advanced := request.Mode == "advanced"
	expression, _ := search.Expression(request.Query)
	if advanced {
		plan, _ := search.ParseAdvanced(request.Query)
		expression = compilePositivePlan(plan)
	}
	where := "documents_fts MATCH ?"
	args := []any{expression}
	if advanced {
		plan, _ := search.ParseAdvanced(request.Query)
		predicate, predicateArgs := advancedPredicate(plan)
		where += " AND (" + predicate + ")"
		args = append(args, predicateArgs...)
	}
	if request.SourceID != "" {
		where += " AND d.source_id=?"
		args = append(args, request.SourceID)
	}
	if request.Format != "" && request.Format != "all" {
		switch request.Format {
		case "pdf":
			where += " AND d.media_type='application/pdf'"
		case "docx":
			where += " AND d.media_type='application/vnd.openxmlformats-officedocument.wordprocessingml.document'"
		case "text":
			where += " AND d.media_type NOT IN ('application/pdf','application/vnd.openxmlformats-officedocument.wordprocessingml.document')"
		}
	}
	if request.PathPrefix != "" {
		p := strings.TrimSuffix(request.PathPrefix, "/")
		where += " AND (d.path=? OR substr(d.path,1,length(?)+1)=?||'/')"
		args = append(args, p, p, p)
	}
	if request.TitleContains != "" {
		where += " AND instr(d.title,?)>0"
		args = append(args, request.TitleContains)
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
	var advancedPlan search.Plan
	if advanced {
		advancedPlan, _ = search.ParseAdvanced(request.Query)
		pageExpression = compilePositivePlan(advancedPlan)
	}
	for i := range response.Results {
		r := &response.Results[i]
		if r.PageCount == 0 {
			continue
		}
		queryPageExpression := pageExpression
		pageAlreadySelected := false
		if advanced {
			queryPageExpression = ""
			for _, branch := range advancedPlan {
				predicate, predicateArgs := advancedPredicate(search.Plan{branch})
				candidate := compilePositivePlan(search.Plan{branch})
				var found int
				checkArgs := append([]any{candidate}, predicateArgs...)
				checkArgs = append(checkArgs, r.ID)
				checkErr := tx.QueryRowContext(ctx, `SELECT 1 FROM documents_fts JOIN documents d ON d.rowid=documents_fts.rowid WHERE documents_fts MATCH ? AND (`+predicate+`) AND d.id=? LIMIT 1`, checkArgs...).Scan(&found)
				if errors.Is(checkErr, sql.ErrNoRows) {
					continue
				}
				if checkErr != nil {
					return response, checkErr
				}
				queryPageExpression = candidate
				fullErr := tx.QueryRowContext(ctx, `SELECT p.page_number,snippet(pages_fts,0,'[',']','…',32) FROM pages_fts JOIN document_pages p ON p.rowid=pages_fts.rowid WHERE pages_fts MATCH ? AND p.document_id=? ORDER BY bm25(pages_fts),p.page_number LIMIT 1`, candidate, r.ID).Scan(&r.Page, &r.Snippet)
				if fullErr == nil {
					pageAlreadySelected = true
					break
				}
				if !errors.Is(fullErr, sql.ErrNoRows) {
					return response, fullErr
				}
				queryPageExpression = branchPositiveAlternatives(branch)
				break
			}
			if pageAlreadySelected {
				r.URI += fmt.Sprintf("#page=%d", r.Page)
				continue
			}
			if queryPageExpression == "" {
				r.Page = 0
				continue
			}
		}
		err := tx.QueryRowContext(ctx, `SELECT p.page_number,snippet(pages_fts,0,'[',']','…',32)
            FROM pages_fts JOIN document_pages p ON p.rowid=pages_fts.rowid
            WHERE pages_fts MATCH ? AND p.document_id=? ORDER BY bm25(pages_fts),p.page_number LIMIT 1`, queryPageExpression, r.ID).Scan(&r.Page, &r.Snippet)
		if errors.Is(err, sql.ErrNoRows) && advanced {
			r.Page = 0
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return response, err
		}
		if r.Page > 0 {
			r.URI += fmt.Sprintf("#page=%d", r.Page)
		}
	}
	return response, nil
}

func ftsAtom(a search.Atom) string {
	x := strings.ReplaceAll(a.Text, `"`, `""`)
	if a.Prefix {
		return `"` + x + `"*`
	}
	return `"` + x + `"`
}

// advancedPredicate applies each branch independently. Phrase checks against the
// document title or one actual PDF page; negative checks cover title and pages.
func advancedPredicate(plan search.Plan) (string, []any) {
	branches := make([]string, 0, len(plan))
	args := []any{}
	for _, branch := range plan {
		terms := make([]string, 0, len(branch))
		for _, atom := range branch {
			expr := ftsAtom(atom)
			var condition string
			if atom.Phrase {
				title := `(EXISTS (SELECT 1 FROM documents_fts WHERE documents_fts MATCH ? AND rowid=d.rowid))`
				pages := `(EXISTS (SELECT 1 FROM pages_fts JOIN document_pages p ON p.rowid=pages_fts.rowid WHERE pages_fts MATCH ? AND p.document_id=d.id))`
				body := `(d.page_count=0 AND EXISTS (SELECT 1 FROM documents_fts WHERE documents_fts MATCH ? AND rowid=d.rowid))`
				args = append(args, `title : `+expr, expr, expr)
				if atom.Exclude {
					condition = "NOT (" + title + " OR " + pages + " OR (" + body + "))"
				} else {
					condition = "(" + title + " OR " + pages + " OR (" + body + "))"
				}
			} else {
				condition = `(EXISTS (SELECT 1 FROM documents_fts WHERE documents_fts MATCH ? AND rowid=d.rowid))`
				args = append(args, expr)
				if atom.Exclude {
					condition = `NOT ` + condition
				}
			}
			terms = append(terms, condition)
		}
		branches = append(branches, `(`+strings.Join(terms, ` AND `)+`)`)
	}
	return strings.Join(branches, ` OR `), args
}
func compilePositivePlan(plan search.Plan) string {
	xs := []string{}
	for _, b := range plan {
		ps := []string{}
		for _, a := range b {
			if !a.Exclude {
				ps = append(ps, ftsAtom(a))
			}
		}
		xs = append(xs, "("+strings.Join(ps, " AND ")+")")
	}
	return strings.Join(xs, " OR ")
}
func branchPositiveAlternatives(branch search.Branch) string {
	terms := []string{}
	for _, atom := range branch {
		if !atom.Exclude {
			terms = append(terms, ftsAtom(atom))
		}
	}
	return strings.Join(terms, " OR ")
}

type SourceStatus struct {
	connector.Source
	Documents            int            `json:"documents"`
	LastIndexedAt        string         `json:"last_indexed_at"`
	GitHub               *GitHubSource  `json:"github,omitempty"`
	Archive              *ArchiveSource `json:"archive,omitempty"`
	IndexingReport       *ReportSummary `json:"indexing_report,omitempty"`
	RegistrationToken    string         `json:"-"`
	RegistrationRevision int64          `json:"-"`
}

type ReportSummary struct {
	Available         bool                 `json:"available"`
	Availability      string               `json:"availability,omitempty"`
	ReportID          string               `json:"report_id,omitempty"`
	Operation         string               `json:"operation,omitempty"`
	FinishedAt        string               `json:"finished_at,omitempty"`
	IndexedDocuments  int64                `json:"indexed_documents,omitempty"`
	SkippedFiles      int64                `json:"skipped_files,omitempty"`
	PrunedDirectories int64                `json:"pruned_directories,omitempty"`
	Coverage          string               `json:"coverage,omitempty"`
	Reasons           []diagnostics.Reason `json:"reasons,omitempty"`
}

var ErrSourceNotFound = errors.New("source not found")

// SourceReport reads source registration and its committed report from one WAL
// snapshot. Examples are omitted unless explicitly requested by the caller.
func (s *Store) SourceReport(ctx context.Context, id string, includePaths bool) (map[string]any, error) {
	tx, err := s.readers.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var kind string
	if err = tx.QueryRowContext(ctx, "SELECT kind FROM sources WHERE id=?", id).Scan(&kind); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSourceNotFound
	} else if err != nil {
		return nil, err
	}
	result := map[string]any{"available": false, "source_id": id, "source_kind": kind}
	if kind == "archive" {
		result["availability"] = "frozen_origin_report_not_in_portable_v1"
		_ = tx.Commit()
		return result, nil
	}
	if s.schemaVersion == 5 {
		result["availability"] = "not_yet_available"
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return result, nil
	}
	var raw string
	err = tx.QueryRowContext(ctx, "SELECT report_json FROM indexing_reports WHERE source_id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		result["availability"] = "not_yet_available"
		_ = tx.Commit()
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	var r diagnostics.Report
	if err = json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, err
	}
	if !includePaths {
		r.Examples = nil
	}
	result["available"] = true
	result["availability"] = "available"
	result["report"] = r
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

type ArchiveSource struct {
	Origin            snapshot.Origin `json:"origin"`
	Fingerprint       string          `json:"fingerprint"`
	OriginalIndexedAt string          `json:"original_indexed_at"`
	ImportedAt        string          `json:"imported_at"`
}

type AlreadyImportedError struct{ SourceID string }

func (e *AlreadyImportedError) Error() string {
	return "snapshot already imported as source " + e.SourceID
}

// StreamSnapshot exports ordered records from one established read transaction.
// JSONL payloads are staged by the snapshot codec, never accumulated in slices.
func (s *Store) StreamSnapshot(ctx context.Context, sourceID, producer, exportedAt string, dst io.Writer) (snapshot.Manifest, int, int, error) {
	tx, err := s.readers.BeginTx(ctx, nil)
	if err != nil {
		return snapshot.Manifest{}, 0, 0, err
	}
	defer tx.Rollback()
	o, err := sourceOrigin(ctx, tx, sourceID)
	if err != nil {
		return snapshot.Manifest{}, 0, 0, err
	}
	m := snapshot.Manifest{Producer: producer, ExportedAt: exportedAt, Origin: o}
	documents := func(ctx context.Context, w io.Writer) (int, error) {
		rows, e := tx.QueryContext(ctx, `SELECT CASE WHEN s.kind='archive' AND d.origin_document_id<>'' THEN d.origin_document_id ELSE d.id END,d.title,d.uri,d.path,d.media_type,d.content,CASE WHEN s.kind='archive' AND d.origin_content_hash<>'' THEN d.origin_content_hash ELSE d.content_hash END,d.size_bytes,d.modified_at,d.page_count FROM documents d JOIN sources s ON s.id=d.source_id WHERE d.source_id=? ORDER BY d.path`, sourceID)
		if e != nil {
			return 0, e
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			if e = ctx.Err(); e != nil {
				return count, e
			}
			var d snapshot.Document
			if e = rows.Scan(&d.ID, &d.Title, &d.URI, &d.Path, &d.MediaType, &d.Text, &d.ContentHash, &d.SizeBytes, &d.ModifiedAt, &d.PageCount); e != nil {
				return count, e
			}
			if e = snapshot.ValidateDocumentRecord(d, o); e != nil {
				return count, e
			}
			if count >= snapshot.MaxDocuments {
				return count, errors.New("document count exceeds portable snapshot v1 limit")
			}
			if e = snapshot.WriteJSONLRecord(w, d); e != nil {
				return count, e
			}
			count++
		}
		if e = rows.Err(); e != nil {
			return count, e
		}
		if e = rows.Close(); e != nil {
			return count, e
		}
		return count, nil
	}
	pages := func(ctx context.Context, w io.Writer) (int, error) {
		rows, e := tx.QueryContext(ctx, `SELECT d.path,p.page_number,p.content FROM document_pages p JOIN documents d ON d.id=p.document_id WHERE d.source_id=? ORDER BY d.path,p.page_number`, sourceID)
		if e != nil {
			return 0, e
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			if e = ctx.Err(); e != nil {
				return count, e
			}
			var p snapshot.Page
			if e = rows.Scan(&p.Path, &p.Number, &p.Text); e != nil {
				return count, e
			}
			if len(p.Text) > snapshot.MaxText || !utf8.ValidString(p.Text) || strings.ContainsRune(p.Text, 0) {
				return count, errors.New("page text exceeds portable snapshot v1 limit")
			}
			if count >= snapshot.MaxPages {
				return count, errors.New("page count exceeds portable snapshot v1 limit")
			}
			if e = snapshot.WriteJSONLRecord(w, p); e != nil {
				return count, e
			}
			count++
		}
		if e = rows.Err(); e != nil {
			return count, e
		}
		if e = rows.Close(); e != nil {
			return count, e
		}
		return count, nil
	}
	manifest, err := snapshot.WriteArchive(ctx, dst, m, documents, pages)
	if err != nil {
		return manifest, 0, 0, err
	}
	if err = tx.Commit(); err != nil {
		return manifest, 0, 0, err
	}
	return manifest, manifest.DocumentCount, manifest.PageCount, nil
}

func sourceOrigin(ctx context.Context, tx *sql.Tx, sourceID string) (snapshot.Origin, error) {
	var o snapshot.Origin
	err := tx.QueryRowContext(ctx, `SELECT id,kind,name,root,last_indexed_at,max_text_bytes,max_pdf_bytes,max_docx_bytes FROM sources WHERE id=?`, sourceID).Scan(&o.ID, &o.Kind, &o.Name, &o.Location, &o.IndexedAt, &o.MaxTextBytes, &o.MaxPDFBytes, &o.MaxDOCXBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return o, fmt.Errorf("source %q not found", sourceID)
	}
	if err != nil {
		return o, err
	}
	if o.Kind == "archive" {
		var raw string
		if err = tx.QueryRowContext(ctx, "SELECT origin_json FROM archive_sources WHERE source_id=?", sourceID).Scan(&raw); err != nil {
			return o, errors.New("archive provenance missing")
		}
		err = json.Unmarshal([]byte(raw), &o)
		return o, err
	}
	if o.Kind == "github" {
		g, e := githubSourceQuery(ctx, tx, sourceID)
		if e != nil {
			return o, e
		}
		o.RepositoryURL, o.Owner, o.Repository, o.FullCommitSHA = g.RepositoryURL, g.Owner, g.Repo, g.SHA
		o.RefMode, o.RefValue, o.SelectedPath = g.RefMode, g.RefValue, g.SelectedPath
		o.GitHubMaxBytes, o.GitHubPolicyVersion, o.CommitTime = g.MaxBytes, g.PolicyVersion, g.CommitTime.Format(time.RFC3339Nano)
	}
	return o, nil
}

// ImportSnapshot publishes source, provenance, documents, pages and FTS rows in
// one transaction. The caller must fully validate the archive before calling.
func (s *Store) ImportSnapshot(ctx context.Context, name string, a snapshot.Archive) (string, error) {
	if s.db == nil {
		return "", errors.New("index is read-only")
	}
	if name == "" || len(name) > 512 || !utf8.ValidString(name) {
		return "", errors.New("import name must be 1–512 UTF-8 bytes")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("import name contains a control character")
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fingerprint := a.Manifest.Fingerprint
	var existing string
	err := s.db.QueryRowContext(ctx, "SELECT source_id FROM archive_sources WHERE fingerprint=?", fingerprint).Scan(&existing)
	if err == nil {
		return "", &AlreadyImportedError{SourceID: existing}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	h := sha256.Sum256([]byte("findrail-archive-source-v1\x00" + fingerprint))
	sourceID := "archive-" + hex.EncodeToString(h[:16])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var same string
	err = tx.QueryRowContext(ctx, "SELECT source_id FROM archive_sources WHERE fingerprint=?", fingerprint).Scan(&same)
	if err == nil {
		return "", &AlreadyImportedError{SourceID: same}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var duplicate string
	if err = tx.QueryRowContext(ctx, "SELECT source_id FROM archive_sources WHERE source_id=?", sourceID).Scan(&duplicate); err == nil {
		return "", &AlreadyImportedError{SourceID: duplicate}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	token, err := registrationToken()
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sources(id,kind,name,root,last_indexed_at,max_text_bytes,max_pdf_bytes,max_docx_bytes,registration_token,revision) VALUES(?,'archive',?,'',?,0,0,0,?,1)`, sourceID, name, time.Now().UTC().Format(time.RFC3339Nano), token); err != nil {
		_ = tx.Rollback()
		if prior := s.waitImported(ctx, fingerprint); prior != "" {
			return "", &AlreadyImportedError{SourceID: prior}
		}
		return "", err
	}
	origin, err := json.Marshal(a.Manifest.Origin)
	if err != nil {
		return "", err
	}
	importedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `INSERT INTO archive_sources(source_id,origin_json,fingerprint,original_indexed_at,imported_at) VALUES(?,?,?,?,?)`, sourceID, string(origin), fingerprint, a.Manifest.Origin.IndexedAt, importedAt); err != nil {
		_ = tx.Rollback()
		if prior := s.waitImported(ctx, fingerprint); prior != "" {
			return "", &AlreadyImportedError{SourceID: prior}
		}
		return "", err
	}
	pagesByPath := make(map[string][]snapshot.Page, len(a.Documents))
	for _, p := range a.Pages {
		pagesByPath[p.Path] = append(pagesByPath[p.Path], p)
	}
	documentIDs := make(map[string]string, len(a.Documents))
	for _, d := range a.Documents {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		idHash := sha256.Sum256([]byte(sourceID + "\x00" + d.Path))
		id := hex.EncodeToString(idHash[:])
		contentHash := sha256.New()
		contentHash.Write([]byte("findrail-import-content-v1\x00"))
		contentHash.Write([]byte(d.MediaType))
		contentHash.Write([]byte{0})
		contentHash.Write([]byte(d.Text))
		for _, p := range pagesByPath[d.Path] {
			contentHash.Write([]byte{0})
			contentHash.Write([]byte(p.Text))
		}
		modified := d.ModifiedAt
		if modified == "" {
			modified = "1970-01-01T00:00:00Z"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO documents(id,source_id,title,uri,path,content,content_hash,size_bytes,modified_at,scan_token,media_type,page_count,origin_content_hash,origin_document_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, sourceID, d.Title, d.URI, d.Path, d.Text, hex.EncodeToString(contentHash.Sum(nil)), d.SizeBytes, modified, token, d.MediaType, d.PageCount, d.ContentHash, d.ID); err != nil {
			return "", err
		}
		documentIDs[d.Path] = id
	}
	for _, p := range a.Pages {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		documentID := documentIDs[p.Path]
		if documentID == "" {
			return "", errors.New("validated page lost its document during import")
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO document_pages(document_id,page_number,content) VALUES(?,?,?)", documentID, p.Number, p.Text); err != nil {
			return "", err
		}
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		_ = tx.Rollback()
		if prior := s.waitImported(ctx, fingerprint); prior != "" {
			return "", &AlreadyImportedError{SourceID: prior}
		}
		return "", err
	}
	return sourceID, nil
}

func (s *Store) waitImported(ctx context.Context, fingerprint string) string {
	if ctx.Err() != nil {
		return ""
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var id string
		if err := s.readers.QueryRowContext(wait, "SELECT source_id FROM archive_sources WHERE fingerprint=?", fingerprint).Scan(&id); err == nil {
			return id
		}
		select {
		case <-wait.Done():
			return ""
		case <-ticker.C:
		}
	}
}

// DiagnosticSummary returns a bounded aggregate for offline doctor output.
// It never selects source names, paths, document titles, or indexed content.
type DiagnosticSummary struct {
	Sources           int `json:"sources"`
	FilesystemSources int `json:"filesystem_sources"`
	GitHubSources     int `json:"github_sources"`
	ArchiveSources    int `json:"archive_sources"`
	OtherSources      int `json:"other_sources"`
	CustomTextLimits  int `json:"custom_text_limits"`
	PDFEnabled        int `json:"pdf_enabled"`
	DOCXEnabled       int `json:"docx_enabled"`
	DOCXDisabled      int `json:"docx_disabled"`
}

// DiagnosticSummary reads scalar policy/count aggregates from the already
// opened read-only database handle. Its result size is fixed regardless of the
// number of indexed sources.
func (s *Store) DiagnosticSummary(ctx context.Context) (DiagnosticSummary, error) {
	var summary DiagnosticSummary
	if s == nil || s.readers == nil {
		return summary, errors.New("index is not open for reading")
	}
	err := s.readers.QueryRowContext(ctx, `SELECT COUNT(*),
        COALESCE(SUM(CASE WHEN kind='filesystem' THEN 1 ELSE 0 END),0),
        COALESCE(SUM(CASE WHEN kind='github' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN kind='archive' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN kind NOT IN ('filesystem','github','archive') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN kind<>'archive' AND max_text_bytes<>1048576 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN kind<>'archive' AND max_pdf_bytes>0 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN kind<>'archive' AND max_docx_bytes>0 THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN kind<>'archive' AND max_docx_bytes=0 THEN 1 ELSE 0 END),0)
        FROM sources`).Scan(&summary.Sources, &summary.FilesystemSources, &summary.GitHubSources,
		&summary.ArchiveSources, &summary.OtherSources, &summary.CustomTextLimits, &summary.PDFEnabled,
		&summary.DOCXEnabled, &summary.DOCXDisabled)
	return summary, err
}

type GitHubSource struct {
	SourceID          string    `json:"source_id,omitempty"`
	RepositoryID      int64     `json:"repository_id"`
	Owner             string    `json:"owner"`
	Repo              string    `json:"repo"`
	RepositoryURL     string    `json:"repository_url"`
	RefMode           string    `json:"ref_mode"`
	RefValue          string    `json:"ref_value,omitempty"`
	SelectedPath      string    `json:"selected_path,omitempty"`
	MaxBytes          int64     `json:"max_bytes"`
	PolicyVersion     int       `json:"policy_version"`
	SHA               string    `json:"sha"`
	CommitTime        time.Time `json:"commit_time"`
	RegistrationToken string    `json:"-"`
	Revision          int64     `json:"revision"`
	LastError         string    `json:"last_error,omitempty"`
}

func (s *Store) Sources(ctx context.Context) ([]SourceStatus, error) {
	tx, err := s.readers.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.kind,s.name,s.root,s.last_indexed_at,COUNT(d.id),s.max_text_bytes,s.max_pdf_bytes,s.max_docx_bytes,s.registration_token,s.revision
        FROM sources s LEFT JOIN documents d ON d.source_id=s.id GROUP BY s.id ORDER BY s.name,s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SourceStatus{}
	for rows.Next() {
		var source SourceStatus
		if err := rows.Scan(&source.ID, &source.Kind, &source.Name, &source.Root, &source.LastIndexedAt, &source.Documents, &source.MaxTextBytes, &source.MaxPDFBytes, &source.MaxDOCXBytes, &source.RegistrationToken, &source.RegistrationRevision); err != nil {
			return nil, err
		}
		source.Source.RegistrationToken = source.RegistrationToken
		source.Source.RegistrationRevision = source.RegistrationRevision
		result = append(result, source)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range result {
		var summary ReportSummary
		var reasonsJSON string
		err := tx.QueryRowContext(ctx, `SELECT report_id,operation,finished_at,indexed_documents,skipped_files,pruned_directories,coverage,reasons_json FROM indexing_reports WHERE source_id=?`, result[i].ID).Scan(&summary.ReportID, &summary.Operation, &summary.FinishedAt, &summary.IndexedDocuments, &summary.SkippedFiles, &summary.PrunedDirectories, &summary.Coverage, &reasonsJSON)
		if err == nil {
			summary.Available = true
			if err = json.Unmarshal([]byte(reasonsJSON), &summary.Reasons); err != nil {
				return nil, err
			}
			result[i].IndexingReport = &summary
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		} else if result[i].Kind == "archive" {
			result[i].IndexingReport = &ReportSummary{Available: false, Availability: "frozen_origin_report_not_in_portable_v1"}
		} else {
			result[i].IndexingReport = &ReportSummary{Available: false, Availability: "not_yet_available"}
		}
		switch result[i].Kind {
		case "github":
			g, err := githubSourceQuery(ctx, tx, result[i].ID)
			if err != nil {
				if errors.Is(err, ingest.ErrSourceGone) {
					return nil, errors.New("source changed while reading source status")
				}
				return nil, err
			}
			g.RegistrationToken = ""
			result[i].GitHub = &g
		case "archive":
			var originJSON string
			a := ArchiveSource{}
			err := tx.QueryRowContext(ctx, `SELECT origin_json,fingerprint,original_indexed_at,imported_at FROM archive_sources WHERE source_id=?`, result[i].ID).Scan(&originJSON, &a.Fingerprint, &a.OriginalIndexedAt, &a.ImportedAt)
			if err != nil {
				return nil, errors.New("archive source metadata missing or changed")
			}
			if err = json.Unmarshal([]byte(originJSON), &a.Origin); err != nil {
				return nil, err
			}
			result[i].Archive = &a
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) GitHubSource(ctx context.Context, id string) (GitHubSource, error) {
	return githubSourceQuery(ctx, s.readers, id)
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func githubSourceQuery(ctx context.Context, q queryer, id string) (GitHubSource, error) {
	var g GitHubSource
	var commit string
	err := q.QueryRowContext(ctx, `SELECT source_id,repository_id,owner,repo,repository_url,ref_mode,ref_value,selected_path,max_bytes,policy_version,snapshot_sha,commit_time,registration_token,revision,last_error FROM github_sources WHERE source_id=?`, id).Scan(&g.SourceID, &g.RepositoryID, &g.Owner, &g.Repo, &g.RepositoryURL, &g.RefMode, &g.RefValue, &g.SelectedPath, &g.MaxBytes, &g.PolicyVersion, &g.SHA, &commit, &g.RegistrationToken, &g.Revision, &g.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ingest.ErrSourceGone
	}
	if err != nil {
		return g, err
	}
	g.CommitTime, err = time.Parse(time.RFC3339Nano, commit)
	return g, err
}

// PublishGitHub atomically publishes a fully prepared snapshot. expected is nil
// for a first registration and otherwise guards against stale downloads.
func (s *Store) PublishGitHub(ctx context.Context, snapshot *githubconnector.Snapshot, expected *GitHubSource) (ingest.Result, GitHubSource, error) {
	result := ingest.Result{Source: snapshot.Source()}
	meta := snapshot.Metadata()
	if s.db == nil {
		return result, GitHubSource{}, errors.New("index is read-only")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, GitHubSource{}, err
	}
	defer tx.Rollback()
	var currentToken string
	var currentRevision, repositoryID int64
	err = tx.QueryRowContext(ctx, "SELECT registration_token,revision,repository_id FROM github_sources WHERE source_id=?", result.Source.ID).Scan(&currentToken, &currentRevision, &repositoryID)
	if expected == nil {
		if err == nil {
			return result, GitHubSource{}, fmt.Errorf("source_changed: GitHub source was concurrently registered")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return result, GitHubSource{}, err
		}
	} else {
		if errors.Is(err, sql.ErrNoRows) {
			return result, GitHubSource{}, ingest.ErrSourceGone
		}
		if err != nil {
			return result, GitHubSource{}, err
		}
		if currentToken != expected.RegistrationToken || currentRevision != expected.Revision || repositoryID != meta.RepositoryID {
			return result, GitHubSource{}, fmt.Errorf("source_changed: GitHub source changed during refresh")
		}
	}
	maxText := result.Source.MaxTextBytes
	sourceToken, err := registrationToken()
	if err != nil {
		return result, GitHubSource{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sources(id,kind,name,root,max_text_bytes,max_pdf_bytes,max_docx_bytes,registration_token,revision) VALUES(?,?,?,?,?,0,0,?,1) ON CONFLICT(id) DO UPDATE SET name=excluded.name,root=excluded.root,max_text_bytes=excluded.max_text_bytes WHERE sources.kind='github'`, result.Source.ID, "github", result.Source.Name, result.Source.Root, maxText, sourceToken); err != nil {
		return result, GitHubSource{}, err
	}
	var token string
	revision := int64(1)
	if expected != nil {
		token = currentToken
		revision = currentRevision + 1
	} else {
		var b [16]byte
		if _, err = rand.Read(b[:]); err != nil {
			return result, GitHubSource{}, err
		}
		token = fmt.Sprintf("%x", b)
	}
	sc := &scan{tx: tx, sourceID: result.Source.ID, token: token + fmt.Sprintf("-%d", revision)}
	report, err := snapshot.Scan(ctx, func(doc connector.Document) error {
		changed, e := sc.Upsert(ctx, doc)
		if e == nil {
			if changed {
				result.Updated++
			} else {
				result.Unchanged++
			}
		}
		return e
	})
	result.Seen, result.Skipped = report.Seen, report.Skipped
	if err != nil {
		return result, GitHubSource{}, fmt.Errorf("scan failed; previous index preserved: %w", err)
	}
	r, err := tx.ExecContext(ctx, "DELETE FROM documents WHERE source_id=? AND scan_token<>?", result.Source.ID, sc.token)
	if err != nil {
		return result, GitHubSource{}, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return result, GitHubSource{}, err
	}
	result.Removed = int(n)
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, "UPDATE sources SET last_indexed_at=? WHERE id=?", now.Format(time.RFC3339Nano), result.Source.ID); err != nil {
		return result, GitHubSource{}, err
	}
	var reportIDBytes [16]byte
	if _, err = rand.Read(reportIDBytes[:]); err != nil {
		return result, GitHubSource{}, err
	}
	reportID := hex.EncodeToString(reportIDBytes[:])
	payload, ok := report.Diagnostics.(diagnostics.Payload)
	if !ok {
		return result, GitHubSource{}, errors.New("GitHub snapshot diagnostics unavailable")
	}
	skippedFiles := int64(0)
	for _, reason := range payload.Reasons {
		if reason.Unit == "file" {
			skippedFiles += reason.Count
		}
	}
	startedAt := snapshot.StartedAt()
	if startedAt.IsZero() {
		startedAt = now
	}
	dr := &diagnostics.Report{FormatVersion: diagnostics.FormatVersion, ID: reportID, SourceID: result.Source.ID, SourceKind: "github", SnapshotID: reportID, Operation: "github_publication", StartedAt: startedAt, FinishedAt: now, DurationMillis: now.Sub(startedAt).Milliseconds(), Committed: true, Complete: true, IndexedDocuments: int64(result.Updated + result.Unchanged), UpdatedDocuments: int64(result.Updated), UnchangedDocuments: int64(result.Unchanged), RemovedDocuments: int64(result.Removed), ObservedFiles: payload.ObservedFiles, ObservedFilesKnown: payload.ObservedFilesKnown, ObservedDirectories: payload.ObservedDirectories, ObservedDirectoriesKnown: payload.ObservedDirectoriesKnown, SkippedFiles: skippedFiles, Reasons: payload.Reasons, Examples: payload.Examples, ExamplesOmitted: payload.ExamplesOmitted, RedactedSamples: payload.RedactedSamples, Coverage: payload.Coverage}
	if err = dr.Validate(); err != nil {
		return result, GitHubSource{}, err
	}
	reportJSON, marshalErr := json.Marshal(dr)
	if marshalErr != nil {
		return result, GitHubSource{}, marshalErr
	}
	reasonsJSON, marshalErr := json.Marshal(dr.Reasons)
	if marshalErr != nil {
		return result, GitHubSource{}, marshalErr
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO indexing_reports(source_id,report_id,snapshot_id,report_json,committed_at,operation,finished_at,indexed_documents,skipped_files,pruned_directories,coverage,reasons_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source_id) DO UPDATE SET report_id=excluded.report_id,snapshot_id=excluded.snapshot_id,report_json=excluded.report_json,committed_at=excluded.committed_at,operation=excluded.operation,finished_at=excluded.finished_at,indexed_documents=excluded.indexed_documents,skipped_files=excluded.skipped_files,pruned_directories=excluded.pruned_directories,coverage=excluded.coverage,reasons_json=excluded.reasons_json`, result.Source.ID, reportID, reportID, string(reportJSON), now.Format(time.RFC3339Nano), dr.Operation, now.Format(time.RFC3339Nano), dr.IndexedDocuments, dr.SkippedFiles, dr.PrunedDirectories, dr.Coverage, string(reasonsJSON)); err != nil {
		return result, GitHubSource{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO github_sources(source_id,repository_id,owner,repo,repository_url,ref_mode,ref_value,selected_path,max_bytes,policy_version,snapshot_sha,commit_time,registration_token,revision,last_error) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?, '') ON CONFLICT(source_id) DO UPDATE SET repository_id=excluded.repository_id,owner=excluded.owner,repo=excluded.repo,repository_url=excluded.repository_url,ref_mode=excluded.ref_mode,ref_value=excluded.ref_value,selected_path=excluded.selected_path,max_bytes=excluded.max_bytes,policy_version=excluded.policy_version,snapshot_sha=excluded.snapshot_sha,commit_time=excluded.commit_time,revision=excluded.revision,last_error='' WHERE github_sources.registration_token=? AND github_sources.revision=?`, result.Source.ID, meta.RepositoryID, meta.Owner, meta.Repo, meta.RepositoryURL, meta.RefMode, meta.RefValue, meta.SelectedPath, meta.MaxBytes, meta.PolicyVersion, meta.SHA, meta.CommitTime.Format(time.RFC3339Nano), token, revision, currentToken, currentRevision)
	if err != nil {
		return result, GitHubSource{}, err
	}
	if err = tx.Commit(); err != nil {
		return result, GitHubSource{}, err
	}
	g := GitHubSource{SourceID: result.Source.ID, RepositoryID: meta.RepositoryID, Owner: meta.Owner, Repo: meta.Repo, RepositoryURL: meta.RepositoryURL, RefMode: meta.RefMode, RefValue: meta.RefValue, SelectedPath: meta.SelectedPath, MaxBytes: meta.MaxBytes, PolicyVersion: meta.PolicyVersion, SHA: meta.SHA, CommitTime: meta.CommitTime, RegistrationToken: token, Revision: revision}
	return result, g, nil
}

func (s *Store) RecordGitHubError(ctx context.Context, expected GitHubSource, message string) {
	if s.db == nil {
		return
	}
	message = safeError(message, 512)
	_, _ = s.db.ExecContext(ctx, "UPDATE github_sources SET last_error=? WHERE source_id=? AND registration_token=? AND revision=?", message, expected.SourceID, expected.RegistrationToken, expected.Revision)
}
func safeError(v string, n int) string {
	v = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, v)
	if len(v) > n {
		v = v[:n]
	}
	return v
}

// Evidence reads only the indexed snapshot and returns at most 64 Ki characters.
func (s *Store) Evidence(ctx context.Context, id string, page int) (search.Evidence, error) {
	return s.evidence(ctx, "", id, page)
}

// EvidenceForSource reads evidence only after the document identity and source
// scope have been checked by the same read transaction.
func (s *Store) EvidenceForSource(ctx context.Context, sourceID, id string, page int) (search.Evidence, error) {
	if sourceID == "" {
		return search.Evidence{}, search.ErrNotFound
	}
	return s.evidence(ctx, sourceID, id, page)
}

func (s *Store) evidence(ctx context.Context, sourceID, id string, page int) (search.Evidence, error) {
	var e search.Evidence
	if page < -1 {
		return e, search.ErrPage
	}
	tx, err := s.readers.BeginTx(ctx, nil)
	if err != nil {
		return e, err
	}
	defer tx.Rollback()
	where := "d.id=?"
	args := []any{id}
	if sourceID != "" {
		where += " AND d.source_id=?"
		args = append(args, sourceID)
	}
	err = tx.QueryRowContext(ctx, `SELECT d.id,d.title,d.uri,d.path,d.source_id,s.name,s.kind,d.media_type,d.content_hash,d.modified_at,d.page_count
	        FROM documents d JOIN sources s ON s.id=d.source_id WHERE `+where, args...).Scan(&e.ID, &e.Title, &e.URI, &e.Path, &e.SourceID, &e.SourceName, &e.SourceKind, &e.MediaType, &e.ContentHash, &e.ModifiedAt, &e.PageCount)
	if errors.Is(err, sql.ErrNoRows) {
		return e, search.ErrNotFound
	}
	if err != nil {
		return e, err
	}
	if e.PageCount > 0 {
		if page == -1 {
			if err := tx.QueryRowContext(ctx, "SELECT substr(content,1,65536),length(content)>65536 FROM documents WHERE id=?", e.ID).Scan(&e.Text, &e.Truncated); err != nil {
				return e, err
			}
			return e, nil
		}
		if page == 0 {
			page = 1
		}
		if page > e.PageCount {
			return e, search.ErrPage
		}
		if err := tx.QueryRowContext(ctx, "SELECT substr(content,1,65536),length(content)>65536 FROM document_pages WHERE document_id=? AND page_number=?", e.ID, page).Scan(&e.Text, &e.Truncated); err != nil {
			return e, err
		}
		e.Page = page
		e.URI += fmt.Sprintf("#page=%d", page)
	} else if page != 0 {
		return e, search.ErrPage
	} else if err := tx.QueryRowContext(ctx, "SELECT substr(content,1,65536),length(content)>65536 FROM documents WHERE id=?", e.ID).Scan(&e.Text, &e.Truncated); err != nil {
		return e, err
	}
	return e, nil
}

// ForgetSource removes a source and its searchable documents. Originals are
// untouched. This is a logical deletion, not forensic secure erasure.
func (s *Store) ForgetSource(ctx context.Context, id string) error {
	if s.db == nil {
		return errors.New("index is read-only")
	}
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

// ForgetSourceRegistration prevents delayed UI removals from deleting a source
// that was forgotten and re-added under the same path-derived ID.
func (s *Store) ForgetSourceRegistration(ctx context.Context, id, token string) error {
	if s.db == nil {
		return errors.New("index is read-only")
	}
	if id == "" || token == "" {
		return ingest.ErrSourceGone
	}
	r, err := s.db.ExecContext(ctx, "DELETE FROM sources WHERE id=? AND registration_token=?", id, token)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ingest.ErrSourceGone
	}
	return nil
}
