package main

import (
	"fmt"
	"os"

	"github.com/petal-labs/cortex/internal/cmd"
)

// Build information, injected at link time by the release workflow and the
// Makefile via -X main.<name>. These must stay in package main with these
// exact names: the linker silently ignores an -X flag naming a symbol that
// does not exist, which is how these went unpopulated through v1.0.0.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	cmd.SetVersionInfo(version, commit, date)

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
