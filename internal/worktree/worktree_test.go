package worktree

import (
	"strings"
	"testing"

	"github.com/gonaas/devctl/internal/gitx"
)

func makeReport(item gitx.Worktree, state State, analysis *Analysis) *Report {
	return &Report{
		Repository: &gitx.Repository{CommonDir: "/r/.git", MainWorktree: "/r"},
		Worktree:   item,
		State:      state,
		Analysis:   analysis,
	}
}

func hasFlag(report *Report, flag string) bool {
	for _, item := range report.Flags {
		if item == flag {
			return true
		}
	}
	return false
}

func blockedBy(report *Report, fragment string) bool {
	for _, reason := range report.BlockingReasons {
		if strings.Contains(reason, fragment) {
			return true
		}
	}
	return false
}

func TestTemporaryPathIsFlaggedAndSentToRescue(t *testing.T) {
	report := makeReport(
		gitx.Worktree{Path: "/private/tmp/session/scratch/wt", Branch: "refs/heads/x", HasBranch: true},
		State{Readable: true}, nil,
	)
	Classify(report, ClassifyInput{TemporaryPrefixes: []string{"/private/tmp"}})
	if !hasFlag(report, Temporary) {
		t.Error("a purge-prone path must be flagged")
	}
	if !blockedBy(report, "use rescue, not clean") {
		t.Error("a temporary worktree must be routed to rescue, never to removal")
	}
}

func TestMainCheckoutIsNeverFlaggedForDrift(t *testing.T) {
	// The main checkout is named after the repository, not after its branch, so
	// the comparison is meaningless there.
	report := makeReport(
		gitx.Worktree{Path: "/r", Branch: "refs/heads/develop", HasBranch: true, IsMain: true},
		State{Readable: true}, nil,
	)
	Classify(report, ClassifyInput{RemoteBranches: map[string]bool{"develop": true}})
	if hasFlag(report, Drift) {
		t.Error("the main checkout must never be flagged as drifted")
	}
	if !blockedBy(report, "main checkout") {
		t.Error("the main checkout must always block removal")
	}
}

func TestDirtyStateBlocksAndIsCounted(t *testing.T) {
	state := State{
		Readable:       true,
		ChangedPaths:   []string{"a", "b"},
		UntrackedPaths: []string{"c"},
	}
	if state.DirtyCount() != 3 || !state.IsDirty() {
		t.Fatalf("dirty count = %d", state.DirtyCount())
	}
	report := makeReport(gitx.Worktree{Path: "/r/wt", Branch: "refs/heads/x", HasBranch: true}, state, nil)
	Classify(report, ClassifyInput{})
	if !hasFlag(report, Dirty) || !blockedBy(report, "3 uncommitted or untracked path(s)") {
		t.Errorf("dirty state must block removal: %+v", report.BlockingReasons)
	}
}

func TestGlobalStashCountDoesNotBlock(t *testing.T) {
	// refs/stash is repository-global, not per-worktree. Treating a stash count
	// as per-worktree dirt would block every worktree in the repository forever.
	state := State{Readable: true, StashCount: 12}
	if state.IsDirty() {
		t.Error("a repository-wide stash count must not make a worktree dirty")
	}
	report := makeReport(gitx.Worktree{Path: "/r/wt", Branch: "refs/heads/x", HasBranch: true}, state, nil)
	Classify(report, ClassifyInput{})
	if len(report.BlockingReasons) != 0 {
		t.Errorf("stashes must not block: %v", report.BlockingReasons)
	}
}

func TestFailedReachabilityCheckCountsAsRisk(t *testing.T) {
	analysis := &Analysis{Reachability: -1}
	if !analysis.AtRisk() {
		t.Error("a check that could not run must be treated as risk")
	}
	report := makeReport(gitx.Worktree{Path: "/r/wt"}, State{Readable: true}, analysis)
	Classify(report, ClassifyInput{})
	if !hasFlag(report, AtRisk) || !blockedBy(report, "reachability check failed") {
		t.Errorf("flags=%v reasons=%v", report.Flags, report.BlockingReasons)
	}
}

func TestZeroReachabilityIsNotRisk(t *testing.T) {
	if (&Analysis{Reachability: 0}).AtRisk() {
		t.Error("zero commits on no other ref is exactly what safe means")
	}
}

func TestCurrentDirectoryIsProtected(t *testing.T) {
	report := makeReport(gitx.Worktree{Path: "/r/wt", Branch: "refs/heads/x", HasBranch: true}, State{Readable: true}, nil)
	Classify(report, ClassifyInput{CurrentDirectory: "/r/wt/nested/deeper"})
	if !blockedBy(report, "contains the current working directory") {
		t.Error("the worktree holding the current directory must never be removed")
	}
}

func TestIsUnderRejectsSharedNamePrefixes(t *testing.T) {
	if IsUnder("/private/tmpfoo", []string{"/private/tmp"}) {
		t.Error("/private/tmpfoo is not under /private/tmp")
	}
	if !IsUnder("/private/tmp/x", []string{"/private/tmp"}) {
		t.Error("/private/tmp/x is under /private/tmp")
	}
	if !IsUnder("/private/tmp", []string{"/private/tmp"}) {
		t.Error("a path is under itself for this purpose")
	}
}

func TestOnlyHazardsCountAsProblems(t *testing.T) {
	// Ordinary working state must not be reported as a problem, or the problem
	// view lists nearly everything and stops being a view.
	ordinary := []string{Dirty, Detached, NoRemote, Drift}
	for _, flag := range ordinary {
		report := &Report{Flags: []string{flag}}
		if report.HasHazard() {
			t.Errorf("%s describes ordinary state and must not be a hazard", flag)
		}
	}
	hazards := []string{Temporary, Ghost, AtRisk, UnreferencedHead, Locked, InProgress}
	for _, flag := range hazards {
		report := &Report{Flags: []string{flag}}
		if !report.HasHazard() {
			t.Errorf("%s is worth acting on and must be a hazard", flag)
		}
	}
	if (&Report{}).HasHazard() {
		t.Error("no flags means no hazard")
	}
	if !(&Report{Flags: []string{Dirty, AtRisk}}).HasHazard() {
		t.Error("one hazard among ordinary flags still counts")
	}
}
