// Package cleanup plans and applies worktree removal behind a recovery manifest.
package cleanup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gonaas/devctl/internal/gitx"
	"github.com/gonaas/devctl/internal/process"
	"github.com/gonaas/devctl/internal/worktree"
)

// ManifestSchema names the recovery record format.
const ManifestSchema = "devctl/manifest/1"

const manifestNote = "Reachability was evaluated against remote-tracking refs held " +
	"locally at scan time. Do not run git fetch --prune before restoring. Removed " +
	"objects stay recoverable through git reflog and git fsck --lost-found for as " +
	"long as gc.reflogExpireUnreachable allows, 90 days by default."

// ManifestRoot is where recovery records are written.
func ManifestRoot(home string) string { return filepath.Join(home, ".devctl", "manifests") }

// Proposal is one worktree proposed for removal, with its justification.
type Proposal struct {
	Report       *worktree.Report
	Reasons      []string
	RemoveBranch bool
}

// Path returns the worktree path this proposal would remove.
func (p Proposal) Path() string { return p.Report.Worktree.Path }

// Skipped is a worktree that was kept, and the reason it was kept.
type Skipped struct {
	Report *worktree.Report
	Reason string
}

// Plan is everything a dry run decided.
type Plan struct {
	Proposals []Proposal
	Skipped   []Skipped
}

// IsEmpty reports whether there is nothing to do.
func (p Plan) IsEmpty() bool { return len(p.Proposals) == 0 }

// Build decides which worktrees may be removed, refusing on any doubt at all.
//
// Deletion needs three independent things to line up: nothing blocks removal, the
// branch's content is provably already in the base, and no commit on it exists
// anywhere else. An UNKNOWN verdict is never enough — degradation must shrink
// this list, never grow it.
func Build(reports []*worktree.Report) Plan {
	plan := Plan{}
	for _, report := range reports {
		if len(report.BlockingReasons) > 0 {
			plan.Skipped = append(plan.Skipped, Skipped{report, strings.Join(report.BlockingReasons, "; ")})
			continue
		}
		analysis := report.Analysis
		if analysis == nil {
			plan.Skipped = append(plan.Skipped, Skipped{report, "no branch analysis available"})
			continue
		}
		if analysis.Merged.Status != gitx.Merged {
			plan.Skipped = append(plan.Skipped, Skipped{
				report, fmt.Sprintf("merge status is %s, not MERGED", analysis.Merged.Status),
			})
			continue
		}
		if analysis.Reachability != 0 {
			plan.Skipped = append(plan.Skipped, Skipped{report, "commits exist on no other ref"})
			continue
		}

		reasons := []string{"merged via " + strings.Join(analysis.Merged.Signals, ", ")}
		if analysis.Merged.PullRequestNumber > 0 {
			reasons = append(reasons, fmt.Sprintf("change request #%d", analysis.Merged.PullRequestNumber))
		}
		reasons = append(reasons, "no commit exists on this branch alone")
		plan.Proposals = append(plan.Proposals, Proposal{Report: report, Reasons: reasons, RemoveBranch: true})
	}
	return plan
}

// ManifestEntry records everything needed to undo one removal.
type ManifestEntry struct {
	RepositoryCommonDir string   `json:"repository_common_dir"`
	MainWorktree        string   `json:"main_worktree"`
	WorktreePath        string   `json:"worktree_path"`
	Branch              string   `json:"branch"`
	BranchShort         string   `json:"branch_short"`
	Tip                 string   `json:"tip"`
	TipSubject          string   `json:"tip_subject"`
	BaseRef             string   `json:"base_ref"`
	BaseOIDAtScan       string   `json:"base_oid_at_scan"`
	MergedSignals       []string `json:"merged_signals"`
	ChangeRequestNumber int      `json:"change_request_number"`
	ChangeRequestURL    string   `json:"change_request_url"`
	Reachability        int      `json:"reachability"`
	DirtyPaths          []string `json:"dirty_paths"`
	Restore             []string `json:"restore"`
}

// Manifest is the recovery record written before anything is removed.
type Manifest struct {
	Schema    string          `json:"schema"`
	CreatedAt string          `json:"created_at"`
	Note      string          `json:"note"`
	Entries   []ManifestEntry `json:"entries"`
}

