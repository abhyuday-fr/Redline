// Command redline is the CLI entrypoint.
package main

import (
	"fmt"
	"os"

	"github.com/lawless/redline/internal/cli"
)

// Populated at build time via -ldflags by .goreleaser.yaml. Defaults below
// are what you see when running a plain `go build` without those flags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("redline %s (commit %s, built %s)\n", version, commit, date)
		return
	}
	os.Exit(cli.Execute(os.Args[1:]))
}
