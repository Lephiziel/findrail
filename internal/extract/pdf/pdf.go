// Package pdf extracts text in a short-lived child of the Findrail executable.
// The child limits input, pages and output; its deadline is enforced by the parent.
package pdf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Lephiziel/findrail/internal/extract/text"
	"github.com/Lephiziel/findrail/pkg/connector"
	reader "github.com/ledongthuc/pdf"
)

const MaxInput int64 = 32 << 20
const MaxText = 1 << 20
const MaxPages = 500
const workerOutputLimit = 8 << 20

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("PDF worker output limit exceeded")
	}
	return b.Buffer.Write(p)
}

// Extractor never invokes a shell or an executable from an indexed document.
func Extractor(executable string) func(context.Context, io.Reader, int64) ([]connector.Page, error) {
	return func(ctx context.Context, input io.Reader, size int64) ([]connector.Page, error) {
		if size < 0 || size > MaxInput {
			return nil, text.ErrTooLarge
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, "__extract-pdf")
		cmd.Stdin = io.LimitReader(input, size+1)
		stdout := &boundedBuffer{limit: workerOutputLimit}
		stderr := &boundedBuffer{limit: 4096}
		cmd.Stdout, cmd.Stderr = stdout, stderr
		cmd.Env = append(os.Environ(), "GOMEMLIMIT=128MiB") // soft GC target, not an OS memory sandbox
		cmd.WaitDelay = time.Second
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("PDF extraction interrupted: %w", ctx.Err())
			}
			return nil, errors.New("PDF extraction failed (invalid, encrypted, or resource limit exceeded)")
		}
		var pages []connector.Page
		if err := json.Unmarshal(stdout.Bytes(), &pages); err != nil {
			return nil, errors.New("invalid PDF worker response")
		}
		if len(pages) == 0 {
			return nil, text.ErrUnsupported
		}
		if len(pages) > MaxPages {
			return nil, text.ErrTooLarge
		}
		total := 0
		for i, p := range pages {
			total += len(p.Text)
			if p.Number != i+1 || !utf8.ValidString(p.Text) || strings.ContainsRune(p.Text, 0) {
				return nil, errors.New("invalid PDF page response")
			}
		}
		if total > MaxText {
			return nil, text.ErrTooLarge
		}
		return pages, nil
	}
}

// Worker is called only by the hidden internal command. No files are opened here.
func Worker(ctx context.Context, input io.Reader, output io.Writer) error {
	data, err := io.ReadAll(io.LimitReader(input, MaxInput+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > MaxInput {
		return text.ErrTooLarge
	}
	pages, err := Parse(ctx, data)
	if errors.Is(err, text.ErrUnsupported) || errors.Is(err, text.ErrTooLarge) {
		pages = []connector.Page{}
		err = nil
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(pages)
}

func Parse(ctx context.Context, data []byte) (pages []connector.Page, err error) {
	defer func() {
		if recover() != nil {
			pages = nil
			err = errors.New("invalid PDF structure")
		}
	}()
	if int64(len(data)) > MaxInput {
		return nil, text.ErrTooLarge
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, errors.New("invalid PDF header")
	}
	r, err := reader.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("invalid or encrypted PDF")
	}
	n := r.NumPage()
	if n < 1 || n > MaxPages {
		return nil, text.ErrTooLarge
	}
	pages = make([]connector.Page, 0, n)
	total, hasText := 0, false
	for i := 1; i <= n; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p := r.Page(i)
		if p.V.IsNull() {
			return nil, errors.New("missing PDF page")
		}
		body, err := p.GetPlainText(nil)
		if err != nil {
			return nil, errors.New("invalid PDF page text")
		}
		if !utf8.ValidString(body) || strings.ContainsRune(body, 0) {
			return nil, text.ErrUnsupported
		}
		total += len(body)
		if total > MaxText {
			return nil, text.ErrTooLarge
		}
		if strings.TrimSpace(body) != "" {
			hasText = true
		}
		pages = append(pages, connector.Page{Number: i, Text: body})
	}
	if !hasText {
		return nil, text.ErrUnsupported
	}
	return pages, nil
}
