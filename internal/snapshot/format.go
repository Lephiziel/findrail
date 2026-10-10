// Package snapshot implements the portable, plaintext Findrail snapshot format.
// It deliberately treats document paths as metadata; ZIP entries are fixed names.
package snapshot

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	FormatID        = "findrail-portable-snapshot"
	Version         = 1
	MaxArchive      = 256 << 20
	MaxManifest     = 256 << 10
	MaxUncompressed = 512 << 20
	MaxDocuments    = 10000
	MaxPages        = 100000
	MaxRecord       = 16 << 20
	MaxText         = 8 << 20
	allowedZIPFlags = 0x080e // deflate options, data descriptor and UTF-8 names only
)

type Manifest struct {
	Format          string `json:"format"`
	Version         int    `json:"version"`
	Producer        string `json:"producer"`
	ExportedAt      string `json:"exported_at"`
	Origin          Origin `json:"origin"`
	DocumentCount   int    `json:"document_count"`
	PageCount       int    `json:"page_count"`
	DocumentsBytes  int64  `json:"documents_bytes"`
	DocumentsSHA256 string `json:"documents_sha256"`
	PagesBytes      int64  `json:"pages_bytes"`
	PagesSHA256     string `json:"pages_sha256"`
	Fingerprint     string `json:"fingerprint"`
}

type Origin struct {
	ID                  string `json:"id"`
	Kind                string `json:"kind"`
	Name                string `json:"name"`
	Location            string `json:"location"`
	IndexedAt           string `json:"indexed_at"`
	MaxTextBytes        int64  `json:"max_text_bytes"`
	MaxPDFBytes         int64  `json:"max_pdf_bytes"`
	MaxDOCXBytes        int64  `json:"max_docx_bytes"`
	RepositoryURL       string `json:"repository_url,omitempty"`
	Owner               string `json:"owner,omitempty"`
	Repository          string `json:"repository,omitempty"`
	FullCommitSHA       string `json:"full_commit_sha,omitempty"`
	RefMode             string `json:"ref_mode,omitempty"`
	RefValue            string `json:"ref_value,omitempty"`
	SelectedPath        string `json:"selected_path,omitempty"`
	GitHubMaxBytes      int64  `json:"github_max_bytes,omitempty"`
	GitHubPolicyVersion int    `json:"github_policy_version,omitempty"`
	CommitTime          string `json:"commit_time,omitempty"`
}

type Document struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Title       string `json:"title"`
	URI         string `json:"uri"`
	MediaType   string `json:"media_type"`
	Text        string `json:"text"`
	ContentHash string `json:"content_hash"`
	SizeBytes   int64  `json:"size_bytes"`
	ModifiedAt  string `json:"modified_at"`
	PageCount   int    `json:"page_count"`
}

type Page struct {
	Path   string `json:"path"`
	Number int    `json:"number"`
	Text   string `json:"text"`
}

type Archive struct {
	Manifest  Manifest
	Documents []Document
	Pages     []Page
}

// Fingerprint uses an explicit, length-prefixed provenance encoding and excludes
// exporter/time metadata. It is a consistency identifier, not an authenticity proof.
func Fingerprint(o Origin, documentCount, pageCount int, documentsHash, pagesHash string) string {
	var b bytes.Buffer
	io := []string{"findrail-snapshot-fingerprint-v1", o.ID, o.Kind, o.Name, o.Location, o.IndexedAt,
		fmt.Sprint(o.MaxTextBytes), fmt.Sprint(o.MaxPDFBytes), fmt.Sprint(o.MaxDOCXBytes),
		o.RepositoryURL, o.Owner, o.Repository, o.FullCommitSHA, o.RefMode, o.RefValue, o.SelectedPath,
		fmt.Sprint(o.GitHubMaxBytes), fmt.Sprint(o.GitHubPolicyVersion), o.CommitTime,
		fmt.Sprint(documentCount), fmt.Sprint(pageCount), documentsHash, pagesHash}
	for _, field := range io {
		fmt.Fprintf(&b, "%d:", len(field))
		b.WriteString(field)
	}
	h := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(h[:])
}

