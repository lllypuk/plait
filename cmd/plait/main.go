// Command plait guides a coding agent through the team dev-flow.
package main

import (
	"os"

	"github.com/lllypuk/plait/internal/cli"
)

// version is set by the linker via -ldflags -X.
var version string

func main() {
	if err := cli.NewRoot(version).Execute(); err != nil {
		os.Exit(1)
	}
}
