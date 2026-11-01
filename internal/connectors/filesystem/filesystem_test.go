package filesystem_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lephiziel/findrail/internal/connectors/filesystem"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func TestIgnoreRuleRedactsSensitiveAncestorSamples(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "secret-folder"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".findrailignore"), []byte("secret-folder/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := filesystem.New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	_, report, err := c.ScanWithDiagnostics(context.Background(), func(connector.Document) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if report.RedactedSamples == 0 {
		t.Fatal("sensitive user-excluded path was not redacted")
	}
	for _, sample := range report.Examples {
		if strings.Contains(sample.Path, "secret-folder") {
			t.Fatalf("sensitive ancestor retained in sample: %+v", sample)
		}
	}
}

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

func TestIgnorePolicyFreshSnapshotAndHardGates(t *testing.T) {
	root := t.TempDir()
	_ = os.Mkdir(filepath.Join(root, "generated"), 0700)
	_ = os.WriteFile(filepath.Join(root, "generated", "made.md"), []byte("generated"), 0600)
	_ = os.WriteFile(filepath.Join(root, "keep.md"), []byte("keep"), 0600)
	_ = os.WriteFile(filepath.Join(root, ".findrailignore"), []byte("generated/\n*.tmp\n"), 0600)
	c, _ := filesystem.New(root, 1024)
	var got []string
	r, e := c.Scan(context.Background(), func(d connector.Document) error { got = append(got, d.Path); return nil })
	if e != nil || r.Seen != 1 || len(got) != 1 || got[0] != "keep.md" {
		t.Fatalf("ignored scan=%+v %v %v", r, got, e)
	}
	if _, e = os.Stat(filepath.Join(root, ".findrailignore")); e != nil {
		t.Fatal("policy file changed", e)
	}
	if e = os.WriteFile(filepath.Join(root, ".findrailignore"), []byte("!generated/made.md\n"), 0600); e != nil {
		t.Fatal(e)
	}
	// An excluded parent is pruned; a descendant negation cannot recover it.
	_ = os.WriteFile(filepath.Join(root, ".findrailignore"), []byte("generated/\n!generated/made.md\n"), 0600)
	r, e = c.Scan(context.Background(), func(d connector.Document) error { got = append(got, d.Path); return nil })
	if e != nil || r.Seen != 1 {
		t.Fatalf("pruned parent semantics: %+v %v", r, e)
	}
	_ = os.WriteFile(filepath.Join(root, ".findrailignore"), []byte("[broken\n"), 0600)
	r, e = c.Scan(context.Background(), func(connector.Document) error { return nil })
	if e == nil || r.Seen != 0 {
		t.Fatalf("malformed policy did not fail: %+v %v", r, e)
	}
	_ = os.Remove(filepath.Join(root, ".findrailignore"))
	r, e = c.Scan(context.Background(), func(connector.Document) error { return nil })
	if e != nil || r.Seen != 2 {
		t.Fatalf("policy removal: %+v %v", r, e)
	}
}

