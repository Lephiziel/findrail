// catalog-scan demonstrates an explicit read-only inventory, not a Findrail plugin protocol.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	catalog "example.com/findrail-catalog"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("catalog-scan", flag.ContinueOnError)
	flags.SetOutput(errOut)
	origin := flags.String("origin", "", "explicit HTTPS API origin")
	collection := flags.String("collection", "", "collection ID")
	loopback := flags.Bool("allow-loopback-http", false, "allow cleartext numeric loopback for local fixtures only")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *origin == "" || *collection == "" {
		fmt.Fprintln(errOut, "catalog-scan: --origin and --collection are required")
		return 2
	}
	adapter, err := catalog.New(catalog.Config{Origin: *origin, Collection: *collection, AllowLoopbackHTTP: *loopback, RequestTimeout: 5 * time.Second})
	if err != nil {
		fmt.Fprintln(errOut, "catalog-scan: invalid configuration")
		return 2
	}
	if err := json.NewEncoder(out).Encode(map[string]any{"type": "source", "source": adapter.Source()}); err != nil {
		return fail(errOut)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := adapter.Scan(ctx, func(d connector.Document) error {
		return json.NewEncoder(out).Encode(map[string]any{"type": "document", "document": d})
	})
	if err != nil {
		return fail(errOut)
	}
	if err := json.NewEncoder(out).Encode(map[string]any{"type": "complete", "report": report}); err != nil {
		return fail(errOut)
	}
	return 0
}

func fail(out io.Writer) int {
	fmt.Fprintln(out, "catalog-scan: scan failed; emitted documents are provisional; no completion record was written")
	return 1
}
