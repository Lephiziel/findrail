package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Lephiziel/findrail/internal/config"
	"github.com/Lephiziel/findrail/internal/desktop"
)

func runDesktop(ctx context.Context, args []string, out, stderr io.Writer, open func(string) error, version ...string) error {
	dataArg := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--data-dir" && i+1 < len(args) {
			dataArg = args[i+1]
			i++
		} else if v, ok := strings.CutPrefix(args[i], "--data-dir="); ok {
			dataArg = v
		}
	}
	dir, err := config.DataDir(dataArg)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create desktop data directory: %w", err)
	}
	lock, err := desktop.Acquire(dir)
	if errors.Is(err, desktop.ErrOwned) {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			record, readErr := desktop.ReadRecord(dir)
			if readErr == nil && desktop.Verify(record) {
				if _, e := fmt.Fprintf(out, "Findrail is already running: %s\n", record.URL); e != nil {
					return e
				}
				if openErr := open(record.URL); openErr != nil {
					fmt.Fprintf(stderr, "Could not open the existing Findrail window: %v. Open %s manually.\n", openErr, record.URL)
				}
				return nil
			}
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				return fmt.Errorf("cannot verify running desktop instance: %w", readErr)
			}
			time.Sleep(40 * time.Millisecond)
		}
		return errors.New("desktop instance owns this data directory but its endpoint could not be verified")
	}
	if err != nil {
		return err
	}
	return runStartWithManagement(ctx, args, out, stderr, open, true, lock, version...)
}
