package cli

import (
	"fmt"
	"io"

	"github.com/gonaas/devctl/internal/cleanup"
	"github.com/gonaas/devctl/internal/gitx"
	"github.com/gonaas/devctl/internal/output"
	"github.com/gonaas/devctl/internal/rescue"
	"github.com/gonaas/devctl/internal/worktree"
)

// RunWorktrees routes the worktree subcommands.
func RunWorktrees(arguments []string, stdout io.Writer) error {
	if len(arguments) == 0 {
		return usage(stdout, "usage: devctl worktrees <list|status|doctor|clean|rescue> [flags]")
	}
	switch arguments[0] {
	case "list":
		return listWorktrees(arguments[1:], stdout, false, false)
	case "status":
		return listWorktrees(arguments[1:], stdout, true, false)
	case "doctor":
		return listWorktrees(arguments[1:], stdout, true, true)
	case "clean":
		return cleanWorktrees(arguments[1:], stdout)
	case "rescue":
		return rescueWorktrees(arguments[1:], stdout)
	default:
		return usage(stdout, "usage: devctl worktrees <list|status|doctor|clean|rescue> [flags]")
	}
}

func conflictLabel(analysis *worktree.Analysis) string {
	if analysis == nil || analysis.Conflicts == nil {
		return "-"
	}
	switch analysis.Conflicts.Status {
	case gitx.MergeClean:
		return "clean"
	case gitx.MergeConflicts:
		return fmt.Sprintf("%d files", len(analysis.Conflicts.ConflictedPaths))
	default:
		return "error"
	}
}

func listWorktrees(arguments []string, stdout io.Writer, wantConflict, problemsOnly bool) error {
	var flags scanFlags
	set := newFlagSet("worktrees", stdout)
	flags.bind(set)
	set.BoolVar(&flags.size, "size", false, "measure each working tree on disk")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	flags.wantConflict = wantConflict

	_, result, err := prepare(&flags)
	if err != nil {
		return err
	}

	reports := result.Reports
	if problemsOnly {
		filtered := reports[:0:0]
		for _, report := range reports {
			if len(report.Flags) > 0 || len(report.BlockingReasons) > 0 {
				filtered = append(filtered, report)
			}
		}
		reports = filtered
	}

	if flags.json {
		entries := make([]map[string]any, 0, len(reports))
		for _, report := range reports {
			entry := map[string]any{
				"product": report.Product, "repository": report.Repository.MainWorktree,
				"path": report.Worktree.Path, "branch": report.Branch(),
				"is_main": report.Worktree.IsMain, "exists": report.Worktree.Exists(),
				"dirty": report.State.DirtyCount(), "stash_count": report.State.StashCount,
				"size_bytes": report.SizeBytes, "flags": report.Flags,
				"blocking_reasons": report.BlockingReasons,
			}
			if analysis := report.Analysis; analysis != nil {
				entry["ahead"] = analysis.Signal.Ahead
				entry["behind"] = analysis.Signal.Behind
				entry["merged"] = string(analysis.Merged.Status)
				entry["merged_signals"] = analysis.Merged.Signals
				entry["change_request"] = analysis.Merged.PullRequestNumber
				entry["reachability"] = analysis.Reachability
				entry["unpushed"] = analysis.Unpushed
				if analysis.Conflicts != nil {
					entry["conflicts"] = string(analysis.Conflicts.Status)
				}
			}
			entries = append(entries, entry)
		}
		return output.EmitJSON(stdout, "worktrees", map[string]any{
			"worktrees": entries, "bases": result.BaseByRepository(),
		})
	}

	headers := []string{"PRODUCT", "REPO", "PATH", "BRANCH", "DIRTY", "A/B", "CONFLICTS", "MERGED", "PR", "AGE"}
	if flags.size {
		headers = append(headers, "SIZE")
	}
	headers = append(headers, "FLAGS")

	var rows [][]string
	for _, report := range reports {
		branch := report.Branch()
		if branch == "" {
			branch = "(detached)"
		}
		dirty, divergence, merged, request, age := "-", "-", "UNKNOWN", "-", "-"
		if report.State.DirtyCount() > 0 {
			dirty = fmt.Sprint(report.State.DirtyCount())
		}
		if analysis := report.Analysis; analysis != nil {
			divergence = fmt.Sprintf("%d/%d", analysis.Signal.Ahead, analysis.Signal.Behind)
			merged = string(analysis.Merged.Status)
			age = output.Age(analysis.Signal.CommittedAt)
			if analysis.Merged.PullRequestNumber > 0 {
				request = fmt.Sprintf("#%d", analysis.Merged.PullRequestNumber)
			}
		}
		row := []string{
			report.Product, report.Repository.Name(), report.Worktree.Path, branch,
			dirty, divergence, conflictLabel(report.Analysis), merged, request, age,
		}
		if flags.size {
			row = append(row, output.HumanSize(report.SizeBytes))
		}
		row = append(row, joinComma(report.Flags))
		rows = append(rows, row)
	}

	fmt.Fprintln(stdout, output.Table(headers, rows))
	fmt.Fprintf(stdout, "\n%d worktrees shown. Conflict prediction and merge checks use "+
		"\"git merge-tree --write-tree\", which adds unreachable objects to the object store. "+
		"Nothing else is written unless --apply is given.\n", len(reports))
	return nil
}