func Encode(m Manifest, documents []Document, pages []Page) ([]byte, error) {
	docData, err := jsonLines(documents)
	if err != nil {
		return nil, err
	}
	pageData, err := jsonLines(pages)
	if err != nil {
		return nil, err
	}
	dh, ph := sha256.Sum256(docData), sha256.Sum256(pageData)
	m.Format, m.Version = FormatID, Version
	m.DocumentCount, m.PageCount = len(documents), len(pages)
	m.DocumentsBytes, m.PagesBytes = int64(len(docData)), int64(len(pageData))
	m.DocumentsSHA256, m.PagesSHA256 = hex.EncodeToString(dh[:]), hex.EncodeToString(ph[:])
	m.Fingerprint = Fingerprint(m.Origin, m.DocumentCount, m.PageCount, m.DocumentsSHA256, m.PagesSHA256)
	manifest, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if len(manifest) > MaxManifest {
		return nil, errors.New("snapshot manifest exceeds v1 limit")
	}
	var output bytes.Buffer
	w := zip.NewWriter(&output)
	for _, entry := range []struct {
		name string
		data []byte
	}{{"manifest.json", manifest}, {"documents.jsonl", docData}, {"pages.jsonl", pageData}} {
		h := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		h.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		f, e := w.CreateHeader(h)
		if e != nil {
			return nil, e
		}
		if _, e = f.Write(entry.data); e != nil {
			return nil, e
		}
	}
	if err = w.Close(); err != nil {
		return nil, err
	}
	if output.Len() > MaxArchive {
		return nil, errors.New("snapshot archive exceeds v1 limit")
	}
	return output.Bytes(), nil
}

// PayloadFunc streams JSONL records to the supplied bounded staging writer and
// returns its record count. It must not retain database rows after returning.
type PayloadFunc func(context.Context, io.Writer) (int, error)

// WriteArchive stages and hashes two bounded payloads, then writes a complete
// deterministic ZIP to dst. The caller must keep dst private until this returns.
func WriteArchive(ctx context.Context, dst io.Writer, m Manifest, documents, pages PayloadFunc) (Manifest, error) {
	var err error
	dir, err := os.MkdirTemp("", "findrail-snapshot-")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(dir)
	if err = os.Chmod(dir, 0700); err != nil {
		return m, err
	}
	makePayload := func(name string, max int64, fn PayloadFunc) (string, int, int64, string, error) {
		path := filepath.Join(dir, name)
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return "", 0, 0, "", e
		}
		h := sha256.New()
		limited := &limitedHashWriter{w: io.MultiWriter(f, h), max: max}
		count, e := fn(ctx, limited)
		if e == nil {
			e = ctx.Err()
		}
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			return "", 0, 0, "", e
		}
		return path, count, limited.n, hex.EncodeToString(h.Sum(nil)), nil
	}
	docPath, docCount, docBytes, docHash, err := makePayload("documents.jsonl", MaxUncompressed, documents)
	if err != nil {
		return m, err
	}
	pagePath, pageCount, pageBytes, pageHash, err := makePayload("pages.jsonl", MaxUncompressed-docBytes, pages)
	if err != nil {
		return m, err
	}
	if docCount < 0 || docCount > MaxDocuments || pageCount < 0 || pageCount > MaxPages {
		return m, errors.New("snapshot resource limit exceeded")
	}
	m.Format, m.Version = FormatID, Version
	m.DocumentCount, m.PageCount = docCount, pageCount
	m.DocumentsBytes, m.PagesBytes = docBytes, pageBytes
	m.DocumentsSHA256, m.PagesSHA256 = docHash, pageHash
	m.Fingerprint = Fingerprint(m.Origin, docCount, pageCount, docHash, pageHash)
	manifest, err := json.Marshal(m)
	if err != nil {
		return m, err
	}
	if len(manifest) > MaxManifest {
		return m, errors.New("snapshot manifest exceeds v1 limit")
	}
	if docBytes+pageBytes+int64(len(manifest)) > MaxUncompressed {
		return m, errors.New("snapshot uncompressed size exceeds v1 limit")
	}
	limitedOut := &limitedWriter{w: dst, max: MaxArchive}
	zw := zip.NewWriter(limitedOut)
	for _, entry := range []struct{ name, path string }{{"manifest.json", ""}, {"documents.jsonl", docPath}, {"pages.jsonl", pagePath}} {
		h := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		h.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		z, e := zw.CreateHeader(h)
		if e != nil {
			return m, e
		}
		if entry.path == "" {
			if _, e = z.Write(manifest); e != nil {
				return m, e
			}
			continue
		}
		f, e := os.Open(entry.path)
		if e != nil {
			return m, e
		}
		_, copyErr := copyContext(ctx, z, f)
		closeErr := f.Close()
		if copyErr != nil {
			return m, copyErr
		}
		if closeErr != nil {
			return m, closeErr
		}
	}
	if err = ctx.Err(); err != nil {
		return m, err
	}
	if err = zw.Close(); err != nil {
		return m, err
	}
	return m, nil
}

