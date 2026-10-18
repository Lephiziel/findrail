//go:build linux

// Package recovery contains process-level crash tests. SIGKILL models abrupt
// process death on Linux; it is not a power-loss or filesystem-failure test.
package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"
	"testing"
	"time"

	"github.com/Lephiziel/findrail/internal/ingest"
	"github.com/Lephiziel/findrail/internal/search"
	"github.com/Lephiziel/findrail/internal/store/sqlite"
	"github.com/Lephiziel/findrail/pkg/connector"
)

const sourceID = "recovery-synthetic-source"

type contentConnector struct {
	root, content string
	barrier       string
}

func (c contentConnector) Source() connector.Source {
	return connector.Source{ID: sourceID, Kind: "filesystem", Name: "Synthetic", Root: c.root, MaxTextBytes: 1 << 20}
}
func (c contentConnector) Scan(ctx context.Context, emit func(connector.Document) error) (connector.Report, error) {
	digest := sha256.Sum256([]byte(c.content))
	doc := connector.Document{ID: "synthetic-document", SourceID: sourceID, Title: "Synthetic", URI: "file:///synthetic.txt", Path: "synthetic.txt", Content: c.content, Hash: hex.EncodeToString(digest[:]), ModifiedAt: time.Unix(1, 0).UTC(), MediaType: "text/plain"}
	if err := emit(doc); err != nil {
		return connector.Report{}, err
	}
	if c.barrier != "" {
		if err := os.WriteFile(c.barrier, []byte("ready"), 0600); err != nil {
			return connector.Report{}, err
		}
		<-ctx.Done()
		return connector.Report{}, ctx.Err()
	}
	return connector.Report{Seen: 1}, nil
}

// TestCrashHelper is entered only by the child process started by the parent.
func TestCrashHelper(t *testing.T) {
	if os.Getenv("FINDRAIL_RECOVERY_HELPER") != "1" {
		t.Skip("helper process")
	}
	ctx := context.Background()
	store, err := sqlite.Open(ctx, os.Getenv("FINDRAIL_RECOVERY_DATA"))
	if err != nil {
		os.Exit(21)
	}
	_, err = ingest.Refresh(ctx, store, contentConnector{root: "synthetic", content: "uncommittednewtoken", barrier: os.Getenv("FINDRAIL_RECOVERY_BARRIER")})
	_ = store.Close()
	if err != nil {
		os.Exit(22)
	}
	os.Exit(0)
}

// TestSIGKILLDuringRefreshKeepsCommittedSnapshot has an explicit connector
// barrier after real production Upsert calls and before Commit. No sleeps are
// used to choose the crash boundary.
func TestSIGKILLDuringRefreshKeepsCommittedSnapshot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SIGKILL process-group scenario is Linux-specific")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	store, err := sqlite.Open(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ingest.Run(ctx, store, contentConnector{root: "synthetic", content: "committedoldtoken"}); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	barrier := filepath.Join(root, "barrier")
	reportPath := filepath.Join(root, "recovery-report.json")
	report := caseReport{Case: "refresh_before_publish", Status: "failed", Phase: "starting"}
	started := time.Now()
	defer func() {
		report.DurationMillis = time.Since(started).Milliseconds()
		writeReport(reportPath, report)
	}()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCrashHelper$")
	cmd.Env = append(os.Environ(), "FINDRAIL_RECOVERY_HELPER=1", "FINDRAIL_RECOVERY_DATA="+dataDir, "FINDRAIL_RECOVERY_BARRIER="+barrier)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()
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
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			t.Fatalf("deadline waiting for controlled boundary")
		case <-time.After(10 * time.Millisecond):
		}
	}
	report.Phase = "upserted_uncommitted"
	if err = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("child process group did not terminate before deadline")
	}
	report.Phase = "reopened"

	store, err = sqlite.Open(ctx, dataDir)
	if err != nil {
		t.Fatalf("reopen after SIGKILL: %v", err)
	}
	defer store.Close()
	for _, query := range []struct {
		text string
		want int
	}{{"committedoldtoken", 1}, {"uncommittednewtoken", 0}} {
		got, e := store.Search(ctx, search.Request{Query: query.text, Limit: 10})
		if e != nil {
			t.Fatal(e)
		}
		if got.Total != query.want {
			t.Fatalf("query %q total=%d want=%d", query.text, got.Total, query.want)
		}
	}
	sources, err := store.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Documents != 1 {
		t.Fatalf("reopened source state: %+v", sources)
	}
	report.Status = "passed"
	report.Phase = "verified_old_generation"
}

type caseReport struct {
	Case           string `json:"case"`
	Status         string `json:"status"`
	DurationMillis int64  `json:"duration_ms"`
	Phase          string `json:"phase"`
	BinarySHA      string `json:"binary_sha256"`
	Revision       string `json:"revision"`
	Schema         int    `json:"schema"`
	Platform       string `json:"platform"`
}

func writeReport(path string, report caseReport) {
	if exe, err := os.Executable(); err == nil {
		if b, err := os.ReadFile(exe); err == nil {
			h := sha256.Sum256(b)
			report.BinarySHA = hex.EncodeToString(h[:])
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				report.Revision = setting.Value
			}
		}
	}
	report.Schema = sqlite.CurrentWriteSchema()
	report.Platform = runtime.GOOS + "/" + runtime.GOARCH
	b, _ := json.Marshal(report)
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0600) == nil {
		_ = os.Rename(tmp, path)
	}
}
