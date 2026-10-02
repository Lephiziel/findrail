package filesystem

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

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
	source   connector.Source
	maxBytes int64
	excluded []string
}

func New(root string, maxBytes int64, excluded ...string) (*Connector, error) {
	if maxBytes < 1 || maxBytes > 32<<20 {
		return nil, fmt.Errorf("max bytes must be between 1 and 33554432")
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
	return &Connector{source: connector.Source{ID: id, Kind: "filesystem", Name: filepath.Base(abs), Root: abs}, maxBytes: maxBytes, excluded: canonicalExclusions}, nil
}

func (c *Connector) Source() connector.Source { return c.source }

func (c *Connector) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	var report connector.Report
	root, err := os.OpenRoot(c.source.Root)
	if err != nil {
		return report, err
	}
	defer root.Close()
	err = filepath.WalkDir(c.source.Root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return fmt.Errorf("enumerate source: %w", walkErr)
		}
		if path == c.source.Root {
			return nil
		}
		if c.isExcluded(path) {
			report.Skipped++
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if entry.IsDir() {
			if strings.HasPrefix(name, ".") || ignoredDirs[name] {
				report.Skipped++
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || strings.HasPrefix(name, ".") || !supported(name) {
			report.Skipped++
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > c.maxBytes {
			report.Skipped++
			return nil
		}
		rel, err := filepath.Rel(c.source.Root, path)
		if err != nil {
			return err
		}
		file, err := root.Open(rel)
		if err != nil {
			return fmt.Errorf("open document %q: %w", rel, err)
		}
		openedInfo, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			return statErr
		}
		if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
			file.Close()
			return fmt.Errorf("document %q changed during scan; retry", rel)
		}
		body, readErr := text.Read(file, c.maxBytes)
		finalInfo, finalStatErr := file.Stat()
		closeErr := file.Close()
		if errors.Is(readErr, text.ErrUnsupported) || errors.Is(readErr, text.ErrTooLarge) {
			report.Skipped++
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("extract document %q: %w", rel, readErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if finalStatErr != nil {
			return finalStatErr
		}
		if finalInfo.Size() != openedInfo.Size() || !finalInfo.ModTime().Equal(openedInfo.ModTime()) {
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
		if err := emit(doc); err != nil {
			return err
		}
		report.Seen++
		return nil
	})
	return report, err
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
	lower := strings.ToLower(name)
	if strings.Contains(lower, "credential") || strings.Contains(lower, "secret") || strings.Contains(lower, "private_key") {
		return false
	}
	return extensions[strings.ToLower(filepath.Ext(name))]
}
