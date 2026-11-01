//go:build !windows

package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Lock serializes desktop owners before any database is opened.
type Lock struct {
	file        *os.File
	DataDir     string
	recordNonce string
}

func Acquire(dataDir string) (*Lock, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve desktop data directory: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("desktop data directory must exist")
	}
	f, err := os.OpenFile(filepath.Join(abs, ".desktop.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrOwned
		}
		return nil, fmt.Errorf("acquire desktop ownership: %w", err)
	}
	return &Lock{file: f, DataDir: abs}, nil
}
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
