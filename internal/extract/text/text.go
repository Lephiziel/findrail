package text

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

var ErrUnsupported = errors.New("unsupported text encoding or binary content")
var ErrTooLarge = errors.New("document exceeds the configured size limit")

// Read validates and bounds extracted text. It deliberately does not interpret
// HTML, execute Markdown, run source code, or call external services.
func Read(r io.Reader, maxBytes int64) (string, error) {
	if maxBytes < 1 {
		return "", fmt.Errorf("max bytes must be positive")
	}
	b, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(b)) > maxBytes {
		return "", ErrTooLarge
	}
	b = bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return "", ErrUnsupported
	}
	return string(b), nil
}
