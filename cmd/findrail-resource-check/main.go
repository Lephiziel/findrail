package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Lephiziel/findrail/internal/devtools/resourcecheck"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "sigkill":
		f := flag.NewFlagSet("sigkill", flag.ExitOnError)
		binary := f.String("binary", "", "explicit candidate executable")
		hash := f.String("binary-sha256", "", "expected SHA-256 (optional)")
		revision := f.String("revision", "", "full application revision")
		schema := f.Int("schema", 0, "application index schema")
		work := f.String("work-dir", "", "owned parent directory for synthetic recovery data")
		report := f.String("report", "", "atomic JSON report path")
		deadline := f.Float64("deadline", 45, "finite total deadline in seconds (1..120)")
		_ = f.Parse(os.Args[2:])
		if f.NArg() != 0 {
			usage()
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		code := resourcecheck.RunRecovery(ctx, resourcecheck.RecoveryOptions{Binary: *binary, ExpectedHash: *hash, Revision: *revision, Schema: *schema, WorkDir: *work, Report: *report, Deadline: *deadline})
		os.Exit(code)
	case "portable-boundary":
		f := flag.NewFlagSet("portable-boundary", flag.ExitOnError)
		binary := f.String("binary", "", "explicit candidate executable")
		_ = f.Parse(os.Args[2:])
		if f.NArg() != 0 {
			usage()
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		result, err := resourcecheck.RunPortable(ctx, *binary)
		if err != nil {
			fmt.Fprintln(os.Stderr, "portable boundary check failed")
			os.Exit(1)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(result); err != nil {
			os.Exit(1)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: findrail-resource-check {sigkill|portable-boundary} ...")
	os.Exit(2)
}
