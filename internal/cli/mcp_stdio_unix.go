//go:build unix

package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

// Inherited Unix stdio files are normally blocking and outside Go's poller.
// Closing such a file cannot interrupt an in-flight system read or write.
// A nonblocking duplicate lets Go wait in its poller and cancel both directions.
func prepareMCPFile(file *os.File) (*os.File, func(), error) {
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&(os.ModeNamedPipe|os.ModeSocket|os.ModeCharDevice) == 0 {
		return file, func() {}, nil
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	duplicate, flags := -1, 0
	var setupErr error
	err = raw.Control(func(fd uintptr) {
		flags, setupErr = unix.FcntlInt(fd, unix.F_GETFL, 0)
		if setupErr != nil {
			return
		}
		duplicate, setupErr = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0)
	})
	if err != nil {
		return nil, nil, err
	}
	if setupErr != nil {
		return nil, nil, setupErr
	}
	if err := unix.SetNonblock(duplicate, true); err != nil {
		_ = unix.Close(duplicate)
		return nil, nil, err
	}
	stream := os.NewFile(uintptr(duplicate), file.Name())
	cleanup := func() {
		_ = stream.Close()
		// Duplicates share status flags. Restore the caller's original mode,
		// using RawConn so a closed/reused descriptor is never modified.
		_ = raw.Control(func(fd uintptr) {
			_ = unix.SetNonblock(int(fd), flags&unix.O_NONBLOCK != 0)
		})
	}
	return stream, cleanup, nil
}
