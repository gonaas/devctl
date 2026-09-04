// Package worktree inspects working state and classifies each worktree's risks.
package worktree

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gonaas/devctl/internal/gitx"
	"github.com/gonaas/devctl/internal/process"
)

// Risk flags. Every one of them came from an observed failure, not a hypothesis.
const (
	Temporary        = "TMP"
	Ghost            = "GHOST"
	Drift            = "DRIFT"
	Detached         = "DETACHED"
	NoRemote         = "NO_REMOTE"
	AtRisk           = "AT_RISK"
	Locked           = "LOCKED"
	InProgress       = "IN_PROGRESS"
	Dirty            = "DIRTY"
	UnreferencedHead = "UNREFERENCED_HEAD"
)

// hazardFlags are the flags that mean something is wrong or at risk, as opposed
// to the ones that merely describe ordinary working state.
//
// DIRTY, DETACHED, NO_REMOTE and DRIFT all block automated removal and belong in
// a full listing, but none of them is a problem: uncommitted work in the checkout
// you are using today is Tuesday, not an incident. Reporting them as problems
// makes the problem view useless, which is worse than not having one.
var hazardFlags = map[string]bool{
	Temporary:        true,
	Ghost:            true,
	AtRisk:           true,
	UnreferencedHead: true,
	Locked:           true,
	InProgress:       true,
}

// HasHazard reports whether a report carries a flag worth acting on.
func (r Report) HasHazard() bool {
	for _, flag := range r.Flags {
		if hazardFlags[flag] {
			return true
		}
	}
	return false
}

// State is what `git status --porcelain=v2` reports for one worktree.
type State struct {
	HeadOID        string
	Branch         string
	Upstream       string
	Ahead          int
	Behind         int
	StashCount     int
	ChangedPaths   []string
	UntrackedPaths []string
	UnmergedPaths  []string
	Readable       bool
}

// DirtyCount returns how many paths carry work that removal would destroy.
func (s State) DirtyCount() int {
	return len(s.ChangedPaths) + len(s.UntrackedPaths) + len(s.UnmergedPaths)
}

// IsDirty reports whether anything at all would be lost by removing the worktree.
func (s State) IsDirty() bool { return s.DirtyCount() > 0 }

// Analysis is the branch verdict attached to one worktree.
type Analysis struct {
	Signal       gitx.BranchSignal
	Merged       gitx.MergedVerdict
	Reachability int
	Unpushed     int
	HasRemote    bool
	Conflicts    *gitx.MergeTreeResult
}

// AtRisk reports whether commits on this branch exist nowhere else. A negative
// count means the check could not run, which is treated as risk.
func (a Analysis) AtRisk() bool { return a.Reachability != 0 }

// Report is a worktree with its working state, branch analysis and risk flags.
type Report struct {
	Repository      *gitx.Repository
	Worktree        gitx.Worktree
	State           State
	Analysis        *Analysis
	Flags           []string
	Product         string
	SizeBytes       int64
	BlockingReasons []string
}

// Branch returns the checked-out branch name, or "" when detached.
func (r Report) Branch() string { return r.Worktree.ShortBranch() }

// ReadState reads one worktree's working state in a single git invocation.
//
// --untracked-files defaults to "normal" on purpose: an untracked directory is
// still reported, so "all" cannot change the yes/no answer and only costs more
// on large trees.
func ReadState(path string) State {
	state := State{Readable: true}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		state.Readable = false
		return state
	}

	result := process.Git(
		[]string{"status", "--porcelain=v2", "--branch", "--show-stash", "--ignored=no"},
		process.Options{Dir: path, Timeout: 120 * time.Second},
	)
	if !result.OK() {
		state.Readable = false
		return state
	}

	for _, line := range strings.Split(result.Stdout, "\n") {
		switch {
		case line == "":
		case strings.HasPrefix(line, "# branch.oid "):
			state.HeadOID = strings.TrimSpace(strings.TrimPrefix(line, "# branch.oid "))
		case strings.HasPrefix(line, "# branch.head "):
			state.Branch = strings.TrimSpace(strings.TrimPrefix(line, "# branch.head "))
		case strings.HasPrefix(line, "# branch.upstream "):
			state.Upstream = strings.TrimSpace(strings.TrimPrefix(line, "# branch.upstream "))
		case strings.HasPrefix(line, "# branch.ab "):
			for _, field := range strings.Fields(strings.TrimPrefix(line, "# branch.ab ")) {
				value, err := strconv.Atoi(field[1:])
				if err != nil {
					continue
				}
				if field[0] == '+' {
					state.Ahead = value
				} else if field[0] == '-' {
					state.Behind = value
				}
			}
		case strings.HasPrefix(line, "# stash "):
			state.StashCount, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "# stash ")))
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "):
			fields := strings.Fields(line)
			state.ChangedPaths = append(state.ChangedPaths, fields[len(fields)-1])
		case strings.HasPrefix(line, "u "):
			fields := strings.Fields(line)
			state.UnmergedPaths = append(state.UnmergedPaths, fields[len(fields)-1])
		case strings.HasPrefix(line, "? "):
			state.UntrackedPaths = append(state.UntrackedPaths, line[2:])
		}
	}
	return state
}

