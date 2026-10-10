// Package snapshot implements the portable, plaintext Findrail snapshot format.
// It deliberately treats document paths as metadata; ZIP entries are fixed names.
package snapshot

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
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
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Location      string `json:"location"`
	IndexedAt     string `json:"indexed_at"`
	MaxTextBytes  int64  `json:"max_text_bytes"`
	MaxPDFBytes   int64  `json:"max_pdf_bytes"`
	MaxDOCXBytes  int64  `json:"max_docx_bytes"`
	RepositoryURL string `json:"repository_url,omitempty"`
	Owner         string `json:"owner,omitempty"`
	Repository    string `json:"repository,omitempty"`
	FullCommitSHA string `json:"full_commit_sha,omitempty"`
	RefMode       string `json:"ref_mode,omitempty"`
	RefValue      string `json:"ref_value,omitempty"`
	SelectedPath  string `json:"selected_path,omitempty"`
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
	var result Archive
	if len(data) > MaxArchive {
		return result, errors.New("snapshot archive exceeds v1 limit")
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
		if f.Name != "manifest.json" && f.Name != "documents.jsonl" && f.Name != "pages.jsonl" {
			return result, fmt.Errorf("unexpected snapshot entry %q", f.Name)
		}
		if _, ok := files[f.Name]; ok {
			return result, fmt.Errorf("duplicate snapshot entry %q", f.Name)
		}
		if f.Mode().IsDir() || !f.Mode().IsRegular() || (f.Method != zip.Store && f.Method != zip.Deflate) {
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
	manifestBytes, err := readEntry(files["manifest.json"], MaxManifest)
	if err != nil {
		return result, err
	}
	if !utf8.Valid(manifestBytes) {
		return result, errors.New("snapshot manifest is not valid UTF-8")
	}
	if err = strictJSON(manifestBytes, &result.Manifest); err != nil {
		return result, fmt.Errorf("invalid snapshot manifest: %w", err)
	}
	m := result.Manifest
	if m.Format != FormatID {
		return result, errors.New("unrecognized snapshot format")
	}
	if m.Version != Version {
		return result, fmt.Errorf("unsupported snapshot format version %d; upgrade Findrail", m.Version)
	}
	if m.DocumentCount < 0 || m.DocumentCount > MaxDocuments || m.PageCount < 0 || m.PageCount > MaxPages {
		return result, errors.New("snapshot record count exceeds v1 limits")
	}
	docs, err := readEntry(files["documents.jsonl"], MaxUncompressed)
	if err != nil {
		return result, err
	}
	pages, err := readEntry(files["pages.jsonl"], MaxUncompressed)
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
	if result.Documents, err = parseLines[Document](docs, MaxDocuments); err != nil {
		return result, err
	}
	if result.Pages, err = parseLines[Page](pages, MaxPages); err != nil {
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

func readEntry(f *zip.File, max uint64) ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if uint64(len(b)) > max {
		return nil, errors.New("snapshot entry exceeds v1 limit")
	}
	return b, nil
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
	// encoding/json accepts duplicate object keys. Walk each object token before decode.
	d = json.NewDecoder(bytes.NewReader(data))
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

func parseLines[T any](data []byte, limit int) ([]T, error) {
	var out []T
	for len(data) > 0 {
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

func validateRecords(a Archive) error {
	paths := make(map[string]Document, len(a.Documents))
	ids := make(map[string]bool, len(a.Documents))
	if err := validateOrigin(a.Manifest.Origin); err != nil {
		return err
	}
	for i, d := range a.Documents {
		if d.Path == "" || len(d.Path) > 2048 || utf8.RuneCountInString(d.Path) > 512 || !utf8.ValidString(d.Path) || strings.HasPrefix(d.Path, "/") || strings.ContainsAny(d.Path, "\\:\x00") {
			return errors.New("invalid snapshot document path")
		}
		if err := validateMetadataString(d.Path); err != nil {
			return err
		}
		for _, part := range strings.Split(d.Path, "/") {
			if part == "" || part == "." || part == ".." {
				return errors.New("invalid snapshot document path segment")
			}
		}
		if len(d.Text) > MaxText || !utf8.ValidString(d.Text) || strings.ContainsRune(d.Text, 0) {
			return errors.New("invalid or oversized document text")
		}
		if d.ID == "" || ids[d.ID] || d.Title == "" || len(d.Title) > 4096 || len(d.URI) == 0 || len(d.URI) > 8192 || d.SizeBytes < 0 || d.PageCount < 0 || d.PageCount > 2000 || !sha256Hex(d.ContentHash) {
			return errors.New("invalid snapshot document metadata")
		}
		ids[d.ID] = true
		if err := validateMetadataString(d.ID); err != nil {
			return err
		}
		if err := validateMetadataString(d.Title); err != nil {
			return err
		}
		if err := validateURI(d.URI, a.Manifest.Origin); err != nil {
			return fmt.Errorf("invalid original URI for %q: %w", d.Path, err)
		}
		isPDF := d.MediaType == "application/pdf"
		if isPDF != (d.PageCount > 0) {
			return errors.New("document media type and page count disagree")
		}
		if !isPDF && d.MediaType != "text/plain" && d.MediaType != "text/markdown" && d.MediaType != "text/x-go" && d.MediaType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" && !strings.HasPrefix(d.MediaType, "text/") {
			return errors.New("unsupported document media type")
		}
		if d.ModifiedAt == "" {
			return errors.New("missing document modification timestamp")
		}
		if _, e := time.Parse(time.RFC3339Nano, d.ModifiedAt); e != nil {
			return errors.New("invalid document modification timestamp")
		}
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
	for path, d := range paths {
		if d.PageCount > 0 && (lastPath != path || lastPage != d.PageCount) {
			return errors.New("missing PDF page record")
		}
	}
	return nil
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

func sha256Hex(s string) bool { return sha256Pattern.MatchString(s) }

func validateOrigin(o Origin) error {
	for _, s := range []string{o.ID, o.Kind, o.Name, o.Location, o.IndexedAt} {
		if err := validateMetadataString(s); err != nil {
			return err
		}
	}
	if o.ID == "" || o.Name == "" || len(o.Name) > 512 || len(o.Location) > 8192 || o.MaxTextBytes < 0 || o.MaxPDFBytes < 0 || o.MaxDOCXBytes < 0 {
		return errors.New("invalid original source provenance")
	}
	if _, err := time.Parse(time.RFC3339Nano, o.IndexedAt); err != nil {
		return errors.New("invalid original indexing timestamp")
	}
	switch o.Kind {
	case "filesystem":
		if o.Location == "" {
			return errors.New("missing original filesystem location")
		}
	case "github":
		if o.Owner == "" || o.Repository == "" || len(o.FullCommitSHA) != 40 || !regexp.MustCompile(`^[0-9a-fA-F]{40}$`).MatchString(o.FullCommitSHA) || o.RepositoryURL != "https://github.com/"+o.Owner+"/"+o.Repository {
			return errors.New("invalid pinned GitHub provenance")
		}
	default:
		return errors.New("unsupported original source kind")
	}
	return nil
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

func validateURI(raw string, o Origin) error {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || u.RawQuery != "" {
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
		if u.Fragment != "" && !strings.HasPrefix(u.Fragment, "page=") {
			return errors.New("invalid file URI fragment")
		}
		if u.Fragment != "" {
			n, e := strconv.Atoi(strings.TrimPrefix(u.Fragment, "page="))
			if e != nil || n < 1 {
				return errors.New("invalid file URI page fragment")
			}
		}
	case "https":
		if o.Kind != "github" || u.Host != "github.com" || u.Fragment != "" {
			return errors.New("only commit-pinned public GitHub URIs are accepted")
		}
		prefix := "/" + o.Owner + "/" + o.Repository + "/blob/" + strings.ToLower(o.FullCommitSHA) + "/"
		if !strings.HasPrefix(u.EscapedPath(), prefix) || len(u.EscapedPath()) <= len(prefix) {
			return errors.New("GitHub URI is not pinned to the original repository commit")
		}
	default:
		return errors.New("unsafe URI scheme")
	}
	return nil
}
