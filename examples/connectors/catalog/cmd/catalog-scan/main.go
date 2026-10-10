// catalog-scan demonstrates an explicit read-only inventory, not a Findrail plugin protocol.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"example.com/findrail-catalog"
	"github.com/Lephiziel/findrail/pkg/connector"
)

func main() {
	origin := flag.String("origin", "", "explicit HTTPS API origin")
	collection := flag.String("collection", "", "collection ID")
	flag.Parse()
	if *origin == "" || *collection == "" {
		fmt.Fprintln(os.Stderr, "catalog-scan: --origin and --collection are required")
		os.Exit(2)
	}
	adapter, err := catalog.New(catalog.Config{Origin: *origin, Collection: *collection})
	if err != nil {
		safeExit(err)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "source", "source": adapter.Source()})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := adapter.Scan(ctx, func(d connector.Document) error {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "document", "document": d})
	})
	if err != nil {
		safeExit(err)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "complete", "report": report})
}
func safeExit(_ error) {
	fmt.Fprintln(os.Stderr, "catalog-scan: scan failed; emitted documents are provisional; no completion record was written")
	os.Exit(1)
}
