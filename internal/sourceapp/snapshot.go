package sourceapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Lephiziel/findrail/internal/snapshot"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
)

const (
	SnapshotCompressedLimit = 32 << 20
	SnapshotExpandedLimit   = 64 << 20
	SnapshotDocumentLimit   = 2000
	SnapshotPageLimit       = 20000
)

type SnapshotSummary struct {
	Format      string `json:"format"`
	Version     int    `json:"version"`
	Kind        string `json:"kind"`
	Documents   int    `json:"documents"`
	Pages       int    `json:"pages"`
	Bytes       int64  `json:"bytes"`
	Fingerprint string `json:"fingerprint"`
	Frozen      bool   `json:"frozen"`
	Claim       string `json:"claim,omitempty"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
}

type snapshotLease struct {
	path        string
	fingerprint string
	expires     time.Time
	summary     SnapshotSummary
	consumed    bool
}

func (a *App) InspectSnapshot(path string) (Job, error) {
	if !filepath.IsAbs(path) || !utf8.ValidString(path) || len([]byte(path)) > 4096 || strings.IndexByte(path, 0) >= 0 {
		return Job{}, errors.New("invalid_path")
	}
	canonical, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return Job{}, errors.New("invalid_path")
	}
	if err = regularFile(canonical); err != nil {
		return Job{}, err
	}
	canonical, err = filepath.EvalSymlinks(canonical)
	if err != nil {
		return Job{}, errors.New("invalid_path")
	}
	return a.submit("snapshot-inspect", "portable snapshot inspect", "", func(ctx context.Context, job *Job) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		archive, size, err := a.readSnapshot(ctx, canonical)
		if err != nil {
			return "", err
		}
		claim, err := randomClaim()
		if err != nil {
			return "", err
		}
		summary := SnapshotSummary{Format: archive.Manifest.Format, Version: archive.Manifest.Version, Kind: archive.Manifest.Origin.Kind, Documents: len(archive.Documents), Pages: len(archive.Pages), Bytes: size, Fingerprint: archive.Manifest.Fingerprint, Frozen: true, Status: "succeeded"}
		a.mu.Lock()
		defer a.mu.Unlock()
		a.expireSnapshotLeasesLocked()
		if len(a.snapshotLeases) >= 8 {
			return "", errors.New("snapshot_preview_busy")
		}
		a.snapshotLeases[claim] = snapshotLease{path: canonical, fingerprint: summary.Fingerprint, expires: time.Now().Add(5 * time.Minute), summary: summary}
		a.snapshotResults[job.ID] = claim
		// The claim is intentionally returned only by the gated inspection result endpoint.
		return "", nil
	})
}

func (a *App) SnapshotInspection(jobID string) SnapshotSummary {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.expireSnapshotLeasesLocked()
	w := a.byID[jobID]
	if w == nil || w.job.Type != "snapshot-inspect" {
		return SnapshotSummary{Status: "not_found"}
	}
	switch w.job.Status {
	case "queued", "running":
		return SnapshotSummary{Status: w.job.Status}
	case "failed", "canceled":
		return SnapshotSummary{Status: w.job.Status, Error: w.job.Error}
	}
	claim := a.snapshotResults[jobID]
	lease, ok := a.snapshotLeases[claim]
	if !ok || time.Now().After(lease.expires) {
		return SnapshotSummary{Status: "expired", Error: "Inspection preview expired; inspect the archive again."}
	}
	lease.summary.Claim = claim
	return lease.summary
}

func (a *App) ImportSnapshot(claim, name string) (Job, error) {
	if len([]byte(name)) == 0 || len([]byte(name)) > 512 || !utf8.ValidString(name) || strings.TrimSpace(name) == "" || strings.IndexByte(name, 0) >= 0 {
		return Job{}, errors.New("invalid_name")
	}
	a.mu.Lock()
	a.expireSnapshotLeasesLocked()
	lease, ok := a.snapshotLeases[claim]
	if !ok || lease.consumed {
		a.mu.Unlock()
		return Job{}, errors.New("invalid_claim")
	}
	lease.consumed = true
	a.snapshotLeases[claim] = lease
	a.mu.Unlock()
	job, err := a.submit("snapshot-import", "portable snapshot import", "", func(ctx context.Context, job *Job) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		archive, _, err := a.readSnapshot(ctx, lease.path)
		if err != nil {
			return "", err
		}
		if archive.Manifest.Fingerprint != lease.fingerprint {
			return "", errors.New("snapshot_changed")
		}
		id, err := a.store.ImportSnapshot(ctx, name, archive)
		var duplicate *sqlite.AlreadyImportedError
		if errors.As(err, &duplicate) {
			return "already_imported:" + duplicate.SourceID, nil
		}
		if err != nil {
			return "", err
		}
		a.progress(job, len(archive.Documents), 0, true, true)
		return "imported:" + id, nil
	})
	if err != nil {
		a.mu.Lock()
		delete(a.snapshotLeases, claim)
		a.mu.Unlock()
	}
	return job, err
}

func (a *App) ExportSnapshot(sourceID, output string) (Job, error) {
	if !filepath.IsAbs(output) || !utf8.ValidString(output) || len([]byte(output)) > 4096 || strings.IndexByte(output, 0) >= 0 {
		return Job{}, errors.New("invalid_path")
	}
	canonical, err := filepath.Abs(filepath.Clean(output))
	if err != nil {
		return Job{}, errors.New("invalid_path")
	}
	return a.submit("snapshot-export", "portable snapshot export", sourceID, func(ctx context.Context, job *Job) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		unlock, err := a.coordinator.Acquire(ctx, sourceID)
		if err != nil {
			return "", err
		}
		defer unlock()
		sources, err := a.store.Sources(ctx)
		if err != nil {
			return "", err
		}
		registered := false
		roots := []string{a.dataDir}
		for _, s := range sources {
			if s.ID == sourceID {
				registered = true
			}
			if s.Kind == "filesystem" {
				roots = append(roots, s.Root)
			}
		}
		if !registered {
			return "", errors.New("not_found")
		}
		if err = snapshot.ValidateExportTarget(canonical, roots...); err != nil {
			return "", err
		}
		if err = a.takeSnapshotSlot(ctx); err != nil {
			return "", err
		}
		defer a.releaseSnapshotSlot()
		_, err = snapshot.PublishNoReplaceStream(canonical, func(w io.Writer) error {
			f, ok := w.(*os.File)
			if !ok {
				return errors.New("archive staging is not a file")
			}
			limited := &snapshotOutputLimit{w: f, remaining: SnapshotCompressedLimit}
			manifest, _, _, streamErr := a.store.StreamSnapshotProfile(ctx, sourceID, "findrail", time.Now().UTC().Format(time.RFC3339Nano), limited, SnapshotCompressedLimit, SnapshotExpandedLimit, SnapshotDocumentLimit, SnapshotPageLimit)
			if streamErr != nil {
				return streamErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := f.Sync(); err != nil {
				return err
			}
			info, err := f.Stat()
			if err != nil {
				return err
			}
			verified, err := snapshot.VerifyArchive(ctx, f, info.Size())
			if err != nil {
				return err
			}
			if verified.Fingerprint != manifest.Fingerprint {
				return errors.New("snapshot verification failed")
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		a.progress(job, 0, 0, true, true)
		return "exported", nil
	})
}

type snapshotOutputLimit struct {
	w         io.Writer
	remaining int64
}

func (w *snapshotOutputLimit) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("snapshot output exceeds management profile")
	}
	n, err := w.w.Write(p)
	w.remaining -= int64(n)
	return n, err
}

func (a *App) readSnapshot(ctx context.Context, path string) (snapshot.Archive, int64, error) {
	var zero snapshot.Archive
	if err := a.takeSnapshotSlot(ctx); err != nil {
		return zero, 0, err
	}
	defer a.releaseSnapshotSlot()
	return snapshot.InspectFileProfile(ctx, path, SnapshotCompressedLimit, SnapshotExpandedLimit, SnapshotDocumentLimit, SnapshotPageLimit, true)
}

func (a *App) takeSnapshotSlot(ctx context.Context) error {
	select {
	case a.snapshotGate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (a *App) releaseSnapshotSlot() { <-a.snapshotGate }
func (a *App) expireSnapshotLeasesLocked() {
	now := time.Now()
	for k, v := range a.snapshotLeases {
		if now.After(v.expires) || v.consumed {
			delete(a.snapshotLeases, k)
		}
	}
}
func regularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return errors.New("archive input must be an existing regular file")
	}
	if !info.Mode().IsRegular() {
		return errors.New("archive input must be a regular non-symlink file")
	}
	return nil
}
func randomClaim() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