func commitSubject(directory, commit string) string {
	result := process.Git([]string{"log", "-1", "--format=%s", commit},
		process.Options{Dir: directory, Timeout: 15 * time.Second})
	if !result.OK() {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

// BuildManifest assembles the recovery record for a plan.
func BuildManifest(plan Plan, bases map[string]string) Manifest {
	manifest := Manifest{
		Schema:    ManifestSchema,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Note:      manifestNote,
		Entries:   []ManifestEntry{},
	}

	for _, proposal := range plan.Proposals {
		report := proposal.Report
		repository := report.Repository
		item := report.Worktree
		branch := item.ShortBranch()
		base := bases[repository.CommonDir]

		baseOID := ""
		if base != "" {
			resolved := process.Git([]string{"rev-parse", base},
				process.Options{Dir: repository.MainWorktree, Timeout: 15 * time.Second})
			if resolved.OK() {
				baseOID = strings.TrimSpace(resolved.Stdout)
			}
		}

		restore := []string{}
		if branch != "" {
			restore = append(restore, fmt.Sprintf("git -C %s branch %s %s", repository.MainWorktree, branch, item.Head))
		}
		restore = append(restore, fmt.Sprintf("git -C %s worktree add %s %s", repository.MainWorktree, item.Path, item.Head))

		dirty := append([]string{}, report.State.ChangedPaths...)
		dirty = append(dirty, report.State.UntrackedPaths...)

		entry := ManifestEntry{
			RepositoryCommonDir: repository.CommonDir,
			MainWorktree:        repository.MainWorktree,
			WorktreePath:        item.Path,
			Branch:              item.Branch,
			BranchShort:         branch,
			Tip:                 item.Head,
			TipSubject:          commitSubject(repository.MainWorktree, item.Head),
			BaseRef:             base,
			BaseOIDAtScan:       baseOID,
			Reachability:        -1,
			DirtyPaths:          dirty,
			Restore:             restore,
		}
		if report.Analysis != nil {
			entry.MergedSignals = report.Analysis.Merged.Signals
			entry.ChangeRequestNumber = report.Analysis.Merged.PullRequestNumber
			entry.ChangeRequestURL = report.Analysis.Merged.PullRequestURL
			entry.Reachability = report.Analysis.Reachability
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	return manifest
}

// WriteManifest writes the manifest durably and returns its path.
//
// Written to a temporary file, flushed, synced, then renamed into place, so a
// crash can never leave a half-written recovery record.
func WriteManifest(manifest Manifest, root string) (string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}

	temporary, err := os.CreateTemp(root, ".manifest-*.tmp")
	if err != nil {
		return "", err
	}
	name := temporary.Name()
	cleanup := func() { _ = os.Remove(name) }

	if _, err := temporary.Write(payload); err != nil {
		temporary.Close()
		cleanup()
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		cleanup()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return "", err
	}

	destination := filepath.Join(root, time.Now().UTC().Format("20060102T150405Z")+".json")
	if err := os.Rename(name, destination); err != nil {
		cleanup()
		return "", err
	}
	return destination, nil
}

// Outcome is what actually happened to one proposal when it was applied.
type Outcome struct {
	Proposal        Proposal
	WorktreeRemoved bool
	BranchDeleted   bool
	BranchRetained  string
	Error           string
}

// Apply removes the proposed worktrees, then their branches, in that order only.
//
// The order is forced by git: a branch checked out in a linked worktree cannot be
// deleted, not even with --force. Removing the worktree touches no refs and
// orphans nothing; only the branch deletion can, which is what the reachability
// gate already proved safe.
//
// Plain `worktree remove` and `branch -d` are used deliberately. Each refuses on
// unclean or unmerged state, giving two more independent checks evaluated at the
// moment of mutation rather than at scan time. Neither is ever forced.
func Apply(plan Plan) []Outcome {
	var outcomes []Outcome

	for _, proposal := range plan.Proposals {
		report := proposal.Report
		main := report.Repository.MainWorktree
		outcome := Outcome{Proposal: proposal}

		removal := process.Git([]string{"worktree", "remove", report.Worktree.Path},
			process.Options{Dir: main, Timeout: 120 * time.Second})
		if !removal.OK() {
			outcome.Error = firstLineOr(removal.Stderr, "worktree remove failed")
			outcomes = append(outcomes, outcome)
			continue
		}
		outcome.WorktreeRemoved = true

		branch := report.Worktree.ShortBranch()
		if proposal.RemoveBranch && branch != "" {
			deletion := process.Git([]string{"branch", "-d", branch},
				process.Options{Dir: main, Timeout: 60 * time.Second})
			if deletion.OK() {
				outcome.BranchDeleted = true
			} else {
				// git's -d judges "merged" relative to the configured upstream, so
				// it refuses on squash-merged branches even when their content is
				// demonstrably in the base. That refusal is a safety net working,
				// not a failure: the worktree is gone, the branch label stays, and
				// nothing is lost. Escalating to -D would defeat the net.
				outcome.BranchRetained = firstLineOr(deletion.Stderr, "branch -d declined")
			}
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
