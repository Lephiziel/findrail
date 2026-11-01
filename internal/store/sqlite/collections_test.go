package sqlite

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/internal/evidencebundle"
	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/pkg/connector"
	modernsqlite "modernc.org/sqlite"
)

var bundleCancelTrigger = struct {
	register         sync.Once
	mu               sync.Mutex
	entered, release chan struct{}
	err              error
}{}

func registerBundleCancelTrigger(t *testing.T) {
	t.Helper()
	bundleCancelTrigger.register.Do(func() {
		bundleCancelTrigger.err = modernsqlite.RegisterScalarFunction("findrail_test_cancel_bundle_insert", 0, func(*modernsqlite.FunctionContext, []driver.Value) (driver.Value, error) {
			bundleCancelTrigger.mu.Lock()
			entered, release := bundleCancelTrigger.entered, bundleCancelTrigger.release
			bundleCancelTrigger.mu.Unlock()
			close(entered)
			<-release
			return int64(1), nil
		})
	})
	if bundleCancelTrigger.err != nil {
		t.Fatal(bundleCancelTrigger.err)
	}
}

func TestPersistentCollectionSnapshotRevisionAndDocumentRemoval(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	root := t.TempDir()
	doc := filepath.Join(root, "note.md")
	if e := os.WriteFile(doc, []byte("alpha beta gamma"), 0600); e != nil {
		t.Fatal(e)
	}
	s, e := Open(ctx, data)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if s != nil {
			_ = s.Close()
		}
	}()
	conn, e := filesystem.New(root, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ingest.Run(ctx, s, conn); e != nil {
		t.Fatal(e)
	}
	// Resolve the indexed ID without trusting metadata from a client.
	var id string
	if e = s.readers.QueryRowContext(ctx, "SELECT id FROM documents LIMIT 1").Scan(&id); e != nil {
		t.Fatal(e)
	}
	c, e := s.CreateCollection(ctx, "Research")
	if e != nil {
		t.Fatal(e)
	}
	pinned, e := s.PinCollectionEntry(ctx, c.ID, c.Revision, id, 0, "alpha beta", false, collectionIdentity(t, s, id))
	if e != nil {
		t.Fatal(e)
	}
	if pinned.Revision != 2 || len(pinned.Entries) != 1 {
		t.Fatalf("pinned=%+v", pinned)
	}
	entry := pinned.Entries[0]
	entry.Note = "note"
	saved, e := s.UpdateCollection(ctx, c.ID, pinned.Revision, "Renamed", []CollectionEntry{entry})
	if e != nil {
		t.Fatal(e)
	}
	if saved.Revision != 3 || saved.Entries[0].Note != "note" {
		t.Fatalf("saved=%+v", saved)
	}
	if _, e = s.UpdateCollection(ctx, c.ID, pinned.Revision, "Stale", nil); e != ErrCollectionConflict {
		t.Fatalf("stale edit=%v", e)
	}
	forged := saved.Entries[0]
	forged.Evidence.URI = "file:///forged"
	if _, e = s.UpdateCollection(ctx, c.ID, saved.Revision, "Forged", []CollectionEntry{forged}); e == nil {
		t.Fatal("accepted forged evidence snapshot")
	}
	oldHash := saved.Entries[0].Evidence.ContentHash
	if e = os.WriteFile(doc, []byte("changed indexed version"), 0600); e != nil {
		t.Fatal(e)
	}
	conn, e = filesystem.New(root, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ingest.Run(ctx, s, conn); e != nil {
		t.Fatal(e)
	}
	historical, e := s.LoadCollection(ctx, c.ID)
	if e != nil {
		t.Fatal(e)
	}
	if historical.Entries[0].Evidence.ContentHash != oldHash || historical.Entries[0].Evidence.Text != "alpha beta gamma" {
		t.Fatalf("refresh rewrote historical snapshot: %+v", historical.Entries[0])
	}
	historicalBundle, err := s.ExportEvidenceBundle(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	historicalPreview, err := s.PreviewEvidenceBundle(ctx, historicalBundle, map[string]string{"s1": conn.Source().ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(historicalPreview.Entries) != 1 || historicalPreview.Entries[0].Status != "changed_hash" {
		t.Fatalf("historical evidence silently became current: %+v", historicalPreview)
	}
	if e = os.Remove(doc); e != nil {
		t.Fatal(e)
	}
	conn, e = filesystem.New(root, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ingest.Run(ctx, s, conn); e != nil {
		t.Fatal(e)
	}
	after, e := s.LoadCollection(ctx, c.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(after.Entries) != 0 || after.Title != "Renamed" {
		t.Fatalf("removed document did not cascade: %+v", after)
	}
	if e = s.DeleteCollection(ctx, c.ID, after.Revision); e != nil {
		t.Fatal(e)
	}
}

func TestEvidenceBundleExportPreviewAndAtomicImport(t *testing.T) {
	registerBundleCancelTrigger(t)
	ctx := context.Background()
	data, root := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "note.md")
	if err := os.WriteFile(path, []byte("αβ repeated excerpt repeated excerpt"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	conn, err := filesystem.New(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, s, conn); err != nil {
		t.Fatal(err)
	}
	var docID, sourceID string
	if err = s.readers.QueryRowContext(ctx, "SELECT id,source_id FROM documents LIMIT 1").Scan(&docID, &sourceID); err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateCollection(ctx, "Bundle source")
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.PinCollectionEntry(ctx, c.ID, c.Revision, docID, 0, "repeated excerpt", false, collectionIdentity(t, s, docID))
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.SaveCollection(ctx, c.ID, c.Revision, c.Title, []CollectionEdit{{Key: c.Entries[0].Key, Note: "keep exact note"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.ExportEvidenceBundle(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Entries) != 1 || b.Entries[0].StartChar != 3 || b.Entries[0].Note != "keep exact note" {
		t.Fatalf("Unicode offset/note not preserved: %+v", b.Entries)
	}
	raw, err := evidencebundle.Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	b, err = evidencebundle.Decode(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewEvidenceBundle(ctx, b, map[string]string{"s1": sourceID})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Entries) != 1 || preview.Entries[0].Status != "match" {
		t.Fatalf("preview=%+v", preview)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*evidencebundle.Bundle)
		status string
	}{{"unknown source", func(x *evidencebundle.Bundle) {}, "unknown_source"}, {"missing document", func(x *evidencebundle.Bundle) { x.Entries[0].RelativePath = "elsewhere.md" }, "missing_document"}, {"changed hash", func(x *evidencebundle.Bundle) { x.Entries[0].ContentHash = strings.Repeat("0", 64) }, "changed_hash"}, {"quote mismatch", func(x *evidencebundle.Bundle) { x.Entries[0].Text = "different quote" }, "quote_mismatch"}} {
		bad := b
		bad.Entries = append([]evidencebundle.Entry(nil), b.Entries...)
		tc.mutate(&bad)
		mapping := map[string]string{"s1": sourceID}
		if tc.name == "unknown source" {
			mapping["s1"] = "missing-source"
		}
		got, e := s.PreviewEvidenceBundle(ctx, bad, mapping)
		if e != nil || len(got.Entries) != 1 || got.Entries[0].Status != tc.status || got.Claim != "" {
			t.Fatalf("%s preview=%+v err=%v", tc.name, got, e)
		}
	}
	imported, err := s.ImportEvidenceBundle(ctx, b, map[string]string{"s1": sourceID}, "Imported", preview.Binding)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Entries) != 1 || imported.Entries[0].Note != "keep exact note" || imported.Entries[0].Excerpt != "repeated excerpt" {
		t.Fatalf("import=%+v", imported)
	}
	if _, err = s.ImportEvidenceBundle(ctx, b, map[string]string{"s1": sourceID}, "", preview.Binding); err == nil {
		t.Fatal("invalid title unexpectedly imported")
	}
	if _, err = s.LoadCollection(ctx, imported.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	restarted, err := s.LoadCollection(ctx, imported.ID)
	if err != nil || len(restarted.Entries) != 1 || restarted.Entries[0].Note != "keep exact note" || restarted.Entries[0].Excerpt != "repeated excerpt" {
		t.Fatalf("restart changed imported evidence: %+v err=%v", restarted, err)
	}
	// A separate destination index has different source/document identities and
	// accepts the transfer only through the explicit source mapping.
	destRoot := t.TempDir()
	if err = os.WriteFile(filepath.Join(destRoot, "note.md"), []byte("αβ repeated excerpt repeated excerpt"), 0600); err != nil {
		t.Fatal(err)
	}
	dest, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	destConn, err := filesystem.New(destRoot, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, dest, destConn); err != nil {
		t.Fatal(err)
	}
	var destSource string
	if err = dest.readers.QueryRowContext(ctx, "SELECT source_id FROM documents LIMIT 1").Scan(&destSource); err != nil {
		t.Fatal(err)
	}
	destPreview, err := dest.PreviewEvidenceBundle(ctx, b, map[string]string{"s1": destSource})
	if err != nil || !destPreview.Ready {
		t.Fatalf("destination preview=%+v err=%v", destPreview, err)
	}
	destCollection, err := dest.ImportEvidenceBundle(ctx, b, map[string]string{"s1": destSource}, "Moved collection", destPreview.Binding)
	if err != nil {
		t.Fatal(err)
	}
	if len(destCollection.Entries) != 1 || destCollection.Entries[0].Evidence.SourceID != destSource || destCollection.Entries[0].Note != "keep exact note" {
		t.Fatalf("cross-index import=%+v", destCollection)
	}
	// A failure after the collection row and first entry are inserted rolls the
	// whole transaction back, including all earlier entry inserts.
	twice := b
	twice.Entries = append(append([]evidencebundle.Entry(nil), b.Entries...), b.Entries[0])
	if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fail_second_bundle_entry BEFORE INSERT ON evidence_collection_entries WHEN NEW.ordinal=1 BEGIN SELECT RAISE(ABORT,'synthetic insert failure'); END`); err != nil {
		t.Fatal(err)
	}
	var before int
	if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM evidence_collections").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportEvidenceBundle(ctx, twice, map[string]string{"s1": sourceID}, "Rollback", preview.Binding); err == nil {
		t.Fatal("insert trigger did not fail import")
	}
	var after int
	if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM evidence_collections").Scan(&after); err != nil || after != before {
		t.Fatalf("failed import left collection: before=%d after=%d err=%v", before, after, err)
	}
	if _, err = s.db.ExecContext(ctx, "DROP TRIGGER fail_second_bundle_entry"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER cancel_bundle_after_first_entry AFTER INSERT ON evidence_collection_entries WHEN NEW.ordinal=0 BEGIN SELECT findrail_test_cancel_bundle_insert(); END`); err != nil {
		t.Fatal(err)
	}
	bundleCancelTrigger.mu.Lock()
	bundleCancelTrigger.entered = make(chan struct{})
	bundleCancelTrigger.release = make(chan struct{})
	entered, release := bundleCancelTrigger.entered, bundleCancelTrigger.release
	bundleCancelTrigger.mu.Unlock()
	cancelBundle := b
	cancelBundle.Entries = append(append([]evidencebundle.Entry(nil), b.Entries...), b.Entries[0])
	before = after
	importCtx, cancelImport := context.WithCancel(ctx)
	cancelResult := make(chan error, 1)
	go func() {
		_, e := s.ImportEvidenceBundle(importCtx, cancelBundle, map[string]string{"s1": sourceID}, "Canceled", preview.Binding)
		cancelResult <- e
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		cancelImport()
		t.Fatal("import did not reach first entry insert")
	}
	cancelImport()
	close(release)
	if e := <-cancelResult; !errors.Is(e, context.Canceled) {
		t.Fatalf("mid-insert cancel error=%v", e)
	}
	if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM evidence_collections").Scan(&after); err != nil || after != before {
		t.Fatalf("canceled import left collection: before=%d after=%d err=%v", before, after, err)
	}
	if _, err = s.db.ExecContext(ctx, "DROP TRIGGER cancel_bundle_after_first_entry"); err != nil {
		t.Fatal(err)
	}
	// Registration re-creation with the same stable source ID invalidates the
	// preview binding even when path and content become identical again.
	stalePreview, err := s.PreviewEvidenceBundle(ctx, b, map[string]string{"s1": sourceID})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ForgetSource(ctx, sourceID); err != nil {
		t.Fatal(err)
	}
	conn, err = filesystem.New(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, s, conn); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportEvidenceBundle(ctx, b, map[string]string{"s1": sourceID}, "Stale registration", stalePreview.Binding); !errors.Is(err, ErrCollectionSnapshotChanged) {
		t.Fatalf("re-added source accepted stale preview: %v", err)
	}
	scanPreview, err := s.PreviewEvidenceBundle(ctx, b, map[string]string{"s1": sourceID})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("changed after preview"), 0600); err != nil {
		t.Fatal(err)
	}
	conn, err = filesystem.New(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, s, conn); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportEvidenceBundle(ctx, b, map[string]string{"s1": sourceID}, "Changed during scan", scanPreview.Binding); !errors.Is(err, ErrCollectionSnapshotChanged) {
		t.Fatalf("changed indexed snapshot accepted: %v", err)
	}
	if err = os.WriteFile(path, []byte("αβ repeated excerpt repeated excerpt"), 0600); err != nil {
		t.Fatal(err)
	}
	conn, err = filesystem.New(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, s, conn); err != nil {
		t.Fatal(err)
	}
	current, err := s.PreviewEvidenceBundle(ctx, b, map[string]string{"s1": sourceID})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 97; i++ {
		if _, err = s.CreateCollection(ctx, fmt.Sprintf("quota fill %02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, e := s.ImportEvidenceBundle(ctx, b, map[string]string{"s1": sourceID}, "Concurrent", current.Binding)
			results <- e
		}()
	}
	succeeded := 0
	for i := 0; i < 2; i++ {
		if e := <-results; e == nil {
			succeeded++
		} else if !errors.Is(e, ErrCollectionLimit) {
			t.Fatalf("concurrent quota import error: %v", e)
		}
	}
	if succeeded != 1 {
		t.Fatalf("quota race successes=%d, want exactly one", succeeded)
	}
}

func TestEmptyEvidenceBundleImportsAsNewCollection(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw, err := evidencebundle.Encode(evidencebundle.Bundle{Title: "Empty portable bundle"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := evidencebundle.Decode(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PreviewEvidenceBundle(ctx, b, map[string]string{})
	if err != nil || !p.Ready {
		t.Fatalf("empty preview=%+v err=%v", p, err)
	}
	c, err := s.ImportEvidenceBundle(ctx, b, map[string]string{}, "Empty imported", p.Binding)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Entries) != 0 || c.Revision != 1 {
		t.Fatalf("empty collection=%+v", c)
	}
}

func TestEvidenceBundleDOCXOffsetAndPDFPageTwoRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	writeSyntheticDOCX(t, filepath.Join(root, "appendix.docx"))
	if err = os.WriteFile(filepath.Join(root, "synthetic.pdf"), []byte("fake pdf body"), 0600); err != nil {
		t.Fatal(err)
	}
	conn, err := filesystem.NewWithOptions(root, filesystem.Options{MaxTextBytes: 1 << 20, MaxDOCXBytes: 1 << 20, MaxPDFBytes: 1 << 20, ExtractPDF: func(context.Context, io.Reader, int64) ([]connector.Page, error) {
		return []connector.Page{{Number: 1, Text: "first page"}, {Number: 2, Text: "prefix цитата suffix"}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, s, conn); err != nil {
		t.Fatal(err)
	}
	var docxID, pdfID, sourceID string
	if err = s.readers.QueryRowContext(ctx, "SELECT id,source_id FROM documents WHERE path='appendix.docx'").Scan(&docxID, &sourceID); err != nil {
		t.Fatal(err)
	}
	if err = s.readers.QueryRowContext(ctx, "SELECT id FROM documents WHERE path='synthetic.pdf'").Scan(&pdfID); err != nil {
		t.Fatal(err)
	}
	docx, err := s.Evidence(ctx, docxID, 0)
	if err != nil {
		t.Fatal(err)
	}
	pdf, err := s.Evidence(ctx, pdfID, 2)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateCollection(ctx, "Mixed evidence")
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.PinCollectionEntry(ctx, c.ID, c.Revision, docxID, 0, "DOCX quote", false, docx.OriginalIdentity)
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.PinCollectionEntry(ctx, c.ID, c.Revision, pdfID, 2, "цитата", false, pdf.OriginalIdentity)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.ExportEvidenceBundle(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Entries) != 2 || b.Entries[0].StartChar != 7 || b.Entries[0].MediaType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" || b.Entries[1].Page != 2 || b.Entries[1].StartChar != 7 {
		t.Fatalf("offset/page attribution lost: %+v", b.Entries)
	}
	badPage := b
	badPage.Entries = append([]evidencebundle.Entry(nil), b.Entries...)
	badPage.Entries[1].Page = 3
	badPagePreview, err := s.PreviewEvidenceBundle(ctx, badPage, map[string]string{"s1": sourceID})
	if err != nil || badPagePreview.Entries[1].Status != "invalid_page" {
		t.Fatalf("invalid PDF page preview=%+v err=%v", badPagePreview, err)
	}
	p, err := s.PreviewEvidenceBundle(ctx, b, map[string]string{"s1": sourceID})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Ready {
		t.Fatalf("mixed evidence preview=%+v", p)
	}
	imported, err := s.ImportEvidenceBundle(ctx, b, map[string]string{"s1": sourceID}, "Mixed imported", p.Binding)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Entries) != 2 || imported.Entries[0].Excerpt != "DOCX quote" || imported.Entries[1].Evidence.Page != 2 || imported.Entries[1].Excerpt != "цитата" {
		t.Fatalf("mixed import=%+v", imported)
	}
	reexport, err := s.ExportEvidenceBundle(ctx, imported.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reexport.Entries) != 2 || reexport.Entries[0].StartChar != b.Entries[0].StartChar || reexport.Entries[1].Page != 2 || reexport.Entries[1].StartChar != 7 {
		t.Fatalf("re-export lost locators: %+v", reexport.Entries)
	}
}

func writeSyntheticDOCX(t *testing.T, path string) {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	parts := map[string]string{"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`, "_rels/.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="r1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`, "word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>prefix DOCX quote suffix</w:t></w:r></w:p></w:body></w:document>`}
	for name, content := range parts {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceBundlePreviewEnforcesSnapshotTextQuotaAtBoundary(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	quote := strings.Repeat("🫶", 65536)
	if err = os.WriteFile(filepath.Join(root, "large.txt"), []byte(strings.Repeat("🫶", 262144)), 0600); err != nil {
		t.Fatal(err)
	}
	conn, err := filesystem.New(root, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, s, conn); err != nil {
		t.Fatal(err)
	}
	var sourceID string
	if err = s.readers.QueryRowContext(ctx, "SELECT source_id FROM documents LIMIT 1").Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	base := evidencebundle.Bundle{Title: "quota", Sources: []evidencebundle.Source{{Alias: "s1", Kind: "filesystem", DisplayName: "Synthetic"}}, Entries: make([]evidencebundle.Entry, 4)}
	for i := range base.Entries {
		base.Entries[i] = evidencebundle.Entry{SourceAlias: "s1", RelativePath: "large.txt", MediaType: "text/plain", ContentHash: func() string {
			var h string
			_ = s.readers.QueryRowContext(ctx, "SELECT content_hash FROM documents LIMIT 1").Scan(&h)
			return h
		}(), StartChar: 0, Text: quote}
	}
	if _, err = evidencebundle.Encode(base); err != nil {
		t.Fatalf("boundary bundle invalid: %v", err)
	}
	preview, err := s.PreviewEvidenceBundle(ctx, base, map[string]string{"s1": sourceID})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range preview.Entries {
		if row.Status != "match" {
			t.Fatalf("2 MiB boundary rejected: %+v", preview)
		}
	}
	if _, err = s.ImportEvidenceBundle(ctx, base, map[string]string{"s1": sourceID}, "Boundary", preview.Binding); err != nil {
		t.Fatalf("2 MiB boundary import: %v", err)
	}
	over := base
	over.Entries = append(append([]evidencebundle.Entry(nil), base.Entries...), base.Entries[0])
	preview, err = s.PreviewEvidenceBundle(ctx, over, map[string]string{"s1": sourceID})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range preview.Entries {
		if row.Status != "quota_conflict" {
			t.Fatalf("over-quota entry status=%+v", row)
		}
	}
}

func TestPassageAnchorsLateLiteralAndPinsCanonicalWindow(t *testing.T) {
	ctx := context.Background()
	data, root := t.TempDir(), t.TempDir()
	content := strings.Repeat("padding ", 10000) + "needlelatebody tail"
	if err := os.WriteFile(filepath.Join(root, "late.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			_ = s.Close()
		}
	}()
	conn, err := filesystem.New(root, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, s, conn); err != nil {
		t.Fatal(err)
	}
	var id string
	if err = s.readers.QueryRowContext(ctx, "SELECT id FROM documents").Scan(&id); err != nil {
		t.Fatal(err)
	}
	ev, err := s.Evidence(ctx, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	passage, err := s.Passage(ctx, id, "needlelatebody", ev.OriginalIdentity, 0)
	if err != nil {
		t.Fatal(err)
	}
	if passage.MatchKind != "literal_body" || passage.TextStartChar < 80000 || !strings.Contains(passage.Text, "needlelatebody") || passage.TextTotalChars != len([]rune(content)) {
		t.Fatalf("passage=%+v", passage)
	}
	c, err := s.CreateCollection(ctx, "Late quote")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PinCollectionEntryAt(ctx, c.ID, c.Revision, id, 0, "needlelatebody", false, ev.OriginalIdentity, 0); err == nil {
		t.Fatal("accepted an excerpt outside the canonical prefix window")
	}
	unchanged, err := s.LoadCollection(ctx, c.ID)
	if err != nil || unchanged.Revision != c.Revision || len(unchanged.Entries) != 0 {
		t.Fatalf("partial failed pin: %+v err=%v", unchanged, err)
	}
	saved, err := s.PinCollectionEntryAt(ctx, c.ID, c.Revision, id, 0, "needlelatebody", false, ev.OriginalIdentity, passage.TextStartChar)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Entries[0]; got.Evidence.TextStartChar != passage.TextStartChar || !strings.Contains(got.Evidence.Text, "needlelatebody") {
		t.Fatalf("saved=%+v", got)
	}
	var column int
	if err = s.readers.QueryRowContext(ctx, "SELECT preview_start_char FROM evidence_collection_entries WHERE collection_id=?", c.ID).Scan(&column); err != nil || column != passage.TextStartChar {
		t.Fatalf("column=%d err=%v", column, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := s.LoadCollection(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Entries[0].Evidence.TextStartChar != passage.TextStartChar || restored.Entries[0].Excerpt != "needlelatebody" {
		t.Fatalf("restored=%+v", restored.Entries[0])
	}
}

func TestForgetAndReaddDoesNotResurrectCollectionEntry(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	root := t.TempDir()
	if e = os.WriteFile(filepath.Join(root, "note.md"), []byte("forget lifecycle evidence"), 0600); e != nil {
		t.Fatal(e)
	}
	conn, e := filesystem.New(root, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ingest.Run(ctx, s, conn); e != nil {
		t.Fatal(e)
	}
	var id string
	if e = s.readers.QueryRowContext(ctx, "SELECT id FROM documents LIMIT 1").Scan(&id); e != nil {
		t.Fatal(e)
	}
	c, e := s.CreateCollection(ctx, "Keep name")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.PinCollectionEntry(ctx, c.ID, c.Revision, id, 0, "forget lifecycle", false, collectionIdentity(t, s, id)); e != nil {
		t.Fatal(e)
	}
	if e = s.ForgetSource(ctx, conn.Source().ID); e != nil {
		t.Fatal(e)
	}
	after, e := s.LoadCollection(ctx, c.ID)
	if e != nil || len(after.Entries) != 0 || after.Title != "Keep name" {
		t.Fatalf("forget result=%+v err=%v", after, e)
	}
	conn, e = filesystem.New(root, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ingest.Run(ctx, s, conn); e != nil {
		t.Fatal(e)
	}
	after, e = s.LoadCollection(ctx, c.ID)
	if e != nil || len(after.Entries) != 0 {
		t.Fatalf("re-add restored forgotten entry: %+v %v", after, e)
	}
}

func TestCollectionSaveIsAtomicForNewPinsAndOrdering(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.md"), []byte("first saved excerpt and second saved excerpt"), 0600)
	conn, e := filesystem.New(root, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ingest.Run(ctx, s, conn); e != nil {
		t.Fatal(e)
	}
	var id string
	if e = s.readers.QueryRowContext(ctx, "SELECT id FROM documents LIMIT 1").Scan(&id); e != nil {
		t.Fatal(e)
	}
	c, e := s.CreateCollection(ctx, "Batch")
	if e != nil {
		t.Fatal(e)
	}
	bad := []CollectionEdit{{Note: "first", DocumentID: id, OriginalIdentity: collectionIdentity(t, s, id), Excerpt: "first saved excerpt"}, {Note: "bad", DocumentID: "missing", Excerpt: "missing"}}
	if _, e = s.SaveCollection(ctx, c.ID, c.Revision, "Batch", bad); e == nil {
		t.Fatal("partial save unexpectedly succeeded")
	}
	after, e := s.LoadCollection(ctx, c.ID)
	if e != nil || after.Revision != c.Revision || len(after.Entries) != 0 {
		t.Fatalf("failed batch committed partially: %+v %v", after, e)
	}
	good := []CollectionEdit{{Note: "first", DocumentID: id, OriginalIdentity: collectionIdentity(t, s, id), Excerpt: "first saved excerpt"}, {Note: "second", DocumentID: id, OriginalIdentity: collectionIdentity(t, s, id), Excerpt: "second saved excerpt"}}
	saved, e := s.SaveCollection(ctx, c.ID, c.Revision, "Renamed", good)
	if e != nil {
		t.Fatal(e)
	}
	if len(saved.Entries) != 2 || saved.Entries[0].Note != "first" || saved.Revision != 2 {
		t.Fatalf("saved=%+v", saved)
	}
	if _, e = s.SaveCollection(ctx, c.ID, c.Revision, "stale", good); e != ErrCollectionConflict {
		t.Fatalf("stale save=%v", e)
	}
}

func TestCollectionCountUTF8NameAndAggregateTextQuotas(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < maxCollections; i++ {
		if _, e = s.CreateCollection(ctx, fmt.Sprintf("set %d", i)); e != nil {
			t.Fatalf("create %d: %v", i, e)
		}
	}
	if _, e = s.CreateCollection(ctx, "101"); e != ErrCollectionLimit {
		t.Fatalf("101st collection=%v", e)
	}
	s.Close()
	s, e = Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.CreateCollection(ctx, strings.Repeat("🦊", 64)); e != nil {
		t.Fatalf("256-byte UTF-8 name rejected: %v", e)
	}
	if _, e = s.CreateCollection(ctx, strings.Repeat("🦊", 65)); e == nil {
		t.Fatal("accepted >256 UTF-8 byte name")
	}
	root := t.TempDir()
	doc := filepath.Join(root, "large.md")
	body := strings.Repeat("a", 65500) + "z"
	if e = os.WriteFile(doc, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	conn, e := filesystem.New(root, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ingest.Run(ctx, s, conn); e != nil {
		t.Fatal(e)
	}
	var id string
	if e = s.readers.QueryRowContext(ctx, "SELECT id FROM documents LIMIT 1").Scan(&id); e != nil {
		t.Fatal(e)
	}
	c, e := s.CreateCollection(ctx, "aggregate")
	if e != nil {
		t.Fatal(e)
	}
	for i := 1; i <= maxCollectionEntries; i++ {
		_, e = s.PinCollectionEntry(ctx, c.ID, int64(i), id, 0, strings.Repeat("a", i)+"z", false, collectionIdentity(t, s, id))
		if i <= 32 && e != nil {
			t.Fatalf("entry %d: %v", i, e)
		}
		if i == 33 && e != ErrCollectionLimit {
			t.Fatalf("aggregate overflow error=%v", e)
		}
	}
	loaded, e := s.LoadCollection(ctx, c.ID)
	if e != nil || len(loaded.Entries) != 32 || loaded.Revision != 33 {
		t.Fatalf("partial quota write: len=%d revision=%d err=%v", len(loaded.Entries), loaded.Revision, e)
	}
}

func TestSerializedPayloadQuotaAt64MiBBoundary(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "one.md"), []byte("quota binding"), 0600)
	conn, e := filesystem.New(root, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ingest.Run(ctx, s, conn); e != nil {
		t.Fatal(e)
	}
	var doc string
	if e = s.readers.QueryRowContext(ctx, "SELECT id FROM documents LIMIT 1").Scan(&doc); e != nil {
		t.Fatal(e)
	}
	holderTitle := strings.Repeat("h", 256)
	finalTitle := strings.Repeat("f", 256)
	holder, e := s.CreateCollection(ctx, holderTitle)
	if e != nil {
		t.Fatal(e)
	}
	rowMax := 4 << 20
	target := (64 << 20) - len(holderTitle) - len(finalTitle)
	last := target - 15*rowMax
	if last <= 0 || last > rowMax {
		t.Fatalf("invalid test accounting last=%d", last)
	}
	full := strings.Repeat("x", rowMax)
	tail := strings.Repeat("y", last)
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 16; i++ {
		payload := full
		if i == 15 {
			payload = tail
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO evidence_collection_entries(collection_id,entry_id,ordinal,source_id,document_id,snapshot_json) VALUES(?,?,?,?,?,?)", holder.ID, fmt.Sprintf("quota-%02d", i), i, conn.Source().ID, doc, payload); e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateCollection(ctx, finalTitle); e != nil {
		t.Fatalf("exact 64 MiB quota rejected: %v", e)
	}
	if _, e = s.CreateCollection(ctx, "x"); e != ErrCollectionLimit {
		t.Fatalf("payload overage=%v", e)
	}
}

func TestSchemaSixMigrationAndRollback(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(ctx, dir)
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	if e = os.WriteFile(filepath.Join(root, "schema6.md"), []byte("schema six retained search data"), 0600); e != nil {
		t.Fatal(e)
	}
	conn, e := filesystem.New(root, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ingest.Run(ctx, s, conn); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.ExecContext(ctx, "CREATE TABLE sources_copy AS SELECT * FROM sources; DROP TABLE evidence_collection_entries; DROP TABLE evidence_collections; PRAGMA user_version=6"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(ctx, dir)
	if e != nil {
		t.Fatal(e)
	}
	var version int
	if e = s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); e != nil || version != 8 {
		t.Fatalf("version=%d err=%v", version, e)
	}
	if sources, err := s.Sources(ctx); err != nil || len(sources) != 1 || sources[0].Documents != 1 {
		t.Fatalf("sources after migration=%+v err=%v", sources, err)
	}
	if result, err := s.Search(ctx, search.Request{Query: "schema six retained", Limit: 5}); err != nil || result.Total != 1 {
		t.Fatalf("search after migration=%+v err=%v", result, err)
	}
	s.Close()
	broken := t.TempDir()
	s, e = Open(ctx, broken)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.ExecContext(ctx, "DROP TABLE evidence_collection_entries; DROP TABLE evidence_collections; CREATE TABLE evidence_collections(id TEXT); PRAGMA user_version=6"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if _, e = Open(ctx, broken); e == nil {
		t.Fatal("expected migration failure")
	}
	db, e := sql.Open("sqlite", filepath.Join(broken, "findrail.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = db.QueryRow("PRAGMA user_version").Scan(&version); e != nil || version != 6 {
		t.Fatalf("failed migration changed version=%d err=%v", version, e)
	}
}

func collectionIdentity(t testing.TB, s *Store, id string) string {
	t.Helper()
	v, err := s.Evidence(context.Background(), id, 0)
	if err != nil {
		t.Fatal(err)
	}
	return v.OriginalIdentity
}

func TestDelayedNewPinsCannotFollowForgottenSourceRegistration(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "same.md"), []byte("same passage after registration"), 0600); err != nil {
		t.Fatal(err)
	}
	conn, err := filesystem.New(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, s, conn); err != nil {
		t.Fatal(err)
	}
	result, err := s.Search(ctx, search.Request{Query: "passage", Limit: 5})
	if err != nil || len(result.Results) != 1 {
		t.Fatalf("search: %+v %v", result, err)
	}
	doc := result.Results[0].ID
	oldIdentity := collectionIdentity(t, s, doc)
	c, err := s.CreateCollection(ctx, "Empty saved collection")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ForgetSource(ctx, conn.Source().ID); err != nil {
		t.Fatal(err)
	}
	conn, err = filesystem.New(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, s, conn); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PinCollectionEntry(ctx, c.ID, c.Revision, doc, 0, "same passage", false, oldIdentity); !errors.Is(err, ErrCollectionSnapshotChanged) {
		t.Fatalf("stale direct pin: %v", err)
	}
	edits := []CollectionEdit{{DocumentID: doc, Excerpt: "same passage", OriginalIdentity: oldIdentity}}
	if _, err = s.SaveCollection(ctx, c.ID, c.Revision, "Stale save", edits); !errors.Is(err, ErrCollectionSnapshotChanged) {
		t.Fatalf("stale staged save: %v", err)
	}
	after, err := s.LoadCollection(ctx, c.ID)
	if err != nil || after.Revision != c.Revision || len(after.Entries) != 0 || after.Title != c.Title {
		t.Fatalf("rejected save changed collection: %+v %v", after, err)
	}
	edits[0].OriginalIdentity = collectionIdentity(t, s, doc)
	if _, err = s.SaveCollection(ctx, c.ID, c.Revision, "Fresh selection", edits); err != nil {
		t.Fatal(err)
	}
}