// IsUnder reports whether a path sits under any of the given prefixes.
func IsUnder(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if prefix == "" {
			continue
		}
		trimmed := strings.TrimSuffix(prefix, "/")
		if path == trimmed || strings.HasPrefix(path, trimmed+"/") {
			return true
		}
	}
	return false
}

// ClassifyInput carries the context a classification needs.
type ClassifyInput struct {
	TemporaryPrefixes []string
	RemoteBranches    map[string]bool
	CurrentDirectory  string
}

// Classify attaches risk flags and blocking reasons to a report.
//
// Flags describe the worktree; blocking reasons are the specific facts that
// forbid automated removal.
func Classify(report *Report, input ClassifyInput) {
	item := report.Worktree
	exists := item.Exists()

	if !exists {
		report.Flags = append(report.Flags, Ghost)
	}
	if IsUnder(item.Path, input.TemporaryPrefixes) {
		report.Flags = append(report.Flags, Temporary)
		report.BlockingReasons = append(report.BlockingReasons,
			"hosted under a purge-prone temporary path; use rescue, not clean")
	}
	if item.Detached {
		report.Flags = append(report.Flags, Detached)
	}
	if item.Locked {
		report.Flags = append(report.Flags, Locked)
		reason := item.LockedReason
		if reason == "" {
			reason = "no reason recorded"
		}
		report.BlockingReasons = append(report.BlockingReasons, "locked: "+reason)
	}

	branch := item.ShortBranch()
	if branch != "" && exists && !item.IsMain {
		// A directory named after a branch it no longer holds is the classic sign
		// of a hand-managed worktree reused for different work. The main checkout
		// is named after the repository, so the comparison is meaningless there.
		leaf := branch
		if index := strings.LastIndex(branch, "/"); index >= 0 {
			leaf = branch[index+1:]
		}
		name := filepath.Base(item.Path)
		if name != leaf && name != branch {
			report.Flags = append(report.Flags, Drift)
		}
	}
	if branch != "" && !input.RemoteBranches[branch] {
		report.Flags = append(report.Flags, NoRemote)
	}

	if report.State.IsDirty() {
		report.Flags = append(report.Flags, Dirty)
		report.BlockingReasons = append(report.BlockingReasons,
			strconv.Itoa(report.State.DirtyCount())+" uncommitted or untracked path(s)")
	}
	if !report.State.Readable && exists {
		report.BlockingReasons = append(report.BlockingReasons, "working state could not be read")
	}

	if exists {
		if operation := gitx.InProgressOperation(item.Path); operation != "" {
			report.Flags = append(report.Flags, InProgress)
			report.BlockingReasons = append(report.BlockingReasons,
				"interrupted operation in progress: "+operation)
		}
	}

	if item.Detached && item.Head != "" {
		directory := item.Path
		if !exists {
			directory = report.Repository.MainWorktree
		}
		if len(gitx.ContainingRefs(directory, item.Head)) == 0 {
			report.Flags = append(report.Flags, UnreferencedHead)
			report.BlockingReasons = append(report.BlockingReasons,
				"detached HEAD is on no other ref; its reflog is the last reference")
		}
	}

	if report.Analysis != nil && report.Analysis.AtRisk() {
		report.Flags = append(report.Flags, AtRisk)
		if count := report.Analysis.Reachability; count < 0 {
			report.BlockingReasons = append(report.BlockingReasons, "reachability check failed")
		} else {
			report.BlockingReasons = append(report.BlockingReasons,
				strconv.Itoa(count)+" commit(s) exist on no other ref")
		}
	}

	if item.IsMain {
		report.BlockingReasons = append(report.BlockingReasons, "main checkout")
	}
	// Deliberately not gated on the directory existing. A path comparison costs
	// nothing, and gating it means a transient stat failure would make this guard
	// silently disappear.
	if input.CurrentDirectory != "" && IsUnder(input.CurrentDirectory, []string{item.Path}) {
		report.BlockingReasons = append(report.BlockingReasons, "contains the current working directory")
	}
}

// SizeBytes returns a worktree's working-tree size, or -1 when unmeasurable.
//
// Only the working tree is measured. Objects live in the shared common directory,
// so charging them to a worktree would count them once per worktree and wildly
// overstate what removing one reclaims.
func SizeBytes(path string) int64 {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return -1
	}
	var total int64
	walkErr := filepath.WalkDir(path, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() {
			if fileInfo, statErr := entry.Info(); statErr == nil {
				total += fileInfo.Size()
			}
		}
		return nil
	})
	if walkErr != nil {
		return -1
	}
	return total
}
