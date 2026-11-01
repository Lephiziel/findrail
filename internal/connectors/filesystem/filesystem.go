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
	"github.com/Lephiziel/findrail/internal/ignorepolicy"
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

type ignoreSnapshot struct {
	policy      *ignorepolicy.Policy
	info        os.FileInfo
	fingerprint string
}

func (p *ignoreSnapshot) matcher() *ignorepolicy.Policy {
	if p == nil {
		return nil
	}
	return p.policy
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
	policy, policyErr := loadIgnore(ctx, root)
	if policyErr != nil {
		return report, emptyDiagnostics, policyErr
	}
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
			if reason := builtinExclusion(name, true, false, true, true, true); reason != "" {
				report.Skipped++
				d.Add(reason, "directory", filepath.ToSlash(strings.TrimPrefix(path, c.source.Root+string(filepath.Separator))), sensitive(name))
				return filepath.SkipDir
			}
			decision := policyDecision(ctx, policy.matcher(), c.source.Root, path, true)
			if decision.Reason == "canceled" {
				return cancellationError(ctx)
			}
			if decision.Reason == "matching_limit" || decision.Reason == "invalid_path" {
				code := "matching_limit"
				if decision.Reason == "invalid_path" {
					code = "invalid_relative_path"
				}
				return &ignorepolicy.Error{Code: code}
			}
			if decision.Excluded {
				report.Skipped++
				relative := filepath.ToSlash(strings.TrimPrefix(path, c.source.Root+string(filepath.Separator)))
				d.Add("excluded_path", "directory", relative, sensitiveRelativePath(relative))
				return filepath.SkipDir
			}
			return nil
		}
		isPDF := strings.EqualFold(filepath.Ext(name), ".pdf") && c.maxPDFBytes > 0
		docxFile := strings.EqualFold(filepath.Ext(name), ".docx")
		isDOCX := docxFile && c.maxDOCXBytes > 0
		hardReason := builtinExclusion(name, false, entry.Type()&os.ModeSymlink != 0, entry.Type().IsRegular(), c.maxPDFBytes > 0, c.maxDOCXBytes > 0)
		if hardReason != "" {
			report.Skipped++
			unit := "file"
			if !entry.Type().IsRegular() {
				unit = "entry"
			}
			d.Add(hardReason, unit, filepath.ToSlash(strings.TrimPrefix(path, c.source.Root+string(filepath.Separator))), sensitive(name))
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
		decision := policyDecision(ctx, policy.matcher(), c.source.Root, path, false)
		if decision.Reason == "canceled" {
			return cancellationError(ctx)
		}
		if decision.Reason == "matching_limit" || decision.Reason == "invalid_path" {
			code := "matching_limit"
			if decision.Reason == "invalid_path" {
				code = "invalid_relative_path"
			}
			return &ignorepolicy.Error{Code: code}
		}
		if decision.Excluded {
			report.Skipped++
			relative := filepath.ToSlash(strings.TrimPrefix(path, c.source.Root+string(filepath.Separator)))
			d.Add("excluded_path", "file", relative, sensitiveRelativePath(relative))
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
	if err == nil {
		err = verifyIgnore(ctx, root, policy)
	}
	rs, examples, omitted, redacted := d.Finish()
	// The filesystem legacy counter includes pruned directories; the detailed
	// aggregate keeps file and directory decisions in separate units.
	payload := diagnostics.Payload{Overflow: d.Overflow, ObservedFiles: observedFiles, ObservedEntries: observedEntries, ObservedEntriesKnown: true, ObservedDirectories: observedDirectories, ObservedFilesKnown: true, ObservedDirectoriesKnown: true, Reasons: rs, Examples: examples, ExamplesOmitted: omitted, RedactedSamples: redacted, Coverage: "complete_filesystem", FailurePath: failurePath}
	if d.Overflow && err == nil {
		err = errors.New("diagnostic counter overflow")
	}
	return report, payload, err
}

func policyDecision(ctx context.Context, p *ignorepolicy.Policy, root, path string, dir bool) ignorepolicy.Decision {
	if ctx.Err() != nil {
		return ignorepolicy.Decision{Reason: "canceled"}
	}
	if p == nil {
		return ignorepolicy.Decision{Reason: "eligible"}
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ignorepolicy.Decision{}
	}
	return p.Decide(ctx, filepath.ToSlash(rel), dir)
}

func loadIgnore(ctx context.Context, root *os.Root) (*ignoreSnapshot, error) {
	info, err := root.Lstat(".findrailignore")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &ignorepolicy.Error{Code: "unreadable"}
	}
	if !info.Mode().IsRegular() || info.Size() > ignorepolicy.MaxInput {
		return nil, &ignorepolicy.Error{Code: "invalid_file_type_or_size"}
	}
	f, err := openIgnoreFile(root)
	if err != nil {
		return nil, &ignorepolicy.Error{Code: "unreadable"}
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		f.Close()
		return nil, &ignorepolicy.Error{Code: "changed"}
	}
	data, err := io.ReadAll(io.LimitReader(f, ignorepolicy.MaxInput+1))
	final, se := f.Stat()
	ce := f.Close()
	if err != nil || se != nil || ce != nil || int64(len(data)) != info.Size() || !final.ModTime().Equal(opened.ModTime()) {
		return nil, &ignorepolicy.Error{Code: "changed"}
	}
	p, err := ignorepolicy.Parse(ctx, data)
	if err != nil {
		return nil, err
	}
	return &ignoreSnapshot{policy: p, info: opened, fingerprint: p.Fingerprint()}, nil
}
func verifyIgnore(ctx context.Context, root *os.Root, p *ignoreSnapshot) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	current, err := root.Lstat(".findrailignore")
	if p == nil && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || p == nil || !current.Mode().IsRegular() || !os.SameFile(p.info, current) || p.info.Size() != current.Size() || !p.info.ModTime().Equal(current.ModTime()) || current.Size() > ignorepolicy.MaxInput {
		return &ignorepolicy.Error{Code: "changed"}
	}
	f, err := openIgnoreFile(root)
	if err != nil {
		return &ignorepolicy.Error{Code: "changed"}
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(current, opened) {
		f.Close()
		return &ignorepolicy.Error{Code: "changed"}
	}
	data, readErr := io.ReadAll(io.LimitReader(f, ignorepolicy.MaxInput+1))
	final, statErr := f.Stat()
	closeErr := f.Close()
	if readErr != nil || statErr != nil || closeErr != nil || int64(len(data)) != current.Size() || final.Size() != opened.Size() || final.Size() != current.Size() || !final.ModTime().Equal(opened.ModTime()) || !os.SameFile(opened, final) || ignorepolicy.Fingerprint(data) != p.fingerprint {
		return &ignorepolicy.Error{Code: "changed"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// Explain evaluates path policy and filesystem metadata only; it never reads document content.
func Explain(ctx context.Context, rootPath, relative string, showRule bool) (ignorepolicy.Decision, error) {
	decisions, err := ExplainMany(ctx, rootPath, []string{relative}, showRule)
	if err != nil {
		return ignorepolicy.Decision{}, err
	}
	return decisions[0], nil
}

// ExplainMany loads a single immutable policy snapshot and evaluates all paths
// against it, publishing no decisions if the policy changes during the request.
func ExplainMany(ctx context.Context, rootPath string, relatives []string, showRule bool, excluded ...string) ([]ignorepolicy.Decision, error) {
	if len(relatives) == 0 || len(relatives) > 100 {
		return nil, errors.New("invalid path count")
	}
	for _, relative := range relatives {
		if err := ignorepolicy.ValidatePath(relative); err != nil {
			return nil, err
		}
	}
	abs, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, errors.New("invalid root")
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("invalid root")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, errors.New("invalid root")
	}
	abs = resolved
	r, err := os.OpenRoot(abs)
	if err != nil {
		return nil, errors.New("cannot open root")
	}
	defer r.Close()
	p, err := loadIgnore(ctx, r)
	if err != nil {
		return nil, err
	}
	decisions := make([]ignorepolicy.Decision, 0, len(relatives))
	for _, relative := range relatives {
		d, e := explainWithPolicy(ctx, r, abs, p, relative, showRule, excluded)
		if e != nil {
			return nil, e
		}
		decisions = append(decisions, d)
	}
	if err := verifyIgnore(ctx, r, p); err != nil {
		return nil, err
	}
	return decisions, nil
}

func explainWithPolicy(ctx context.Context, r *os.Root, rootPath string, p *ignoreSnapshot, relative string, showRule bool, excluded []string) (ignorepolicy.Decision, error) {
	parts := strings.Split(relative, "/")
	for i := range parts {
		if err := ctx.Err(); err != nil {
			return ignorepolicy.Decision{}, err
		}
		partial := strings.Join(parts[:i+1], "/")
		if insideExcludedPath(filepath.Join(rootPath, filepath.FromSlash(partial)), excluded) {
			reason := ignorepolicy.Decision{Reason: "excluded_builtin:index_directory", Fingerprint: policyFingerprint(p), Ancestor: partial}
			if partial == relative {
				reason.Ancestor = ""
			}
			return reason, nil
		}
		st, e := r.Lstat(filepath.FromSlash(partial))
		if e != nil {
			if errors.Is(e, os.ErrNotExist) {
				return ignorepolicy.Decision{Reason: "not_found", Fingerprint: policyFingerprint(p)}, nil
			}
			return ignorepolicy.Decision{}, errors.New("metadata unavailable")
		}
		isDir := st.IsDir()
		name := parts[i]
		reason := builtinExclusion(name, isDir, st.Mode()&os.ModeSymlink != 0, st.Mode().IsRegular(), true, true)
		if reason != "" {
			return ignorepolicy.Decision{Reason: "excluded_builtin:" + reason, Fingerprint: policyFingerprint(p)}, nil
		}
		d := p.matcher().Decide(ctx, partial, isDir)
		if d.Reason == "canceled" {
			return ignorepolicy.Decision{}, cancellationError(ctx)
		}
		if d.Reason == "matching_limit" {
			return ignorepolicy.Decision{}, &ignorepolicy.Error{Code: "matching_limit"}
		}
		if d.Excluded {
			d.Reason = "blocked_by_pruned_ancestor"
			d.Ancestor = partial
			if partial == relative {
				d.Reason = "excluded_user_rule"
			}
			if !showRule {
				d.Rule = ""
			}
			return d, nil
		}
		if i < len(parts)-1 && !isDir {
			return ignorepolicy.Decision{Reason: "not_found", Fingerprint: policyFingerprint(p)}, nil
		}
	}
	return ignorepolicy.Decision{Reason: "eligible_by_path_policy", Fingerprint: policyFingerprint(p)}, nil
}

func policyFingerprint(p *ignoreSnapshot) string { return p.matcher().Fingerprint() }
func insideExcludedPath(path string, excluded []string) bool {
	for _, base := range excluded {
		base := canonicalPath(base)
		if pathWithin(base, path) {
			return true
		}
	}
	return false
}
func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	candidate := abs
	suffix := []string{}
	for {
		if resolved, e := filepath.EvalSymlinks(candidate); e == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return abs
		}
		suffix = append(suffix, filepath.Base(candidate))
		candidate = parent
	}
}
func pathWithin(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func cancellationError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return context.Canceled
}

