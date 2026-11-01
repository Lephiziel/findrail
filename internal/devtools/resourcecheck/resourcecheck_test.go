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

func TestAtomicReportRejectsSymlinkTargetAndDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux report path contract")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "report.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(link, map[string]string{"status": "failed"}); err == nil {
		t.Fatal("symlink output path accepted")
	}
	parentLink := filepath.Join(root, "linked-dir")
	if err := os.Symlink(root, parentLink); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(filepath.Join(parentLink, "other.json"), map[string]string{"status": "failed"}); err == nil {
		t.Fatal("symlink report directory accepted")
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "keep" {
		t.Fatalf("symlink target modified: %q, %v", b, err)
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

func TestRecoveryRejectsMissingBinaryIdentityAndHashMismatch(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux recovery preflight")
	}
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		opts RecoveryOptions
		code string
	}{
		{"missing binary", RecoveryOptions{Revision: strings.Repeat("a", 40), Schema: 8, WorkDir: dir, Report: filepath.Join(dir, "missing.json"), Deadline: 45}, "invalid_binary"},
		{"missing revision", RecoveryOptions{Binary: exe, Schema: 8, WorkDir: dir, Report: filepath.Join(dir, "revision.json"), Deadline: 45}, "invalid_identity"},
		{"invalid revision", RecoveryOptions{Binary: exe, Revision: "short", Schema: 8, WorkDir: dir, Report: filepath.Join(dir, "bad-revision.json"), Deadline: 45}, "invalid_identity"},
		{"missing schema", RecoveryOptions{Binary: exe, Revision: strings.Repeat("a", 40), WorkDir: dir, Report: filepath.Join(dir, "schema.json"), Deadline: 45}, "invalid_identity"},
		{"hash mismatch", RecoveryOptions{Binary: exe, ExpectedHash: strings.Repeat("0", 64), Revision: strings.Repeat("a", 40), Schema: 8, WorkDir: dir, Report: filepath.Join(dir, "hash.json"), Deadline: 45}, "binary_hash_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := RunRecovery(context.Background(), tc.opts); code == 0 {
				t.Fatal("invalid preflight passed")
			}
			b, e := os.ReadFile(tc.opts.Report)
			if e != nil {
				t.Fatal(e)
			}
			var r recoveryReport
			if json.Unmarshal(b, &r) != nil || r.FailureCode != tc.code || r.Status != "failed" {
				t.Fatalf("expected %s failure, got %s", tc.code, b)
			}
		})
	}
}

func TestRecoveryRejectsNonFiniteAndOutOfRangeDeadlines(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux recovery preflight")
	}
	dir := t.TempDir()
	for name, deadline := range map[string]float64{"nan": math.NaN(), "positive infinity": math.Inf(1), "negative infinity": math.Inf(-1), "zero": 0, "over limit": 121} {
		t.Run(name, func(t *testing.T) {
			report := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
			code := RunRecovery(context.Background(), RecoveryOptions{Revision: strings.Repeat("a", 40), Schema: 8, WorkDir: dir, Report: report, Deadline: deadline})
			b, err := os.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			var r recoveryReport
			if json.Unmarshal(b, &r) != nil || code == 0 || r.FailureCode != "invalid_deadline" {
				t.Fatalf("code=%d report=%s", code, b)
			}
		})
	}
}

func TestRecoveryRejectsEarlyCandidateExit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process observation")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "fake-findrail")
	script := "#!/bin/sh\ncase \"$1\" in\n index)\n  shift; data=\"\"; while [ $# -gt 0 ]; do if [ \"$1\" = --data-dir ]; then data=\"$2\"; shift 2; else shift; fi; done\n  mkdir -p \"$data\"; if [ -e \"$data/seeded\" ]; then exit 0; fi; touch \"$data/seeded\"; exit 0;;\n search) echo '{\"total\":1}'; exit 0;;\n *) exit 1;;\nesac\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(dir, "report.json")
	code := RunRecovery(context.Background(), RecoveryOptions{Binary: binary, Revision: strings.Repeat("a", 40), Schema: 8, WorkDir: dir, Report: report, Deadline: 5})
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var r recoveryReport
	if json.Unmarshal(b, &r) != nil {
		t.Fatalf("invalid report %s", b)
	}
	if code == 0 || r.Status != "failed" || r.FailureCode != "early_exit" || r.Phase != "candidate_index_started" {
		t.Fatalf("expected early-exit failure, got %s", b)
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
	if doctorHealthy(valid, strings.Repeat("b", 40), 8) || doctorHealthy(valid, strings.Repeat("a", 40), 7) {
		t.Fatal("doctor identity mismatch passed")
	}
	if doctorHealthy(bytes.Replace(valid, []byte(`"status":"ok"`), []byte(`"status":"error"`), 1), strings.Repeat("a", 40), 8) {
		t.Fatal("failed integrity check passed")
	}
}

func TestOversizedChildOutputIsBoundedAndKilled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := start(ctx, "/bin/sh", "-c", "yes oversized-output")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.wait(ctx); err == nil || err.Error() != "oversized output" {
		t.Fatalf("wait error=%v, want bounded-output failure", err)
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
	command := "(while :; do printf x >> '" + heartbeat + "'; done) & while [ ! -f '" + heartbeat + "' ]; do sleep .005; done; exit 0"
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
	command := "(while :; do printf x >> '" + heartbeat + "'; done) & while [ ! -f '" + heartbeat + "' ]; do sleep .005; done; sleep 30"
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
