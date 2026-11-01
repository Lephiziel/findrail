//go:build linux || darwin

package filesystem

import (
	"os"
	"syscall"
)

// Open nonblocking and without following a final symlink. A racing FIFO/device
// replacement must not turn policy loading into a blocking content read.
func openIgnoreFile(root *os.Root) (*os.File, error) {
	return root.OpenFile(".findrailignore", os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