func cleanWorktrees(arguments []string, stdout io.Writer) error {
	var flags scanFlags
	set := newFlagSet("worktrees clean", stdout)
	flags.bind(set)
	set.BoolVar(&flags.apply, "apply", false, "perform the removals")
	if err := set.Parse(arguments); err != nil {
		return err
	}

	_, result, err := prepare(&flags)
	if err != nil {
		return err
	}
	plan := cleanup.Build(result.Reports)

	if flags.json {
		proposals := make([]map[string]any, 0, len(plan.Proposals))
		for _, proposal := range plan.Proposals {
			proposals = append(proposals, map[string]any{
				"path": proposal.Path(), "branch": proposal.Report.Branch(), "reasons": proposal.Reasons,
			})
		}
		skipped := make([]map[string]any, 0, len(plan.Skipped))
		for _, item := range plan.Skipped {
			skipped = append(skipped, map[string]any{
				"path": item.Report.Worktree.Path, "branch": item.Report.Branch(), "reason": item.Reason,
			})
		}
		return output.EmitJSON(stdout, "cleanup", map[string]any{
			"applied": flags.apply, "proposals": proposals, "skipped": skipped,
		})
	}

	if plan.IsEmpty() {
		fmt.Fprintln(stdout, "Nothing is provably safe to remove.")
	} else {
		var rows [][]string
		for _, proposal := range plan.Proposals {
			rows = append(rows, []string{proposal.Path(), proposal.Report.Branch(), joinSemicolon(proposal.Reasons)})
		}
		fmt.Fprintln(stdout, output.Table([]string{"PATH", "BRANCH", "WHY"}, rows))
		fmt.Fprintln(stdout)
	}

	if !flags.apply {
		fmt.Fprintf(stdout, "Dry run: %d removable, %d kept. Re-run with --apply to act.\n",
			len(plan.Proposals), len(plan.Skipped))
		for _, item := range plan.Skipped {
			fmt.Fprintf(stdout, "  keep %s (%s)\n", item.Report.Worktree.Path, item.Reason)
		}
		return nil
	}
	if plan.IsEmpty() {
		return nil
	}

	manifest := cleanup.BuildManifest(plan, result.BaseByRepository())
	path, err := cleanup.WriteManifest(manifest, cleanup.ManifestRoot(homeDirectory()))
	if err != nil {
		return fmt.Errorf("refusing to remove anything: the recovery manifest could not be written: %w", err)
	}
	fmt.Fprintf(stdout, "Recovery manifest written to %s\n", path)

	outcomes := cleanup.Apply(plan)
	removed, deleted, retained, failures := 0, 0, 0, 0
	for _, outcome := range outcomes {
		if outcome.WorktreeRemoved {
			removed++
		}
		if outcome.BranchDeleted {
			deleted++
		}
		if outcome.Error != "" {
			failures++
			fmt.Fprintf(stdout, "  FAILED %s: %s\n", outcome.Proposal.Path(), outcome.Error)
		}
		if outcome.BranchRetained != "" {
			retained++
			fmt.Fprintf(stdout, "  kept branch %s: %s\n", outcome.Proposal.Report.Branch(), outcome.BranchRetained)
		}
	}
	fmt.Fprintf(stdout, "Removed %d worktrees, deleted %d branches, kept %d branch labels, %d failures.\n",
		removed, deleted, retained, failures)
	if retained > 0 {
		fmt.Fprintln(stdout, "\nA kept branch label means git's own merge check declined, which it does "+
			"for squash merges because it compares against the configured upstream. The worktree is gone "+
			"and nothing was lost. Delete the label yourself only after checking the evidence in the "+
			"manifest; this tool will not force it.")
	}
	if failures > 0 {
		return fmt.Errorf("%d removals failed", failures)
	}
	return nil
}

