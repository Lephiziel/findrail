package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCappedBufferBoundsOutput(t *testing.T) {
	var buffer cappedBuffer
	if _, err := buffer.Write([]byte(strings.Repeat("x", outputLimit+100))); err != nil {
		t.Fatal(err)
	}
	if len(buffer.String()) != outputLimit {
		t.Fatalf("captured %d bytes, want %d", len(buffer.String()), outputLimit)
	}
}

func TestCandidateRejectsInvalidJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX test executable")
	}
	binary := testScript(t, "printf 'not-json\\n'")
	if _, err := runCandidateJSON(binary); err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("invalid JSON was accepted: %v", err)
	}
}

func TestCandidateReportsEarlyExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX test executable")
	}
	binary := testScript(t, "printf '{\"ok\":false}\\n'; exit 7")
	if _, err := runCandidate(binary); err == nil || !strings.Contains(err.Error(), "exited 7") {
		t.Fatalf("early exit was not reported: %v", err)
	}
}

func TestCandidateDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX test executable")
	}
	binary := testScript(t, "sleep 2")
	if _, err := runCandidateTimeout(20*time.Millisecond, binary); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("deadline was not reported: %v", err)
	}
}

func testScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "candidate")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
