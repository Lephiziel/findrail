package filesystem

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Lephiziel/findrail/internal/diagnostics"
	docxextract "github.com/Lephiziel/findrail/internal/extract/docx"
	"github.com/Lephiziel/findrail/internal/extract/text"
	"github.com/Lephiziel/findrail/pkg/connector"
)

const DefaultMaxBytes int64 = 1 << 20

var extensions = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".rst": true,
	".go": true, ".py": true, ".js": true, ".jsx": true, ".ts": true,
	".tsx": true, ".rs": true, ".java": true, ".c": true, ".h": true,
	".cpp": true, ".hpp": true, ".rb": true, ".php": true, ".sh": true,
	".sql": true, ".html": true, ".css": true, ".json": true,
	".yaml": true, ".yml": true, ".toml": true,
}

var ignoredDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true,
	"__pycache__": true, "target": true,
}

type Connector struct {
	source       connector.Source
	maxBytes     int64
	maxPDFBytes  int64
	maxDOCXBytes int64
	extractPDF   func(context.Context, io.Reader, int64) ([]connector.Page, error)
	excluded     []string
}

func New(root string, maxBytes int64, excluded ...string) (*Connector, error) {
	return NewWithOptions(root, Options{MaxTextBytes: maxBytes}, excluded...)
}

type Options struct {
	MaxTextBytes         int64
	MaxPDFBytes          int64
	ExtractPDF           func(context.Context, io.Reader, int64) ([]connector.Page, error)
	MaxDOCXBytes         int64
	RegistrationToken    string
	RegistrationRevision int64
}

func NewWithOptions(root string, options Options, excluded ...string) (*Connector, error) {
	maxBytes := options.MaxTextBytes
	if maxBytes < 1 || maxBytes > 32<<20 {
		return nil, fmt.Errorf("max bytes must be between 1 and 33554432")
	}
	if options.MaxPDFBytes < 0 || options.MaxPDFBytes > 32<<20 || (options.MaxPDFBytes > 0 && options.ExtractPDF == nil) {
		return nil, fmt.Errorf("PDF limit must be 0–33554432 with an extractor when enabled")
	}
	if options.MaxDOCXBytes < 0 || options.MaxDOCXBytes > 16<<20 {
		return nil, fmt.Errorf("DOCX limit must be 0–16777216")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve source: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("source must be a directory")
	}
	canonicalExclusions := make([]string, 0, len(excluded))
	for _, path := range excluded {
		excludedAbs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if resolved, err := filepath.EvalSymlinks(excludedAbs); err == nil {
			excludedAbs = resolved
		}
		canonicalExclusions = append(canonicalExclusions, excludedAbs)
	}
	id := fmt.Sprintf("fs_%x", sha256.Sum256([]byte(abs)))
	return &Connector{source: connector.Source{ID: id, Kind: "filesystem", Name: filepath.Base(abs), Root: abs, MaxTextBytes: maxBytes, MaxPDFBytes: options.MaxPDFBytes, MaxDOCXBytes: options.MaxDOCXBytes, RegistrationToken: options.RegistrationToken, RegistrationRevision: options.RegistrationRevision}, maxBytes: maxBytes, maxPDFBytes: options.MaxPDFBytes, maxDOCXBytes: options.MaxDOCXBytes, extractPDF: options.ExtractPDF, excluded: canonicalExclusions}, nil
}

func (c *Connector) Source() connector.Source { return c.source }

func (c *Connector) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	report, _, err := c.ScanWithDiagnostics(ctx, emit)
	return report, err
}

