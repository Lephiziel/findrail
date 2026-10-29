package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// StartupNotifier displays a fixed, user-facing startup message. The original
// error is deliberately not passed to the native notification surface.
type StartupNotifier func(string) error

const startupNotice = "Findrail could not start.\n\nCheck that the selected data folder exists and is accessible. For details, open a terminal in the app folder and run .\\findrail.exe doctor. Do not delete or replace your index."

// IsDesktopInvocation excludes known machine/CLI commands from modal startup
// errors. The launcher still dispatches those commands as before; this only
// classifies whether an interactive GUI notice is appropriate.
func IsDesktopInvocation(args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "version", "__extract-pdf", "help", "--help", "-h", "--version",
		"index", "index-github", "refresh-github", "start", "demo", "search",
		"evaluate", "sources", "source-report", "export-source", "inspect-export",
		"import-source", "forget", "serve", "watch", "explain-path", "mcp",
		"mcp-config", "doctor":
		return false
	default:
		return true
	}
}

// StartupDialogAllowed is kept platform-independent so policy can be tested
// without opening a native window. CI and explicit automation always retain the
// stderr fallback instead of waiting for a person to dismiss a dialog.
func StartupDialogAllowed(args []string, nativeAvailable, hasConsole bool, lookupEnv func(string) (string, bool)) bool {
	if !nativeAvailable || hasConsole || !IsDesktopInvocation(args) {
		return false
	}
	if lookupEnv == nil {
		return true
	}
	for _, name := range []string{"CI", "GITHUB_ACTIONS", "FINDRAIL_NO_GUI_ERRORS"} {
		if value, ok := lookupEnv(name); ok && value != "" && value != "0" && !strings.EqualFold(value, "false") {
			return false
		}
	}
	return true
}

// ReportStartupFailure preserves the CLI's stderr and exit-code contract. A
// notification failure is intentionally ignored; it must not mask the cause.
func ReportStartupFailure(err error, allowDialog bool, stderr io.Writer, notify StartupNotifier) int {
	if err == nil {
		return 0
	}
	_, _ = fmt.Fprintln(stderr, "findrail-desktop:", err)
	if allowDialog && notify != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		_ = notify(startupNotice)
	}
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return 1
}
