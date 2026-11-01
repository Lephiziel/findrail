// Package originals resolves indexed filesystem documents without trusting a
// client-supplied path and launches only a fixed platform opener.
package originals

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	ErrInvalidPath     = errors.New("invalid original path")
	ErrUnsupported     = errors.New("document type or platform is not supported")
	ErrSnapshotChanged = errors.New("indexed original snapshot changed")
	ErrNotFilesystem   = errors.New("source is not a live filesystem source")
)

type Action string

const (
	OpenFile   Action = "file"
	OpenFolder Action = "folder"
)

type Launch func(executable string, args ...string) error

type Opener struct {
	GOOS   string
	Launch Launch
}

func New(launch Launch) Opener { return Opener{GOOS: runtime.GOOS, Launch: launch} }

// Identity binds a visible card to a source registration and indexed content.
func Identity(registration string, revision int64, contentHash, sourceID, documentID string) string {
	h := sha256.Sum256([]byte(registration + "\x00" + fmt.Sprint(revision) + "\x00" + contentHash + "\x00" + sourceID + "\x00" + documentID))
	return hex.EncodeToString(h[:])
}

// Resolve opens no content; it checks current path metadata under the selected root.
func Resolve(root, rel string) (string, error) {
	if root == "" || rel == "" || strings.ContainsRune(rel, 0) || strings.Contains(rel, `\`) || filepath.IsAbs(rel) {
		return "", ErrInvalidPath
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." || strings.Contains(part, ":") {
			return "", ErrInvalidPath
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", ErrInvalidPath
	}
	if !safeRootForOS(root, runtime.GOOS) {
		return "", ErrInvalidPath
	}
	rootInfo, err := os.Lstat(absRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", ErrInvalidPath
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil || filepath.Clean(resolvedRoot) != filepath.Clean(absRoot) {
		return "", ErrInvalidPath
	}
	r, err := os.OpenRoot(absRoot)
	if err != nil {
		return "", ErrInvalidPath
	}
	defer r.Close()
	parts := strings.Split(rel, "/")
	for i := range parts {
		info, e := r.Lstat(filepath.Join(parts[:i+1]...))
		if e != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", ErrInvalidPath
		}
		if i < len(parts)-1 && !info.IsDir() {
			return "", ErrInvalidPath
		}
		if i == len(parts)-1 && !info.Mode().IsRegular() {
			return "", ErrInvalidPath
		}
	}
	path := filepath.Join(absRoot, filepath.FromSlash(rel))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !within(absRoot, resolved) {
		return "", ErrInvalidPath
	}
	return path, nil
}

func safeRootForOS(root, goos string) bool {
	if goos != "windows" {
		return true
	}
	root = strings.ReplaceAll(root, "/", `\`)
	root = strings.ToLower(root)
	return !strings.HasPrefix(root, `\`) && !strings.HasPrefix(root, `\\?\`) && !strings.HasPrefix(root, `\\.\`)
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func AllowedDocument(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf", ".docx", ".txt", ".md", ".markdown", ".rst", ".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".rs", ".java", ".c", ".h", ".cpp", ".hpp", ".rb", ".php", ".sh", ".sql", ".html", ".css", ".json", ".yaml", ".yml", ".toml":
		return true
	default:
		return false
	}
}

func isText(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext != ".pdf" && ext != ".docx"
}

// Command returns a fixed executable and argv; no shell or interpolation is used.
func Command(goos string, action Action, path string) (string, []string, error) {
	if path == "" || (action != OpenFile && action != OpenFolder) {
		return "", nil, ErrInvalidPath
	}
	if action == OpenFile && !AllowedDocument(path) {
		return "", nil, ErrUnsupported
	}
	switch goos {
	case "linux":
		if action == OpenFolder {
			return "xdg-open", []string{path}, nil
		}
		if isText(path) {
			return "gedit", []string{"--", path}, nil
		}
		return "xdg-open", []string{path}, nil
	case "darwin":
		if action == OpenFolder {
			return "open", []string{"--", path}, nil
		}
		if isText(path) {
			return "open", []string{"-t", "--", path}, nil
		}
		return "open", []string{"--", path}, nil
	case "windows":
		if action == OpenFolder {
			return "explorer.exe", []string{path}, nil
		}
		if isText(path) {
			return "notepad.exe", []string{path}, nil
		}
		return "rundll32.exe", []string{"url.dll,FileProtocolHandler", path}, nil
	default:
		return "", nil, fmt.Errorf("%w: operating system", ErrUnsupported)
	}
}

func (o Opener) Open(action Action, path string) error {
	if o.Launch == nil {
		return errors.New("native launcher unavailable")
	}
	name, args, err := Command(o.GOOS, action, path)
	if err != nil {
		return err
	}
	return o.Launch(name, args...)
}

// SystemLaunch starts the fixed executable without a shell and reaps it later.
func SystemLaunch(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
