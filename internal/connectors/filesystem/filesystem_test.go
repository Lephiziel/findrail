package filesystem_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func writeDOCX(t *testing.T, path string) {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	parts := map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="r1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>folder docx searchable</w:t></w:r></w:p></w:body></w:document>`,
	}
	for n, v := range parts {
		f, e := zw.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = f.Write([]byte(v))
	}
	if e := zw.Close(); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, b.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
}

func TestDOCXEnableDisableAndWordLockPolicy(t *testing.T) {
	root := t.TempDir()
	writeDOCX(t, filepath.Join(root, "Notes.DOCX"))
	if e := os.WriteFile(filepath.Join(root, "~$Notes.DOCX"), []byte("lock"), 0600); e != nil {
		t.Fatal(e)
	}
	newConn := func(limit int64) *filesystem.Connector {
		c, e := filesystem.NewWithOptions(root, filesystem.Options{MaxTextBytes: 128, MaxDOCXBytes: limit})
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	var docs []connector.Document
	r, e := newConn(8<<20).Scan(context.Background(), func(d connector.Document) error { docs = append(docs, d); return nil })
	if e != nil || r.Seen != 1 || len(docs) != 1 || docs[0].MediaType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" || docs[0].Content != "folder docx searchable" {
		t.Fatalf("enabled scan: %+v %+v %v", r, docs, e)
	}
	if docs[0].URI == "" || docs[0].Pages != nil {
		t.Fatalf("bad DOCX provenance: %+v", docs[0])
	}
	r, e = newConn(0).Scan(context.Background(), func(connector.Document) error { t.Fatal("disabled DOCX emitted"); return nil })
	if e != nil || r.Seen != 0 || r.SkippedDOCX == 0 {
		t.Fatalf("disabled scan: %+v %v", r, e)
	}
}

func TestScanBoundsAndExclusions(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"notes.md": "find this passage", ".env": "password=example",
		"credentials.json": "sensitive", "binary.txt": "a\x00b", "large.txt": string(make([]byte, 129)),
		".hidden/note.md": "hidden", "node_modules/module.js": "dependency", "private-index/cache.json": "index data",
	}
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := filesystem.New(root, 128, filepath.Join(root, "private-index"))
	if err != nil {
		t.Fatal(err)
	}
	var docs []connector.Document
	r, err := c.Scan(context.Background(), func(d connector.Document) error { docs = append(docs, d); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if r.Seen != 1 || len(docs) != 1 || docs[0].Title != "notes.md" {
		t.Fatalf("unexpected inventory: %+v, %+v", r, docs)
	}
	if docs[0].URI == "" || docs[0].Hash == "" || docs[0].ID == "" {
		t.Fatalf("missing provenance: %+v", docs[0])
	}
}

func TestSymlinkOutsideRootIsNotIndexed(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	path := filepath.Join(outside, "outside.md")
	if err := os.WriteFile(path, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(root, "linked.md")); err != nil {
		t.Skip("symlink unavailable:", err)
	}
	c, err := filesystem.New(root, 128)
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Scan(context.Background(), func(d connector.Document) error { t.Errorf("indexed symlink: %+v", d); return nil })
	if err != nil || r.Seen != 0 {
		t.Fatalf("symlink scan: %+v %v", r, err)
	}
}

func TestPDFPolicyAndPageHash(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "guide.pdf"), []byte("synthetic input"), 0600); err != nil {
		t.Fatal(err)
	}
	pages := []connector.Page{{Number: 1, Text: "ab"}, {Number: 2, Text: "c"}}
	extractor := func(context.Context, io.Reader, int64) ([]connector.Page, error) { return pages, nil }
	c, err := filesystem.NewWithOptions(root, filesystem.Options{MaxTextBytes: 128, MaxPDFBytes: 128, ExtractPDF: extractor})
	if err != nil {
		t.Fatal(err)
	}
	scan := func() connector.Document {
		t.Helper()
		var doc connector.Document
		r, e := c.Scan(context.Background(), func(d connector.Document) error { doc = d; return nil })
		if e != nil || r.Seen != 1 {
			t.Fatalf("PDF scan: %+v %v", r, e)
		}
		return doc
	}
	before := scan()
	pages = []connector.Page{{Number: 1, Text: "a"}, {Number: 2, Text: "bc"}}
	after := scan()
	if before.MediaType != "application/pdf" || len(before.Pages) != 2 || before.Hash == after.Hash {
		t.Fatal("page boundaries missing from PDF identity")
	}
	if c.RelevantPath(filepath.Join(root, ".hidden", "note.md")) || c.RelevantPath(filepath.Join(root, "node_modules", "note.md")) {
		t.Fatal("watch policy ignores scan exclusions")
	}
	dirs, err := c.WatchDirectories(context.Background())
	if err != nil || len(dirs) != 1 {
		t.Fatalf("watch directories: %+v %v", dirs, err)
	}
}
