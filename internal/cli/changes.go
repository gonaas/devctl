package cli

import (
	"fmt"
	"io"
	"strconv"

	"github.com/gonaas/devctl/internal/gitx"
	"github.com/gonaas/devctl/internal/output"
)

// RunChanges reports the files a commit range touches: what a pull request would
// contain, computed against the fork point rather than the base's current tip.
//
// It deliberately does not run prepare(). Every other scanning command inspects
// the whole registry; a per-file diff belongs to one repository, and paying for a
// survey to answer "what did this branch change" is the wrong price.
func RunChanges(arguments []string, stdout io.Writer) error {
	var flags scanFlags
	set := newFlagSet("changes", stdout)
	flags.bind(set)
	var wantPatch bool
	set.BoolVar(&wantPatch, "patch", false, "include the unified diff of the range")
	if err := set.Parse(arguments); err != nil {
		return err
	}

	if err := requireGit(); err != nil {
		return err
	}
	// A range belongs to one repository, so a flag that selects among several must
	// be refused rather than silently narrowed to the first value.
	if len(flags.roots) > 1 {
		return fmt.Errorf("changes compares one repository, but %d --root values were given", len(flags.roots))
	}
	if flags.product != "" {
		return fmt.Errorf("changes compares one repository, so --product does not apply")
	}

	directory := "."
	if len(flags.roots) == 1 {
		directory = flags.roots[0]
	}
	_, topLevel, ok := gitx.Canonicalize(directory)
	if !ok {
		return fmt.Errorf("%s is not inside a git repository", directory)
	}

	base := gitx.ResolveBase(topLevel, flags.base, "")
	if base == "" {
		if flags.base != "" {
			return fmt.Errorf("base ref %q does not resolve in %s", flags.base, topLevel)
		}
		return fmt.Errorf("no base ref could be resolved in %s; pass --base REF to name one", topLevel)
	}

	mergeBase, err := gitx.MergeBase(topLevel, base)
	if err != nil {
		return err
	}
	changes, err := gitx.ChangedFiles(topLevel, mergeBase, "HEAD")
	if err != nil {
		return err
	}

	var patch string
	if wantPatch {
		if patch, err = gitx.Patch(topLevel, mergeBase, "HEAD"); err != nil {
			return err
		}
	}

	if flags.json {
		return output.EmitJSON(stdout, "changes", changesPayload(topLevel, base, mergeBase, changes, patch))
	}
	renderChanges(stdout, base, mergeBase, changes, patch)
	return nil
}

func changesPayload(topLevel, base, mergeBase string, changes []gitx.FileChange, patch string) map[string]any {
	files := make([]map[string]any, 0, len(changes))
	added, deleted := 0, 0
	for _, change := range changes {
		entry := map[string]any{
			"path":               change.Path,
			"status":             string(change.Status),
			"binary":             change.Binary,
			"counts_unavailable": change.CountsUnavailable,
		}
		// An unavailable count is reported as absent, never as a zero a consumer
		// would have to know to distrust.
		if change.CountsUnavailable {
			entry["added"] = nil
			entry["deleted"] = nil
		} else {
			entry["added"] = change.Added
			entry["deleted"] = change.Deleted
			added += change.Added
			deleted += change.Deleted
		}
		if change.PreviousPath != "" {
			entry["previous_path"] = change.PreviousPath
		}
		files = append(files, entry)
	}

	payload := map[string]any{
		"repository": topLevel,
		"base":       base,
		"merge_base": mergeBase,
		"files":      files,
		"totals": map[string]any{
			"files":   len(changes),
			"added":   added,
			"deleted": deleted,
		},
	}
	if patch != "" {
		payload["patch"] = patch
	}
	return payload
}

func renderChanges(stdout io.Writer, base, mergeBase string, changes []gitx.FileChange, patch string) {
	if len(changes) == 0 {
		fmt.Fprintf(stdout, "no committed changes between %s and HEAD\n", shortRevision(mergeBase))
		return
	}

	rows := make([][]string, 0, len(changes))
	added, deleted := 0, 0
	for _, change := range changes {
		addedCell, deletedCell := changeCounts(change)
		if !change.CountsUnavailable {
			added += change.Added
			deleted += change.Deleted
		}
		rows = append(rows, []string{changeLabel(change), string(change.Status), addedCell, deletedCell})
	}

	fmt.Fprintln(stdout, output.Table([]string{"FILE", "STATUS", "ADDED", "DELETED"}, rows))
	fmt.Fprintf(stdout, "\n%d files, +%d/-%d against %s at %s\n",
		len(changes), added, deleted, base, shortRevision(mergeBase))
	if patch != "" {
		fmt.Fprintln(stdout)
		fmt.Fprint(stdout, patch)
	}
}

// changeLabel names the path a reader has to act on. A renamed entry shows where it
// came from, because the move is the interesting part.
func changeLabel(change gitx.FileChange) string {
	if change.PreviousPath == "" {
		return change.Path
	}
	return change.PreviousPath + " -> " + change.Path
}

// changeCounts renders line counts, using git's own marker for a binary file so an
// unavailable count is never mistaken for a real zero.
func changeCounts(change gitx.FileChange) (string, string) {
	switch {
	case change.Binary:
		return "-", "-"
	case change.CountsUnavailable:
		return "?", "?"
	default:
		return strconv.Itoa(change.Added), strconv.Itoa(change.Deleted)
	}
}

func shortRevision(revision string) string {
	if len(revision) <= 8 {
		return revision
	}
	return revision[:8]
}
