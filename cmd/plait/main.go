// Command plait guides a coding agent through the team dev-flow.
package main

import (
	"os"

	"github.com/lllypuk/plait/internal/cli"
)

// version is set by the linker via -ldflags -X.
var version string

func main() {
	os.Exit(run())
}

func run() int {
	return cli.Run(cli.Options{Version: version, Args: os.Args[1:], Stdout: os.Stdout, Stderr: os.Stderr})
}
