package snapshot

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestEncodeInspectRoundTripAndFingerprintGolden(t *testing.T) {
	o := Origin{ID: "origin", Kind: "filesystem", Name: "Synthetic", Location: "/tmp/notes", IndexedAt: "2026-10-09T12:00:00Z", MaxTextBytes: 1048576}
	docs := []Document{{ID: "old-id", Path: "docs/notes.md", Title: "Notes", URI: "file:///tmp/notes/docs/notes.md", MediaType: "text/markdown", Text: "full indexed body", ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 17, ModifiedAt: "2026-10-09T11:00:00Z"}, {ID: "pdf", Path: "z.pdf", Title: "Z", URI: "file:///tmp/notes/z.pdf", MediaType: "application/pdf", Text: "page one\npage two\n", ContentHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SizeBytes: 8, ModifiedAt: "2026-10-09T11:00:00Z", PageCount: 2}}
	pages := []Page{{Path: "z.pdf", Number: 1, Text: "page one"}, {Path: "z.pdf", Number: 2, Text: "page two"}}
	data, err := Encode(Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: o}, docs, pages)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Inspect(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Documents) != 2 || len(got.Pages) != 2 || got.Documents[1].Text != docs[1].Text {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Manifest.Fingerprint != Fingerprint(o, 2, 2, got.Manifest.DocumentsSHA256, got.Manifest.PagesSHA256) {
		t.Fatal("fingerprint mismatch")
	}
	if again, e := Encode(Manifest{Producer: "another producer", ExportedAt: "2027-01-01T00:00:00Z", Origin: o}, docs, pages); e != nil {
		t.Fatal(e)
	} else {
		g, e := Inspect(again)
		if e != nil || g.Manifest.Fingerprint != got.Manifest.Fingerprint {
			t.Fatalf("semantic fingerprint changed: %v", e)
		}
	}
	if fp := Fingerprint(o, 2, 2, "doc-hash", "page-hash"); fp != "b05d2e33164b54e5d8ef170ba5cd83abb92e3c300a1ca7016faab857b2b7a28f" {
		t.Fatalf("update fingerprint golden: %s", fp)
	}
}

func TestInspectRejectsDuplicateJSONKeys(t *testing.T) {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	f, _ := zw.Create("manifest.json")
	_, _ = f.Write([]byte(`{"format":"findrail-portable-snapshot","format":"findrail-portable-snapshot"}`))
	f, _ = zw.Create("documents.jsonl")
	_, _ = f.Write(nil)
	f, _ = zw.Create("pages.jsonl")
	_, _ = f.Write(nil)
	_ = zw.Close()
	if _, err := Inspect(b.Bytes()); err == nil {
		t.Fatal("duplicate keys accepted")
	}
}

func TestInspectValidatesEveryPDF(t *testing.T) {
	o := Origin{ID: "origin", Kind: "filesystem", Name: "Synthetic", Location: "/tmp/notes", IndexedAt: "2026-10-09T12:00:00Z"}
	first := Document{ID: "first", Path: "a.pdf", Title: "First", URI: "file:///tmp/notes/a.pdf", MediaType: "application/pdf", Text: "first page\n", ContentHash: strings.Repeat("a", 64), SizeBytes: 10, ModifiedAt: "2026-10-09T11:00:00Z", PageCount: 1}
	second := first
	second.ID, second.Path, second.Title, second.URI = "second", "b.pdf", "Second", "file:///tmp/notes/b.pdf"
	second.Text = "second page\nlast page\n"
	second.PageCount = 2
	pages := []Page{{Path: "a.pdf", Number: 1, Text: "first page"}, {Path: "b.pdf", Number: 1, Text: "second page"}, {Path: "b.pdf", Number: 2, Text: "last page"}}
	for _, tt := range []struct {
		name  string
		docs  []Document
		pages []Page
		valid bool
	}{
		{"multiple PDFs", []Document{first, second}, pages, true},
		{"missing first PDF", []Document{first, second}, pages[1:], false},
		{"missing last PDF", []Document{first, second}, pages[:1], false},
		{"incomplete last PDF", []Document{first, second}, pages[:2], false},
		{"missing all pages", []Document{first}, nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data, err := Encode(Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: o}, tt.docs, tt.pages)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Inspect(data); (err == nil) != tt.valid {
				t.Fatalf("valid=%v, inspect error: %v", tt.valid, err)
			}
		})
	}
}

