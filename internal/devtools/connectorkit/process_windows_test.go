package connectorkit

import (
	"errors"
	"syscall"
	"testing"
)

func TestResumeOwnedThreadRejectsInvalidHandle(t *testing.T) {
	const errorInvalidHandle = syscall.Errno(6)
	if err := resumeOwnedThread(0); !errors.Is(err, errorInvalidHandle) {
		t.Fatalf("ResumeThread with an invalid handle returned %v, want ERROR_INVALID_HANDLE", err)
	}
}