// ScanWithDiagnostics reports bounded private diagnostics without expanding
// the experimental public connector report contract.
func (c *Connector) ScanWithDiagnostics(ctx context.Context, emit func(connector.Document) error) (connector.Report, diagnostics.Payload, error) {
	var report connector.Report
	emptyDiagnostics := diagnostics.Payload{Coverage: "complete_filesystem"}
	d := diagnostics.NewBuilder()
	var observedFiles, observedDirectories, observedEntries int64
	var failurePath string
	if err := c.validateRoot(); err != nil {
		return report, emptyDiagnostics, err
	}
	root, err := os.OpenRoot(c.source.Root)
	if err != nil {
		return report, emptyDiagnostics, err
	}
	defer root.Close()
	err = filepath.WalkDir(c.source.Root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if path != c.source.Root {
				if rel, e := filepath.Rel(c.source.Root, path); e == nil && diagnostics.SafeRelativePath(filepath.ToSlash(rel)) {
					failurePath = filepath.ToSlash(rel)
				}
			}
			return fmt.Errorf("enumerate source: %w", walkErr)
		}
		if path == c.source.Root {
			return nil
		}
		if !diagnostics.Increment(&observedEntries) {
			return errors.New("diagnostic counter overflow")
		}
		if entry.IsDir() {
			if !diagnostics.Increment(&observedDirectories) {
				return errors.New("diagnostic counter overflow")
			}
		} else if entry.Type().IsRegular() {
			if !diagnostics.Increment(&observedFiles) {
				return errors.New("diagnostic counter overflow")
			}
		}
		if c.isExcluded(path) {
			report.Skipped++
			reason, unit := "excluded_path", "entry"
			if entry.IsDir() {
				unit = "directory"
			} else if entry.Type().IsRegular() {
				unit = "file"
			}
			d.Add(reason, unit, filepath.ToSlash(strings.TrimPrefix(path, c.source.Root+string(filepath.Separator))), sensitive(entry.Name()))
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if strings.HasPrefix(name, ".") || ignoredDirs[name] {
				report.Skipped++
				reason := "hidden_entry"
				if ignoredDirs[name] {
					reason = "dependency_directory"
				}
				d.Add(reason, "directory", filepath.ToSlash(strings.TrimPrefix(path, c.source.Root+string(filepath.Separator))), sensitive(name))
				return filepath.SkipDir
			}
			return nil
		}
		isPDF := strings.EqualFold(filepath.Ext(name), ".pdf") && c.maxPDFBytes > 0
		docxFile := strings.EqualFold(filepath.Ext(name), ".docx")
		isDOCX := docxFile && c.maxDOCXBytes > 0
		if strings.HasPrefix(name, "~$") && strings.EqualFold(filepath.Ext(name), ".docx") {
			report.Skipped++
			unit := "file"
			if !entry.Type().IsRegular() {
				unit = "entry"
			}
			d.Add("office_lock_file", unit, filepath.ToSlash(strings.TrimPrefix(path, c.source.Root+string(filepath.Separator))), sensitive(name))
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || strings.HasPrefix(name, ".") || sensitive(name) || (!isPDF && !isDOCX && !supported(name)) {
			report.Skipped++
			unit := "file"
			if !entry.Type().IsRegular() {
				unit = "entry"
			}
			reason, redact := "unsupported_format", sensitive(name)
			switch {
			case entry.Type()&os.ModeSymlink != 0:
				reason = "symlink"
				unit = "entry"
			case strings.HasPrefix(name, "."):
				reason = "hidden_entry"
			case sensitive(name):
				reason = "sensitive_name"
				redact = true
			case (docxFile && c.maxDOCXBytes == 0) || (strings.EqualFold(filepath.Ext(name), ".pdf") && c.maxPDFBytes == 0):
				reason = "format_disabled"
			}
			d.Add(reason, unit, filepath.ToSlash(strings.TrimPrefix(path, c.source.Root+string(filepath.Separator))), redact)
			if docxFile {
				report.SkippedDOCX++
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			if rel, e := filepath.Rel(c.source.Root, path); e == nil && diagnostics.SafeRelativePath(filepath.ToSlash(rel)) {
				failurePath = filepath.ToSlash(rel)
			}
			return err
		}
		limit := c.maxBytes
		if isPDF {
			limit = c.maxPDFBytes
		}
		if isDOCX {
			limit = c.maxDOCXBytes
		}
		if !info.Mode().IsRegular() || info.Size() > limit {
			report.Skipped++
			reason := "input_too_large"
			unit := "file"
			if !info.Mode().IsRegular() {
				reason = "special_entry"
				unit = "entry"
			}
			d.Add(reason, unit, filepath.ToSlash(strings.TrimPrefix(path, c.source.Root+string(filepath.Separator))), false)
			if isPDF {
				report.SkippedPDF++
			}
			return nil
		}
		rel, err := filepath.Rel(c.source.Root, path)
		if err != nil {
			return err
		}
		file, err := root.Open(rel)
		if err != nil {
			if diagnostics.SafeRelativePath(filepath.ToSlash(rel)) {
				failurePath = filepath.ToSlash(rel)
			}
			return fmt.Errorf("open document %q: %w", rel, err)
		}
		openedInfo, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			if diagnostics.SafeRelativePath(filepath.ToSlash(rel)) {
				failurePath = filepath.ToSlash(rel)
			}
			return statErr
		}
		if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
			file.Close()
			if diagnostics.SafeRelativePath(filepath.ToSlash(rel)) {
				failurePath = filepath.ToSlash(rel)
			}
			return fmt.Errorf("document %q changed during scan; retry", rel)
		}
		var body string
		var pages []connector.Page
		var readErr error
		if isPDF {
			pages, readErr = c.extractPDF(ctx, file, info.Size())
			var joined strings.Builder
			for _, p := range pages {
				joined.WriteString(p.Text)
				joined.WriteByte('\n')
			}
			body = joined.String()
		} else if isDOCX {
			body, readErr = docxextract.Extract(ctx, file, info.Size(), c.maxDOCXBytes)
		} else {
			body, readErr = text.Read(file, c.maxBytes)
		}
		finalInfo, finalStatErr := file.Stat()
		closeErr := file.Close()
		if (closeErr != nil || finalStatErr != nil) && diagnostics.SafeRelativePath(filepath.ToSlash(rel)) {
			failurePath = filepath.ToSlash(rel)
		}
		if errors.Is(readErr, text.ErrUnsupported) || errors.Is(readErr, text.ErrTooLarge) || errors.Is(readErr, docxextract.ErrSkip) || errors.Is(readErr, docxextract.ErrLimit) || errors.Is(readErr, docxextract.ErrNoText) {
			report.Skipped++
			reason := "extraction_limit"
			switch {
			case errors.Is(readErr, text.ErrUnsupported):
				reason = "binary_or_non_utf8"
				if isPDF {
					reason = "no_extractable_text"
				}
			case errors.Is(readErr, text.ErrTooLarge):
				reason = "input_too_large"
				if isPDF {
					reason = "extraction_limit"
				}
			case errors.Is(readErr, docxextract.ErrNoText):
				reason = "no_extractable_text"
			case errors.Is(readErr, docxextract.ErrSkip):
				reason = "unsupported_docx"
			}
			d.Add(reason, "file", filepath.ToSlash(strings.TrimPrefix(path, c.source.Root+string(filepath.Separator))), false)
			if isPDF {
				report.SkippedPDF++
			}
			if isDOCX {
				report.SkippedDOCX++
			}
			return nil
		}
		if readErr != nil {
			if diagnostics.SafeRelativePath(filepath.ToSlash(rel)) {
				failurePath = filepath.ToSlash(rel)
			}
			return fmt.Errorf("extract document %q: %w", rel, readErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if finalStatErr != nil {
			return finalStatErr
		}
		if finalInfo.Size() != openedInfo.Size() || !finalInfo.ModTime().Equal(openedInfo.ModTime()) {
			if diagnostics.SafeRelativePath(filepath.ToSlash(rel)) {
				failurePath = filepath.ToSlash(rel)
			}
			return fmt.Errorf("document %q changed during extraction; retry", rel)
		}
		uriPath := filepath.ToSlash(path)
		if !strings.HasPrefix(uriPath, "/") {
			uriPath = "/" + uriPath
		}
		doc := connector.Document{
			ID:       fmt.Sprintf("doc_%x", sha256.Sum256([]byte(c.source.ID+"\x00"+filepath.ToSlash(rel)))),
			SourceID: c.source.ID, Title: name,
			URI: (&url.URL{Scheme: "file", Path: uriPath}).String(), Path: filepath.ToSlash(rel),
			Content: body, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(body))),
			SizeBytes: openedInfo.Size(), ModifiedAt: openedInfo.ModTime().UTC(),
		}
		doc.MediaType = "text/plain"
		if isPDF {
			doc.MediaType = "application/pdf"
			doc.Pages = pages
			// Include page boundaries and extraction version in the snapshot identity.
			h := sha256.New()
			io.WriteString(h, "pdf-v1\x00")
			for _, p := range pages {
				fmt.Fprintf(h, "%d:%d:", p.Number, len(p.Text))
				io.WriteString(h, p.Text)
			}
			doc.Hash = fmt.Sprintf("%x", h.Sum(nil))
		}
		if isDOCX {
			doc.MediaType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
			h := sha256.New()
			io.WriteString(h, "docx-body-v1\x00")
			io.WriteString(h, body)
			doc.Hash = fmt.Sprintf("%x", h.Sum(nil))
		}
		if err := emit(doc); err != nil {
			return err
		}
		report.Seen++
		return nil
	})
	rs, examples, omitted, redacted := d.Finish()
	// The filesystem legacy counter includes pruned directories; the detailed
	// aggregate keeps file and directory decisions in separate units.
	payload := diagnostics.Payload{Overflow: d.Overflow, ObservedFiles: observedFiles, ObservedEntries: observedEntries, ObservedEntriesKnown: true, ObservedDirectories: observedDirectories, ObservedFilesKnown: true, ObservedDirectoriesKnown: true, Reasons: rs, Examples: examples, ExamplesOmitted: omitted, RedactedSamples: redacted, Coverage: "complete_filesystem", FailurePath: failurePath}
	if d.Overflow && err == nil {
		err = errors.New("diagnostic counter overflow")
	}
	return report, payload, err
}