type limitedHashWriter struct {
	w   io.Writer
	max int64
	n   int64
}

func (w *limitedHashWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.max-w.n {
		return 0, errors.New("snapshot payload exceeds v1 limit")
	}
	n, e := w.w.Write(p)
	w.n += int64(n)
	if e == nil && n != len(p) {
		e = io.ErrShortWrite
	}
	return n, e
}

type limitedWriter struct {
	w   io.Writer
	max int64
	n   int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.max-w.n {
		return 0, errors.New("snapshot archive exceeds v1 limit")
	}
	n, e := w.w.Write(p)
	w.n += int64(n)
	if e == nil && n != len(p) {
		e = io.ErrShortWrite
	}
	return n, e
}
func copyContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 64<<10)
	var total int64
	for {
		if e := ctx.Err(); e != nil {
			return total, e
		}
		n, re := src.Read(buf)
		if n > 0 {
			wn, we := dst.Write(buf[:n])
			total += int64(wn)
			if we != nil {
				return total, we
			}
			if wn != n {
				return total, io.ErrShortWrite
			}
		}
		if re == io.EOF {
			return total, nil
		}
		if re != nil {
			return total, re
		}
	}
}

// WriteJSONLRecord encodes one bounded JSON value with its required newline.
func WriteJSONLRecord(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data)+1 > MaxRecord {
		return errors.New("JSONL record exceeds v1 limit")
	}
	data = append(data, '\n')
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}

func jsonLines[T any](records []T) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			return nil, err
		}
		if b.Len() > MaxUncompressed {
			return nil, errors.New("snapshot payload exceeds v1 limit")
		}
	}
	return b.Bytes(), nil
}

// Inspect validates the complete fixed-entry archive before returning records.
// Inputs exceeding the hard compressed or expanded limits are rejected.
func Inspect(data []byte) (Archive, error) {
	return InspectContext(context.Background(), data)
}

