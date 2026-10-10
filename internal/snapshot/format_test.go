package snapshot

import (
	"archive/zip"
	"bytes"
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
	if fp := Fingerprint(o, 2, 2, "doc-hash", "page-hash"); fp != "0324f1f296f2bdc4ef42e4f96dbc704ab0e65a1a66e41657c0bb6d32d0735920" {
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
