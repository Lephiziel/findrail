//go:build windows

package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/Lephiziel/findrail/pkg/connector"
)

const windowsRecoverySource = "windows-recovery-synthetic"

type windowsBarrierConnector struct{ root, barrier string }

func (c windowsBarrierConnector) Source() connector.Source {
	return connector.Source{ID: windowsRecoverySource, Kind: "filesystem", Name: "Synthetic Windows", Root: c.root, MaxTextBytes: 1 << 20}
}
func (c windowsBarrierConnector) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	text := "windowsuncommittedtoken"
	doc := connector.Document{ID: "windows-synthetic-document", SourceID: windowsRecoverySource, Title: "Synthetic", URI: "file:///synthetic.txt", Path: "synthetic.txt", Content: text, Hash: text, ModifiedAt: time.Unix(1, 0).UTC(), MediaType: "text/plain"}
	if err := emit(doc); err != nil {
		return connector.Report{}, err
	}
	if err := os.WriteFile(c.barrier, []byte("ready"), 0600); err != nil {
		return connector.Report{}, err
	}
	<-ctx.Done()
	return connector.Report{}, ctx.Err()
}

func TestWindowsCrashHelper(t *testing.T) {
	if os.Getenv("FINDRAIL_WINDOWS_RECOVERY_HELPER") != "1" {
		t.Skip("helper process")
	}
	store, err := sqlite.Open(context.Background(), os.Getenv("FINDRAIL_WINDOWS_RECOVERY_DATA"))
	if err != nil {
		os.Exit(21)
	}
	_, err = ingest.Refresh(context.Background(), store, windowsBarrierConnector{root: "synthetic", barrier: os.Getenv("FINDRAIL_WINDOWS_RECOVERY_BARRIER")})
	_ = store.Close()
	if err != nil {
		os.Exit(22)
	}
	os.Exit(0)
}

func TestWindowsProcessTerminationRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	report := windowsReport{Case: "windows_refresh_before_publish", Status: "failed", Phase: "starting"}
	started := time.Now()
	evidenceDir := os.Getenv("FINDRAIL_RECOVERY_REPORT_DIR")
	if evidenceDir == "" {
		evidenceDir = os.TempDir()
	}
	if err := os.MkdirAll(evidenceDir, 0700); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(evidenceDir, "findrail-recovery-windows.json")
	defer func() {
		report.DurationMillis = time.Since(started).Milliseconds()
		if err := writeWindowsReport(reportPath, report); err != nil {
			t.Errorf("write recovery report: %v", err)
		}
	}()
	if err := writeWindowsReport(reportPath, report); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	seed := connector.Document{ID: "windows-synthetic-document", SourceID: windowsRecoverySource, Title: "Synthetic", URI: "file:///synthetic.txt", Path: "synthetic.txt", Content: "windowscommittedtoken", Hash: "seed", ModifiedAt: time.Unix(1, 0).UTC(), MediaType: "text/plain"}
	seedConnector := windowsSeedConnector{doc: seed}
	if _, err = ingest.Run(ctx, store, seedConnector); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	barrier := filepath.Join(root, "barrier")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsCrashHelper$")
	cmd.Env = append(os.Environ(), "FINDRAIL_WINDOWS_RECOVERY_HELPER=1", "FINDRAIL_WINDOWS_RECOVERY_DATA="+dataDir, "FINDRAIL_WINDOWS_RECOVERY_BARRIER="+barrier)
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		if _, err = os.Stat(barrier); err == nil {
			break
		}
		select {
		case err = <-done:
			t.Fatalf("helper exited before controlled boundary: %v", err)
		case <-ctx.Done():
			t.Fatal("deadline waiting for controlled Windows boundary")
		case <-time.After(10 * time.Millisecond):
		}
	}
	report.Phase = "upserted_uncommitted"
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("terminated process did not exit before deadline")
	}
	store, err = sqlite.Open(ctx, dataDir)
	if err != nil {
		t.Fatalf("reopen after process termination: %v", err)
	}
	defer store.Close()
	for _, check := range []struct {
		query string
		total int
	}{{"windowscommittedtoken", 1}, {"windowsuncommittedtoken", 0}} {
		result, e := store.Search(ctx, search.Request{Query: check.query, Limit: 10})
		if e != nil || result.Total != check.total {
			t.Fatalf("recovered query %q: %+v %v", check.query, result, e)
		}
	}
	report.Status, report.Phase = "passed", "verified_old_generation"
}

type windowsSeedConnector struct{ doc connector.Document }

func (c windowsSeedConnector) Source() connector.Source {
	return connector.Source{ID: windowsRecoverySource, Kind: "filesystem", Name: "Synthetic Windows", Root: "synthetic", MaxTextBytes: 1 << 20}
}
func (c windowsSeedConnector) Scan(_ context.Context, emit func(connector.Document) error) (connector.Report, error) {
	if err := emit(c.doc); err != nil {
		return connector.Report{}, err
	}
	return connector.Report{Seen: 1}, nil
}

type windowsReport struct {
	Case           string `json:"case"`
	Status         string `json:"status"`
	DurationMillis int64  `json:"duration_ms"`
	Phase          string `json:"phase"`
	BinarySHA      string `json:"binary_sha256"`
	Revision       string `json:"revision"`
	Schema         int    `json:"schema"`
	Platform       string `json:"platform"`
}

func writeWindowsReport(path string, report windowsReport) error {
	if exe, err := os.Executable(); err == nil {
		if f, err := os.Open(exe); err == nil {
			h := sha256.New()
			_, copyErr := io.Copy(h, f)
			_ = f.Close()
			if copyErr == nil {
				report.BinarySHA = hex.EncodeToString(h.Sum(nil))
			}
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				report.Revision = s.Value
			}
		}
	}
	report.Schema, report.Platform = sqlite.CurrentWriteSchema(), runtime.GOOS+"/"+runtime.GOARCH
	b, err := json.Marshal(report)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".findrail-recovery-*.json")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if _, err = file.Write(b); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
