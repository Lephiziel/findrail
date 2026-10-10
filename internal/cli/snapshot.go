package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Lephiziel/findrail/internal/config"
	"github.com/Lephiziel/findrail/internal/snapshot"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
)

func runSnapshotCommand(ctx context.Context, command string, args []string, out io.Writer, version string) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dataDir := fs.String("data-dir", "", "Findrail index directory")
	sourceID := fs.String("source", "", "one indexed source ID")
	output := fs.String("output", "", "new archive path (plaintext content and provenance; never overwritten)")
	name := fs.String("name", "", "local display name for imported frozen source")
	timeout := fs.Duration("timeout", 2*time.Minute, "operation timeout, 1s–10m")
	jsonOutput := fs.Bool("json", false, "output JSON")
	showPaths := fs.Bool("show-paths", false, "show original source name and location; documents/text are never shown")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *timeout < time.Second || *timeout > 10*time.Minute {
		return errors.New("timeout must be between 1s and 10m")
	}
	if fs.NArg() != 1 && command != "export-source" {
		return fmt.Errorf("%s requires one archive file", command)
	}
	if command == "export-source" && (*sourceID == "" || *output == "" || fs.NArg() != 0) {
		return errors.New("export-source requires --source ID and --output FILE")
	}
	if command == "import-source" && *name == "" {
		return errors.New("import-source requires --name NAME")
	}
	opCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	switch command {
	case "inspect-export":
		archive, err := readAndInspect(opCtx, fs.Arg(0))
		if err != nil {
			return err
		}
		return writeInspection(out, archive, *jsonOutput, *showPaths)
	case "import-source":
		archive, err := readAndInspect(opCtx, fs.Arg(0))
		if err != nil {
			return err
		}
		dir, err := config.DataDir(*dataDir)
		if err != nil {
			return err
		}
		store, err := sqlite.Open(opCtx, dir)
		if err != nil {
			return fmt.Errorf("open destination index: %w", err)
		}
		defer store.Close()
		id, err := store.ImportSnapshot(opCtx, *name, archive)
		var duplicate *sqlite.AlreadyImportedError
		if errors.As(err, &duplicate) {
			if *jsonOutput {
				return json.NewEncoder(out).Encode(map[string]any{"status": "already_imported", "source_id": duplicate.SourceID, "fingerprint": archive.Manifest.Fingerprint})
			}
			return fmt.Errorf("already imported as source %s", duplicate.SourceID)
		}
		if err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(out).Encode(map[string]any{"status": "imported", "source_id": id, "documents": len(archive.Documents), "pages": len(archive.Pages), "fingerprint": archive.Manifest.Fingerprint})
		}
		_, err = fmt.Fprintf(out, "Imported frozen archive source %s (%d documents, %d pages).\n", id, len(archive.Documents), len(archive.Pages))
		return err
	case "export-source":
		dir, err := config.DataDir(*dataDir)
		if err != nil {
			return err
		}
		store, err := sqlite.OpenReadOnly(opCtx, dir)
		if err != nil {
			return fmt.Errorf("open existing index: %w", err)
		}
		defer store.Close()
		archive, err := store.SnapshotForExport(opCtx, *sourceID)
		if err != nil {
			return err
		}
		sources, err := store.Sources(opCtx)
		if err != nil {
			return err
		}
		protectedRoots := []string{dir}
		for _, source := range sources {
			if source.Kind == "filesystem" {
				protectedRoots = append(protectedRoots, source.Root)
			}
		}
		if err = validateExportTarget(*output, protectedRoots...); err != nil {
			return err
		}
		archive.Manifest.Producer, archive.Manifest.ExportedAt = version, time.Now().UTC().Format(time.RFC3339Nano)
		data, err := snapshot.Encode(archive.Manifest, archive.Documents, archive.Pages)
		if err != nil {
			return err
		}
		verified, err := snapshot.Inspect(data)
		if err != nil {
			return fmt.Errorf("verify completed archive: %w", err)
		}
		if err = publishNoReplace(*output, data); err != nil {
			return err
		}
		sha := sha256.Sum256(data)
		result := map[string]any{"source_id": *sourceID, "documents": len(archive.Documents), "pages": len(archive.Pages), "archive_bytes": len(data), "fingerprint": verified.Manifest.Fingerprint, "archive_sha256": hex.EncodeToString(sha[:])}
		if *jsonOutput {
			return json.NewEncoder(out).Encode(result)
		}
		_, err = fmt.Fprintf(out, "Exported source %s: %d documents, %d pages, %d bytes, fingerprint %s. Archive is plaintext and contains source provenance.\n", *sourceID, len(archive.Documents), len(archive.Pages), len(data), result["fingerprint"])
		return err
	}
	return fmt.Errorf("unknown snapshot command %q", command)
}