func rescueWorktrees(arguments []string, stdout io.Writer) error {
	var flags scanFlags
	set := newFlagSet("worktrees rescue", stdout)
	flags.bind(set)
	set.BoolVar(&flags.apply, "apply", false, "perform the rescue")
	if err := set.Parse(arguments); err != nil {
		return err
	}

	_, result, err := prepare(&flags)
	if err != nil {
		return err
	}
	plans := rescue.BuildPlans(result.Reports)

	if flags.json {
		entries := make([]map[string]any, 0, len(plans))
		for _, plan := range plans {
			entries = append(entries, map[string]any{
				"path": plan.Report.Worktree.Path, "branch": plan.Report.Branch(),
				"strategy": plan.Strategy, "destination": plan.Destination,
				"dirty": plan.Report.State.DirtyCount(), "notes": plan.Notes,
			})
		}
		return output.EmitJSON(stdout, "rescue", map[string]any{"applied": flags.apply, "plans": entries})
	}

	if len(plans) == 0 {
		fmt.Fprintln(stdout, "No worktree is hosted on a purge-prone path.")
		return nil
	}

	var rows [][]string
	for _, plan := range plans {
		branch := plan.Report.Branch()
		if branch == "" {
			branch = "(detached)"
		}
		destination := plan.Destination
		if destination == "" {
			destination = "-"
		}
		rows = append(rows, []string{
			plan.Report.Worktree.Path, branch,
			fmt.Sprint(plan.Report.State.DirtyCount()), plan.Strategy, destination,
		})
	}
	fmt.Fprintln(stdout, output.Table([]string{"PATH", "BRANCH", "DIRTY", "STRATEGY", "DESTINATION"}, rows))
	fmt.Fprintln(stdout)
	for _, plan := range plans {
		for _, note := range plan.Notes {
			fmt.Fprintf(stdout, "  %s: %s\n", baseName(plan.Report.Worktree.Path), note)
		}
	}

	if !flags.apply {
		fmt.Fprintf(stdout, "\nDry run: %d worktrees would be secured. Re-run with --apply to act.\n", len(plans))
		return nil
	}

	failures := 0
	for _, outcome := range rescue.Apply(plans) {
		if outcome.Error != "" {
			failures++
			fmt.Fprintf(stdout, "  FAILED %s: %s\n", outcome.Plan.Report.Worktree.Path, outcome.Error)
			continue
		}
		suffix := ""
		if outcome.SnapshotTag != "" {
			suffix = fmt.Sprintf(" (snapshot %s)", outcome.SnapshotTag)
		}
		fmt.Fprintf(stdout, "  secured %s -> %s%s\n", outcome.Plan.Report.Worktree.Path, outcome.RelocatedTo, suffix)
	}
	if failures > 0 {
		return fmt.Errorf("%d rescues failed", failures)
	}
	return nil
}

func joinSemicolon(values []string) string {
	if len(values) == 0 {
		return ""
	}
	joined := values[0]
	for _, value := range values[1:] {
		joined += "; " + value
	}
	return joined
}
