// Command devctl inspects and safely tidies git worktrees and agent-stack config.
package main

import (
	"fmt"
	"os"

	"github.com/gonaas/devctl/internal/app"
)

// version is replaced at build time with -X main.version=<tag>.
var version = "dev"

func main() {
	app.Version = app.ResolveVersion(version)
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "devctl: %v\n", err)
		os.Exit(1)
	}
}
