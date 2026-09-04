// Package app dispatches command-line arguments to command implementations.
package app

import (
	"fmt"
	"io"
	"os"

	"github.com/gonaas/devctl/internal/cli"
)

// Run dispatches os.Args against standard output.
func Run() error { return RunArgs(os.Args[1:], os.Stdout) }

// RunArgs dispatches one argument list to a writer.
//
// Dispatch is a switch rather than a command tree: the surface is small, the
// routing is obvious to read, and every handler takes the writer so the whole
// CLI can be tested without capturing global state.
func RunArgs(arguments []string, stdout io.Writer) error {
	if len(arguments) == 0 {
		printHelp(stdout, Version)
		return nil
	}

	switch arguments[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "devctl %s\n", Version)
		return nil
	case "help", "--help", "-h":
		printHelp(stdout, Version)
		return nil
	case "projects":
		return cli.RunProjects(arguments[1:], stdout)
	case "worktrees":
		return cli.RunWorktrees(arguments[1:], stdout)
	case "branches":
		return cli.RunBranches(arguments[1:], stdout)
	case "runtime":
		return cli.RunRuntime(arguments[1:], stdout)
	case "config":
		return cli.RunConfig(arguments[1:], stdout)
	case "doctor":
		return cli.RunDoctor(arguments[1:], stdout)
	case "context":
		return cli.RunContext(arguments[1:], stdout)
	case "install":
		return cli.RunInstall(arguments[1:], stdout)
	default:
		printHelp(stdout, Version)
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}
