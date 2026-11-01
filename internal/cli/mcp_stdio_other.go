//go:build !unix

package cli

import "os"

// Windows File.Close already cancels pending I/O on pipe handles.
func prepareMCPFile(file *os.File) (*os.File, func(), error) {
	return file, func() {}, nil
}
