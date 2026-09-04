package cleanup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gonaas/devctl/internal/gitx"
	"github.com/gonaas/devctl/internal/worktree"
)

type reportOptions struct {
	merged       gitx.MergedStatus
	reachability int
	blocking     []string
	dirty        int
	isMain       bool
	noAnalysis   bool
}

func makeReport(options reportOptions) *worktree.Report {
	repository := &gitx.Repository{CommonDir: "/r/.git", MainWorktree: "/r"}
	item := gitx.Worktree{
		Path: "/r/wt", Head: strings.Repeat("a", 40),
		Branch: "refs/heads/topic", HasBranch: true, IsMain: options.isMain,
	}
	state := worktree.State{Readable: true}
	for index := 0; index < options.dirty; index++ {
		state.ChangedPaths = append(state.ChangedPaths, "file")
	}
	report := &worktree.Report{
		Repository: repository, Worktree: item, State: state,
		BlockingReasons: options.blocking,
	}
	if !options.noAnalysis {
		var signals []string
		if options.merged == gitx.Merged {
			signals = []string{"tree-equality"}
		}
		report.Analysis = &worktree.Analysis{
			Signal:       gitx.BranchSignal{Name: "topic", Tip: item.Head},
			Merged:       gitx.MergedVerdict{Status: options.merged, Signals: signals},
			Reachability: options.reachability,
		}
	}
	return report
}

// Three things must line up; any one missing means the worktree is kept.

func TestProvenMergedAndFullyReachableIsProposed(t *testing.T) {
	plan := Build([]*worktree.Report{makeReport(reportOptions{merged: gitx.Merged})})
	if len(plan.Proposals) != 1 {
		t.Fatalf("want one proposal, got %d", len(plan.Proposals))
	}
	if !strings.Contains(plan.Proposals[0].Reasons[0], "merged via tree-equality") {
		t.Errorf("reason must name the proving signal: %v", plan.Proposals[0].Reasons)
	}
}

func TestUnknownIsNeverProposed(t *testing.T) {
	plan := Build([]*worktree.Report{makeReport(reportOptions{merged: gitx.Unknown})})
	if !plan.IsEmpty() {
		t.Fatal("UNKNOWN must never be deletable")
	}
	if !strings.Contains(plan.Skipped[0].Reason, "UNKNOWN") {
		t.Errorf("reason must name the status: %s", plan.Skipped[0].Reason)
	}
}

func TestNotMergedIsNeverProposed(t *testing.T) {
	if !Build([]*worktree.Report{makeReport(reportOptions{merged: gitx.NotMerged})}).IsEmpty() {
		t.Error("NOT_MERGED must never be deletable")
	}
}

func TestCommitsOnNoOtherRefVetoAMergedBranch(t *testing.T) {
	plan := Build([]*worktree.Report{makeReport(reportOptions{merged: gitx.Merged, reachability: 1})})
	if !plan.IsEmpty() {
		t.Fatal("orphan risk must veto a merged branch")
	}
	if !strings.Contains(plan.Skipped[0].Reason, "no other ref") {
		t.Errorf("reason must name the risk: %s", plan.Skipped[0].Reason)
	}
}

func TestFailedReachabilityCheckIsTreatedAsRisk(t *testing.T) {
	if !Build([]*worktree.Report{makeReport(reportOptions{merged: gitx.Merged, reachability: -1})}).IsEmpty() {
		t.Error("a check that could not run must not be read as safe")
	}
}

func TestAnyBlockingReasonWinsOverACleanVerdict(t *testing.T) {
	reasons := []string{
		"hosted under a purge-prone temporary path; use rescue, not clean",
		"3 uncommitted or untracked path(s)",
		"locked: under review",
		"interrupted operation in progress: rebase-merge",
		"main checkout",
		"contains the current working directory",
	}
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			plan := Build([]*worktree.Report{makeReport(reportOptions{merged: gitx.Merged, blocking: []string{reason}})})
			if !plan.IsEmpty() {
				t.Errorf("%q must veto removal", reason)
			}
		})
	}
}

func TestMissingAnalysisIsKept(t *testing.T) {
	if !Build([]*worktree.Report{makeReport(reportOptions{noAnalysis: true})}).IsEmpty() {
		t.Error("no analysis means no proof, so nothing may be removed")
	}
}

func TestEveryKeptWorktreeCarriesAReason(t *testing.T) {
	plan := Build([]*worktree.Report{
		makeReport(reportOptions{merged: gitx.Unknown}),
		makeReport(reportOptions{merged: gitx.Merged, reachability: 2}),
		makeReport(reportOptions{merged: gitx.Merged, blocking: []string{"main checkout"}}),
	})
	if len(plan.Skipped) != 3 {
		t.Fatalf("want three kept, got %d", len(plan.Skipped))
	}
	for _, item := range plan.Skipped {
		if strings.TrimSpace(item.Reason) == "" {
			t.Error("a kept worktree must say why")
		}
	}
}

func TestManifestIsWrittenAtomicallyAndCarriesRestoreCommands(t *testing.T) {
	plan := Build([]*worktree.Report{makeReport(reportOptions{merged: gitx.Merged})})
	manifest := BuildManifest(plan, map[string]string{"/r/.git": "origin/main"})

	root := t.TempDir()
	path, err := WriteManifest(manifest, root)
	if err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var decoded Manifest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if decoded.Schema != ManifestSchema {
		t.Errorf("schema %q", decoded.Schema)
	}
	if len(decoded.Entries) != 1 {
		t.Fatalf("want one entry, got %d", len(decoded.Entries))
	}
	entry := decoded.Entries[0]
	if len(entry.Restore) != 2 {
		t.Fatalf("want a branch and a worktree restore command, got %v", entry.Restore)
	}
	for _, command := range entry.Restore {
		if !strings.HasPrefix(command, "git -C /r ") {
			t.Errorf("restore command is not runnable as written: %q", command)
		}
		if !strings.Contains(command, entry.Tip) {
			t.Errorf("restore command must pin the tip commit: %q", command)
		}
	}
	if !strings.Contains(decoded.Note, "fetch --prune") {
		t.Error("the manifest must warn against pruning before restoring")
	}

	entries, _ := os.ReadDir(root)
	for _, item := range entries {
		if strings.HasSuffix(item.Name(), ".tmp") {
			t.Errorf("a temporary file was left behind: %s", item.Name())
		}
	}
	if filepath.Ext(path) != ".json" {
		t.Errorf("manifest should be a .json file: %s", path)
	}
}
