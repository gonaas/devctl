// Package rescue secures worktrees hosted on paths the system may purge.
package rescue

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gonaas/devctl/internal/process"
	"github.com/gonaas/devctl/internal/worktree"
)

// Strategies a rescue can take.
const (
	Recreate       = "recreate"
	SnapshotMove   = "snapshot-and-move"
	PruneCandidate = "prune-candidate"
)

// Plan is what securing one at-risk worktree would involve.
type Plan struct {
	Report      *worktree.Report
	Strategy    string
	Destination string
	Notes       []string
}

// Outcome is what actually happened when a rescue was applied.
type Outcome struct {
	Plan        Plan
	SnapshotTag string
	RelocatedTo string
	Error       string
}

// safeDestination returns where a temporary worktree should live instead.
//
// Placed beside the main checkout, which is the layout already in use, and named
// after the branch so the directory stops drifting from its contents.
func safeDestination(report *worktree.Report) string {
	branch := report.Worktree.ShortBranch()
	if branch == "" {
		branch = report.Worktree.Head
		if len(branch) > 12 {
			branch = branch[:12]
		}
	}
	if branch == "" {
		branch = "rescued"
	}
	leaf := strings.ReplaceAll(branch, "/", "-")
	parent := filepath.Dir(report.Repository.MainWorktree)
	stem := filepath.Base(report.Repository.MainWorktree)

	candidate := filepath.Join(parent, stem+"-"+leaf)
	for suffix := 2; pathExists(candidate); suffix++ {
		candidate = filepath.Join(parent, fmt.Sprintf("%s-%s-%d", stem, leaf, suffix))
	}
	return candidate
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// BuildPlans decides how to secure each worktree on a purge-prone path.
//
// Only uncommitted and untracked content is actually at risk: the worktree's
// administrative directory and every object already live in the repository's
// shared common directory, which is not on the temporary volume. So a clean
// worktree needs no copy at all, and a dirty one needs its working files moved.
func BuildPlans(reports []*worktree.Report) []Plan {
	var plans []Plan
	for _, report := range reports {
		temporary := false
		for _, flag := range report.Flags {
			if flag == worktree.Temporary {
				temporary = true
			}
		}
		if !temporary {
			continue
		}
		if !report.Worktree.Exists() {
			plans = append(plans, Plan{
				Report: report, Strategy: PruneCandidate,
				Notes: []string{"directory already gone; inspect its reflog before pruning"},
			})
			continue
		}

		destination := safeDestination(report)
		if report.State.IsDirty() {
			plans = append(plans, Plan{
				Report: report, Strategy: SnapshotMove, Destination: destination,
				Notes: []string{
					fmt.Sprintf("%d uncommitted or untracked path(s) would be lost on purge", report.State.DirtyCount()),
					"a snapshot commit is tagged first, so content survives even if the move fails",
				},
			})
			continue
		}
		plans = append(plans, Plan{
			Report: report, Strategy: Recreate, Destination: destination,
			Notes: []string{"clean tree; the checkout is re-derived from the object store, nothing is copied"},
		})
	}
	return plans
}

// Snapshot captures uncommitted work as a tagged commit, disturbing nothing.
//
// `git stash create` builds a commit and returns its id without touching
// refs/stash, the index or the working tree. Tagging it puts the content in the
// shared object store, on durable storage, before any move is attempted.
func Snapshot(report *worktree.Report) (tag string, failure string) {
	path := report.Worktree.Path
	created := process.Git([]string{"stash", "create"}, process.Options{Dir: path, Timeout: 120 * time.Second})
	if !created.OK() {
		return "", firstLineOr(created.Stderr, "stash create failed")
	}
	commit := strings.TrimSpace(created.Stdout)
	if commit == "" {
		return "", ""
	}

	leaf := report.Worktree.ShortBranch()
	if leaf == "" {
		leaf = "detached"
	}
	tag = "rescue/" + strings.ReplaceAll(leaf, "/", "-") + "-" + time.Now().UTC().Format("20060102T150405Z")
	tagged := process.Git([]string{"tag", tag, commit}, process.Options{Dir: path, Timeout: 30 * time.Second})
	if !tagged.OK() {
		return "", firstLineOr(tagged.Stderr, "tag failed")
	}
	return tag, ""
}

// Apply secures each planned worktree, snapshotting before any relocation.
func Apply(plans []Plan) []Outcome {
	var outcomes []Outcome

	for _, plan := range plans {
		outcome := Outcome{Plan: plan}
		report := plan.Report
		main := report.Repository.MainWorktree

		if plan.Strategy == PruneCandidate {
			outcome.Error = "directory is gone; prune manually after checking its reflog"
			outcomes = append(outcomes, outcome)
			continue
		}

		if report.State.IsDirty() {
			tag, failure := Snapshot(report)
			if failure != "" {
				outcome.Error = "snapshot failed, nothing moved: " + failure
				outcomes = append(outcomes, outcome)
				continue
			}
			outcome.SnapshotTag = tag
		}

		if plan.Destination == "" {
			outcome.Error = "no destination computed"
			outcomes = append(outcomes, outcome)
			continue
		}

		if plan.Strategy == Recreate {
			branch := report.Worktree.ShortBranch()
			if branch == "" {
				outcome.Error = "cannot recreate a detached worktree safely; move it instead"
				outcomes = append(outcomes, outcome)
				continue
			}
			removal := process.Git([]string{"worktree", "remove", report.Worktree.Path},
				process.Options{Dir: main, Timeout: 120 * time.Second})
			if !removal.OK() {
				outcome.Error = firstLineOr(removal.Stderr, "worktree remove refused")
				outcomes = append(outcomes, outcome)
				continue
			}
			recreated := process.Git([]string{"worktree", "add", plan.Destination, branch},
				process.Options{Dir: main, Timeout: 300 * time.Second})
			if !recreated.OK() {
				outcome.Error = firstLineOr(recreated.Stderr, "worktree add failed")
			} else {
				outcome.RelocatedTo = plan.Destination
			}
			outcomes = append(outcomes, outcome)
			continue
		}

		moved := process.Git([]string{"worktree", "move", report.Worktree.Path, plan.Destination},
			process.Options{Dir: main, Timeout: 300 * time.Second})
		if !moved.OK() {
			tag := outcome.SnapshotTag
			if tag == "" {
				tag = "(none)"
			}
			outcome.Error = fmt.Sprintf("move failed (%s); content is preserved in tag %s",
				firstLineOr(moved.Stderr, "unknown error"), tag)
		} else {
			outcome.RelocatedTo = plan.Destination
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}

func firstLineOr(text, fallback string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return fallback
	}
	return strings.SplitN(trimmed, "\n", 2)[0]
}
