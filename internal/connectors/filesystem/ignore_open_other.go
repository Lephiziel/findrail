//go:build !linux && !darwin

package filesystem

import "os"

// Root confines the open on platforms without the Unix nonblocking flags;
// metadata/type and SameFile checks reject unsupported entries/replacements.
func openIgnoreFile(root *os.Root) (*os.File, error) {
	return root.OpenFile(".findrailignore", os.O_RDONLY, 0)
}
