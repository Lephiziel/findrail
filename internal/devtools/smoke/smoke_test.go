package smoke

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	helperRoleEnv = "FINDRAIL_SMOKE_TEST_ROLE"
	helperFileEnv = "FINDRAIL_SMOKE_TEST_FILE"
)

func TestCappedBufferRejectsOverflow(t *testing.T) {
	var buffer cappedBuffer
	if _, err := buffer.Write([]byte(strings.Repeat("x", outputLimit+100))); err != nil {
		t.Fatal(err)
	}
	if got := len(buffer.String()); got != outputLimit {
		t.Fatalf("captured %d bytes, want %d", got, outputLimit)
	}
	if !buffer.overflow {
		t.Fatal("overflow was not recorded")
	}
}

func TestDiagnosticExamplesRejectSensitiveSyntheticNames(t *testing.T) {
	for _, path := range []string{"secret-token.txt", "~$private_key.docx", ".secret/not-enumerated.md"} {
		report := map[string]any{
			"examples":         []any{map[string]any{"path": path}},
			"examples_omitted": float64(1),
		}
		if err := validateDiagnosticExamples(report); err == nil {
			t.Errorf("sensitive synthetic example %q was accepted", path)
		}
	}
	report := map[string]any{"examples": []any{map[string]any{"path": "picture.png"}}, "examples_omitted": float64(1)}
	if err := validateDiagnosticExamples(report); err != nil {
		t.Fatalf("ordinary diagnostic example rejected: %v", err)
	}
}

func TestCandidateJSONRejectsTruncatedOverflow(t *testing.T) {
	t.Setenv(helperRoleEnv, "overflow-success")
	if value, err := runCandidateJSON(testExecutable(t), "-test.run=^TestSmokeProcessHelper$"); err == nil {
		t.Fatalf("oversized JSON accepted after truncation: %v", value)
	}
}

func TestCandidateFailureRejectsTruncatedOverflow(t *testing.T) {
	t.Setenv(helperRoleEnv, "overflow-failure")
	if value, err := runCandidateFailure(testExecutable(t), "-test.run=^TestSmokeProcessHelper$"); err == nil {
		t.Fatalf("oversized failure JSON accepted after truncation: %v", value)
	}
}

func TestCandidateRejectsInvalidJSON(t *testing.T) {
	t.Setenv(helperRoleEnv, "invalid-json")
	if value, err := runCandidateJSON(testExecutable(t), "-test.run=^TestSmokeProcessHelper$"); err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("invalid command JSON accepted: %v, %v", value, err)
	}
}

func TestCandidateFailureAcceptsExpectedNonzeroJSON(t *testing.T) {
	t.Setenv(helperRoleEnv, "failure-json")
	value, err := runCandidateFailure(testExecutable(t), "-test.run=^TestSmokeProcessHelper$")
	if err != nil || value["expected_failure"] != true {
		t.Fatalf("expected failure JSON not preserved: value=%v err=%v", value, err)
	}
}

func TestCandidateMissingBinaryIsReported(t *testing.T) {
	if _, err := runCandidate(testExecutable(t) + ".missing-binary"); err == nil {
		t.Fatal("missing candidate binary was accepted")
	}
}

func TestHTTPRejectsTruncatedOverflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`+strings.Repeat(" ", outputLimit+100)+"INVALID_TRAILING_DATA")
	}))
	defer server.Close()
	client := newHTTPClient(server.URL)
	if value, err := client.get("/"); err == nil {
		t.Errorf("oversized HTTP GET JSON accepted after truncation: %v", value)
	}
	if value, err := client.post("/", "synthetic-token", nil); err == nil {
		t.Errorf("oversized HTTP POST JSON accepted after truncation: %v", value)
	}
}

func TestCandidateDeadlineKillsDescendantsHoldingPipes(t *testing.T) {
	assertDeadlineStopsChild(t, "tree-parent")
}

func TestCandidateDeadlineKillsDescendantAfterLeaderExit(t *testing.T) {
	assertDeadlineStopsChild(t, "tree-leader-exit")
}

func TestServerReadinessReportsEarlyExit(t *testing.T) {
	t.Setenv(helperRoleEnv, "early-exit")
	server, err := startCandidate(testExecutable(t), "-test.run=^TestSmokeProcessHelper$")
	if err != nil {
		t.Fatal(err)
	}
	defer server.stop()
	err = server.waitReady(newHTTPClient("http://127.0.0.1:1"), 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "server exited before readiness") {
		t.Fatalf("early server exit was not reported: %v", err)
	}
}

func assertDeadlineStopsChild(t *testing.T, role string) {
	t.Helper()
	exe := testExecutable(t)
	pidfile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv(helperRoleEnv, role)
	t.Setenv(helperFileEnv, pidfile)
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := runCandidateTimeout(400*time.Millisecond, exe, "-test.run=^TestSmokeProcessHelper$")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "deadline") {
			t.Fatalf("expected bounded deadline failure, got %v", err)
		}
	case <-time.After(4 * time.Second):
		stopTestChild(t, pidfile)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
		t.Fatal("deadline failed to bound process and inherited-pipe cleanup")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("process cleanup exceeded finite budget: %s", elapsed)
	}
	pid, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatalf("descendant did not publish PID: %v", err)
	}
	before, err := os.Stat(heartbeatPath(pidfile))
	if err != nil {
		t.Fatalf("descendant did not publish readiness heartbeat: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	after, err := os.Stat(heartbeatPath(pidfile))
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() > before.Size() {
		stopTestChild(t, pidfile)
		t.Fatalf("owned descendant %s continued writing after runner returned", strings.TrimSpace(string(pid)))
	}
}

func TestSmokeProcessHelper(t *testing.T) {
	switch os.Getenv(helperRoleEnv) {
	case "overflow-success":
		_, _ = fmt.Fprint(os.Stdout, `{"ok":true}`+strings.Repeat(" ", outputLimit+100)+"INVALID_TRAILING_DATA")
		return
	case "overflow-failure":
		_, _ = fmt.Fprint(os.Stdout, `{"ok":true}`+strings.Repeat(" ", outputLimit+100)+"INVALID_TRAILING_DATA")
		os.Exit(7)
	case "invalid-json":
		_, _ = fmt.Fprint(os.Stdout, "not-json\n")
	case "failure-json":
		_, _ = fmt.Fprint(os.Stdout, `{"expected_failure":true}`)
		os.Exit(7)
	case "tree-parent", "tree-leader-exit":
		startTestDescendant(t)
		if os.Getenv(helperRoleEnv) == "tree-leader-exit" {
			return
		}
		for {
			time.Sleep(time.Hour)
		}
	case "tree-descendant":
		path := heartbeatPath(os.Getenv(helperFileEnv))
		for {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				os.Exit(41)
			}
			_, _ = f.Write([]byte("x"))
			_ = f.Close()
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func startTestDescendant(t *testing.T) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "-test.run=^TestSmokeProcessHelper$")
	child.Env = replaceTestEnv(os.Environ(), map[string]string{helperRoleEnv: "tree-descendant"})
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv(helperFileEnv), []byte(fmt.Sprint(child.Process.Pid)), 0o600); err != nil {
		_ = child.Process.Kill()
		t.Fatal(err)
	}
	heartbeat := heartbeatPath(os.Getenv(helperFileEnv))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(heartbeat); err == nil && info.Size() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = child.Process.Kill()
	t.Fatal("descendant heartbeat readiness timed out")
}

func testExecutable(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func heartbeatPath(pidfile string) string { return pidfile + ".heartbeat" }

func stopTestChild(t *testing.T, pidfile string) {
	t.Helper()
	raw, err := os.ReadFile(pidfile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return
	}
	process, err := os.FindProcess(pid)
	if err == nil {
		_ = process.Kill()
	}
}

func replaceTestEnv(base []string, values map[string]string) []string {
	result := make([]string, 0, len(base)+len(values))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, overridden := values[key]; !overridden {
			result = append(result, entry)
		}
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}
