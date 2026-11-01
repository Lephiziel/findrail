package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// InspectFileProfile safely stages a user-selected archive before full codec
// validation. The CLI uses v1 hard caps; management supplies a stricter profile.
func InspectFileProfile(ctx context.Context, path string, compressedLimit, expandedLimit int64, documentLimit, pageLimit int, rejectSymlink bool) (Archive, int64, error) {
	var empty Archive
	if err := ctx.Err(); err != nil {
		return empty, 0, err
	}
	var linkInfo os.FileInfo
	var err error
	if rejectSymlink {
		linkInfo, err = os.Lstat(path)
	} else {
		linkInfo, err = os.Stat(path)
	}
	if err != nil || !linkInfo.Mode().IsRegular() {
		return empty, 0, errors.New("archive input must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return empty, 0, errors.New("archive input must be a readable regular file")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || rejectSymlink && !os.SameFile(linkInfo, info) {
		return empty, 0, errors.New("archive input must be a regular file")
	}
	if compressedLimit <= 0 || info.Size() > compressedLimit {
		return empty, 0, errors.New("archive exceeds compressed-size limit")
	}
	dir, err := os.MkdirTemp("", "findrail-inspect-")
	if err != nil {
		return empty, 0, err
	}
	defer os.RemoveAll(dir)
	if err = os.Chmod(dir, 0700); err != nil {
		return empty, 0, err
	}
	staged := filepath.Join(dir, "input.zip")
	dst, err := os.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return empty, 0, err
	}
	_, copyErr := copyBoundedContext(ctx, dst, file, compressedLimit)
	syncErr := dst.Sync()
	closeErr := dst.Close()
	if copyErr != nil {
		return empty, 0, copyErr
	}
	if syncErr != nil {
		return empty, 0, syncErr
	}
	if closeErr != nil {
		return empty, 0, closeErr
	}
	data, err := os.ReadFile(staged)
	if err != nil {
		return empty, 0, err
	}
	archive, err := InspectProfile(ctx, data, expandedLimit, documentLimit, pageLimit)
	return archive, int64(len(data)), err
}

func copyBoundedContext(ctx context.Context, dst io.Writer, src io.Reader, limit int64) (int64, error) {
	buf := make([]byte, 64<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			if int64(n) > limit-total {
				return total, errors.New("archive exceeds compressed-size limit")
			}
			written, writeErr := dst.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

// ValidateExportTarget preserves the CLI's symlink-aware containment checks for
// both CLI and writable management export.
func ValidateExportTarget(target string, protectedRoots ...string) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	parent := filepath.Dir(abs)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("output parent must exist: %w", err)
	}
	if !info.IsDir() {
		return errors.New("output parent is not a directory")
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	candidate := filepath.Join(realParent, filepath.Base(abs))
	canonicalCandidate, err := canonicalSafetyPath(candidate)
	if err != nil {
		return err
	}
	for _, protected := range protectedRoots {
		if protected == "" {
			continue
		}
		p, e := filepath.Abs(protected)
		if e != nil {
			return e
		}
		canonicalRoot, e := canonicalSafetyPath(p)
		if e != nil {
			return e
		}
		for _, pair := range [][2]string{{p, abs}, {canonicalRoot, canonicalCandidate}} {
			rel, e := filepath.Rel(pair[0], pair[1])
			if e == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
				return errors.New("refusing to export inside the index directory or registered source root")
			}
		}
	}
	if _, err = os.Lstat(candidate); err == nil {
		return errors.New("output already exists; refusing to overwrite")
	}
	if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func canonicalSafetyPath(value string) (string, error) {
	current, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	suffix := []string{}
	for {
		resolved, e := filepath.EvalSymlinks(current)
		if e == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(e) {
			return "", e
		}
		info, lerr := os.Lstat(current)
		if lerr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("ambiguous symlink in protected output path")
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", e
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

// PublishNoReplaceStream stages privately and atomically links a complete output
// without replacing a pre-existing file or symlink. The callback may verify data
// through the passed *os.File before publication.
func PublishNoReplaceStream(target string, build func(io.Writer) error) (int64, error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return 0, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return 0, err
	}
	abs = filepath.Join(parent, filepath.Base(abs))
	f, err := os.CreateTemp(parent, ".findrail-export-*")
	if err != nil {
		return 0, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return 0, err
	}
	if err = build(f); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return 0, err
	}
	if closeErr != nil {
		return 0, closeErr
	}
	info, err := os.Stat(tmp)
	if err != nil {
		return 0, err
	}
	if err = os.Link(tmp, abs); err != nil {
		return 0, fmt.Errorf("publish archive without replacing existing output: %w", err)
	}
	return info.Size(), nil
}