func (c *Connector) isExcluded(path string) bool {
	for _, exclude := range c.excluded {
		if pathWithin(exclude, path) {
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

// builtinExclusion is shared by complete scans and metadata explanations so a
// user negation cannot override a hard path gate in either view.
func builtinExclusion(name string, directory, symlink, regular, pdfEnabled, docxEnabled bool) string {
	if directory {
		if strings.HasPrefix(name, ".") {
			return "hidden_entry"
		}
		if ignoredDirs[name] {
			return "dependency_directory"
		}
		return ""
	}
	ext := filepath.Ext(name)
	if strings.HasPrefix(name, "~$") && strings.EqualFold(ext, ".docx") {
		return "office_lock_file"
	}
	if symlink {
		return "symlink"
	}
	if strings.HasPrefix(name, ".") {
		return "hidden_entry"
	}
	if sensitive(name) {
		return "sensitive_name"
	}
	if strings.EqualFold(ext, ".pdf") {
		if !pdfEnabled {
			return "format_disabled"
		}
	} else if strings.EqualFold(ext, ".docx") {
		if !docxEnabled {
			return "format_disabled"
		}
	} else if !supported(name) {
		return "unsupported_format"
	}
	if !regular {
		return "special_entry"
	}
	return ""
}

func sensitive(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "credential") || strings.Contains(lower, "secret") || strings.Contains(lower, "private_key")
}

func sensitiveRelativePath(relative string) bool {
	for _, part := range strings.Split(relative, "/") {
		if sensitive(part) {
			return true
		}
	}
	return false
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
	rel, err := filepath.Rel(c.source.Root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	if rel == "." {
		return true
	}
	parts := strings.Split(rel, string(filepath.Separator))
	// Editors often replace a root control file through a hidden temporary
	// sibling; treat root-hidden hints as relevant while nested dotfiles remain inert.
	if len(parts) == 1 && strings.HasPrefix(parts[0], ".") {
		return true
	}
	if c.isExcluded(path) {
		return false
	}
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
	root, err := os.OpenRoot(c.source.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	policy, err := loadIgnore(ctx, root)
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(c.source.Root, func(path string, e fs.DirEntry, walkErr error) error {
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
		if path != c.source.Root {
			decision := policyDecision(ctx, policy.matcher(), c.source.Root, path, true)
			if decision.Reason == "matching_limit" || decision.Reason == "invalid_path" {
				code := "matching_limit"
				if decision.Reason == "invalid_path" {
					code = "invalid_relative_path"
				}
				return &ignorepolicy.Error{Code: code}
			}
			if decision.Excluded {
				return filepath.SkipDir
			}
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
	if err == nil {
		err = verifyIgnore(ctx, root, policy)
	}
	return dirs, err
}
