// Package github prepares bounded, immutable snapshots of public GitHub repositories.
// It performs network I/O only from Prepare; a Snapshot is an in-memory connector.
package github

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Lephiziel/findrail/internal/extract/text"
	"github.com/Lephiziel/findrail/pkg/connector"
)

const (
	DefaultMaxBytes   = int64(1 << 20)
	MaxFileBytes      = int64(8 << 20)
	MaxMetadataBytes  = int64(2 << 20)
	MaxArchiveBytes   = int64(32 << 20)
	MaxExpandedBytes  = int64(128 << 20)
	MaxInventoryBytes = int64(32 << 20)
	MaxEntries        = 50000
	MaxDocuments      = 5000
	MaxPathBytes      = 2048
	PolicyVersion     = 1
	APIVersion        = "2026-03-10"
)

var fullSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
var sourceIDPattern = regexp.MustCompile(`^gh_[0-9a-f]{64}$`)
var component = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,99})$`)

type Selection struct {
	Owner, Repo       string
	RefMode, RefValue string
	Path              string
	MaxBytes          int64
}

type Metadata struct {
	RepositoryID  int64     `json:"repository_id"`
	Owner         string    `json:"owner"`
	Repo          string    `json:"repo"`
	RepositoryURL string    `json:"repository_url"`
	RefMode       string    `json:"ref_mode"`
	RefValue      string    `json:"ref_value,omitempty"`
	SelectedPath  string    `json:"selected_path,omitempty"`
	MaxBytes      int64     `json:"max_bytes"`
	PolicyVersion int       `json:"policy_version"`
	SHA           string    `json:"sha"`
	CommitTime    time.Time `json:"commit_time"`
}

type SkipCounts struct {
	Unsupported int `json:"unsupported_format,omitempty"`
	Excluded    int `json:"excluded_path,omitempty"`
	Binary      int `json:"binary_or_non_utf8,omitempty"`
	TooLarge    int `json:"per_file_limit,omitempty"`
	Special     int `json:"symlink_or_special,omitempty"`
	LFS         int `json:"git_lfs_pointer,omitempty"`
}

type Snapshot struct {
	source connector.Source
	docs   []connector.Document
	meta   Metadata
	skips  SkipCounts
}

func (s *Snapshot) Source() connector.Source { return s.source }
func (s *Snapshot) Metadata() Metadata       { return s.meta }
func (s *Snapshot) SkipCounts() SkipCounts   { return s.skips }
func (s *Snapshot) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	r := connector.Report{Skipped: s.skips.Unsupported + s.skips.Excluded + s.skips.Binary + s.skips.TooLarge + s.skips.Special + s.skips.LFS}
	for _, d := range s.docs {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		if err := emit(d); err != nil {
			return r, err
		}
		r.Seen++
	}
	return r, nil
}

func ParseRepository(input string) (string, string, error) {
	raw := strings.TrimSpace(input)
	if strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "github.com") || u.Port() != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "", "", repoError()
		}
		raw = strings.TrimSuffix(u.EscapedPath(), "/")
		if strings.Contains(raw, "%") {
			return "", "", repoError()
		}
		raw = strings.TrimPrefix(raw, "/")
	} else if strings.Contains(raw, "://") || strings.Contains(raw, "@") || strings.HasPrefix(strings.ToLower(raw), "github.com/") {
		return "", "", repoError()
	}
	raw = strings.TrimSuffix(strings.TrimSuffix(raw, "/"), ".git")
	parts := strings.Split(raw, "/")
	if len(parts) != 2 || !component.MatchString(parts[0]) || !component.MatchString(parts[1]) || parts[0] == "." || parts[1] == "." {
		return "", "", repoError()
	}
	return parts[0], parts[1], nil
}

func repoError() error {
	return errors.New("repository must be OWNER/REPO or https://github.com/OWNER/REPO; use --ref and --path separately")
}

func NormalizePath(value string) (string, error) {
	value = strings.TrimSuffix(value, "/")
	if value == "" {
		return "", nil
	}
	if len(value) > 1024 || !utf8.ValidString(value) || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || regexp.MustCompile(`^[A-Za-z]:`).MatchString(value) {
		return "", errors.New("path must be a relative POSIX directory of at most 1024 UTF-8 bytes")
	}
	for _, p := range strings.Split(value, "/") {
		if p == "" || p == "." || p == ".." || hasControl(p) {
			return "", errors.New("path contains an unsafe segment")
		}
	}
	if excludedPath(value) {
		return "", errors.New("selected path is excluded by the indexing policy")
	}
	return value, nil
}

func ValidateRef(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 1024 || !utf8.ValidString(value) || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "//") || strings.Contains(value, "..") || hasControl(value) {
		return errors.New("ref must be a branch, tag, heads/NAME, tags/NAME, or full commit SHA of at most 1024 bytes")
	}
	return nil
}

func SelectionFrom(owner, repo, ref, selectedPath string, maxBytes int64) (Selection, error) {
	if err := ValidateRef(ref); err != nil {
		return Selection{}, err
	}
	p, err := NormalizePath(selectedPath)
	if err != nil {
		return Selection{}, err
	}
	if maxBytes < 1 || maxBytes > MaxFileBytes {
		return Selection{}, fmt.Errorf("max bytes must be between 1 and %d", MaxFileBytes)
	}
	mode := "default"
	if ref != "" {
		mode = "ref"
		if fullSHA.MatchString(ref) {
			mode = "commit"
			ref = strings.ToLower(ref)
		}
	}
	return Selection{Owner: owner, Repo: repo, RefMode: mode, RefValue: ref, Path: p, MaxBytes: maxBytes}, nil
}

func SourceID(repositoryID int64, selection Selection) string {
	h := sha256.New()
	fmt.Fprintf(h, "github-source-v1\x00%d\x00%s\x00%s\x00%s", repositoryID, selection.RefMode, selection.RefValue, selection.Path)
	return fmt.Sprintf("gh_%x", h.Sum(nil))
}

func ValidateSourceID(value string) error {
	if !sourceIDPattern.MatchString(value) {
		return errors.New("GitHub source ID must be gh_ followed by 64 lowercase hexadecimal characters")
	}
	return nil
}

func Permalink(owner, repo, sha, filename string) (string, error) {
	if !component.MatchString(owner) || !component.MatchString(repo) || !fullSHA.MatchString(sha) {
		return "", errors.New("invalid GitHub permalink identity")
	}
	parts := strings.Split(filename, "/")
	if len(parts) == 0 {
		return "", errors.New("empty GitHub path")
	}
	for i, p := range parts {
		if p == "" || p == "." || p == ".." || hasControl(p) {
			return "", errors.New("unsafe GitHub path")
		}
		parts[i] = url.PathEscape(p)
	}
	return "https://github.com/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/blob/" + strings.ToLower(sha) + "/" + strings.Join(parts, "/"), nil
}

type Client struct {
	HTTP    *http.Client
	apiBase string
}

func NewClient() *Client {
	c := &http.Client{Timeout: 0}
	c.CheckRedirect = secureRedirect
	return &Client{HTTP: c, apiBase: "https://api.github.com"}
}

func secureRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return errors.New("too many GitHub redirects")
	}
	u := req.URL
	if u.Scheme != "https" || u.User != nil || u.Port() != "" || (u.Hostname() != "api.github.com" && u.Hostname() != "codeload.github.com") {
		return errors.New("unsafe GitHub redirect rejected")
	}
	if len(via) > 0 && via[0].URL.Hostname() == "api.github.com" && u.Hostname() == "api.github.com" {
		return errors.New("repository API redirect rejected; select the canonical repository name")
	}
	if u.Hostname() == "codeload.github.com" {
		origin := strings.Split(strings.Trim(via[0].URL.EscapedPath(), "/"), "/")
		target := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
		if len(origin) != 5 || origin[0] != "repos" || origin[3] != "tarball" || len(target) != 4 ||
			!strings.EqualFold(origin[1], target[0]) || !strings.EqualFold(origin[2], target[1]) ||
			(target[2] != "legacy.tar.gz" && target[2] != "tar.gz") || !strings.EqualFold(origin[4], target[3]) {
			return errors.New("GitHub archive redirect does not match the selected repository and commit")
		}
	}
	return nil
}

func (c *Client) Prepare(ctx context.Context, sel Selection) (*Snapshot, error) {
	var repo struct {
		ID            int64  `json:"id"`
		Name          string `json:"name"`
		FullName      string `json:"full_name"`
		HTMLURL       string `json:"html_url"`
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
		Owner         struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := c.getJSON(ctx, "/repos/"+url.PathEscape(sel.Owner)+"/"+url.PathEscape(sel.Repo), &repo); err != nil {
		return nil, err
	}
	if repo.ID <= 0 || repo.Private || repo.Owner.Login == "" || repo.Name == "" || repo.DefaultBranch == "" {
		return nil, errors.New("GitHub repository metadata is invalid or repository is not public")
	}
	if !strings.EqualFold(repo.FullName, sel.Owner+"/"+sel.Repo) {
		return nil, errors.New("repository canonical name changed; select it again explicitly")
	}
	ref := sel.RefValue
	if sel.RefMode == "default" {
		ref = repo.DefaultBranch
	}
	var commit struct {
		SHA    string `json:"sha"`
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := c.getJSON(ctx, "/repos/"+url.PathEscape(repo.Owner.Login)+"/"+url.PathEscape(repo.Name)+"/commits/"+url.PathEscape(ref), &commit); err != nil {
		return nil, err
	}
	if !fullSHA.MatchString(commit.SHA) || commit.Commit.Committer.Date.IsZero() {
		return nil, errors.New("GitHub commit metadata is invalid")
	}
	commit.SHA = strings.ToLower(commit.SHA)
	body, err := c.get(ctx, "/repos/"+url.PathEscape(repo.Owner.Login)+"/"+url.PathEscape(repo.Name)+"/tarball/"+commit.SHA, MaxArchiveBytes, false)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	meta := Metadata{RepositoryID: repo.ID, Owner: repo.Owner.Login, Repo: repo.Name, RepositoryURL: "https://github.com/" + repo.Owner.Login + "/" + repo.Name, RefMode: sel.RefMode, RefValue: sel.RefValue, SelectedPath: sel.Path, MaxBytes: sel.MaxBytes, PolicyVersion: PolicyVersion, SHA: commit.SHA, CommitTime: commit.Commit.Committer.Date.UTC()}
	return prepareArchive(ctx, body, meta)
}

func (c *Client) getJSON(ctx context.Context, endpoint string, target any) error {
	r, err := c.get(ctx, endpoint, MaxMetadataBytes, true)
	if err != nil {
		return err
	}
	defer r.Close()
	dec := json.NewDecoder(r)
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("decode GitHub metadata: %w", err)
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return errors.New("GitHub metadata has trailing data")
	}
	return nil
}

func (c *Client) get(ctx context.Context, endpoint string, limit int64, jsonBody bool) (io.ReadCloser, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "findrail-public-github-snapshots")
		req.Header.Set("X-GitHub-Api-Version", APIVersion)
		if jsonBody {
			req.Header.Set("Accept", "application/vnd.github+json")
		} else {
			req.Header.Set("Accept", "application/vnd.github+json")
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			last = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return &limitedBody{ReadCloser: resp.Body, reader: io.LimitReader(resp.Body, limit+1), limit: limit}, nil
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		last = fmt.Errorf("GitHub returned %s", resp.Status)
		if resp.StatusCode == 403 || resp.StatusCode == 429 {
			if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
				last = fmt.Errorf("GitHub rate limit reached (reset %s)", reset)
			}
			return nil, last
		}
		if resp.StatusCode < 500 {
			return nil, last
		}
	}
	return nil, fmt.Errorf("GitHub request failed: %w", last)
}

type limitedBody struct {
	io.ReadCloser
	reader   io.Reader
	limit, n int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	n, e := b.reader.Read(p)
	b.n += int64(n)
	if b.n > b.limit {
		return n, fmt.Errorf("response exceeds %d byte limit", b.limit)
	}
	return n, e
}

func prepareArchive(ctx context.Context, compressed io.Reader, meta Metadata) (*Snapshot, error) {
	buffered := bufio.NewReader(compressed)
	gz, err := gzip.NewReader(buffered)
	if err != nil {
		return nil, fmt.Errorf("open GitHub archive: %w", err)
	}
	gz.Multistream(false)
	expanded := &countingReader{r: gz, max: MaxExpandedBytes}
	tr := tar.NewReader(expanded)
	var docs []connector.Document
	var skips SkipCounts
	seen := map[string]bool{}
	wrapper := ""
	scopeFound := meta.SelectedPath == ""
	var inventory int64
	entries := 0
	for {
		if err := ctx.Err(); err != nil {
			gz.Close()
			return nil, err
		}
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			gz.Close()
			return nil, fmt.Errorf("read GitHub archive: %w", err)
		}
		entries++
		if entries > MaxEntries {
			gz.Close()
			return nil, errors.New("GitHub archive entry limit exceeded")
		}
		name := h.Name
		if err := validateTarPath(name); err != nil {
			gz.Close()
			return nil, err
		}
		parts := strings.Split(name, "/")
		if wrapper == "" {
			wrapper = parts[0]
		}
		if parts[0] != wrapper {
			gz.Close()
			return nil, errors.New("GitHub archive has multiple roots")
		}
		if len(parts) == 1 {
			continue
		}
		rel := strings.Join(parts[1:], "/")
		if len(rel) > MaxPathBytes {
			gz.Close()
			return nil, errors.New("GitHub archive path limit exceeded")
		}
		inScope := meta.SelectedPath == "" || rel == meta.SelectedPath || strings.HasPrefix(rel, meta.SelectedPath+"/")
		if rel == meta.SelectedPath && h.FileInfo().IsDir() {
			scopeFound = true
		}
		if !inScope {
			continue
		}
		scopeFound = true
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			if !h.FileInfo().IsDir() {
				skips.Special++
			}
			continue
		}
		if seen[rel] {
			gz.Close()
			return nil, fmt.Errorf("duplicate GitHub archive path %q", rel)
		}
		seen[rel] = true
		if excludedPath(rel) {
			skips.Excluded++
			continue
		}
		if !supported(path.Base(rel)) {
			skips.Unsupported++
			continue
		}
		if h.Size < 0 || h.Size > meta.MaxBytes {
			skips.TooLarge++
			continue
		}
		if len(docs) >= MaxDocuments {
			gz.Close()
			return nil, errors.New("GitHub document limit exceeded")
		}
		body, err := text.Read(io.LimitReader(tr, meta.MaxBytes+1), meta.MaxBytes)
		if errors.Is(err, text.ErrUnsupported) {
			skips.Binary++
			continue
		}
		if errors.Is(err, text.ErrTooLarge) {
			skips.TooLarge++
			continue
		}
		if err != nil {
			gz.Close()
			return nil, err
		}
		if strings.HasPrefix(body, "version https://git-lfs.github.com/spec/v1\n") {
			skips.LFS++
			continue
		}
		inventory += int64(len(body))
		if inventory > MaxInventoryBytes {
			gz.Close()
			return nil, errors.New("GitHub text inventory limit exceeded")
		}
		uri, err := Permalink(meta.Owner, meta.Repo, meta.SHA, rel)
		if err != nil {
			gz.Close()
			return nil, err
		}
		sid := SourceID(meta.RepositoryID, Selection{RefMode: meta.RefMode, RefValue: meta.RefValue, Path: meta.SelectedPath})
		docs = append(docs, connector.Document{ID: fmt.Sprintf("doc_%x", sha256.Sum256([]byte(sid+"\x00"+rel))), SourceID: sid, Title: path.Base(rel), URI: uri, Path: rel, Content: body, Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(body))), SizeBytes: h.Size, ModifiedAt: meta.CommitTime, MediaType: "text/plain"})
	}
	if _, err := io.Copy(io.Discard, gz); err != nil {
		gz.Close()
		return nil, fmt.Errorf("verify GitHub archive: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("verify GitHub archive: %w", err)
	}
	if _, err := buffered.Peek(1); err == nil {
		return nil, errors.New("GitHub archive contains trailing compressed data")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("verify GitHub archive boundary: %w", err)
	}
	if !scopeFound {
		return nil, errors.New("selected path does not exist in the GitHub snapshot")
	}
	sel := Selection{RefMode: meta.RefMode, RefValue: meta.RefValue, Path: meta.SelectedPath}
	sid := SourceID(meta.RepositoryID, sel)
	name := meta.Owner + "/" + meta.Repo + " (" + meta.RefMode
	if meta.RefValue != "" {
		name += " " + meta.RefValue
	}
	if meta.SelectedPath != "" {
		name += ", " + meta.SelectedPath
	}
	name += ")"
	return &Snapshot{source: connector.Source{ID: sid, Kind: "github", Name: name, Root: meta.RepositoryURL, MaxTextBytes: meta.MaxBytes}, docs: docs, meta: meta, skips: skips}, nil
}

type countingReader struct {
	r      io.Reader
	n, max int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, e := c.r.Read(p)
	c.n += int64(n)
	if c.n > c.max {
		return n, errors.New("GitHub expanded archive limit exceeded")
	}
	return n, e
}
func validateTarPath(v string) error {
	if v == "" || len(v) > MaxPathBytes || !utf8.ValidString(v) || strings.HasPrefix(v, "/") || strings.Contains(v, "\\") || hasControl(v) {
		return errors.New("unsafe GitHub archive path")
	}
	for _, p := range strings.Split(strings.TrimSuffix(v, "/"), "/") {
		if p == "" || p == "." || p == ".." {
			return errors.New("unsafe GitHub archive path")
		}
	}
	return nil
}
func hasControl(v string) bool {
	for _, r := range v {
		if r < 32 || r == 127 {
			return true
		}
	}
	return false
}

var extensions = map[string]bool{".txt": true, ".md": true, ".markdown": true, ".rst": true, ".go": true, ".py": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".rs": true, ".java": true, ".c": true, ".h": true, ".cpp": true, ".hpp": true, ".rb": true, ".php": true, ".sh": true, ".sql": true, ".html": true, ".css": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true}

func supported(name string) bool {
	base := strings.ToLower(name)
	if base == "readme" || base == "license" || base == "notice" || base == "makefile" || base == "dockerfile" {
		return true
	}
	return extensions[strings.ToLower(path.Ext(name))]
}
func excludedPath(v string) bool {
	for _, p := range strings.Split(v, "/") {
		l := strings.ToLower(p)
		if strings.HasPrefix(p, ".") && p != ".github" {
			return true
		}
		if l == "node_modules" || l == "vendor" || l == "dist" || l == "build" || l == "target" || l == "__pycache__" || l == ".git" {
			return true
		}
		if strings.Contains(l, "credential") || strings.Contains(l, "secret") || strings.Contains(l, "private_key") {
			return true
		}
	}
	return false
}

// TestClient permits package and integration tests to inject a transport without
// exposing alternate origins as a production CLI option.
func TestClient(httpClient *http.Client, apiBase string) *Client {
	return &Client{HTTP: httpClient, apiBase: strings.TrimSuffix(apiBase, "/")}
}

var _ = strconv.IntSize
