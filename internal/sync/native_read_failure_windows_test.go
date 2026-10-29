package sync_test

import (
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

func blockNativeFileRead(t *testing.T, path string) func() {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(
		name,
		syscall.GENERIC_READ,
		0,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("could not establish an exclusive native sharing fixture: %v", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			if err := syscall.CloseHandle(handle); err != nil {
				t.Errorf("could not release native sharing fixture: %v", err)
			}
		})
	}
}