func InspectContext(ctx context.Context, data []byte) (Archive, error) {
	var result Archive
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if len(data) > MaxArchive {
		return result, errors.New("snapshot archive exceeds v1 limit")
	}
	if err := validateZIPEnvelope(data); err != nil {
		return result, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return result, fmt.Errorf("invalid snapshot ZIP: %w", err)
	}
	if len(zr.File) != 3 {
		return result, errors.New("snapshot must contain exactly manifest.json, documents.jsonl, and pages.jsonl")
	}
	files := map[string]*zip.File{}
	var expanded uint64
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if f.Name != "manifest.json" && f.Name != "documents.jsonl" && f.Name != "pages.jsonl" {
			return result, fmt.Errorf("unexpected snapshot entry %q", f.Name)
		}
		if _, ok := files[f.Name]; ok {
			return result, fmt.Errorf("duplicate snapshot entry %q", f.Name)
		}
		if f.Mode().IsDir() || !f.Mode().IsRegular() || (f.Method != zip.Store && f.Method != zip.Deflate) || f.Flags&^allowedZIPFlags != 0 || f.CompressedSize64 > uint64(len(data)) {
			return result, errors.New("snapshot contains a non-regular or unsupported ZIP entry")
		}
		if f.UncompressedSize64 > MaxUncompressed || expanded > MaxUncompressed-f.UncompressedSize64 {
			return result, errors.New("snapshot expanded size exceeds v1 limit")
		}
		expanded += f.UncompressedSize64
		files[f.Name] = f
	}
	if files["manifest.json"].UncompressedSize64 > MaxManifest {
		return result, errors.New("snapshot manifest exceeds v1 limit")
	}
	manifestBytes, err := readEntryContext(ctx, files["manifest.json"], MaxManifest)
	if err != nil {
		return result, err
	}
	if !utf8.Valid(manifestBytes) {
		return result, errors.New("snapshot manifest is not valid UTF-8")
	}
	if err = checkJSONStructure(manifestBytes); err != nil {
		return result, err
	}
	if err = requiredFields(manifestBytes, "format", "version"); err != nil {
		return result, err
	}
	var top map[string]json.RawMessage
	if err = json.Unmarshal(manifestBytes, &top); err != nil {
		return result, err
	}
	var formatID string
	var version int
	if err = json.Unmarshal(top["format"], &formatID); err != nil {
		return result, errors.New("invalid snapshot format identifier")
	}
	if err = json.Unmarshal(top["version"], &version); err != nil {
		return result, errors.New("invalid snapshot format version")
	}
	if formatID != FormatID {
		return result, errors.New("unrecognized snapshot format")
	}
	if version != Version {
		return result, fmt.Errorf("unsupported snapshot format version %d; upgrade Findrail", version)
	}
	if err = requiredFields(manifestBytes, "format", "version", "producer", "exported_at", "origin", "document_count", "page_count", "documents_bytes", "documents_sha256", "pages_bytes", "pages_sha256", "fingerprint"); err != nil {
		return result, err
	}
	var originFields map[string]json.RawMessage
	if err = json.Unmarshal(top["origin"], &originFields); err != nil {
		return result, err
	}
	for _, key := range []string{"id", "kind", "name", "location", "indexed_at", "max_text_bytes", "max_pdf_bytes", "max_docx_bytes"} {
		if _, ok := originFields[key]; !ok {
			return result, fmt.Errorf("missing required source provenance field %q", key)
		}
	}
	if err = strictJSON(manifestBytes, &result.Manifest); err != nil {
		return result, fmt.Errorf("invalid snapshot manifest: %w", err)
	}
	m := result.Manifest
	if err = validateManifest(m); err != nil {
		return result, err
	}
	if m.DocumentCount < 0 || m.DocumentCount > MaxDocuments || m.PageCount < 0 || m.PageCount > MaxPages {
		return result, errors.New("snapshot record count exceeds v1 limits")
	}
	remaining := uint64(MaxUncompressed) - uint64(len(manifestBytes))
	docs, err := readEntryContext(ctx, files["documents.jsonl"], remaining)
	if err != nil {
		return result, err
	}
	remaining -= uint64(len(docs))
	pages, err := readEntryContext(ctx, files["pages.jsonl"], remaining)
	if err != nil {
		return result, err
	}
	if int64(len(docs)) != m.DocumentsBytes || int64(len(pages)) != m.PagesBytes {
		return result, errors.New("snapshot payload byte count mismatch")
	}
	dh, ph := sha256.Sum256(docs), sha256.Sum256(pages)
	if hex.EncodeToString(dh[:]) != m.DocumentsSHA256 || hex.EncodeToString(ph[:]) != m.PagesSHA256 {
		return result, errors.New("snapshot payload hash mismatch")
	}
	if Fingerprint(m.Origin, m.DocumentCount, m.PageCount, m.DocumentsSHA256, m.PagesSHA256) != m.Fingerprint {
		return result, errors.New("snapshot fingerprint mismatch")
	}
	if !utf8.Valid(docs) || !utf8.Valid(pages) {
		return result, errors.New("snapshot JSONL is not valid UTF-8")
	}
	if result.Documents, err = parseLinesContext[Document](ctx, docs, MaxDocuments, "id", "path", "title", "uri", "media_type", "text", "content_hash", "size_bytes", "modified_at", "page_count"); err != nil {
		return result, err
	}
	if result.Pages, err = parseLinesContext[Page](ctx, pages, MaxPages, "path", "number", "text"); err != nil {
		return result, err
	}
	if len(result.Documents) != m.DocumentCount || len(result.Pages) != m.PageCount {
		return result, errors.New("snapshot record count mismatch")
	}
	if err = validateRecords(result); err != nil {
		return result, err
	}
	return result, nil
}

func validateZIPEnvelope(data []byte) error {
	if len(data) < 22 || binary.LittleEndian.Uint32(data[:4]) != 0x04034b50 {
		return errors.New("snapshot ZIP must start with a regular ZIP entry (no prefix data)")
	}
	start := len(data) - 22 - 65535
	if start < 0 {
		start = 0
	}
	for i := len(data) - 22; i >= start; i-- {
		if binary.LittleEndian.Uint32(data[i:i+4]) == 0x06054b50 {
			comment := int(binary.LittleEndian.Uint16(data[i+20 : i+22]))
			if comment == 0 && i+22 == len(data) {
				return nil
			}
		}
	}
	return errors.New("invalid ZIP trailer, archive comment, or trailing archive data")
}

