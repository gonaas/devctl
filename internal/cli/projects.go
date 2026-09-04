package cli

import (
	"fmt"
	"io"
	"sort"

	"github.com/gonaas/devctl/internal/adapters"
	"github.com/gonaas/devctl/internal/discovery"
	"github.com/gonaas/devctl/internal/output"
)

// RunProjects lists products, their repositories and where each was learned from.
//
// Discovery only. Branch analysis and provider calls cost seconds and answer a
// different question, so this command never pays for them.
func RunProjects(arguments []string, stdout io.Writer) error {
	if len(arguments) == 0 || arguments[0] != "list" {
		return usage(stdout, "usage: devctl projects list [--root PATH]... [--json]")
	}
	var flags scanFlags
	set := newFlagSet("projects list", stdout)
	flags.bind(set)
	if err := set.Parse(arguments[1:]); err != nil {
		return err
	}
	if err := requireGit(); err != nil {
		return err
	}

	reg, err := loadRegistry()
	if err != nil {
		return err
	}
	result := discovery.Discover(reg, adapters.Build(reg), flags.roots)

	if flags.json {
		products := map[string]any{}
		for product, repositories := range result.ByProduct() {
			entries := make([]map[string]any, 0, len(repositories))
			for _, repository := range repositories {
				sources := make([]string, 0, len(repository.Sources))
				for source := range repository.Sources {
					sources = append(sources, source)
				}
				sort.Strings(sources)
				entries = append(entries, map[string]any{
					"repository":  repository.MainWorktree,
					"worktrees":   len(repository.LinkedWorktrees()),
					"sources":     sources,
					"last_active": repository.LastActive,
					"remote":      repository.RemoteURL,
				})
			}
			products[product] = entries
		}
		findings := make([]map[string]any, 0, len(result.Findings))
		for _, finding := range result.Findings {
			findings = append(findings, map[string]any{
				"kind": finding.Kind, "directory": finding.Directory,
				"project": finding.Project, "source": finding.Source, "detail": finding.Detail,
			})
		}
		return output.EmitJSON(stdout, "projects", map[string]any{
			"sources": result.SourceStatus, "products": products, "findings": findings,
		})
	}

	var rows [][]string
	grouped := result.ByProduct()
	for _, product := range result.SortedProducts() {
		for _, repository := range grouped[product] {
			sources := make([]string, 0, len(repository.Sources))
			for source := range repository.Sources {
				sources = append(sources, source)
			}
			sort.Strings(sources)
			lastActive := repository.LastActive
			if lastActive == "" {
				lastActive = "-"
			}
			rows = append(rows, []string{
				product, repository.Name(), repository.MainWorktree,
				fmt.Sprint(len(repository.LinkedWorktrees())), joinComma(sources), lastActive,
			})
		}
	}

	fmt.Fprintln(stdout, output.Table(
		[]string{"PRODUCT", "REPO", "PATH", "WORKTREES", "SOURCES", "LAST ACTIVE"}, rows))

	stale, containers := 0, 0
	for _, finding := range result.Findings {
		switch finding.Kind {
		case discovery.StaleEntry:
			stale++
		case discovery.NotARepository:
			containers++
		}
	}
	fmt.Fprintf(stdout, "\n%d repositories across %d products; %d stale source entries, "+
		"%d recorded directories that are not repositories.\n",
		len(result.Repositories), len(grouped), stale, containers)

	names := make([]string, 0, len(result.SourceStatus))
	for name := range result.SourceStatus {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if status := result.SourceStatus[name]; status != "available" {
			fmt.Fprintf(stdout, "source %s unavailable: %s\n", name, status)
		}
	}
	return nil
}

func joinComma(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	joined := values[0]
	for _, value := range values[1:] {
		joined += "," + value
	}
	return joined
}