func TestInspectRejectsTrailingPDFBody(t *testing.T) {
	o := Origin{ID: "origin", Kind: "filesystem", Name: "Synthetic", Location: "/tmp/notes", IndexedAt: "2026-10-09T12:00:00Z"}
	doc := Document{ID: "pdf", Path: "a.pdf", Title: "PDF", URI: "file:///tmp/notes/a.pdf", MediaType: "application/pdf", Text: "page text\nunmatched body", ContentHash: strings.Repeat("a", 64), SizeBytes: 10, ModifiedAt: "2026-10-09T11:00:00Z", PageCount: 1}
	data, err := Encode(Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: o}, []Document{doc}, []Page{{Path: "a.pdf", Number: 1, Text: "page text"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Inspect(data); err == nil {
		t.Fatal("PDF body with text absent from its pages accepted")
	}
}

func TestEmptySnapshotAndStreamingWriter(t *testing.T) {
	o := Origin{ID: "empty", Kind: "filesystem", Name: "Empty", Location: "/tmp/empty", IndexedAt: "2026-10-09T00:00:00Z"}
	data, err := Encode(Manifest{Producer: "test", ExportedAt: "2026-10-09T00:00:00Z", Origin: o}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := Inspect(data)
	if err != nil || empty.Manifest.DocumentCount != 0 || empty.Manifest.PageCount != 0 {
		t.Fatalf("empty snapshot: %+v %v", empty, err)
	}
	doc := Document{ID: "origin", Path: "notes.md", Title: "Notes", URI: "file:///tmp/empty/notes.md", MediaType: "text/plain", Text: "streamed", ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 8, ModifiedAt: "2026-10-09T00:00:00Z"}
	var staged bytes.Buffer
	m, err := WriteArchive(context.Background(), &staged, Manifest{Producer: "test", ExportedAt: "2026-10-09T00:00:00Z", Origin: o}, func(_ context.Context, w io.Writer) (int, error) { return 1, WriteJSONLRecord(w, doc) }, func(_ context.Context, _ io.Writer) (int, error) { return 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	got, err := Inspect(staged.Bytes())
	if err != nil || got.Manifest.Fingerprint != m.Fingerprint || got.Documents[0].Text != "streamed" {
		t.Fatalf("stream output: %+v %v", got, err)
	}
}

func TestInspectAndStreamingWriterHonorCancellation(t *testing.T) {
	o := Origin{ID: "cancel", Kind: "filesystem", Name: "Cancel", Location: "/tmp/cancel", IndexedAt: "2026-10-09T00:00:00Z"}
	data, err := Encode(Manifest{Producer: "test", ExportedAt: "2026-10-09T00:00:00Z", Origin: o}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = InspectContext(ctx, data); err != context.Canceled {
		t.Fatalf("inspect cancellation: %v", err)
	}
	var out bytes.Buffer
	if _, err = WriteArchive(ctx, &out, Manifest{Producer: "test", ExportedAt: "2026-10-09T00:00:00Z", Origin: o}, func(context.Context, io.Writer) (int, error) { return 0, nil }, func(context.Context, io.Writer) (int, error) { return 0, nil }); err != context.Canceled {
		t.Fatalf("export cancellation: %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("canceled export wrote a partial archive")
	}
}

func TestInspectRejectsUnknownRecordFieldUnsafeURIAndPDFMismatch(t *testing.T) {
	o := Origin{ID: "origin", Kind: "filesystem", Name: "Synthetic", Location: "/tmp/notes", IndexedAt: "2026-10-09T12:00:00Z"}
	valid := Document{ID: "id", Path: "note.md", Title: "Note", URI: "file:///tmp/notes/note.md", MediaType: "text/plain", Text: "body", ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 4, ModifiedAt: "2026-10-09T11:00:00Z"}
	unknown := []byte(`{"id":"id","path":"note.md","title":"Note","uri":"file:///tmp/notes/note.md","media_type":"text/plain","text":"body","content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size_bytes":4,"modified_at":"2026-10-09T11:00:00Z","page_count":0,"extra":1}` + "\n")
	if _, err := Inspect(rawArchive(t, Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: o}, unknown, nil)); err == nil {
		t.Fatal("unknown record field accepted")
	}
	badURI := valid
	badURI.URI = "file:///etc/passwd"
	data, err := Encode(Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: o}, []Document{badURI}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Inspect(data); err == nil {
		t.Fatal("unrelated file URI accepted")
	}
	pdf := valid
	pdf.Path = "p.pdf"
	pdf.Title = "p.pdf"
	pdf.URI = "file:///tmp/notes/p.pdf"
	pdf.MediaType = "application/pdf"
	pdf.PageCount = 1
	data, err = Encode(Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: o}, []Document{pdf}, []Page{{Path: "p.pdf", Number: 1, Text: "different"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Inspect(data); err == nil {
		t.Fatal("inconsistent PDF body accepted")
	}
}

func TestInspectRejectsInvalidUTF8CRCCountsVersionAndDuplicateEntries(t *testing.T) {
	o := Origin{ID: "origin", Kind: "filesystem", Name: "Synthetic", Location: "/tmp/notes", IndexedAt: "2026-10-09T12:00:00Z"}
	if _, err := Inspect(rawArchive(t, Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: o}, []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}', '\n'}, nil)); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	if _, err := Inspect(rawArchive(t, Manifest{Version: 2, Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: o}, []byte("{}\n"), nil)); err == nil {
		t.Fatal("unknown version accepted")
	}
	if _, err := Inspect(rawArchive(t, Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: o}, nil, nil)); err == nil {
		t.Fatal("dishonest record count accepted")
	}
	valid := Document{ID: "id", Path: "note.md", Title: "Note", URI: "file:///tmp/notes/note.md", MediaType: "text/plain", Text: "body", ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 4, ModifiedAt: "2026-10-09T11:00:00Z"}
	record, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	record = append(record, '\n')
	data := rawArchive(t, Manifest{Producer: "test", ExportedAt: "2026-10-09T12:00:00Z", Origin: o}, record, nil)
	i := bytes.Index(data, []byte("note.md"))
	if i < 0 {
		t.Fatal("stored JSONL payload not found in test ZIP")
	}
	data[i] ^= 1
	if _, err = Inspect(data); err == nil {
		t.Fatal("CRC/corruption accepted")
	}
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	f, _ := zw.Create("manifest.json")
	_, _ = f.Write([]byte("{}"))
	f, _ = zw.Create("manifest.json")
	_, _ = f.Write([]byte("{}"))
	f, _ = zw.Create("pages.jsonl")
	_, _ = f.Write(nil)
	_ = zw.Close()
	if _, err = Inspect(b.Bytes()); err == nil {
		t.Fatal("duplicate ZIP entry accepted")
	}
}

func TestUnsupportedVersionPrecedesFutureUnknownFields(t *testing.T) {
	o := Origin{ID: "version", Kind: "filesystem", Name: "Version", Location: "/tmp/version", IndexedAt: "2026-10-09T00:00:00Z"}
	data, err := Encode(Manifest{Producer: "test", ExportedAt: "2026-10-09T00:00:00Z", Origin: o}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	data = rewriteManifest(t, data, func(m map[string]json.RawMessage) {
		m["version"] = json.RawMessage("2")
		m["future_field"] = json.RawMessage(`"new schema"`)
	})
	if _, err = Inspect(data); err == nil || !strings.Contains(err.Error(), "unsupported snapshot format version 2") {
		t.Fatalf("unknown version did not give upgrade guidance: %v", err)
	}
}

func rewriteManifest(t *testing.T, data []byte, change func(map[string]json.RawMessage)) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{}
	for _, f := range zr.File {
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		entries[f.Name] = b
	}
	var m map[string]json.RawMessage
	if err = json.Unmarshal(entries["manifest.json"], &m); err != nil {
		t.Fatal(err)
	}
	change(m)
	entries["manifest.json"], err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, name := range []string{"manifest.json", "documents.jsonl", "pages.jsonl"} {
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		f, e := w.CreateHeader(h)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write(entries[name]); e != nil {
			t.Fatal(e)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func rawArchive(t *testing.T, m Manifest, docs, pages []byte) []byte {
	t.Helper()
	dh, ph := sha256.Sum256(docs), sha256.Sum256(pages)
	m.Format = FormatID
	if m.Version == 0 {
		m.Version = Version
	}
	m.DocumentCount = 1
	m.PageCount = 0
	m.DocumentsBytes = int64(len(docs))
	m.PagesBytes = int64(len(pages))
	m.DocumentsSHA256 = hex.EncodeToString(dh[:])
	m.PagesSHA256 = hex.EncodeToString(ph[:])
	m.Fingerprint = Fingerprint(m.Origin, 1, 0, m.DocumentsSHA256, m.PagesSHA256)
	manifest, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, entry := range []struct {
		name string
		data []byte
	}{{"manifest.json", manifest}, {"documents.jsonl", docs}, {"pages.jsonl", pages}} {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		f, e := w.CreateHeader(header)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write(entry.data); e != nil {
			t.Fatal(e)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func FuzzInspectBounded(f *testing.F) {
	o := Origin{ID: "fuzz", Kind: "filesystem", Name: "Fuzz", Location: "/tmp/fuzz", IndexedAt: "2026-10-09T00:00:00Z"}
	valid, _ := Encode(Manifest{Producer: "fuzz", ExportedAt: "2026-10-09T00:00:00Z", Origin: o}, nil, nil)
	f.Add(valid)
	f.Add([]byte("not zip"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > MaxArchive {
			return
		}
		_, _ = Inspect(data)
	})
}