// VerifyArchive validates a generated archive in-place without loading its
// payloads into memory. It checks the fixed ZIP table, CRCs, JSONL framing,
// declared/actual lengths, payload hashes and semantic fingerprint.
func VerifyArchive(ctx context.Context, ra io.ReaderAt, size int64) (Manifest, error) {
	var m Manifest
	if size < 0 || size > MaxArchive {
		return m, errors.New("snapshot archive exceeds v1 limit")
	}
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return m, err
	}
	if len(zr.File) != 3 {
		return m, errors.New("snapshot must contain exactly three entries")
	}
	entries := map[string]*zip.File{}
	var declared uint64
	for _, f := range zr.File {
		if err = ctx.Err(); err != nil {
			return m, err
		}
		if f.Name != "manifest.json" && f.Name != "documents.jsonl" && f.Name != "pages.jsonl" {
			return m, errors.New("unexpected ZIP entry")
		}
		if entries[f.Name] != nil {
			return m, errors.New("duplicate ZIP entry")
		}
		if f.Mode().IsDir() || !f.Mode().IsRegular() || (f.Method != zip.Store && f.Method != zip.Deflate) || f.Flags&^allowedZIPFlags != 0 {
			return m, errors.New("unsupported ZIP entry")
		}
		if f.UncompressedSize64 > MaxUncompressed || declared > MaxUncompressed-f.UncompressedSize64 {
			return m, errors.New("snapshot expanded size exceeds v1 limit")
		}
		declared += f.UncompressedSize64
		entries[f.Name] = f
	}
	if entries["manifest.json"].UncompressedSize64 > MaxManifest {
		return m, errors.New("snapshot manifest exceeds v1 limit")
	}
	data, err := readEntryContext(ctx, entries["manifest.json"], MaxManifest)
	if err != nil {
		return m, err
	}
	if !utf8.Valid(data) {
		return m, errors.New("snapshot manifest is not valid UTF-8")
	}
	if err = requiredFields(data, "format", "version", "producer", "exported_at", "origin", "document_count", "page_count", "documents_bytes", "documents_sha256", "pages_bytes", "pages_sha256", "fingerprint"); err != nil {
		return m, err
	}
	if err = strictJSON(data, &m); err != nil {
		return m, err
	}
	if m.Format != FormatID || m.Version != Version {
		return m, errors.New("unsupported generated snapshot manifest")
	}
	if m.DocumentCount < 0 || m.DocumentCount > MaxDocuments || m.PageCount < 0 || m.PageCount > MaxPages {
		return m, errors.New("snapshot record count exceeds v1 limits")
	}
	if err = validateManifest(m); err != nil {
		return m, err
	}
	var actual int64 = int64(len(data))
	for _, spec := range []struct {
		name  string
		size  int64
		hash  string
		count int
	}{{"documents.jsonl", m.DocumentsBytes, m.DocumentsSHA256, m.DocumentCount}, {"pages.jsonl", m.PagesBytes, m.PagesSHA256, m.PageCount}} {
		f := entries[spec.name]
		if f == nil {
			return m, errors.New("missing payload entry")
		}
		r, e := f.Open()
		if e != nil {
			return m, e
		}
		h := sha256.New()
		lines := 0
		record := 0
		last := byte('\n')
		buf := make([]byte, 64<<10)
		var nbytes int64
		for {
			if e = ctx.Err(); e != nil {
				r.Close()
				return m, e
			}
			n, re := r.Read(buf)
			if n > 0 {
				nbytes += int64(n)
				actual += int64(n)
				if actual > MaxUncompressed || record > MaxRecord {
					return m, errors.New("snapshot actual uncompressed limits exceeded")
				}
				if _, e = h.Write(buf[:n]); e != nil {
					r.Close()
					return m, e
				}
				for _, c := range buf[:n] {
					if c == '\n' {
						lines++
						record = 0
					} else {
						record++
						if record > MaxRecord {
							return m, errors.New("JSONL record exceeds v1 limit")
						}
					}
					last = c
				}
			}
			if re == io.EOF {
				break
			}
			if re != nil {
				r.Close()
				return m, re
			}
		}
		closeErr := r.Close()
		if closeErr != nil {
			return m, closeErr
		}
		if last != '\n' || record != 0 || nbytes != spec.size || hex.EncodeToString(h.Sum(nil)) != spec.hash || lines != spec.count {
			return m, fmt.Errorf("%s integrity/count mismatch", spec.name)
		}
	}
	if Fingerprint(m.Origin, m.DocumentCount, m.PageCount, m.DocumentsSHA256, m.PagesSHA256) != m.Fingerprint {
		return m, errors.New("snapshot fingerprint mismatch")
	}
	return m, nil
}

