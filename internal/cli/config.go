package cli

import (
	"fmt"
	"io"

	"github.com/gonaas/devctl/internal/config"
	"github.com/gonaas/devctl/internal/output"
	"github.com/gonaas/devctl/internal/registry"
)

// RunConfig routes the agent-stack inspection subcommands.
func RunConfig(arguments []string, stdout io.Writer) error {
	if len(arguments) == 0 {
		return usage(stdout, "usage: devctl config <doctor|drift|registry> [--json]")
	}
	switch arguments[0] {
	case "doctor":
		return configDoctor(arguments[1:], stdout)
	case "drift":
		return configDrift(arguments[1:], stdout)
	case "registry":
		return configRegistry(arguments[1:], stdout)
	default:
		return usage(stdout, "usage: devctl config <doctor|drift|registry> [--json]")
	}
}

func configDoctor(arguments []string, stdout io.Writer) error {
	set := newFlagSet("config doctor", stdout)
	asJSON := set.Bool("json", false, "emit a machine-readable document")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	reg, err := loadRegistry()
	if err != nil {
		return err
	}
	results := config.ToolHealthAll(reg)

	if *asJSON {
		return output.EmitJSON(stdout, "config-doctor", map[string]any{
			"registry": reg.SourcePath, "tools": results,
		})
	}
	if len(results) == 0 {
		fmt.Fprintln(stdout, "No tools registered.")
		return nil
	}
	var rows [][]string
	for _, item := range results {
		installed := "no"
		if item.Installed {
			installed = "yes"
		}
		rows = append(rows, []string{item.Name, item.Binary, installed, item.Status, item.Detail})
	}
	fmt.Fprintln(stdout, output.Table([]string{"TOOL", "BINARY", "INSTALLED", "STATUS", "DETAIL"}, rows))
	return nil
}

func configDrift(arguments []string, stdout io.Writer) error {
	set := newFlagSet("config drift", stdout)
	asJSON := set.Bool("json", false, "emit a machine-readable document")
	skills := set.String("skills", "", "repository skills directory; overrides the registry")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	reg, err := loadRegistry()
	if err != nil {
		return err
	}
	root := *skills
	if root == "" {
		root = reg.Skills.Repository
	}
	if root == "" {
		return fmt.Errorf("no repository skills directory: set [skills].repository in the registry or pass --skills")
	}

	summaries := config.SkillDriftAll(reg, root)
	if *asJSON {
		entries := make([]map[string]any, 0, len(summaries))
		for _, summary := range summaries {
			entries = append(entries, map[string]any{
				"agent": summary.Agent, "root": summary.Root, "present": summary.Present,
				"counts": summary.Counts(), "entries": summary.Entries,
			})
		}
		return output.EmitJSON(stdout, "config-drift", map[string]any{
			"repository_skills": root, "agents": entries,
		})
	}

	for _, summary := range summaries {
		if !summary.Present {
			fmt.Fprintf(stdout, "%s: %s is not present\n", summary.Agent, summary.Root)
			continue
		}
		fmt.Fprintf(stdout, "%s:", summary.Agent)
		counts := summary.Counts()
		for _, state := range []string{config.Identical, config.RepoNewer, config.InstalledNewer, config.RepoOnly, config.InstalledOnly} {
			if count := counts[state]; count > 0 {
				fmt.Fprintf(stdout, " %d %s", count, state)
			}
		}
		fmt.Fprintln(stdout)
		for _, entry := range summary.Entries {
			if entry.State == config.Identical || entry.State == config.InstalledOnly {
				continue
			}
			fmt.Fprintf(stdout, "  %-16s %s\n", entry.State, entry.Skill)
		}
	}
	fmt.Fprintln(stdout, "\nRead-only, and compared by content rather than timestamps: a copy-based "+
		"installer resets mtimes without changing a byte. Choosing which copy wins can destroy work, "+
		"so it is a separate action.")
	return nil
}

func configRegistry(arguments []string, stdout io.Writer) error {
	set := newFlagSet("config registry", stdout)
	defaults := set.Bool("defaults", false, "print the built-in defaults instead of the effective file")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if *defaults {
		_, err := stdout.Write(registry.Default())
		return err
	}
	reg, err := loadRegistry()
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "source: %s\n", reg.SourcePath)
	fmt.Fprintf(stdout, "user override path: %s\n", registry.UserConfigPath(homeDirectory()))
	fmt.Fprintf(stdout, "project sources: %d, tools: %d, agent runtimes: %d, product rules: %d\n",
		len(reg.ProjectSources), len(reg.Tools), len(reg.Agents), len(reg.ProductRules))
	return nil
}