func (c *Connector) isExcluded(path string) bool {
	for _, exclude := range c.excluded {
		rel, err := filepath.Rel(exclude, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func supported(name string) bool {
	if sensitive(name) {
		return false
	}
	return extensions[strings.ToLower(filepath.Ext(name))]
}

func sensitive(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "credential") || strings.Contains(lower, "secret") || strings.Contains(lower, "private_key")
}

func (c *Connector) validateRoot() error {
	info, err := os.Lstat(c.source.Root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("source root is no longer the selected directory")
	}
	resolved, err := filepath.EvalSymlinks(c.source.Root)
	if err != nil {
		return err
	}
	if resolved != c.source.Root {
		return fmt.Errorf("source root moved through a symlink")
	}
	return nil
}

// RelevantPath applies the same directory exclusions to native watch events.
func (c *Connector) RelevantPath(path string) bool {
	if c.isExcluded(path) {
		return false
	}
	rel, err := filepath.Rel(c.source.Root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	if rel == "." {
		return true
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for _, p := range parts {
		if strings.HasPrefix(p, ".") || ignoredDirs[p] {
			return false
		}
	}
	return !sensitive(parts[len(parts)-1])
}

// WatchDirectories excludes hidden, dependency and index directories; symlinks
// are never traversed. The caller falls back to polling if the budget is exceeded.
func (c *Connector) WatchDirectories(ctx context.Context) ([]string, error) {
	if err := c.validateRoot(); err != nil {
		return nil, err
	}
	dirs := []string{}
	err := filepath.WalkDir(c.source.Root, func(path string, e fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if !e.IsDir() {
			return nil
		}
		if path != c.source.Root && !c.RelevantPath(path) {
			return filepath.SkipDir
		}
		if c.isExcluded(path) {
			return filepath.SkipDir
		}
		dirs = append(dirs, path)
		if len(dirs) > 8192 {
			return fmt.Errorf("directory watch budget exceeded; periodic refresh remains enabled")
		}
		return nil
	})
	return dirs, err
}
