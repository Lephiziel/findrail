package docx

import (
	"archive/zip"
	"bytes"
	"context"
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
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	parts := map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   document,
	}
	for n, v := range parts {
		f, _ := w.Create(n)
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
		"spoof":     `<document xmlns="urn:no"><body><p><t>hello</t></p></body></document>`,
		"truncated": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`,
		"entity":    `<!DOCTYPE x [<!ENTITY a "boom">]><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>&a;</w:t></w:r></w:p></w:body></w:document>`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := extract(t, packageBytes(body))
			if err == nil {
				t.Fatal("expected rejection")
			}
		})
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
