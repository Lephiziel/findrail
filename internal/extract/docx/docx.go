// Package docx extracts a bounded plain-text snapshot from the main
// WordprocessingML part of ordinary (transitional) DOCX packages.
package docx

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultMaxInput int64  = 8 << 20
	MaxEntries             = 2048
	MaxEntryName           = 2048
	MaxInventory    uint64 = 128 << 20
	MaxXMLPart      uint64 = 8 << 20
	MaxXMLTotal     uint64 = 16 << 20
	MaxText         uint64 = 1 << 20
	MaxDepth               = 128
	MaxTokens              = 500000
)

const (
	wNS       = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	relNS     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	pkgRelNS  = "http://schemas.openxmlformats.org/package/2006/relationships"
	ctNS      = "http://schemas.openxmlformats.org/package/2006/content-types"
	strictWNS = "http://purl.oclc.org/ooxml/wordprocessingml/main"
	mainType  = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
	macroType = "application/vnd.ms-word.document.macroEnabled.main+xml"
)

var (
	ErrSkip    = errors.New("DOCX is unsupported or contains no usable text")
	ErrLimit   = errors.New("DOCX extraction limit exceeded")
	ErrCorrupt = errors.New("invalid DOCX package")
	ErrNoText  = errors.New("DOCX contains no usable main-body text")
)

// Extract reads one complete bounded DOCX snapshot. ErrSkip and ErrLimit are
// policy skips; all malformed/ambiguous packages return an error that callers
// must fail atomically. Input is consumed synchronously and never retained.
func Extract(ctx context.Context, r io.ReaderAt, size int64, maxInput int64) (string, error) {
	if maxInput == 0 {
		return "", ErrSkip
	}
	if size < 0 || size > maxInput {
		return "", ErrSkip
	}
	if maxInput < 1 || maxInput > 16<<20 {
		return "", fmt.Errorf("invalid DOCX input limit")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var signature [8]byte
	if size >= int64(len(signature)) {
		if _, err := r.ReadAt(signature[:], 0); err != nil {
			return "", fmt.Errorf("%w: read DOCX header: %v", ErrCorrupt, err)
		}
		if bytes.Equal(signature[:], []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}) {
			return "", ErrSkip
		}
	}
	limited := &countReaderAt{r: r, size: size, ctx: ctx}
	zr, err := zip.NewReader(limited, size)
	if err != nil {
		return "", fmt.Errorf("%w: ZIP directory: %v", ErrCorrupt, err)
	}
	if len(zr.File) > MaxEntries {
		return "", ErrLimit
	}
	entries := make(map[string]*zip.File, len(zr.File))
	canonicalEntries := make(map[string]bool, len(zr.File))
	var inventory uint64
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if f.Flags&1 != 0 {
			return "", ErrSkip
		}
		entryName := strings.TrimSuffix(f.Name, "/")
		if len(f.Name) == 0 || len(f.Name) > MaxEntryName || strings.ContainsAny(f.Name, "\\\x00") || strings.HasPrefix(f.Name, "/") || path.Clean(entryName) != entryName || entryName == ".." || strings.HasPrefix(entryName, "../") {
			return "", fmt.Errorf("%w: unsafe ZIP entry name", ErrCorrupt)
		}
		if canonicalEntries[entryName] {
			return "", fmt.Errorf("%w: ambiguous ZIP entry name", ErrCorrupt)
		}
		canonicalEntries[entryName] = true
		if _, exists := entries[f.Name]; exists {
			return "", fmt.Errorf("%w: duplicate ZIP entry", ErrCorrupt)
		}
		mode := f.Mode()
		if mode&os.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
			return "", fmt.Errorf("%w: special ZIP entry", ErrCorrupt)
		}
		if ^uint64(0)-inventory < f.UncompressedSize64 {
			return "", ErrLimit
		}
		inventory += f.UncompressedSize64
		if inventory > MaxInventory {
			return "", ErrLimit
		}
		entries[f.Name] = f
	}
	ct, err := readPart(ctx, entries, "[Content_Types].xml", 256<<10, MaxXMLTotal)
	if err != nil {
		return "", err
	}
	mainNames, macro, err := parseContentTypes(ctx, ct)
	if err != nil {
		return "", err
	}
	if macro {
		return "", ErrSkip
	}
	rels, err := readPart(ctx, entries, "_rels/.rels", 256<<10, MaxXMLTotal)
	if err != nil {
		return "", err
	}
	target, err := mainTarget(ctx, rels)
	if err != nil {
		return "", err
	}
	if !mainNames[target] {
		return "", fmt.Errorf("%w: main part content type mismatch", ErrCorrupt)
	}
	if uint64(len(ct))+uint64(len(rels)) >= MaxXMLTotal {
		return "", ErrLimit
	}
	body, err := readPart(ctx, entries, target, MaxXMLPart, MaxXMLTotal-uint64(len(ct))-uint64(len(rels)))
	if err != nil {
		return "", err
	}
	text, err := parseBody(ctx, body)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", ErrNoText
	}
	return text, nil
}