func readEntry(f *zip.File, max uint64) ([]byte, error) {
	return readEntryContext(context.Background(), f, max)
}
func readEntryContext(ctx context.Context, f *zip.File, max uint64) ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var b bytes.Buffer
	buf := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, e := r.Read(buf)
		if n > 0 {
			if uint64(b.Len())+uint64(n) > max {
				return nil, errors.New("snapshot entry exceeds v1 limit")
			}
			if _, err := b.Write(buf[:n]); err != nil {
				return nil, err
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
	}
	return b.Bytes(), nil
}

func strictJSON(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("extra JSON value")
	}
	return checkJSONStructure(data)
}

func checkJSONStructure(data []byte) error {
	// encoding/json accepts duplicate object keys. Walk each object token before decode.
	d := json.NewDecoder(bytes.NewReader(data))
	if err := checkValue(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func checkValue(d *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("JSON nesting exceeds v1 limit")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			key, ok := k.(string)
			if !ok {
				return errors.New("invalid JSON object key")
			}
			if seen[key] {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = true
			if e = checkValue(d, depth+1); e != nil {
				return e
			}
		}
		_, err = d.Token()
	case json.Delim('['):
		for d.More() {
			if err = checkValue(d, depth+1); err != nil {
				return err
			}
		}
		_, err = d.Token()
	}
	return err
}

func parseLines[T any](data []byte, limit int, required ...string) ([]T, error) {
	return parseLinesContext[T](context.Background(), data, limit, required...)
}
func parseLinesContext[T any](ctx context.Context, data []byte, limit int, required ...string) ([]T, error) {
	var out []T
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			return nil, errors.New("truncated JSONL record")
		}
		line := data[:i]
		data = data[i+1:]
		if len(line) == 0 {
			return nil, errors.New("empty JSONL record")
		}
		if len(line) > MaxRecord {
			return nil, errors.New("JSONL record exceeds v1 limit")
		}
		if err := requiredFields(line, required...); err != nil {
			return nil, err
		}
		var row T
		if err := strictJSON(line, &row); err != nil {
			return nil, fmt.Errorf("invalid JSONL record: %w", err)
		}
		out = append(out, row)
		if len(out) > limit {
			return nil, errors.New("snapshot record count exceeds v1 limit")
		}
	}
	return out, nil
}

func requiredFields(data []byte, fields ...string) error {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	for _, field := range fields {
		if _, ok := values[field]; !ok {
			return fmt.Errorf("missing required JSON field %q", field)
		}
	}
	return nil
}

func validateManifest(m Manifest) error {
	if err := validateMetadataString(m.Producer); err != nil {
		return err
	}
	if m.Producer == "" {
		return errors.New("missing snapshot producer version")
	}
	exported, err := time.Parse(time.RFC3339Nano, m.ExportedAt)
	if err != nil {
		return errors.New("invalid snapshot export timestamp")
	}
	_, offset := exported.Zone()
	if offset != 0 {
		return errors.New("snapshot export timestamp must be UTC")
	}
	if m.DocumentsBytes < 0 || m.PagesBytes < 0 || !sha256Hex(m.DocumentsSHA256) || !sha256Hex(m.PagesSHA256) || !sha256Hex(m.Fingerprint) {
		return errors.New("invalid snapshot sizes or hash fields")
	}
	return validateOrigin(m.Origin)
}

