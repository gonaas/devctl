package cli

import (
	"fmt"
	"io"
	"sort"

	"github.com/gonaas/devctl/internal/output"
	"github.com/gonaas/devctl/internal/survey"
)

// RunBranches lists local branches with divergence, merge status and orphan risk.
func RunBranches(arguments []string, stdout io.Writer) error {
	if len(arguments) == 0 || arguments[0] != "list" {
		return usage(stdout, "usage: devctl branches list [--repo PATH] [--base REF] [--conflicts] [--json]")
	}
	var flags scanFlags
	set := newFlagSet("branches list", stdout)
	flags.bind(set)
	set.BoolVar(&flags.wantConflict, "conflicts", false, "predict conflicts against the base")
	if err := set.Parse(arguments[1:]); err != nil {
		return err
	}

	_, result, err := prepare(&flags)
	if err != nil {
		return err
	}

	var rows [][]string
	entries := []map[string]any{}
	for _, context := range result.SortedContexts() {
		if len(context.Signals) == 0 {
			continue
		}
		names := make([]string, 0, len(context.Signals))
		for name := range context.Signals {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			signal := context.Signals[name]
			analysis := survey.AnalyseBranch(context, name, signal.Tip, flags.wantConflict)
			remote := "no"
			if analysis.HasRemote {
				remote = "yes"
			}
			worktreePath := signal.WorktreePath
			if worktreePath == "" {
				worktreePath = "-"
			}
			rows = append(rows, []string{
				context.Repository.Name(), name,
				fmt.Sprintf("%d/%d", signal.Ahead, signal.Behind),
				string(analysis.Merged.Status), fmt.Sprint(analysis.Reachability),
				remote, worktreePath, output.Age(signal.CommittedAt),
			})
			entries = append(entries, map[string]any{
				"repository": context.Repository.MainWorktree, "branch": name, "tip": signal.Tip,
				"ahead": signal.Ahead, "behind": signal.Behind,
				"merged": string(analysis.Merged.Status), "merged_signals": analysis.Merged.Signals,
				"reachability": analysis.Reachability, "unpushed": analysis.Unpushed,
				"has_remote_branch": analysis.HasRemote, "worktree": signal.WorktreePath,
			})
		}
	}

	if flags.json {
		return output.EmitJSON(stdout, "branches", map[string]any{"branches": entries})
	}

	fmt.Fprintln(stdout, output.Table(
		[]string{"REPO", "BRANCH", "A/B", "MERGED", "AT-RISK", "REMOTE", "WORKTREE", "AGE"}, rows))
	fmt.Fprintf(stdout, "\n%d branches. AT-RISK counts commits reachable from no other ref; 0 is safe.\n", len(rows))
	return nil
}
