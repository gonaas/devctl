package app

import (
	"fmt"
	"io"
)

const readOnlyCaveat = `Conflict prediction and merge checks use "git merge-tree --write-tree", which
adds unreachable objects to the object store. Nothing else is written unless
--apply is given.`

func printHelp(stdout io.Writer, version string) {
	fmt.Fprintf(stdout, `devctl %s — inspect and safely tidy git worktrees, branches and agent-stack config.

USAGE
  devctl <command> [subcommand] [flags]

COMMANDS
  projects list         products, repositories, last activity and where each was learned
  worktrees list        every worktree with branch, divergence and risk flags
  worktrees status      as above, with conflict prediction against the base
  worktrees doctor      only worktrees carrying a risk flag
  worktrees clean       remove worktrees that are provably dead (dry run by default)
  worktrees rescue      secure worktrees hosted on purge-prone paths
  branches list         local branches with merge status and orphan risk
  runtime orphans       runtime resources bound to directories that have gone
  config doctor         probe every declared tool
  config drift          compare repository skills with installed copies
  config registry       print the effective registry and where it came from
  doctor                everything worth acting on
  context               a compact machine summary for an agent to read
  install               link this binary onto PATH
  version               print the version

COMMON FLAGS
  --json                emit a machine-readable document
  --root PATH           directory to scan; repeat for several
  --base REF            base ref to compare against; overrides detection
  --product NAME        restrict to one product
  --apply               perform changes instead of a dry run

ENVIRONMENT
  DEVCTL_REGISTRY       path to a registry file, overriding the built-in defaults

%s
`, version, readOnlyCaveat)
}