func validateRecords(a Archive) error {
	paths := make(map[string]Document, len(a.Documents))
	ids := make(map[string]bool, len(a.Documents))
	if err := validateOrigin(a.Manifest.Origin); err != nil {
		return err
	}
	for i, d := range a.Documents {
		if ids[d.ID] {
			return errors.New("duplicate original document ID")
		}
		if err := ValidateDocumentRecord(d, a.Manifest.Origin); err != nil {
			return err
		}
		ids[d.ID] = true
		if i > 0 && a.Documents[i-1].Path >= d.Path {
			return errors.New("snapshot documents are not uniquely ordered by path")
		}
		paths[d.Path] = d
	}
	lastPath, lastPage := "", 0
	pageBodyOffset := map[string]int{}
	for _, p := range a.Pages {
		d, ok := paths[p.Path]
		if !ok || d.PageCount == 0 {
			return errors.New("orphan or non-PDF page record")
		}
		if len(p.Text) > MaxText || !utf8.ValidString(p.Text) || strings.ContainsRune(p.Text, 0) {
			return errors.New("invalid or oversized page text")
		}
		if p.Path != lastPath {
			if lastPath != "" && lastPage != paths[lastPath].PageCount {
				return errors.New("missing PDF page record")
			}
			if p.Path <= lastPath || p.Number != 1 {
				return errors.New("snapshot pages are not ordered and contiguous")
			}
			lastPath, lastPage = p.Path, 0
		}
		lastPage++
		if p.Number != lastPage || p.Number > d.PageCount {
			return errors.New("duplicate, out-of-order, or excess PDF page")
		}
		offset := pageBodyOffset[p.Path]
		piece := p.Text + "\n"
		if offset+len(piece) > len(d.Text) || d.Text[offset:offset+len(piece)] != piece {
			return errors.New("PDF body does not match the indexed page-text convention")
		}
		pageBodyOffset[p.Path] = offset + len(piece)
	}
	if lastPath != "" && lastPage != paths[lastPath].PageCount {
		return errors.New("missing PDF page record")
	}
	for path, d := range paths {
		if d.PageCount > 0 {
			offset, ok := pageBodyOffset[path]
			if !ok {
				return errors.New("missing PDF page record")
			}
			if offset != len(d.Text) {
				return errors.New("PDF body does not match the indexed page-text convention")
			}
		}
	}
	return nil
}

// ValidateDocumentRecord checks bounded document metadata independently, for
// streaming source exporters that cannot retain the full document inventory.
func ValidateDocumentRecord(d Document, o Origin) error {
	if !validRelativePath(d.Path) || !utf8.ValidString(d.Path) || len(d.Text) > MaxText || !utf8.ValidString(d.Text) || strings.ContainsRune(d.Text, 0) {
		return errors.New("invalid or oversized document path/text")
	}
	if d.ID == "" || len(d.ID) > 512 || d.Title == "" || len(d.Title) > 4096 || len(d.URI) == 0 || len(d.URI) > 8192 || len(d.MediaType) > 255 || d.SizeBytes < 0 || d.PageCount < 0 || d.PageCount > 2000 || !sha256Hex(d.ContentHash) {
		return errors.New("invalid snapshot document metadata")
	}
	for _, s := range []string{d.Path, d.ID, d.Title, d.MediaType} {
		if err := validateMetadataString(s); err != nil {
			return err
		}
	}
	if err := validateURI(d.URI, o, d.Path); err != nil {
		return fmt.Errorf("invalid original URI: %w", err)
	}
	isPDF := d.MediaType == "application/pdf"
	if isPDF != (d.PageCount > 0) {
		return errors.New("document media type and page count disagree")
	}
	if !isPDF && d.MediaType != "text/plain" && d.MediaType != "text/markdown" && d.MediaType != "text/x-go" && d.MediaType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" && !strings.HasPrefix(d.MediaType, "text/") {
		return errors.New("unsupported document media type")
	}
	if _, err := time.Parse(time.RFC3339Nano, d.ModifiedAt); err != nil {
		return errors.New("invalid document modification timestamp")
	}
	return nil
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

func sha256Hex(s string) bool { return sha256Pattern.MatchString(s) }

