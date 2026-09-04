package cli

import (
	"io"

	"github.com/gonaas/devctl/internal/output"
)

// RunContext emits a compact machine summary for an agent to read as context.
func RunContext(arguments []string, stdout io.Writer) error {
	var flags scanFlags
	set := newFlagSet("context", stdout)
	flags.bind(set)
	if err := set.Parse(arguments); err != nil {
		return err
	}

	_, result, err := prepare(&flags)
	if err != nil {
		return err
	}

	products := map[string][]string{}
	for product, repositories := range result.Discovery.ByProduct() {
		for _, repository := range repositories {
			products[product] = append(products[product], repository.MainWorktree)
		}
	}

	attention := []map[string]any{}
	for _, report := range result.Reports {
		if len(report.Flags) == 0 || report.Worktree.IsMain {
			continue
		}
		merged := "UNKNOWN"
		if report.Analysis != nil {
			merged = string(report.Analysis.Merged.Status)
		}
		attention = append(attention, map[string]any{
			"path": report.Worktree.Path, "product": report.Product, "branch": report.Branch(),
			"flags": report.Flags, "blocking_reasons": report.BlockingReasons, "merged": merged,
		})
	}

	return output.EmitJSON(stdout, "context", map[string]any{
		"products": products,
		"counts": map[string]int{
			"repositories": len(result.Discovery.Repositories),
			"worktrees":    len(result.Reports),
			"flagged":      len(attention),
		},
		"sources":   result.Discovery.SourceStatus,
		"attention": attention,
	})
}
