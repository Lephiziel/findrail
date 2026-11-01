package connectorkit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const processTreeTestRole = "FINDRAIL_CONNECTORKIT_TREE_TEST_ROLE"

func TestCommandStopsOwnedDescendantsOnTimeout(t *testing.T) {
	if os.Getenv(processTreeTestRole) != "" {
		runProcessTreeTestHelper(t)
		return
	}
	assertTreeStops(t, "timeout", 500*time.Millisecond, 4*time.Second, true)
}

func TestCommandStopsOwnedDescendantsAfterLeaderExits(t *testing.T) {
	if os.Getenv(processTreeTestRole) != "" {
		runProcessTreeTestHelper(t)
		return
	}
	assertTreeStops(t, "leader-exit", 5*time.Second, 5*time.Second, false)
}

func assertTreeStops(t *testing.T, mode string, timeout, budget time.Duration, wantError bool) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	heartbeat := filepath.Join(dir, "heartbeat")
	r := runner{root: dir, env: replaceEnv(os.Environ(), map[string]string{processTreeTestRole: mode, "FINDRAIL_CONNECTORKIT_TREE_HEARTBEAT": heartbeat}), base: context.Background(), goPath: exe}
	started := time.Now()
	err = r.command(dir, timeout, nil, "-test.run=^TestCommandStopsOwnedDescendantsOnTimeout$")
	if (err != nil) != wantError {
		t.Fatalf("command error = %v, want error %t", err, wantError)
	}
	if time.Since(started) > budget {
		t.Fatalf("command cleanup exceeded budget %s: %v", budget, time.Since(started))
	}
	before, err := os.Stat(heartbeat)
	if err != nil {
		t.Fatalf("descendant did not publish readiness heartbeat: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	after, err := os.Stat(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() > before.Size() {
		t.Fatalf("owned descendant kept writing after command returned: %d -> %d", before.Size(), after.Size())
	}
}

func runProcessTreeTestHelper(t *testing.T) {
	role := os.Getenv(processTreeTestRole)
	heartbeat := os.Getenv("FINDRAIL_CONNECTORKIT_TREE_HEARTBEAT")
	if role == "descendant" {
		for {
			f, err := os.OpenFile(heartbeat, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				os.Exit(31)
			}
			_, _ = f.Write([]byte("x"))
			_ = f.Close()
			time.Sleep(10 * time.Millisecond)
		}
	}
	if role != "timeout" && role != "leader-exit" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		os.Exit(32)
	}
	child := exec.Command(exe, "-test.run=^TestCommandStopsOwnedDescendantsOnTimeout$")
	child.Env = replaceEnv(os.Environ(), map[string]string{processTreeTestRole: "descendant", "FINDRAIL_CONNECTORKIT_TREE_HEARTBEAT": heartbeat})
	if err := child.Start(); err != nil {
		os.Exit(33)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(heartbeat); err == nil && info.Size() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	info, err := os.Stat(heartbeat)
	if err != nil || info.Size() == 0 {
		child.Process.Kill()
		os.Exit(34)
	}
	if role == "leader-exit" {
		return
	}
	time.Sleep(time.Hour)
}

func TestReplaceEnvOverridesProcessTreeTestMarker(t *testing.T) {
	if got := replaceEnv([]string{processTreeTestRole + "=old"}, map[string]string{processTreeTestRole: "new"}); len(got) != 1 || !strings.HasSuffix(got[0], "=new") {
		t.Fatalf("test environment marker was not replaced: %v", got)
	}
}
