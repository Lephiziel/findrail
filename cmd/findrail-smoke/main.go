// Command findrail-smoke runs development-only black-box acceptance helpers.
// It is not included in application archives.
package main

import (
	"fmt"
	"os"

	"github.com/Lephiziel/findrail/internal/devtools/smoke"
)

func main() {
	if err := smoke.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "findrail-smoke: %v\n", err)
		os.Exit(1)
	}
}
