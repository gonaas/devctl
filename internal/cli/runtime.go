package cli

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/gonaas/devctl/internal/adapters"
	"github.com/gonaas/devctl/internal/output"
)

// RunRuntime reports runtime resources whose originating directory has gone.
func RunRuntime(arguments []string, stdout io.Writer) error {
	if len(arguments) == 0 || arguments[0] != "orphans" {
		return usage(stdout, "usage: devctl runtime orphans [--json]")
	}
	set := newFlagSet("runtime orphans", stdout)
	asJSON := set.Bool("json", false, "emit a machine-readable document")
	if err := set.Parse(arguments[1:]); err != nil {
		return err
	}

	reg, err := loadRegistry()
	if err != nil {
		return err
	}

	status := map[string]string{}
	var orphans []adapters.RuntimeResource
	for _, runtime := range adapters.Build(reg).Runtimes {
		availability := runtime.Available()
		if !availability.Usable {
			status[runtime.Name()] = availability.Reason
			continue
		}
		status[runtime.Name()] = "available"
		for _, resource := range runtime.Resources() {
			if resource.BoundPath == "" {
				continue
			}
			if _, statErr := os.Stat(resource.BoundPath); os.IsNotExist(statErr) {
				orphans = append(orphans, resource)
			}
		}
	}

	if *asJSON {
		entries := make([]map[string]any, 0, len(orphans))
		for _, resource := range orphans {
			entries = append(entries, map[string]any{
				"name": resource.Name, "kind": resource.Kind, "stack": resource.Detail,
				"bound_path": resource.BoundPath, "size_bytes": resource.SizeBytes,
			})
		}
		return output.EmitJSON(stdout, "runtime", map[string]any{"runtimes": status, "orphans": entries})
	}

	names := make([]string, 0, len(status))
	for name := range status {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if status[name] != "available" {
			fmt.Fprintf(stdout, "runtime %s unavailable: %s\n", name, status[name])
		}
	}
	if len(orphans) == 0 {
		fmt.Fprintln(stdout, "No runtime resource is bound to a directory that has gone.")
		return nil
	}

	var rows [][]string
	for _, resource := range orphans {
		stack := resource.Detail
		if stack == "" {
			stack = "-"
		}
		rows = append(rows, []string{resource.Name, resource.Kind, stack, resource.BoundPath})
	}
	fmt.Fprintln(stdout, output.Table([]string{"NAME", "KIND", "STACK", "MISSING DIRECTORY"}, rows))
	fmt.Fprintf(stdout, "\n%d orphaned resources. This command only reports; it removes nothing.\n", len(orphans))
	return nil
}
