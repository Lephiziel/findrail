package docx

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func FuzzExtract(f *testing.F) {
	f.Add([]byte("not a DOCX"))
	f.Add(packageBytes(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>seed</w:t></w:r></w:p></w:body></w:document>`))
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			t.Skip()
		}
		_, _ = Extract(context.Background(), bytes.NewReader(input), int64(len(input)), 1<<20)
	})
}

func packageBytes(document string) []byte {
	types := `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`
	rels := `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`
	return packageWithParts(document, types, rels)
}

func packageWithParts(document, types, rels string) []byte {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	parts := map[string]string{
		"[Content_Types].xml": types,
		"_rels/.rels":         rels,
		"word/document.xml":   document,
	}
	for n, v := range parts {
		f, _ := w.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Store})
		_, _ = f.Write([]byte(v))
	}
	_ = w.Close()
	return b.Bytes()
}
func extract(t *testing.T, data []byte) (string, error) {
	t.Helper()
	return Extract(context.Background(), bytes.NewReader(data), int64(len(data)), DefaultMaxInput)
}

func TestExtractPlainTextContract(t *testing.T) {
	x := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p><w:r><w:t xml:space="preserve">  Привет</w:t></w:r><w:r><w:t>World</w:t></w:r><w:r><w:tab/><w:br/><w:t>café</w:t></w:r></w:p>
<w:p><w:hyperlink><w:r><w:t>label</w:t></w:r></w:hyperlink><w:sdt><w:sdtContent><w:r><w:t>!</w:t></w:r></w:sdtContent></w:sdt><w:ins><w:r><w:t>added</w:t></w:r></w:ins><w:del><w:r><w:delText>gone</w:delText></w:r></w:del><w:r><w:rPr><w:vanish/></w:rPr><w:t>hidden</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>A</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:p><w:r><w:t>nested</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:tc><w:tc><w:p><w:r><w:t>B</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
</w:body></w:document>`
	got, err := extract(t, packageBytes(x))
	if err != nil {
		t.Fatal(err)
	}
	want := "  ПриветWorld\t\ncafé\nlabel!added\nA | \nnested\nB"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRejectsInvalidPackagesAndNamespacedSpoof(t *testing.T) {
	for name, body := range map[string]string{
		"spoof":      `<document xmlns="urn:no"><body><p><t>hello</t></p></body></document>`,
		"truncated":  `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`,
		"entity":     `<!DOCTYPE x [<!ENTITY a "boom">]><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>&a;</w:t></w:r></w:p></w:body></w:document>`,
		"unused-dtd": `<!DOCTYPE x><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>safe</w:t></w:r></w:p></w:body></w:document>`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := extract(t, packageBytes(body))
			if err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestClassificationForVariantsAndUnsafeRelationships(t *testing.T) {
	document := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>hello</w:t></w:r></w:p></w:body></w:document>`
	types := `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`
	rels := `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`
	macroTypes := strings.Replace(types, "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml", "application/vnd.ms-word.document.macroEnabled.main+xml", 1)
	externalRels := strings.Replace(rels, `Target="word/document.xml"`, `Target="https://outside.invalid/document.xml" TargetMode="External"`, 1)
	traversalRels := strings.Replace(rels, `Target="word/document.xml"`, `Target="../outside.xml"`, 1)
	strictRels := `<Relationships xmlns="http://purl.oclc.org/ooxml/officeDocument/relationships"><Relationship Id="rId1" Type="http://purl.oclc.org/ooxml/officeDocument/relationships/officeDocument" Target="word/document.xml"/></Relationships>`
	tests := []struct {
		name   string
		data   []byte
		target error
	}{
		{"macro-content-type", packageWithParts(document, macroTypes, rels), ErrSkip},
		{"external-main", packageWithParts(document, types, externalRels), ErrCorrupt},
		{"relationship-traversal", packageWithParts(document, types, traversalRels), ErrCorrupt},
		{"missing-main-content-type", packageWithParts(document, `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"></Types>`, rels), ErrCorrupt},
		{"strict-ooxml", packageWithParts(document, types, strictRels), ErrSkip},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := extract(t, tc.data)
			if !errors.Is(err, tc.target) {
				t.Fatalf("got %v, want class %v", err, tc.target)
			}
		})
	}
}

func TestTextDepthAndTokenBudgetsAreAllOrNothing(t *testing.T) {
	base := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`
	end := `</w:body></w:document>`
	tooMuchText := base + `<w:p><w:r><w:t>` + strings.Repeat("x", (1<<20)+1) + `</w:t></w:r></w:p>` + end
	if _, err := extract(t, packageBytes(tooMuchText)); !errors.Is(err, ErrLimit) {
		t.Fatalf("text budget: %v", err)
	}
	var deep strings.Builder
	deep.WriteString(base)
	for i := 0; i < MaxDepth; i++ {
		deep.WriteString(`<w:x>`)
	}
	deep.WriteString(`<w:p><w:r><w:t>x</w:t></w:r></w:p>`)
	for i := 0; i < MaxDepth; i++ {
		deep.WriteString(`</w:x>`)
	}
	deep.WriteString(end)
	if _, err := extract(t, packageBytes(deep.String())); !errors.Is(err, ErrLimit) {
		t.Fatalf("depth budget: %v", err)
	}
	var tokens strings.Builder
	tokens.WriteString(base)
	for i := 0; i < MaxTokens/2+2; i++ {
		tokens.WriteString(`<w:x/>`)
	}
	tokens.WriteString(`<w:p><w:r><w:t>x</w:t></w:r></w:p>` + end)
	if _, err := extract(t, packageBytes(tokens.String())); !errors.Is(err, ErrLimit) {
		t.Fatalf("token budget: %v", err)
	}
}

func TestEntryCountAndTraversalNamesAreRejected(t *testing.T) {
	makePackage := func(extra int, unsafe string) []byte {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		parts := map[string]string{"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`, "_rels/.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="r" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`, "word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>ok</w:t></w:r></w:p></w:body></w:document>`}
		for n, v := range parts {
			f, _ := z.Create(n)
			_, _ = f.Write([]byte(v))
		}
		if unsafe != "" {
			f, _ := z.Create(unsafe)
			_, _ = f.Write([]byte("x"))
		}
		for i := 0; i < extra; i++ {
			f, _ := z.Create(fmt.Sprintf("extra/%04d", i))
			_, _ = f.Write([]byte("x"))
		}
		_ = z.Close()
		return b.Bytes()
	}
	if _, err := extract(t, makePackage(MaxEntries, "")); !errors.Is(err, ErrLimit) {
		t.Fatalf("entry budget: %v", err)
	}
	if _, err := extract(t, makePackage(0, "../outside")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("unsafe entry: %v", err)
	}
}

func TestDuplicateEntriesTruncationAndCRCFailClosed(t *testing.T) {
	doc := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>crcproof</w:t></w:r></w:p></w:body></w:document>`
	valid := packageBytes(doc)
	corrupt := bytes.Clone(valid)
	position := bytes.Index(corrupt, []byte("crcproof"))
	if position < 0 {
		t.Fatal("fixture text missing")
	}
	corrupt[position] ^= 1
	if _, err := extract(t, corrupt); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("CRC mismatch accepted: %v", err)
	}
	if _, err := extract(t, valid[:len(valid)-10]); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("truncated ZIP accepted: %v", err)
	}

	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/document.xml"} {
		var value string
		switch name {
		case "[Content_Types].xml":
			value = `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`
		case "_rels/.rels":
			value = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="r" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`
		default:
			value = doc
		}
		f, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(value))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := extract(t, b.Bytes()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("duplicate package part accepted: %v", err)
	}
}

func TestInputAndContextBounds(t *testing.T) {
	data := packageBytes(`<x/>`)
	if _, err := Extract(context.Background(), bytes.NewReader(data), int64(len(data)), 1); err != ErrSkip {
		t.Fatalf("oversize outcome: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Extract(ctx, bytes.NewReader(data), int64(len(data)), DefaultMaxInput); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("cancellation: %v", err)
	}
}
