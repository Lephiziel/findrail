package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Lephiziel/findrail/internal/devtools/connectorkit"
)

func main() {
	root := flag.String("repo-root", ".", "repository root (defaults to the current directory)")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(2)
	}
	if err := connectorkit.Run(*root); err != nil {
		fmt.Fprintln(os.Stderr, "connector kit:", err)
		os.Exit(1)
	}
}
