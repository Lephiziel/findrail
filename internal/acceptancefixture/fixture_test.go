package acceptancefixture

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"reflect"
	"sort"
	"testing"
	"time"
)

var goldenFileSHA256 = map[string]string{
	"fixture/Copper-Lantern.pdf": "4b4e23438874cb2cfee59abe7b7e0baaa45fad6b599697314c753afe3d4934a6",
	"fixture/Juniper-Notes.md":   "fba4e1393147f6fcc24a85f29a2b3c301c4e036142622985d26b3b74ab1e19e6",
	"fixture/Élodie-Parcel.docx": "6db08fd44232bc0c3dd968d2cf0c1965e3f3b82c55f4f343ef3a58b4065a1694",
}

const goldenCorpusSHA256 = "5cd3a84911666ef810ac38e2a8f494491c9e739ed855c00cc8fdc0ac54a155a6"

func TestFilesAreDeterministicAndBounded(t *testing.T) {
	first, second := Files(), Files()
	if len(first) != 3 || len(second) != 3 {
		t.Fatalf("file count: got %d and %d", len(first), len(second))
	}
	for name, data := range first {
		if len(data) == 0 || len(data) > maxFixtureBytes {
			t.Fatalf("%s size %d outside bound", name, len(data))
		}
		if !bytes.Equal(data, second[name]) {
			t.Fatalf("%s changed between calls", name)
		}
	}
}

func TestGoldenSHA256(t *testing.T) {
	files := Files()
	if len(files) != len(goldenFileSHA256) {
		t.Fatalf("file count: got %d, want %d", len(files), len(goldenFileSHA256))
	}
	names := make([]string, 0, len(files))
	for name, data := range files {
		want, ok := goldenFileSHA256[name]
		if !ok {
			t.Errorf("unexpected fixture path %q", name)
			continue
		}
		got := fmt.Sprintf("%x", sha256.Sum256(data))
		if got != want {
			t.Errorf("%s SHA-256 = %s, want %s", name, got, want)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(files[name])
		h.Write([]byte{0})
	}
	if got := fmt.Sprintf("%x", h.Sum(nil)); got != goldenCorpusSHA256 {
		t.Errorf("corpus SHA-256 = %s, want %s", got, goldenCorpusSHA256)
	}
}

func TestReturnedSlicesAndCallsAreIsolated(t *testing.T) {
	first := Files()
	pristine := Files()
	for name, data := range first {
		if len(data) == 0 {
			t.Fatalf("%s unexpectedly empty", name)
		}
		for i := range data {
			data[i] ^= 0xff
		}
		if bytes.Equal(data, pristine[name]) {
			t.Errorf("mutating returned %s did not affect only that slice", name)
		}
	}
	first["fixture/extra"] = []byte("caller-owned")
	delete(first, "fixture/Juniper-Notes.md")
	second := Files()
	if len(second) != len(pristine) {
		t.Fatalf("later Files call has %d paths, want %d", len(second), len(pristine))
	}
	for name, want := range pristine {
		if !bytes.Equal(second[name], want) {
			t.Errorf("later Files call changed after caller mutation: %s", name)
		}
	}
}

func TestDOCXZIPContract(t *testing.T) {
	const name = "fixture/Élodie-Parcel.docx"
	data := Files()[name]
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	wantContents := map[string][]byte{
		"[Content_Types].xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`),
		"_rels/.rels": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`),
		"word/document.xml": []byte(`<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Fictional fixture: juniper observatory parcel.</w:t></w:r></w:p><w:sectPr/></w:body></w:document>`),
	}
	if len(reader.File) != len(wantContents) {
		t.Fatalf("ZIP entry count = %d, want %d", len(reader.File), len(wantContents))
	}
	seen := make(map[string]bool, len(reader.File))
	for _, member := range reader.File {
		if seen[member.Name] {
			t.Errorf("duplicate ZIP entry %q", member.Name)
			continue
		}
		seen[member.Name] = true
		if member.Name == "" || member.FileInfo().IsDir() || member.Mode().Perm() != 0644 || member.Method != zip.Deflate {
			t.Errorf("unexpected ZIP metadata for %q: mode=%v method=%d", member.Name, member.Mode(), member.Method)
		}
		if got := member.Modified.UTC(); !got.Equal(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("timestamp for %q = %v", member.Name, got)
		}
		want, ok := wantContents[member.Name]
		if !ok {
			t.Errorf("unexpected ZIP entry %q", member.Name)
			continue
		}
		stream, err := member.Open()
		if err != nil {
			t.Fatalf("open %q: %v", member.Name, err)
		}
		got, readErr := io.ReadAll(stream)
		closeErr := stream.Close()
		if readErr != nil {
			t.Fatalf("read %q: %v", member.Name, readErr)
		}
		if closeErr != nil {
			t.Fatalf("close %q: %v", member.Name, closeErr)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("contents differ for %q", member.Name)
		}
	}
	for entry := range wantContents {
		if !seen[entry] {
			t.Errorf("missing ZIP entry %q", entry)
		}
	}
}

func TestCorpusHashUsesStableFilenameOrder(t *testing.T) {
	files := Files()
	names := []string{"fixture/Copper-Lantern.pdf", "fixture/Juniper-Notes.md", "fixture/Élodie-Parcel.docx"}
	// The contract sorts UTF-8 filename bytes before adding NUL separators.
	sort.Strings(names)
	first := sha256.New()
	for _, name := range names {
		first.Write([]byte(name + "\x00"))
		first.Write(files[name])
		first.Write([]byte{0})
	}
	if fmt.Sprintf("%x", first.Sum(nil)) != goldenCorpusSHA256 {
		t.Fatal("stable filename/NUL corpus digest changed")
	}
	if !reflect.DeepEqual(names, []string{"fixture/Copper-Lantern.pdf", "fixture/Juniper-Notes.md", "fixture/Élodie-Parcel.docx"}) {
		t.Fatalf("unexpected filename order: %v", names)
	}
}