type countReaderAt struct {
	r    io.ReaderAt
	size int64
	ctx  context.Context
}

func (c *countReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	if off >= c.size {
		return 0, io.EOF
	}
	if int64(len(p)) > c.size-off {
		p = p[:c.size-off]
	}
	return c.r.ReadAt(p, off)
}

func readPart(ctx context.Context, entries map[string]*zip.File, name string, partMax, totalMax uint64) ([]byte, error) {
	f := entries[name]
	if f == nil {
		return nil, fmt.Errorf("%w: missing part", ErrCorrupt)
	}
	if f.UncompressedSize64 > partMax {
		return nil, ErrLimit
	}
	r, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: open XML part", ErrCorrupt)
	}
	defer r.Close()
	var b bytes.Buffer
	buf := make([]byte, 32<<10)
	var total uint64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, e := r.Read(buf)
		total += uint64(n)
		if total > partMax || total > totalMax {
			return nil, ErrLimit
		}
		if n > 0 {
			if _, err := b.Write(buf[:n]); err != nil {
				return nil, err
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, fmt.Errorf("%w: read XML part: %v", ErrCorrupt, e)
		}
	}
	if total != f.UncompressedSize64 {
		return nil, fmt.Errorf("%w: XML part size mismatch", ErrCorrupt)
	}
	return b.Bytes(), nil
}

func decoder(data []byte) *xml.Decoder {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	d.Entity = nil
	return d
}
func parseContentTypes(ctx context.Context, data []byte) (map[string]bool, bool, error) {
	d := decoder(data)
	names := map[string]bool{}
	defaults := map[string]string{}
	macro := false
	tokens := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		t, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, false, fmt.Errorf("%w: content types XML", ErrCorrupt)
		}
		tokens++
		if tokens > MaxTokens {
			return nil, false, ErrLimit
		}
		s, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		if s.Name.Space == strictWNS {
			return nil, false, ErrSkip
		}
		if s.Name.Space != ctNS {
			return nil, false, fmt.Errorf("%w: content types namespace", ErrCorrupt)
		}
		switch s.Name.Local {
		case "Override":
			var name, typ string
			for _, a := range s.Attr {
				if a.Name.Local == "PartName" {
					name = a.Value
				}
				if a.Name.Local == "ContentType" {
					typ = a.Value
				}
			}
			name = strings.TrimPrefix(name, "/")
			if name == "" || path.Clean(name) != name || strings.Contains(name, "\\") {
				return nil, false, fmt.Errorf("%w: content type path", ErrCorrupt)
			}
			if _, ok := names[name]; ok {
				return nil, false, fmt.Errorf("%w: duplicate content type", ErrCorrupt)
			}
			names[name] = typ == mainType
			if typ == macroType {
				macro = true
			}
		case "Default":
			var ext, typ string
			for _, a := range s.Attr {
				if a.Name.Local == "Extension" {
					ext = a.Value
				}
				if a.Name.Local == "ContentType" {
					typ = a.Value
				}
			}
			defaults[strings.ToLower(ext)] = typ
		}
	}
	for n := range names {
		if !names[n] && strings.HasSuffix(n, ".docm") {
			macro = true
		}
	}
	return names, macro, nil
}
func mainTarget(ctx context.Context, data []byte) (string, error) {
	d := decoder(data)
	target := ""
	count := 0
	tokens := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		t, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", fmt.Errorf("%w: relationships XML", ErrCorrupt)
		}
		tokens++
		if tokens > MaxTokens {
			return "", ErrLimit
		}
		s, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		if s.Name.Space == "http://purl.oclc.org/ooxml/officeDocument/relationships" {
			return "", ErrSkip
		}
		if s.Name.Space != pkgRelNS {
			return "", fmt.Errorf("%w: relationship namespace", ErrCorrupt)
		}
		typ, tar, mode := "", "", ""
		for _, a := range s.Attr {
			switch a.Name.Local {
			case "Type":
				typ = a.Value
			case "Target":
				tar = a.Value
			case "TargetMode":
				mode = a.Value
			}
		}
		if strings.HasSuffix(typ, "/officeDocument") {
			count++
			if mode != "" && mode != "Internal" {
				return "", fmt.Errorf("%w: external main relationship", ErrCorrupt)
			}
			if tar == "" || strings.ContainsAny(tar, "\\\x00?#") || strings.HasPrefix(tar, "/") {
				return "", fmt.Errorf("%w: unsafe relationship target", ErrCorrupt)
			}
			decoded, decodeErr := url.PathUnescape(tar)
			if decodeErr != nil || strings.ContainsAny(decoded, "\\\x00?#") || strings.HasPrefix(decoded, "/") {
				return "", fmt.Errorf("%w: unsafe relationship target", ErrCorrupt)
			}
			joined := path.Clean(path.Join("", decoded))
			if joined == ".." || strings.HasPrefix(joined, "../") {
				return "", fmt.Errorf("%w: relationship escapes package", ErrCorrupt)
			}
			target = joined
		}
	}
	if count != 1 {
		return "", fmt.Errorf("%w: missing or ambiguous main relationship", ErrCorrupt)
	}
	return target, nil
}

