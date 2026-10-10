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
	Overflow                 bool      `json:"overflow"`
	ProcessedDocuments       int64     `json:"processed_documents"`
	ObservedEntries          int64     `json:"observed_entries"`
	ObservedEntriesKnown     bool      `json:"observed_entries_known"`
	ObservedFiles            int64     `json:"observed_files"`
	ObservedDirectories      int64     `json:"observed_directories"`
	ObservedFilesKnown       bool      `json:"observed_files_known"`
	ObservedDirectoriesKnown bool      `json:"observed_directories_known"`
	Reasons                  []Reason  `json:"reasons"`
	Examples                 []Example `json:"examples"`
	ExamplesOmitted          int64     `json:"examples_omitted"`
	RedactedSamples          int64     `json:"redacted_samples"`
	Coverage                 string    `json:"coverage"`
	FailurePath              string    `json:"failure_path,omitempty"`
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
	ObservedEntries          int64     `json:"observed_entries"`
	ObservedEntriesKnown     bool      `json:"observed_entries_known"`
	ObservedDirectories      int64     `json:"observed_directories"`
	ObservedFilesKnown       bool      `json:"observed_files_known"`
	ObservedDirectoriesKnown bool      `json:"observed_directories_known"`
	SkippedFiles             int64     `json:"skipped_files"`
	SkippedEntries           int64     `json:"skipped_entries"`
	PrunedDirectories        int64     `json:"pruned_directories"`
	Reasons                  []Reason  `json:"reasons"`
	Examples                 []Example `json:"examples,omitempty"`
	ExamplesOmitted          int64     `json:"examples_omitted"`
	RedactedSamples          int64     `json:"redacted_samples"`
	Coverage                 string    `json:"coverage"`
}

// Attempt is an ephemeral, incomplete summary. It is never persisted as the
// source's committed report and therefore does not assert published documents.
type Attempt struct {
	AttemptID                string    `json:"attempt_id"`
	SourceID                 string    `json:"source_id"`
	SourceKind               string    `json:"source_kind"`
	Operation                string    `json:"operation"`
	StartedAt                time.Time `json:"started_at"`
	FinishedAt               time.Time `json:"finished_at"`
	DurationMillis           int64     `json:"duration_millis"`
	Committed                bool      `json:"committed"`
	Complete                 bool      `json:"complete"`
	ProcessedDocuments       int64     `json:"processed_documents"`
	UpdatedDocuments         int64     `json:"updated_documents"`
	UnchangedDocuments       int64     `json:"unchanged_documents"`
	ObservedFiles            int64     `json:"observed_files"`
	ObservedEntries          int64     `json:"observed_entries"`
	ObservedEntriesKnown     bool      `json:"observed_entries_known"`
	ObservedFilesKnown       bool      `json:"observed_files_known"`
	ObservedDirectories      int64     `json:"observed_directories"`
	ObservedDirectoriesKnown bool      `json:"observed_directories_known"`
	SkippedFiles             int64     `json:"skipped_files"`
	SkippedEntries           int64     `json:"skipped_entries"`
	PrunedDirectories        int64     `json:"pruned_directories"`
	Reasons                  []Reason  `json:"reasons,omitempty"`
	Examples                 []Example `json:"examples,omitempty"`
	ExamplesOmitted          int64     `json:"examples_omitted"`
	RedactedSamples          int64     `json:"redacted_samples"`
	Coverage                 string    `json:"coverage"`
	FailureCode              string    `json:"failure_code"`
	FailurePath              string    `json:"failure_path,omitempty"`
	Overflow                 bool      `json:"overflow"`
}

// Builder keeps a fixed-size deterministic sample while aggregates remain exact.
type Builder struct {
	Reasons           map[string]Reason
	Examples          []Example
	Omitted, Redacted int64
	sampleBytes       int
	Overflow          bool
}

func NewBuilder() *Builder {
	return &Builder{Reasons: make(map[string]Reason), Examples: make([]Example, 0, MaxExamples)}
}
func (b *Builder) Add(code, unit, rel string, redact bool) {
	if !validReason(code) || (unit != "file" && unit != "directory" && unit != "entry") {
		return
	}
	key := code + "\x00" + unit
	r := b.Reasons[key]
	r.Code = code
	r.Unit = unit
	if r.Count == int64(^uint64(0)>>1) {
		b.Overflow = true
	} else {
		r.Count++
	}
	b.Reasons[key] = r
	if redact {
		if !Increment(&b.Redacted) {
			b.Overflow = true
		}
		return
	}
	if !safeRelative(rel) {
		if !Increment(&b.Omitted) {
			b.Overflow = true
		}
		return
	}
	if len(b.Examples) >= MaxExamples {
		if !Increment(&b.Omitted) {
			b.Overflow = true
		}
		return
	}
	// Reserve room for report metadata and worst-case JSON escaping. Exact long
	// paths are omitted rather than truncated.
	if b.sampleBytes+len(rel)*6+96 > 48<<10 {
		if !Increment(&b.Omitted) {
			b.Overflow = true
		}
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
		for j := i; j > 0 && (rs[j].Code < rs[j-1].Code || (rs[j].Code == rs[j-1].Code && rs[j].Unit < rs[j-1].Unit)); j-- {
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
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return false
		}
	}
	return true
}

func SafeRelativePath(s string) bool { return safeRelative(s) }

// Increment increments a nonnegative counter unless it has reached MaxInt64.
func Increment(value *int64) bool {
	if *value < 0 || *value == int64(^uint64(0)>>1) {
		return false
	}
	*value++
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
	if r.FormatVersion != FormatVersion || r.ID == "" || r.SourceID == "" || r.Operation == "" || r.Coverage == "" || !r.Committed || !r.Complete || r.IndexedDocuments != r.UpdatedDocuments+r.UnchangedDocuments || r.IndexedDocuments < 0 || r.UpdatedDocuments < 0 || r.UnchangedDocuments < 0 || r.RemovedDocuments < 0 || r.ObservedFiles < 0 || r.ObservedEntries < 0 || r.ObservedDirectories < 0 || r.SkippedFiles < 0 || r.SkippedEntries < 0 || r.PrunedDirectories < 0 || r.ExamplesOmitted < 0 || r.RedactedSamples < 0 || r.DurationMillis < 0 || len(r.Examples) > MaxExamples || (!r.ObservedFilesKnown && r.ObservedFiles != 0) || (!r.ObservedEntriesKnown && r.ObservedEntries != 0) || (!r.ObservedDirectoriesKnown && r.ObservedDirectories != 0) {
		return errors.New("invalid indexing report")
	}
	for _, reason := range r.Reasons {
		if !validReason(reason.Code) || reason.Count < 0 || (reason.Unit != "file" && reason.Unit != "directory" && reason.Unit != "entry") {
			return errors.New("invalid indexing report reason")
		}
	}
	for _, example := range r.Examples {
		if !safeRelative(example.Path) || !validReason(example.Reason) || (example.Unit != "file" && example.Unit != "directory" && example.Unit != "entry") {
			return errors.New("invalid indexing report example")
		}
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
