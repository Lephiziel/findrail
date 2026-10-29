package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// StartupNotifier displays a plain-text message without interpreting it as a
// command. It is injected by callers so tests never open native UI.
type StartupNotifier func(message string) error

const maxStartupFailureMessage = 512

const startupFailureMessage = "Findrail could not start.\n\nCheck that you can access the selected data directory. For detailed diagnostics, open a terminal in the extracted folder and run findrail desktop with the same options."

// ReportStartupFailure preserves the original CLI diagnostic and error while
// optionally showing a bounded, non-sensitive message to an interactive user.
func ReportStartupFailure(args []string, failure error, stderr io.Writer, notify StartupNotifier, machine bool) error {
	if failure == nil {
		return nil
	}
	_, _ = fmt.Fprintln(stderr, "findrail-desktop:", failure)
	if ShouldNotifyStartupFailure(args, failure, machine) && notify != nil {
		_ = notify(startupFailureMessage)
	}
	return failure
}

// ShouldNotifyStartupFailure excludes worker, version/help, cancellation, and
// explicitly machine-controlled runs. It classifies the invocation, not the
// arbitrary error text returned by the CLI.
func ShouldNotifyStartupFailure(args []string, failure error, machine bool) bool {
	if failure == nil || machine || errors.Is(failure, context.Canceled) || errors.Is(failure, context.DeadlineExceeded) {
		return false
	}
	if len(args) == 1 {
		switch args[0] {
		case "__extract-pdf", "version", "help", "--help", "-h":
			return false
		}
	}
	return len(startupFailureMessage) <= maxStartupFailureMessage
}

// NotifyStartupFailure delegates to the platform's native notification. On
// platforms without a native implementation, stderr remains the fallback.
func NotifyStartupFailure(message string) error {
	return notifyStartupFailure(message)
}
