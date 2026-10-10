// Package diagnostics contains bounded, content-free scan diagnostics shared by
// connectors, ingestion, storage and read-only clients.
package diagnostics

import (
	"encoding/json"
	"errors"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	FormatVersion      = 1
	MaxExamples        = 200
	MaxSerializedBytes = 64 << 10
	MaxPathBytes       = 2048
)

type Reason struct {
	Code  string `json:"code"`
	Count int64  `json:"count"`
	Unit  string `json:"unit"`
}
type Example struct {
	Path   string `json:"path"`
	Unit   string `json:"unit"`
	Reason string `json:"reason"`
}
type Payload struct {
	ObservedFiles            int64     `json:"observed_files"`
	ObservedDirectories      int64     `json:"observed_directories"`
	ObservedFilesKnown       bool      `json:"observed_files_known"`
	ObservedDirectoriesKnown bool      `json:"observed_directories_known"`
	Reasons                  []Reason  `json:"reasons"`
	Examples                 []Example `json:"examples"`
	ExamplesOmitted          int64     `json:"examples_omitted"`
	RedactedSamples          int64     `json:"redacted_samples"`
	Coverage                 string    `json:"coverage"`
}
type Report struct {
	FormatVersion            int       `json:"format_version"`
	ID                       string    `json:"id"`
	SourceID                 string    `json:"source_id"`
	SourceKind               string    `json:"source_kind"`
	SnapshotID               string    `json:"snapshot_id,omitempty"`
	Operation                string    `json:"operation"`
	StartedAt                time.Time `json:"started_at"`
	FinishedAt               time.Time `json:"finished_at"`
	DurationMillis           int64     `json:"duration_millis"`
	Committed                bool      `json:"committed"`
	Complete                 bool      `json:"complete"`
	IndexedDocuments         int64     `json:"indexed_documents"`
	UpdatedDocuments         int64     `json:"updated_documents"`
	UnchangedDocuments       int64     `json:"unchanged_documents"`
	RemovedDocuments         int64     `json:"removed_documents"`
	ObservedFiles            int64     `json:"observed_files"`
	ObservedDirectories      int64     `json:"observed_directories"`
	ObservedFilesKnown       bool      `json:"observed_files_known"`
	ObservedDirectoriesKnown bool      `json:"observed_directories_known"`
	SkippedFiles             int64     `json:"skipped_files"`
	PrunedDirectories        int64     `json:"pruned_directories"`
	Reasons                  []Reason  `json:"reasons"`
	Examples                 []Example `json:"examples,omitempty"`
	ExamplesOmitted          int64     `json:"examples_omitted"`
	RedactedSamples          int64     `json:"redacted_samples"`
	Coverage                 string    `json:"coverage"`
}

// Builder keeps a fixed-size deterministic sample while aggregates remain exact.
type Builder struct {
	Reasons           map[string]Reason
	Examples          []Example
	Omitted, Redacted int64
	sampleBytes       int
}

func NewBuilder() *Builder {
	return &Builder{Reasons: make(map[string]Reason), Examples: make([]Example, 0, MaxExamples)}
}
func (b *Builder) Add(code, unit, rel string, redact bool) {
	if !validReason(code) || (unit != "file" && unit != "directory" && unit != "entry") {
		return
	}
	r := b.Reasons[code]
	r.Code = code
	r.Unit = unit
	if r.Count < int64(^uint64(0)>>1) {
		r.Count++
	}
	b.Reasons[code] = r
	if redact {
		b.Redacted++
		return
	}
	if !safeRelative(rel) {
		b.Omitted++
		return
	}
	if len(b.Examples) >= MaxExamples {
		b.Omitted++
		return
	}
	// Reserve room for report metadata and worst-case JSON escaping. Exact long
	// paths are omitted rather than truncated.
	if b.sampleBytes+len(rel)*6+96 > 48<<10 {
		b.Omitted++
		return
	}
	b.Examples = append(b.Examples, Example{Path: rel, Unit: unit, Reason: code})
	b.sampleBytes += len(rel)*6 + 96
}
func (b *Builder) Finish() ([]Reason, []Example, int64, int64) {
	rs := make([]Reason, 0, len(b.Reasons))
	for _, r := range b.Reasons {
		rs = append(rs, r)
	}
	// Reason code count is finite; stable ordering also stabilizes JSON.
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j].Code < rs[j-1].Code; j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
	return rs, append([]Example(nil), b.Examples...), b.Omitted, b.Redacted
}
func safeRelative(s string) bool {
	if len(s) == 0 || len(s) > MaxPathBytes || !utf8.ValidString(s) || strings.Contains(s, "\\") || path.IsAbs(s) || path.Clean(s) != s || s == "." {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validReason(s string) bool {
	switch s {
	case "excluded_path", "hidden_entry", "dependency_directory", "sensitive_name", "symlink", "special_entry", "office_lock_file", "unsupported_format", "format_disabled", "input_too_large", "binary_or_non_utf8", "extraction_limit", "no_extractable_text", "unsupported_docx", "git_lfs_pointer":
		return true
	}
	return false
}
func (r Report) Validate() error {
	if r.FormatVersion != FormatVersion || r.ID == "" || r.SourceID == "" || !r.Committed || !r.Complete || r.IndexedDocuments != r.UpdatedDocuments+r.UnchangedDocuments || r.SkippedFiles < 0 || r.PrunedDirectories < 0 || len(r.Examples) > MaxExamples {
		return errors.New("invalid indexing report")
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(b) > MaxSerializedBytes {
		return errors.New("indexing report exceeds serialized size limit")
	}
	return nil
}
