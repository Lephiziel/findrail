package main

import (
	"context"
	"errors"
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
	args := os.Args[1:]
	// The executable also hosts the bounded extraction child and version query.
	// Those must reach CLI dispatch without starting or locking the desktop app.
	if !(len(args) == 1 && (args[0] == "__extract-pdf" || args[0] == "version")) {
		args = append([]string{"desktop"}, args...)
	}
	if err := cli.Run(ctx, args, os.Stdout, os.Stderr, version); err != nil {
		machine := os.Getenv("CI") != ""
		err = desktop.ReportStartupFailure(os.Args[1:], err, os.Stderr, desktop.NotifyStartupFailure, machine)
		var exit interface{ ExitCode() int }
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}
