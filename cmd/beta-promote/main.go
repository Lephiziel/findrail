package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Lephiziel/findrail/internal/betapromotion"
)

func main() {
	if err := betapromotion.Main(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
