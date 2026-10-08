package sqlite_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func TestMigrationPreservesSchemaOneIndex(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "findrail.db"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO sources(id,kind,name,root) VALUES('legacy','filesystem','Legacy','/legacy'); INSERT INTO documents(id,source_id,title,uri,path,content,content_hash,size_bytes,modified_at,scan_token) VALUES('old','legacy','legacy.md','file:///legacy/legacy.md','legacy.md','migration evidence','old',18,'2026-01-01T00:00:00Z','old')"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := find(t, s, "migration", "", 20); got.Total != 1 {
		t.Fatalf("old index lost: %+v", got)
	}
	sources, err := s.Sources(context.Background())
	if err != nil || len(sources) != 1 || sources[0].MaxTextBytes != 1<<20 || sources[0].MaxPDFBytes != 0 || sources[0].MaxDOCXBytes != 0 {
		t.Fatalf("migration source settings: %+v %v", sources, err)
	}
	evidence, err := s.Evidence(context.Background(), "old", 0)
	if err != nil || evidence.Text != "migration evidence" {
		t.Fatalf("legacy preview: %+v %v", evidence, err)
	}
}

func TestMigrationThreePreservesPDFPagesAndGitHubMetadata(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "findrail.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"001_init.sql", "002_local_alpha.sql", "003_github_snapshots.sql"} {
		data, e := os.ReadFile(filepath.Join("migrations", name))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(data)); e != nil {
			t.Fatalf("apply %s: %v", name, e)
		}
	}
	if _, err = db.Exec(`INSERT INTO sources(id,kind,name,root,max_text_bytes,max_pdf_bytes) VALUES('legacy-pdf','filesystem','PDF','/legacy',1048576,16777216),('legacy-gh','github','GitHub','owner/repo',1048576,0);
INSERT INTO documents(id,source_id,title,uri,path,content,content_hash,size_bytes,modified_at,scan_token,media_type,page_count) VALUES('legacy-pdf-doc','legacy-pdf','guide.pdf','file:///legacy/guide.pdf','guide.pdf','intro pdfpageproof','hash',10,'2026-01-01T00:00:00Z','old','application/pdf',2);
INSERT INTO document_pages(document_id,page_number,content) VALUES('legacy-pdf-doc',1,'intro'),('legacy-pdf-doc',2,'pdfpageproof retained');
INSERT INTO github_sources(source_id,repository_id,owner,repo,repository_url,ref_mode,ref_value,selected_path,max_bytes,policy_version,snapshot_sha,commit_time,registration_token,revision) VALUES('legacy-gh',7,'owner','repo','https://github.com/owner/repo','default','','',1048576,1,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','2026-01-01T00:00:00Z','ghtoken',3)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sources, err := s.Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("migrated sources: %+v", sources)
	}
	for _, source := range sources {
		if source.MaxDOCXBytes != 0 {
			t.Fatalf("legacy DOCX policy was enabled: %+v", source)
		}
		if source.ID == "legacy-gh" && (source.GitHub == nil || source.GitHub.SHA != strings.Repeat("a", 40) || source.GitHub.Revision != 3) {
			t.Fatalf("GitHub metadata lost: %+v", source)
		}
	}
	response := find(t, s, "pdfpageproof", "legacy-pdf", 10)
	if response.Total != 1 || response.Results[0].Page != 2 {
		t.Fatalf("PDF page FTS lost on migration: %+v", response)
	}
}

func TestFailedMigrationLeavesPriorSchemaVersionAndColumns(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "findrail.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sources(id TEXT PRIMARY KEY, max_text_bytes INTEGER); PRAGMA user_version=1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlite.Open(context.Background(), dir); err == nil {
		t.Fatal("migration with duplicate column unexpectedly succeeded")
	}
	db, err = sql.Open("sqlite", filepath.Join(dir, "findrail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("failed migration changed schema version: %d", version)
	}
	rows, err := db.Query("PRAGMA table_info(sources)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var def any
		if err := rows.Scan(&cid, &name, &kind, &notnull, &def, &pk); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if columns["max_pdf_bytes"] {
		t.Fatal("failed migration left a partial column")
	}
}

func TestMigrationTwoPreservesPDFPolicyAndPages(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "findrail.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"001_init.sql", "002_local_alpha.sql"} {
		data, e := os.ReadFile(filepath.Join("migrations", name))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(data)); e != nil {
			t.Fatalf("apply %s: %v", name, e)
		}
	}
	if _, err = db.Exec(`INSERT INTO sources(id,kind,name,root,max_text_bytes,max_pdf_bytes) VALUES('legacy-two','filesystem','Schema two','/legacy',524288,16777216);
INSERT INTO documents(id,source_id,title,uri,path,content,content_hash,size_bytes,modified_at,scan_token,media_type,page_count) VALUES('legacy-two-doc','legacy-two','paper.pdf','file:///legacy/paper.pdf','paper.pdf','intro pagemarker','hash',10,'2026-01-01T00:00:00Z','old','application/pdf',2);
INSERT INTO document_pages(document_id,page_number,content) VALUES('legacy-two-doc',1,'intro'),('legacy-two-doc',2,'pagemarker retained page');`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := sqlite.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sources, err := s.Sources(context.Background())
	if err != nil || len(sources) != 1 || sources[0].MaxTextBytes != 524288 || sources[0].MaxPDFBytes != 16777216 || sources[0].MaxDOCXBytes != 0 {
		t.Fatalf("schema 2 settings: %+v %v", sources, err)
	}
	response := find(t, s, "pagemarker", "legacy-two", 10)
	if response.Total != 1 || response.Results[0].Page != 2 {
		t.Fatalf("schema 2 PDF FTS lost: %+v", response)
	}
}

func TestDOCXFolderSnapshotDisableAndRemoval(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	root := t.TempDir()
	write(t, filepath.Join(root, "words.docx"), "not actually a zip")
	// A source scan must roll back rather than silently skip malformed DOCX.
	c, err := filesystem.NewWithOptions(root, filesystem.Options{MaxTextBytes: 128, MaxDOCXBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, s, c); err == nil {
		t.Fatal("corrupt DOCX unexpectedly committed")
	}
	// Replace the fixture with a valid package using the shared synthetic builder.
	writeDOCXPackage(t, filepath.Join(root, "words.docx"), "atomicdocx evidence")
	c, err = filesystem.NewWithOptions(root, filesystem.Options{MaxTextBytes: 128, MaxDOCXBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, s, c); err != nil {
		t.Fatal(err)
	}
	sources, err := s.Sources(ctx)
	if err != nil || len(sources) != 1 || sources[0].MaxDOCXBytes != 1<<20 {
		t.Fatalf("DOCX policy not persisted: %+v %v", sources, err)
	}
	if got := find(t, s, "atomicdocx", c.Source().ID, 10); got.Total != 1 {
		t.Fatalf("DOCX search: %+v", got)
	}
	e, err := s.Evidence(ctx, docID(c.Source().ID, "words.docx"), 0)
	if err != nil || !strings.Contains(e.Text, "atomicdocx") {
		t.Fatalf("DOCX evidence: %+v %v", e, err)
	}
	write(t, filepath.Join(root, "words.docx"), "truncated package")
	if _, err = ingest.Run(ctx, s, c); err == nil {
		t.Fatal("corruption did not fail the scan")
	}
	if got := find(t, s, "atomicdocx", c.Source().ID, 10); got.Total != 1 {
		t.Fatalf("failed DOCX scan lost committed evidence: %+v", got)
	}
	sources, err = s.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := sources[0]
	stale, err := filesystem.NewWithOptions(root, filesystem.Options{MaxTextBytes: old.MaxTextBytes, MaxPDFBytes: old.MaxPDFBytes, MaxDOCXBytes: old.MaxDOCXBytes, RegistrationToken: old.RegistrationToken, RegistrationRevision: old.RegistrationRevision})
	if err != nil {
		t.Fatal(err)
	}
	badConfigure, err := filesystem.NewWithOptions(root, filesystem.Options{MaxTextBytes: old.MaxTextBytes, MaxPDFBytes: old.MaxPDFBytes, MaxDOCXBytes: 2 << 20, RegistrationToken: old.RegistrationToken, RegistrationRevision: old.RegistrationRevision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Configure(ctx, s, badConfigure, old.RegistrationToken, old.RegistrationRevision, old.MaxDOCXBytes); err == nil {
		t.Fatal("corrupt configure committed")
	}
	current, err := s.Sources(ctx)
	if err != nil || current[0].MaxDOCXBytes != old.MaxDOCXBytes {
		t.Fatalf("failed configure changed persisted policy: %+v %v", current, err)
	}
	if got := find(t, s, "atomicdocx", c.Source().ID, 10); got.Total != 1 {
		t.Fatalf("failed configure lost old snapshot: %+v", got)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	started := make(chan struct{})
	configureDone := make(chan error, 1)
	pending := blockedScanConnector{source: badConfigure.Source(), entered: started}
	go func() {
		_, e := ingest.Configure(cancelCtx, s, pending, old.RegistrationToken, old.RegistrationRevision, old.MaxDOCXBytes)
		configureDone <- e
	}()
	<-started // The configuration UPDATE is now staged inside the open transaction.
	current, err = s.Sources(ctx)
	if err != nil || current[0].MaxDOCXBytes != old.MaxDOCXBytes {
		cancel()
		t.Fatalf("uncommitted configure policy leaked: %+v %v", current, err)
	}
	cancel()
	if err = <-configureDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled configure result: %v", err)
	}
	if got := find(t, s, "atomicdocx", c.Source().ID, 10); got.Total != 1 {
		t.Fatalf("canceled configure lost old snapshot: %+v", got)
	}
	writeDOCXPackage(t, filepath.Join(root, "words.docx"), "atomicdocx evidence")
	configured, err := filesystem.NewWithOptions(root, filesystem.Options{MaxTextBytes: old.MaxTextBytes, MaxPDFBytes: old.MaxPDFBytes, MaxDOCXBytes: 0, RegistrationToken: old.RegistrationToken, RegistrationRevision: old.RegistrationRevision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Configure(ctx, s, configured, old.RegistrationToken, old.RegistrationRevision, old.MaxDOCXBytes); err != nil {
		t.Fatal(err)
	}
	if got := find(t, s, "atomicdocx", c.Source().ID, 10); got.Total != 0 {
		t.Fatalf("configured source retained DOCX: %+v", got)
	}
	if _, err = ingest.Refresh(ctx, s, stale); !errors.Is(err, ingest.ErrSourceGone) {
		t.Fatalf("stale refresh crossed Configure: %v", err)
	}
	current, err = s.Sources(ctx)
	if err != nil || current[0].MaxDOCXBytes != 0 {
		t.Fatalf("configured policy not committed: %+v %v", current, err)
	}
	if err = s.ForgetSourceRegistration(ctx, old.ID, current[0].RegistrationToken); err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, s, c); err != nil {
		t.Fatal(err)
	} // re-add same path-derived ID
	if _, err = ingest.Configure(ctx, s, configured, old.RegistrationToken, old.RegistrationRevision, old.MaxDOCXBytes); !errors.Is(err, ingest.ErrSourceGone) {
		t.Fatalf("stale configure crossed forget/re-add ABA: %v", err)
	}
	current, err = s.Sources(ctx)
	if err != nil || current[0].MaxDOCXBytes != 1<<20 {
		t.Fatalf("stale configure changed re-added policy: %+v %v", current, err)
	}
	if got := find(t, s, "atomicdocx", c.Source().ID, 10); got.Total != 1 {
		t.Fatalf("stale configure replaced re-added snapshot: %+v", got)
	}
}

func TestConfigureCompareAndSwapAcrossSQLiteHandles(t *testing.T) {
	ctx := context.Background()
	dir, root := t.TempDir(), t.TempDir()
	writeDOCXPackage(t, filepath.Join(root, "race.docx"), "raceconfig evidence")
	writer, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	other, err := sqlite.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := filesystem.NewWithOptions(root, filesystem.Options{MaxTextBytes: 128, MaxDOCXBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, writer, conn); err != nil {
		t.Fatal(err)
	}
	sources, err := writer.Sources(ctx)
	if err != nil || len(sources) != 1 {
		t.Fatalf("source: %+v %v", sources, err)
	}
	registered := sources[0]
	makeSource := func(limit int64) connector.Source {
		c, e := filesystem.NewWithOptions(root, filesystem.Options{MaxTextBytes: registered.MaxTextBytes, MaxPDFBytes: registered.MaxPDFBytes, MaxDOCXBytes: limit, RegistrationToken: registered.RegistrationToken, RegistrationRevision: registered.RegistrationRevision})
		if e != nil {
			t.Fatal(e)
		}
		return c.Source()
	}
	first, err := writer.BeginConfigure(ctx, makeSource(0), registered.RegistrationToken, registered.RegistrationRevision, registered.MaxDOCXBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Upsert(ctx, connector.Document{ID: docID(registered.ID, "race.docx"), SourceID: registered.ID, Title: "race.docx", URI: "file:///race.docx", Path: "race.docx", Content: "raceconfig evidence", Hash: "same", MediaType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", ModifiedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	started, completed := make(chan struct{}), make(chan error, 1)
	secondSource := makeSource(2 << 20)
	go func() {
		close(started)
		scan, e := other.BeginConfigure(ctx, secondSource, registered.RegistrationToken, registered.RegistrationRevision, registered.MaxDOCXBytes)
		if scan != nil {
			_ = scan.Rollback()
		}
		completed <- e
	}()
	<-started
	if _, err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-completed; !errors.Is(err, ingest.ErrSourceGone) {
		t.Fatalf("stale second configure was not rejected: %v", err)
	}
	current, err := other.Sources(ctx)
	if err != nil || current[0].MaxDOCXBytes != 0 {
		t.Fatalf("configure CAS result: %+v %v", current, err)
	}
	if got := find(t, other, "raceconfig", registered.ID, 10); got.Total != 1 {
		t.Fatalf("configure lost atomic snapshot: %+v", got)
	}
}

type blockedScanConnector struct {
	source  connector.Source
	entered chan struct{}
}

func (c blockedScanConnector) Source() connector.Source { return c.source }
func (c blockedScanConnector) Scan(ctx context.Context, _ func(connector.Document) error) (connector.Report, error) {
	close(c.entered)
	<-ctx.Done()
	return connector.Report{}, ctx.Err()
}

func docID(sourceID, rel string) string {
	sum := sha256.Sum256([]byte(sourceID + "\x00" + rel))
	return fmt.Sprintf("doc_%x", sum)
}

func writeDOCXPackage(t *testing.T, file, text string) {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	parts := map[string]string{"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`, "_rels/.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="r" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`, "word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`}
	for n, v := range parts {
		f, e := z.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = f.Write([]byte(v))
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(file, b.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
}

func TestSearchReadsPreviousSnapshotDuringWrite(t *testing.T) {
	s := open(t)
	root := t.TempDir()
	write(t, filepath.Join(root, "notes.md"), "previous snapshot")
	_, conn := index(t, s, root)
	scan, err := s.BeginRefresh(context.Background(), conn.Source())
	if err != nil {
		t.Fatal(err)
	}
	defer scan.Rollback()
	if _, err := scan.Upsert(context.Background(), connector.Document{ID: "pending", SourceID: conn.Source().ID, Title: "next", Content: "pending update", Hash: "next"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := s.Search(ctx, search.Request{Query: "previous", Limit: 20})
	if err != nil || got.Total != 1 {
		t.Fatalf("read blocked or snapshot lost: %+v %v", got, err)
	}
	got, err = s.Search(ctx, search.Request{Query: "pending", Limit: 20})
	if err != nil || got.Total != 0 {
		t.Fatalf("partial update visible: %+v %v", got, err)
	}
	if _, err := scan.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := find(t, s, "pending", "", 20); got.Total != 1 {
		t.Fatalf("committed snapshot absent: %+v", got)
	}
}

func TestPDFEvidenceUpdateAndDelete(t *testing.T) {
	s := open(t)
	source := connector.Source{ID: "pdfsource", Kind: "test", Name: "PDF"}
	doc := connector.Document{ID: "pdfdoc", SourceID: source.ID, Title: "guide.pdf", URI: "file:///guide.pdf", Path: "guide.pdf", MediaType: "application/pdf", Content: "intro webhook retry", Hash: "v1", Pages: []connector.Page{{Number: 1, Text: "intro"}, {Number: 2, Text: "webhook retry"}}}
	if _, err := ingest.Run(context.Background(), s, collisionConnector{source, doc}); err != nil {
		t.Fatal(err)
	}
	got := find(t, s, "webhook", "", 20)
	if got.Total != 1 || got.Results[0].Page != 2 || !strings.HasSuffix(got.Results[0].URI, "#page=2") {
		t.Fatalf("page citation: %+v", got)
	}
	e, err := s.Evidence(context.Background(), doc.ID, 2)
	if err != nil || e.Text != "webhook retry" || e.PageCount != 2 {
		t.Fatalf("preview: %+v %v", e, err)
	}
	if _, err := s.Evidence(context.Background(), doc.ID, 3); !errors.Is(err, search.ErrPage) {
		t.Fatalf("page range: %v", err)
	}
	doc.Hash = "v2"
	doc.Content = "new evidence"
	doc.Pages = []connector.Page{{Number: 1, Text: "new evidence"}}
	if _, err := ingest.Run(context.Background(), s, collisionConnector{source, doc}); err != nil {
		t.Fatal(err)
	}
	if got := find(t, s, "webhook", "", 20); got.Total != 0 {
		t.Fatalf("stale PDF text: %+v", got)
	}
	if got := find(t, s, "evidence", "", 20); got.Total != 1 || got.Results[0].Page != 1 {
		t.Fatalf("page index update: %+v", got)
	}
	if err := s.ForgetSource(context.Background(), source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Evidence(context.Background(), doc.ID, 0); !errors.Is(err, search.ErrNotFound) {
		t.Fatalf("forgotten snapshot: %v", err)
	}
	if _, err := s.BeginRefresh(context.Background(), source); !errors.Is(err, ingest.ErrSourceGone) {
		t.Fatalf("watcher could recreate forgotten source: %v", err)
	}
	sources, err := s.Sources(context.Background())
	if err != nil || len(sources) != 0 {
		t.Fatalf("source resurrected: %+v %v", sources, err)
	}
}

func TestEvidenceBoundsAndSnapshot(t *testing.T) {
	s := open(t)
	root := t.TempDir()
	path := filepath.Join(root, "long.md")
	write(t, path, strings.Repeat("文", 65540))
	_, c := index(t, s, root)
	got := find(t, s, "long", "", 20).Results[0]
	write(t, path, "new content outside index")
	e, err := s.Evidence(context.Background(), got.ID, 0)
	if err != nil || !e.Truncated || len([]rune(e.Text)) != 65536 || strings.Contains(e.Text, "new content") {
		t.Fatalf("bounded snapshot: %d %+v %v", len([]rune(e.Text)), e.Truncated, err)
	}
	if _, err := s.Evidence(context.Background(), got.ID, 1); !errors.Is(err, search.ErrPage) {
		t.Fatalf("text page accepted: %v", err)
	}
	if err := s.ForgetSource(context.Background(), c.Source().ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.Refresh(context.Background(), s, c); !errors.Is(err, ingest.ErrSourceGone) {
		t.Fatalf("refresh after forget: %v", err)
	}
}