var githubComponent = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var fullSHAPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func validRelativePath(p string) bool {
	if p == "" || len(p) > 2048 || utf8.RuneCountInString(p) > 512 || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") || regexp.MustCompile(`^[A-Za-z]:`).MatchString(p) {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func validateOrigin(o Origin) error {
	for _, s := range []string{o.ID, o.Kind, o.Name, o.Location, o.IndexedAt} {
		if err := validateMetadataString(s); err != nil {
			return err
		}
	}
	if o.ID == "" || len(o.ID) > 512 || o.Name == "" || len(o.Name) > 512 || len(o.Location) > 8192 || o.MaxTextBytes < 0 || o.MaxPDFBytes < 0 || o.MaxDOCXBytes < 0 {
		return errors.New("invalid original source provenance")
	}
	if _, err := time.Parse(time.RFC3339Nano, o.IndexedAt); err != nil {
		return errors.New("invalid original indexing timestamp")
	}
	switch o.Kind {
	case "filesystem":
		if !portableAbsolute(o.Location) {
			return errors.New("missing original filesystem location")
		}
	case "github":
		if !githubComponent.MatchString(o.Owner) || !githubComponent.MatchString(o.Repository) || len(o.FullCommitSHA) != 40 || !fullSHAPattern.MatchString(o.FullCommitSHA) || o.RepositoryURL != "https://github.com/"+o.Owner+"/"+o.Repository || o.Location != o.RepositoryURL || o.GitHubMaxBytes <= 0 || o.GitHubPolicyVersion <= 0 || (o.RefMode != "default" && o.RefMode != "ref" && o.RefMode != "commit") || (o.RefMode == "default" && o.RefValue != "") || (o.RefMode != "default" && o.RefValue == "") || (o.RefMode == "commit" && (!fullSHAPattern.MatchString(o.RefValue) || !strings.EqualFold(o.RefValue, o.FullCommitSHA))) || (o.RefMode == "ref" && (len(o.RefValue) > 1024 || strings.HasPrefix(o.RefValue, "/") || strings.HasSuffix(o.RefValue, "/") || strings.Contains(o.RefValue, "..") || strings.Contains(o.RefValue, "\\"))) {
			return errors.New("invalid pinned GitHub provenance")
		}
		if _, err := time.Parse(time.RFC3339Nano, o.CommitTime); err != nil {
			return errors.New("invalid GitHub commit timestamp")
		}
		if o.SelectedPath != "" && !validRelativePath(o.SelectedPath) {
			return errors.New("invalid selected GitHub path provenance")
		}
	default:
		return errors.New("unsupported original source kind")
	}
	for _, s := range []string{o.RepositoryURL, o.Owner, o.Repository, o.FullCommitSHA, o.RefMode, o.RefValue, o.SelectedPath} {
		if err := validateMetadataString(s); err != nil {
			return err
		}
	}
	return nil
}

func portableAbsolute(s string) bool {
	return strings.HasPrefix(s, "/") || regexp.MustCompile(`^[A-Za-z]:[\\/]`).MatchString(s) || strings.HasPrefix(s, `\\`) || strings.HasPrefix(s, `//`)
}

func validateMetadataString(s string) error {
	if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		return errors.New("invalid UTF-8 or NUL in metadata")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return errors.New("control character in metadata")
		}
	}
	return nil
}

func validateURI(raw string, o Origin, relative string) error {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("malformed URI or embedded credentials/query")
	}
	switch u.Scheme {
	case "file":
		if u.Host != "" && strings.ContainsAny(u.Host, "\\@:%") {
			return errors.New("unsafe file URI authority")
		}
		if u.Path == "" || !strings.HasPrefix(u.Path, "/") {
			return errors.New("file URI must have an absolute path")
		}
		loc := strings.ReplaceAll(o.Location, "\\", "/")
		expectedHost := ""
		expectedPath := ""
		if strings.HasPrefix(loc, "//") {
			parts := strings.SplitN(strings.TrimPrefix(loc, "//"), "/", 2)
			expectedHost = parts[0]
			expectedPath = "/"
			if len(parts) > 1 {
				expectedPath += parts[1]
			}
		} else {
			expectedPath = loc
			if regexp.MustCompile(`^[A-Za-z]:/`).MatchString(loc) {
				expectedPath = "/" + loc
			}
		}
		if u.Host == "localhost" {
			u.Host = ""
		}
		if !strings.EqualFold(u.Host, expectedHost) || path.Clean(u.Path) != path.Clean(path.Join(expectedPath, relative)) {
			return errors.New("file URI does not match original source location and relative path")
		}
	case "https":
		if o.Kind != "github" || u.Host != "github.com" || u.Fragment != "" {
			return errors.New("only commit-pinned public GitHub URIs are accepted")
		}
		prefix := "/" + url.PathEscape(o.Owner) + "/" + url.PathEscape(o.Repository) + "/blob/" + strings.ToLower(o.FullCommitSHA) + "/"
		suffix := escapedRelative(relative)
		if u.EscapedPath() != prefix+suffix {
			return errors.New("GitHub URI is not pinned to the original repository commit")
		}
	default:
		return errors.New("unsafe URI scheme")
	}
	return nil
}

func escapedRelative(s string) string {
	parts := strings.Split(s, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
