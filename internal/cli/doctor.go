package cli

import (
	"fmt"
	"io"

	"github.com/gonaas/devctl/internal/config"
	"github.com/gonaas/devctl/internal/discovery"
	"github.com/gonaas/devctl/internal/output"
	"github.com/gonaas/devctl/internal/worktree"
)

// RunDoctor reports every problem worth acting on, across worktrees and tools.
func RunDoctor(arguments []string, stdout io.Writer) error {
	var flags scanFlags
	set := newFlagSet("doctor", stdout)
	flags.bind(set)
	if err := set.Parse(arguments); err != nil {
		return err
	}

	reg, result, err := prepare(&flags)
	if err != nil {
		return err
	}

	var flagged []*worktree.Report
	for _, report := range result.Reports {
		if report.HasHazard() {
			flagged = append(flagged, report)
		}
	}

	var unhealthy []config.ToolHealth
	for _, tool := range config.ToolHealthAll(reg) {
		if tool.Status != "ok" && tool.Status != "absent" {
			unhealthy = append(unhealthy, tool)
		}
	}

	stale := 0
	for _, finding := range result.Discovery.Findings {
		if finding.Kind == discovery.StaleEntry {
			stale++
		}
	}

	if flags.json {
		entries := make([]map[string]any, 0, len(flagged))
		for _, report := range flagged {
			entries = append(entries, map[string]any{
				"path": report.Worktree.Path, "flags": report.Flags,
				"blocking_reasons": report.BlockingReasons,
			})
		}
		return output.EmitJSON(stdout, "doctor", map[string]any{
			"worktrees": entries, "tools": unhealthy,
			"sources": result.Discovery.SourceStatus, "stale_source_entries": stale,
		})
	}

	if len(flagged) == 0 {
		fmt.Fprintln(stdout, "No worktree carries a risk flag.")
	} else {
		var rows [][]string
		for _, report := range flagged {
			branch := report.Branch()
			if branch == "" {
				branch = "(detached)"
			}
			reasons := joinSemicolon(report.BlockingReasons)
			if reasons == "" {
				reasons = "-"
			}
			rows = append(rows, []string{report.Worktree.Path, branch, joinComma(report.Flags), reasons})
		}
		fmt.Fprintln(stdout, output.Table([]string{"PATH", "BRANCH", "FLAGS", "WHY IT IS KEPT"}, rows))
	}
	for _, tool := range unhealthy {
		fmt.Fprintf(stdout, "tool %s: %s %s\n", tool.Name, tool.Status, tool.Detail)
	}
	return nil
}
