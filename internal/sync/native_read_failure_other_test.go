//go:build !windows

package sync_test

import (
	"os"
	"testing"
)

func blockNativeFileRead(t *testing.T, path string) func() {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mode := info.Mode().Perm()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("could not establish a local read-denial fixture: %v", err)
	}
	restore := func() {
		if err := os.Chmod(path, mode); err != nil {
			t.Errorf("could not restore synthetic fixture permissions: %v", err)
		}
	}
	file, openErr := os.Open(path)
	if openErr == nil {
		_ = file.Close()
		restore()
		t.Skip("local filesystem/account permits reading a mode-000 fixture; native permission denial is unavailable")
	}
	return restore
}