// parseBody enforces XML token/depth budgets while collecting only body text.
func parseBody(ctx context.Context, data []byte) (string, error) {
	d := decoder(data)
	stack := make([]xml.Name, 0, 32)
	var b strings.Builder
	tokens := 0
	paragraphs := 0
	inBody := false
	skip := 0
	hidden := 0
	inP := false
	cells := 0
	rootSeen, bodySeen := false, false
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		t, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", fmt.Errorf("%w: document XML", ErrCorrupt)
		}
		tokens++
		if tokens > MaxTokens {
			return "", ErrLimit
		}
		switch x := t.(type) {
		case xml.StartElement:
			if len(stack) >= MaxDepth {
				return "", ErrLimit
			}
			parent := xml.Name{}
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			if len(stack) == 0 && x.Name.Local == "document" && x.Name.Space == strictWNS {
				return "", ErrSkip
			}
			stack = append(stack, x.Name)
			if x.Name.Space == wNS {
				switch x.Name.Local {
				case "document":
					if len(stack) != 1 || rootSeen {
						return "", fmt.Errorf("%w: invalid document root", ErrCorrupt)
					}
					rootSeen = true
				case "body":
					if parent.Local != "document" || parent.Space != wNS || bodySeen {
						return "", fmt.Errorf("%w: invalid body", ErrCorrupt)
					}
					bodySeen = true
					inBody = true
				case "p":
					if inBody && skip == 0 {
						if paragraphs > 0 {
							b.WriteByte('\n')
						}
						paragraphs++
						inP = true
					}
				case "del", "moveFrom", "instrText", "delText":
					skip++
				case "vanish", "webHidden":
					isVisible := false
					for _, a := range x.Attr {
						if a.Name.Space == wNS && a.Name.Local == "val" && (a.Value == "0" || a.Value == "false" || a.Value == "off") {
							isVisible = true
						}
					}
					if !isVisible {
						hidden++
					}
				case "tab":
					if inBody && skip == 0 && hidden == 0 {
						b.WriteByte('\t')
					}
				case "br", "cr":
					if inBody && skip == 0 && hidden == 0 {
						b.WriteByte('\n')
					}
				case "tc":
					if inBody && skip == 0 {
						if cells > 0 {
							b.WriteString(" | ")
						}
						cells++
					}
				}
			}
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1] != x.Name {
				return "", fmt.Errorf("%w: unbalanced document XML", ErrCorrupt)
			}
			if x.Name.Space == wNS {
				switch x.Name.Local {
				case "del", "moveFrom", "instrText", "delText":
					if skip > 0 {
						skip--
					}
				case "r":
					// Direct run-level vanish remains active through the run's text.
					if hidden > 0 {
						hidden--
					}
				case "p":
					inP = false
				case "body":
					inBody = false
				case "tbl":
					cells = 0
				}
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if inBody && skip == 0 && hidden == 0 && len(stack) > 0 {
				n := stack[len(stack)-1]
				if n.Space == wNS && (n.Local == "t" || n.Local == "delText") {
					for _, r := range string(x) {
						if unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
							continue
						}
						if uint64(b.Len()+utf8.RuneLen(r)) > MaxText {
							return "", ErrLimit
						}
						b.WriteRune(r)
					}
				}
			}
		}
	}
	if len(stack) != 0 || !rootSeen || !bodySeen {
		return "", fmt.Errorf("%w: truncated document XML", ErrCorrupt)
	}
	if !inP && paragraphs == 0 {
		return "", ErrNoText
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}
