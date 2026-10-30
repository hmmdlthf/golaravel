package main

import (
	"fmt"
	"os"

	"github.com/golaravel/golaravel/pkg/artisan"
)

func main() {
	registry := artisan.NewRegistry()
	if err := registry.Execute(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "\033[31mError:\033[0m %v\n", err)
		os.Exit(1)
	}
}