func readAndInspect(ctx context.Context, path string) (snapshot.Archive, error) {
	var empty snapshot.Archive
	f, err := os.Open(path)
	if err != nil {
		return empty, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return empty, err
	}
	if !info.Mode().IsRegular() {
		return empty, errors.New("archive input must be a regular file")
	}
	if info.Size() > snapshot.MaxArchive {
		return empty, errors.New("archive exceeds v1 compressed-size limit")
	}
	dir, err := os.MkdirTemp("", "findrail-inspect-")
	if err != nil {
		return empty, err
	}
	defer os.RemoveAll(dir)
	if err = os.Chmod(dir, 0700); err != nil {
		return empty, err
	}
	tmp := filepath.Join(dir, "input.zip")
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return empty, err
	}
	_, copyErr := copyBounded(ctx, dst, f, snapshot.MaxArchive)
	syncErr := dst.Sync()
	closeErr := dst.Close()
	if copyErr != nil {
		return empty, copyErr
	}
	if syncErr != nil {
		return empty, syncErr
	}
	if closeErr != nil {
		return empty, closeErr
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		return empty, err
	}
	return snapshot.Inspect(data)
}

func copyBounded(ctx context.Context, dst io.Writer, src io.Reader, limit int64) (int64, error) {
	buf := make([]byte, 64<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, e := src.Read(buf)
		if n > 0 {
			if int64(n) > limit-total {
				return total, errors.New("archive exceeds v1 size limit")
			}
			written, we := dst.Write(buf[:n])
			total += int64(written)
			if we != nil {
				return total, we
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if e == io.EOF {
			return total, nil
		}
		if e != nil {
			return total, e
		}
	}
}

func writeInspection(out io.Writer, a snapshot.Archive, jsonOutput, showPaths bool) error {
	result := map[string]any{"format": a.Manifest.Format, "version": a.Manifest.Version, "origin_kind": a.Manifest.Origin.Kind, "documents": a.Manifest.DocumentCount, "pages": a.Manifest.PageCount, "exported_at": a.Manifest.ExportedAt, "fingerprint": a.Manifest.Fingerprint, "integrity": "valid"}
	if showPaths {
		result["origin_name"] = a.Manifest.Origin.Name
		result["origin_location"] = a.Manifest.Origin.Location
	}
	if jsonOutput {
		return json.NewEncoder(out).Encode(result)
	}
	_, err := fmt.Fprintf(out, "Valid %s v%d snapshot · origin %s · %d documents · %d pages · exported %s · fingerprint %s\n", a.Manifest.Format, a.Manifest.Version, a.Manifest.Origin.Kind, a.Manifest.DocumentCount, a.Manifest.PageCount, a.Manifest.ExportedAt, a.Manifest.Fingerprint)
	if err == nil && showPaths {
		_, err = fmt.Fprintf(out, "Original source: %s · %s\n", a.Manifest.Origin.Name, a.Manifest.Origin.Location)
	}
	return err
}

func validateExportTarget(target string, protectedRoots ...string) error {
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
	for _, protected := range protectedRoots {
		if protected == "" {
			continue
		}
		p, e := filepath.Abs(protected)
		if e != nil {
			return e
		}
		if resolved, e := filepath.EvalSymlinks(p); e == nil {
			p = resolved
		}
		rel, e := filepath.Rel(p, candidate)
		if e == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
			return errors.New("refusing to export inside the index directory or registered source root")
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

// A hard-link publication is atomic and fails if the destination exists on
// supported local filesystems; it never replaces a file or symlink.
func publishNoReplace(target string, data []byte) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	parent := filepath.Dir(abs)
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	abs = filepath.Join(realParent, filepath.Base(abs))
	f, err := os.CreateTemp(realParent, ".findrail-export-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Link(tmp, abs); err != nil {
		return fmt.Errorf("publish archive without replacing existing output: %w", err)
	}
	return nil
}
