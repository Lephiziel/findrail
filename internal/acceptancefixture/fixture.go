package acceptancefixture

import (
	"archive/zip"
	"bytes"
	"time"
)

const maxFixtureBytes = 1 << 20

// Files returns the deterministic, fictional manual-acceptance corpus.
// Each returned byte slice is independent of subsequent calls.
func Files() map[string][]byte {
	docx := zipBytes([]zipEntry{
		{"[Content_Types].xml", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`)},
		{"_rels/.rels", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`)},
		{"word/document.xml", []byte(`<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Fictional fixture: juniper observatory parcel.</w:t></w:r></w:p><w:sectPr/></w:body></w:document>`)},
	})
	pdf := pdfBytes()
	return map[string][]byte{
		"fixture/Juniper-Notes.md":   []byte("Fictional field note: amber thimble, stored by the Juniper Observatory.\n"),
		"fixture/Élodie-Parcel.docx": docx,
		"fixture/Copper-Lantern.pdf": pdf,
	}
}

type zipEntry struct {
	name string
	data []byte
}

func zipBytes(entries []zipEntry) []byte {
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetModTime(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
		header.SetMode(0644)
		part, err := writer.CreateHeader(header)
		if err != nil {
			panic(err)
		}
		if _, err := part.Write(entry.data); err != nil {
			panic(err)
		}
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	if output.Len() > maxFixtureBytes {
		panic("acceptance fixture exceeds size bound")
	}
	return output.Bytes()
}

func pdfBytes() []byte {
	first := []byte("BT /F1 16 Tf 30 120 Td (Fictional first page: copper lantern.) Tj ET\n")
	second := []byte("BT /F1 16 Tf 30 120 Td (Page two: violet kestrel waypoint.) Tj ET\n")
	objects := [][]byte{
		[]byte("<< /Type /Catalog /Pages 2 0 R >>"),
		[]byte("<< /Type /Pages /Kids [3 0 R 5 0 R] /Count 2 >>"),
		[]byte("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Resources << /Font << /F1 7 0 R >> >> /Contents 4 0 R >>"),
		streamObject(first),
		[]byte("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Resources << /Font << /F1 7 0 R >> >> /Contents 6 0 R >>"),
		streamObject(second),
		[]byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"),
	}
	var output bytes.Buffer
	output.Write([]byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n"))
	offsets := []int{0}
	for i, body := range objects {
		offsets = append(offsets, output.Len())
		output.WriteString(itoa(i+1) + " 0 obj\n")
		output.Write(body)
		output.WriteString("\nendobj\n")
	}
	xref := output.Len()
	output.WriteString("xref\n0 8\n0000000000 65535 f \n")
	for _, offset := range offsets[1:] {
		output.WriteString(pad10(offset) + " 00000 n \n")
	}
	output.WriteString("trailer\n<< /Size 8 /Root 1 0 R >>\nstartxref\n" + itoa(xref) + "\n%%EOF\n")
	return output.Bytes()
}

func streamObject(data []byte) []byte {
	var b bytes.Buffer
	b.WriteString("<< /Length " + itoa(len(data)) + " >>\nstream\n")
	b.Write(data)
	b.WriteString("endstream")
	return b.Bytes()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
func pad10(n int) string {
	var b [10]byte
	for i := 9; i >= 0; i-- {
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[:])
}
