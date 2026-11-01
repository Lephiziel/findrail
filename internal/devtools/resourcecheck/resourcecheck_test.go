package resourcecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCPUStatHandlesSpacesAndParenthesesInComm(t *testing.T) {
	// Linux proc stat fields after the final ')' start at state (field 3).
	line := "42 (program (with spaces)) R 1 2 3 4 5 6 7 8 9 10 11 12 13 14"
	path := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	// Exercise the extraction rule independently of /proc's pid-derived path.
	if got := ticksFromStat([]byte(line)); got != 23 {
		t.Fatalf("ticks=%d, want 23", got)
	}
}

func TestFailedRunReplacesStalePassAndDoesNotLeakInput(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux recovery contract")
	}
	dir := t.TempDir()
	report := filepath.Join(dir, "report.json")
	if err := os.WriteFile(report, []byte(`{"status":"passed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	code := RunRecovery(context.Background(), RecoveryOptions{Binary: "/private/path", Revision: strings.Repeat("a", 40), Schema: 8, WorkDir: dir, Report: report, Deadline: math.NaN()})
	if code == 0 {
		t.Fatal("invalid NaN deadline passed")
	}
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var r recoveryReport
	if json.Unmarshal(b, &r) != nil || r.Status != "failed" || r.Phase != "unexecuted" || !strings.Contains(string(b), "invalid_deadline") {
		t.Fatalf("unexpected report: %s", b)
	}
	if strings.Contains(string(b), "private/path") {
		t.Fatalf("private path leaked: %s", b)
	}
}

func TestInterruptedRunReplacesStalePass(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "report.json")
	if err := os.WriteFile(report, []byte(`{"status":"passed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code := RunRecovery(ctx, RecoveryOptions{Binary: "/does/not/matter", Revision: strings.Repeat("a", 40), Schema: 8, WorkDir: dir, Report: report, Deadline: 45})
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var r recoveryReport
	if code == 0 || json.Unmarshal(b, &r) != nil || r.Status != "interrupted" || r.FailureCode != "interrupted" {
		t.Fatalf("code=%d report=%s", code, b)
	}
}

func TestCompleteSnapshotOnlyAcceptsBothAtomicStates(t *testing.T) {
	for _, tc := range []struct {
		totals [4]int
		want   bool
	}{
		{[4]int{1, 0, 0, 1}, true}, {[4]int{0, 1, 1, 0}, true},
		{[4]int{1, 1, 0, 0}, false}, {[4]int{0, 1, 0, 1}, false}, {[4]int{1, 0, 1, 0}, false},
	} {
		if got := completeSnapshot(tc.totals); got != tc.want {
			t.Errorf("%v accepted=%v", tc.totals, got)
		}
	}
}

func TestDoctorContractRejectsMalformedAndIncompleteJSON(t *testing.T) {
	if doctorHealthy([]byte(`{"revision":`), strings.Repeat("a", 40), 8) {
		t.Fatal("malformed JSON passed")
	}
	valid := []byte(`{"revision":"` + strings.Repeat("a", 40) + `","current_write_schema":8,"index_schema":8,"checks":[{"id":"integrity_quick_check","status":"ok"},{"id":"integrity_foreign_key_check","status":"ok"}]}`)
	if !doctorHealthy(valid, strings.Repeat("a", 40), 8) {
		t.Fatal("valid doctor JSON rejected")
	}
	if doctorHealthy(bytes.Replace(valid, []byte(`"status":"ok"`), []byte(`"status":"error"`), 1), strings.Repeat("a", 40), 8) {
		t.Fatal("failed integrity check passed")
	}
}

func TestStopGroupKillsLeaderAndDescendant(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process groups")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	heartbeat := filepath.Join(t.TempDir(), "heartbeat")
	// A short-lived shell leader leaves a writing descendant in its process group.
	command := "(while :; do printf x >> '" + heartbeat + "'; done) & exit 0"
	c, err := start(ctx, "/bin/sh", "-c", command)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.wait(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	after, err := os.Stat(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() != after.Size() {
		t.Fatalf("descendant kept writing: %d -> %d", before.Size(), after.Size())
	}
}

func TestTimeoutKillsProcessGroup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process groups")
	}
	heartbeat := filepath.Join(t.TempDir(), "heartbeat")
	command := "(while :; do printf x >> '" + heartbeat + "'; done) & sleep 30"
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	c, err := start(ctx, "/bin/sh", "-c", command)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.wait(ctx); err == nil {
		t.Fatal("expected deadline")
	}
	before, err := os.Stat(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	after, err := os.Stat(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() != after.Size() {
		t.Fatalf("descendant survived timeout: %d -> %d", before.Size(), after.Size())
	}
}
