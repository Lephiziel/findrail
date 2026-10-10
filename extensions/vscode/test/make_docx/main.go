package main

import (
	"archive/zip"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "expected output path")
		os.Exit(2)
	}
	file, err := os.Create(os.Args[1])
	if err != nil {
		panic(err)
	}
	archive := zip.NewWriter(file)
	parts := map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>retrieval docx fixture phrase</w:t></w:r></w:p></w:body></w:document>`,
	}
	for name, content := range parts {
		writer, err := archive.Create(name)
		if err != nil {
			panic(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			panic(err)
		}
	}
	if err := archive.Close(); err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
}
