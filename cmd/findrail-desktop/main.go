package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Lephiziel/findrail/internal/cli"
	"github.com/Lephiziel/findrail/internal/desktop"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr,
		desktop.StartupDialogAllowed(os.Args[1:], desktop.StartupDialogPlatformAvailable(), desktop.HasInteractiveConsole(), os.LookupEnv),
		desktop.NotifyNativeStartupFailure)
	if code != 0 {
		os.Exit(code)
	}
}

func run(ctx context.Context, args []string, out, stderr io.Writer, allowDialog bool, notify desktop.StartupNotifier) int {
	cliArgs := args
	// The executable also hosts the bounded extraction child and version query.
	// Those must reach CLI dispatch without starting or locking the desktop app.
	if !(len(cliArgs) == 1 && (cliArgs[0] == "__extract-pdf" || cliArgs[0] == "version")) {
		cliArgs = append([]string{"desktop"}, cliArgs...)
	}
	err := cli.Run(ctx, cliArgs, out, stderr, version)
	return desktop.ReportStartupFailure(err, allowDialog && desktop.IsDesktopInvocation(args), stderr, notify)
}