func TestNegationCannotOverrideHardExclusionsOrIndexDirectory(t *testing.T) {
	root := t.TempDir()
	indexDir := filepath.Join(root, "private-index")
	for path, body := range map[string]string{"normal.md": "visible", "secret.md": "hidden secret", ".hidden.md": "hidden", "node_modules/dependency.md": "dependency", "private-index/cache.md": "index"} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	policy := "!secret.md\n!.hidden.md\n!node_modules/\n!private-index/\n"
	if err := os.WriteFile(filepath.Join(root, ".findrailignore"), []byte(policy), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := filesystem.New(root, 1024, indexDir)
	if err != nil {
		t.Fatal(err)
	}
	var docs []connector.Document
	_, report, err := c.ScanWithDiagnostics(context.Background(), func(d connector.Document) error { docs = append(docs, d); return nil })
	if err != nil || len(docs) != 1 || docs[0].Path != "normal.md" {
		t.Fatalf("negation overrode hard policy: docs=%v report=%+v err=%v", docs, report, err)
	}
	for _, sample := range report.Examples {
		if strings.Contains(sample.Path, "secret") {
			t.Fatalf("sensitive path leaked in diagnostics: %+v", sample)
		}
	}
}

func TestIgnorePolicyChangeDuringEmissionFailsInventory(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.md", "b.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name+" token"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := filesystem.New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	_, err = c.Scan(context.Background(), func(connector.Document) error {
		if !changed {
			changed = true
			return os.WriteFile(filepath.Join(root, ".findrailignore"), []byte("b.md\n"), 0600)
		}
		return nil
	})
	if err == nil {
		t.Fatal("policy change during inventory was published")
	}
}

func TestIgnorePolicyRemovalDuringEmissionFailsInventory(t *testing.T) {
	root := t.TempDir()
	policy := filepath.Join(root, ".findrailignore")
	if err := os.WriteFile(policy, []byte("*.tmp\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("note"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := filesystem.New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	_, err = c.Scan(context.Background(), func(connector.Document) error {
		if !changed {
			changed = true
			return os.Remove(policy)
		}
		return nil
	})
	if err == nil {
		t.Fatal("policy removal during inventory was published")
	}
}

func TestIgnorePolicyAtomicSameContentReplacementIsDetected(t *testing.T) {
	root := t.TempDir()
	policy := filepath.Join(root, ".findrailignore")
	contents := []byte("ignored/\n")
	if err := os.WriteFile(policy, contents, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("note"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := filesystem.New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	_, err = c.Scan(context.Background(), func(connector.Document) error {
		if !changed {
			changed = true
			temporary := filepath.Join(root, ".replacement")
			if e := os.WriteFile(temporary, contents, 0600); e != nil {
				return e
			}
			return os.Rename(temporary, policy)
		}
		return nil
	})
	if err == nil {
		t.Fatal("same-content replacement changed file identity but scan succeeded")
	}
}

func TestIgnorePolicySymlinkIsRejectedWithoutFollowing(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	external := filepath.Join(outside, "policy")
	if err := os.WriteFile(external, []byte("*.md\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, ".findrailignore")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("must not be emitted"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := filesystem.New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	emitted := false
	_, err = c.Scan(context.Background(), func(connector.Document) error { emitted = true; return nil })
	if err == nil || emitted {
		t.Fatalf("symlink policy followed or scan succeeded: emitted=%v err=%v", emitted, err)
	}
}

func TestIgnorePolicyNonregularAndOversizedFilesFailClosed(t *testing.T) {
	for _, kind := range []string{"directory", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			policy := filepath.Join(root, ".findrailignore")
			if kind == "directory" {
				if err := os.Mkdir(policy, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(policy, make([]byte, (64<<10)+1), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := filesystem.New(root, 1024)
			if err != nil {
				t.Fatal(err)
			}
			emitted := false
			_, err = c.Scan(context.Background(), func(connector.Document) error { emitted = true; return nil })
			if err == nil || emitted {
				t.Fatalf("invalid policy did not fail closed: emitted=%v err=%v", emitted, err)
			}
		})
	}
}

func TestPolicyAppliesBeforePDFAndDOCXExtractionAndIsRootLocal(t *testing.T) {
	parent := t.TempDir()
	selected := filepath.Join(parent, "selected")
	child := filepath.Join(selected, "shared")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "doc.pdf"), []byte("synthetic pdf"), 0600); err != nil {
		t.Fatal(err)
	}
	writeDOCX(t, filepath.Join(child, "doc.docx"))
	if err := os.WriteFile(filepath.Join(selected, ".findrailignore"), []byte("shared/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, ".findrailignore"), []byte("doc.pdf\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pdfCalls := 0
	opts := filesystem.Options{MaxTextBytes: 1024, MaxPDFBytes: 1024, ExtractPDF: func(context.Context, io.Reader, int64) ([]connector.Page, error) { pdfCalls++; return nil, nil }, MaxDOCXBytes: 1024}
	a, err := filesystem.NewWithOptions(selected, opts)
	if err != nil {
		t.Fatal(err)
	}
	ra, err := a.Scan(context.Background(), func(connector.Document) error { t.Fatal("parent policy failed to prune shared subtree"); return nil })
	if err != nil || ra.Seen != 0 || pdfCalls != 0 {
		t.Fatalf("parent policy: %+v pdf calls=%d err=%v", ra, pdfCalls, err)
	}
	// The nested source has its own policy: it filters its PDF without inheriting
	// the parent's directory exclusion and still extracts the synthetic DOCX.
	b, err := filesystem.NewWithOptions(child, opts)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	rb, err := b.Scan(context.Background(), func(d connector.Document) error { paths = append(paths, d.Path); return nil })
	if err != nil || rb.Seen != 1 || len(paths) != 1 || paths[0] != "doc.docx" || pdfCalls != 0 {
		t.Fatalf("nested root leaked parent policy: %+v paths=%v pdf calls=%d err=%v", rb, paths, pdfCalls, err)
	}
}

func TestNestedIgnoreFileIsInertAndRootPolicyFileNeverEmitted(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, ".findrailignore"), []byte("visible.md\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "visible.md"), []byte("nested policy is inert"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".findrailignore"), []byte("# root policy\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := filesystem.New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	r, err := c.Scan(context.Background(), func(d connector.Document) error { paths = append(paths, d.Path); return nil })
	if err != nil || r.Seen != 1 || len(paths) != 1 || paths[0] != "nested/visible.md" {
		t.Fatalf("nested ignore activated or policy indexed: report=%+v paths=%v err=%v", r, paths, err)
	}
}

func TestWatchDirectoriesKeepsRootAndPolicyEventsRelevant(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "generated"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".findrailignore"), []byte("generated/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := filesystem.New(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := c.WatchDirectories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 || dirs[0] != c.Source().Root {
		t.Fatalf("policy-pruned watch directories: %v", dirs)
	}
	canonicalRoot := c.Source().Root
	if !c.RelevantPath(filepath.Join(canonicalRoot, ".findrailignore")) {
		t.Fatal("root policy file event ignored")
	}
	if c.RelevantPath(filepath.Join(canonicalRoot, "nested", ".findrailignore")) {
		t.Fatal("nested policy event activated policy")
	}
}

func TestExplainHonorsDedicatedIndexDirectoryWithoutOpeningIt(t *testing.T) {
	root := t.TempDir()
	indexPath := filepath.Join(root, ".private-index")
	decisions, err := filesystem.ExplainMany(context.Background(), root, []string{".private-index/deep/document.md"}, false, indexPath)
	if err != nil || len(decisions) != 1 || decisions[0].Reason != "excluded_builtin:index_directory" || decisions[0].Ancestor != ".private-index" {
		t.Fatalf("dedicated index decision: %+v %v", decisions, err)
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
	if err := os.WriteFile(filepath.Join(root, ".findrailignore"), []byte("!linked.md\n"), 0600); err != nil {
		t.Fatal(err)
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
