// Package evidencebundle encodes portable, selected evidence without provenance
// authority or any storage and filesystem dependencies.
package evidencebundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	Format     = "findrail-evidence-bundle"
	Version    = 1
	MaxRaw     = 4 << 20
	MaxEntries = 40
	MaxSources = 40
)

var ErrInvalid = errors.New("invalid evidence bundle")
var ErrLimit = errors.New("evidence bundle limit exceeded")

type Bundle struct {
	Format  string   `json:"format"`
	Version int      `json:"version"`
	Title   string   `json:"title"`
	Sources []Source `json:"sources"`
	Entries []Entry  `json:"entries"`
}
type Source struct {
	Alias       string `json:"alias"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
}
type Entry struct {
	SourceAlias  string `json:"source_alias"`
	RelativePath string `json:"relative_path"`
	MediaType    string `json:"media_type"`
	ContentHash  string `json:"content_hash"`
	Page         int    `json:"page"`
	StartChar    int    `json:"start_char"`
	Text         string `json:"text"`
	Note         string `json:"note"`
}

var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var aliasPattern = regexp.MustCompile(`^s([1-9]|[1-3][0-9]|40)$`)

// Decode reads at most MaxRaw+1 bytes, rejects non-UTF-8 and structurally
// ambiguous JSON, then returns a validated owned value.
func Decode(r io.Reader) (Bundle, error) {
	var zero Bundle
	b, err := io.ReadAll(io.LimitReader(r, MaxRaw+1))
	if err != nil {
		return zero, err
	}
	if len(b) > MaxRaw {
		return zero, ErrLimit
	}
	if !utf8.Valid(b) {
		return zero, ErrInvalid
	}
	if err := checkJSON(b); err != nil {
		return zero, err
	}
	var out Bundle
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return zero, ErrInvalid
	}
	if out.Format != Format || out.Version != Version {
		return zero, ErrInvalid
	}
	if err := validate(out); err != nil {
		return zero, err
	}
	return out, nil
}

func Encode(b Bundle) ([]byte, error) {
	if err := validate(b); err != nil {
		return nil, err
	}
	b.Format, b.Version = Format, Version
	if b.Sources == nil {
		b.Sources = []Source{}
	}
	if b.Entries == nil {
		b.Entries = []Entry{}
	}
	out, err := json.Marshal(b)
	if err != nil {
		return nil, ErrInvalid
	}
	if len(out) > MaxRaw {
		return nil, ErrLimit
	}
	return out, nil
}

// Digest is SHA-256 of Encode's canonical JSON bytes, not an authenticity proof.
func Digest(b Bundle) (string, error) {
	data, err := Encode(b)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

func validate(b Bundle) error {
	if b.Format != "" && b.Format != Format || b.Version != 0 && b.Version != Version {
		return ErrInvalid
	}
	if strings.TrimSpace(b.Title) == "" || len(b.Title) > 256 || !utf8.ValidString(b.Title) || strings.IndexByte(b.Title, 0) >= 0 {
		return ErrInvalid
	}
	if len(b.Sources) > MaxSources || len(b.Entries) > MaxEntries {
		return ErrLimit
	}
	aliases := make(map[string]bool, len(b.Sources))
	for _, s := range b.Sources {
		_, exists := aliases[s.Alias]
		if !aliasPattern.MatchString(s.Alias) || exists || len(s.DisplayName) > 512 || !utf8.ValidString(s.DisplayName) || strings.IndexByte(s.DisplayName, 0) >= 0 {
			return ErrInvalid
		}
		switch s.Kind {
		case "filesystem", "github", "archive":
		default:
			return ErrInvalid
		}
		aliases[s.Alias] = false
	}
	total := 0
	for _, e := range b.Entries {
		if _, ok := aliases[e.SourceAlias]; !ok {
			return ErrInvalid
		}
		aliases[e.SourceAlias] = true
		if !validPath(e.RelativePath) || len(e.RelativePath) > 2048 || utf8.RuneCountInString(e.RelativePath) > 512 || len(e.MediaType) > 255 || strings.TrimSpace(e.MediaType) == "" || !utf8.ValidString(e.MediaType) || strings.IndexByte(e.MediaType, 0) >= 0 || !hashPattern.MatchString(e.ContentHash) || e.StartChar < 0 || e.StartChar > 2147483647 {
			return ErrInvalid
		}
		if e.MediaType == "application/pdf" {
			if e.Page < 1 || e.Page > 100000 {
				return ErrInvalid
			}
		} else if e.Page != 0 {
			return ErrInvalid
		}
		meta, err := json.Marshal(struct {
			SourceAlias  string `json:"source_alias"`
			RelativePath string `json:"relative_path"`
			MediaType    string `json:"media_type"`
			ContentHash  string `json:"content_hash"`
			Page         int    `json:"page"`
			StartChar    int    `json:"start_char"`
		}{e.SourceAlias, e.RelativePath, e.MediaType, e.ContentHash, e.Page, e.StartChar})
		if err != nil || len(meta) > 16<<10 || len(e.Text) == 0 || len(e.Text) > 256<<10 || utf8.RuneCountInString(e.Text) > 65536 || len(e.Note) > 2048 || !utf8.ValidString(e.Text) || !utf8.ValidString(e.Note) || strings.IndexByte(e.Text, 0) >= 0 || strings.IndexByte(e.Note, 0) >= 0 {
			return ErrInvalid
		}
		total += len(e.Text)
		if total > 2<<20 {
			return ErrLimit
		}
	}
	for _, used := range aliases {
		if !used {
			return ErrInvalid
		}
	}
	return nil
}
func validPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00:") || strings.HasPrefix(p, "//") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return utf8.ValidString(p)
}

func checkJSON(data []byte) error {
	if err := checkUnicodeEscapes(data); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := checkObject(d, bundleFields); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

type fieldKind byte

const (
	stringField fieldKind = iota
	integerField
	sourcesField
	entriesField
)

var bundleFields = map[string]fieldKind{"format": stringField, "version": integerField, "title": stringField, "sources": sourcesField, "entries": entriesField}
var sourceFields = map[string]fieldKind{"alias": stringField, "kind": stringField, "display_name": stringField}
var entryFields = map[string]fieldKind{"source_alias": stringField, "relative_path": stringField, "media_type": stringField, "content_hash": stringField, "page": integerField, "start_char": integerField, "text": stringField, "note": stringField}

// The v1 shape is fixed: root -> array -> source/entry -> scalar. Checking
// exact keys, non-null types and array counts before typed decoding also bounds
// the temporary key sets and avoids Go's case-insensitive struct-field matching.
func checkObject(d *json.Decoder, fields map[string]fieldKind) error {
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(fields))
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return ErrInvalid
		}
		key, ok := token.(string)
		kind, known := fields[key]
		if !ok || !known || seen[key] {
			return ErrInvalid
		}
		seen[key] = true
		if kind == sourcesField || kind == entriesField {
			child, limit := sourceFields, MaxSources
			if kind == entriesField {
				child, limit = entryFields, MaxEntries
			}
			if err := checkArray(d, child, limit); err != nil {
				return err
			}
			continue
		}
		value, err := d.Token()
		if err != nil {
			return ErrInvalid
		}
		if kind == stringField {
			text, ok := value.(string)
			if !ok {
				return ErrInvalid
			}
			if len(text) > 256<<10 {
				return ErrLimit
			}
		} else {
			number, ok := value.(json.Number)
			if !ok || strings.ContainsAny(string(number), ".eE") {
				return ErrInvalid
			}
			if _, err := strconv.ParseInt(string(number), 10, 64); err != nil {
				return ErrInvalid
			}
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || len(seen) != len(fields) {
		return ErrInvalid
	}
	return nil
}

func checkArray(d *json.Decoder, fields map[string]fieldKind, limit int) error {
	if token, err := d.Token(); err != nil || token != json.Delim('[') {
		return ErrInvalid
	}
	for count := 0; d.More(); count++ {
		if count >= limit {
			return ErrLimit
		}
		if err := checkObject(d, fields); err != nil {
			return err
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim(']') {
		return ErrInvalid
	}
	return nil
}

// encoding/json replaces unpaired escaped surrogates. Reject them before
// decoding so a malformed quote cannot silently become different evidence.
func checkUnicodeEscapes(data []byte) error {
	hex4 := func(at int) (uint64, error) {
		if at+4 > len(data) {
			return 0, ErrInvalid
		}
		return strconv.ParseUint(string(data[at:at+4]), 16, 16)
	}
	for i := 0; i < len(data); i++ {
		if data[i] != '"' {
			continue
		}
		for i++; i < len(data) && data[i] != '"'; i++ {
			if data[i] != '\\' {
				continue
			}
			i++
			if i >= len(data) {
				return ErrInvalid
			}
			if data[i] != 'u' {
				continue
			}
			value, err := hex4(i + 1)
			if err != nil {
				return ErrInvalid
			}
			i += 4
			if value >= 0xd800 && value <= 0xdbff {
				if i+2 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
					return ErrInvalid
				}
				low, err := hex4(i + 3)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return ErrInvalid
				}
				i += 6
			} else if value >= 0xdc00 && value <= 0xdfff {
				return ErrInvalid
			}
		}
		if i >= len(data) {
			return ErrInvalid
		}
	}
	return nil
}
