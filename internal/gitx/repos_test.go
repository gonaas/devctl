package gitx

import (
	"path/filepath"
	"strings"
	"testing"
)

const mixedPorcelain = `worktree /repo/primary
HEAD 1111111111111111111111111111111111111111
branch refs/heads/develop

worktree /repo/detached
HEAD 2222222222222222222222222222222222222222
detached

worktree /repo/ghost
HEAD 3333333333333333333333333333333333333333
detached
prunable gitdir file points to non-existent location

worktree /repo/held
HEAD 4444444444444444444444444444444444444444
branch refs/heads/feature/one
locked under review

worktree /repo/container/primary
HEAD 5555555555555555555555555555555555555555
branch refs/heads/local/docker
`

func TestFirstRecordIsTheMainWorktree(t *testing.T) {
	records := ParseWorktreePorcelain(mixedPorcelain)
	if len(records) != 5 {
		t.Fatalf("expected 5 records, got %d", len(records))
	}
	if !records[0].IsMain {
		t.Error("the first record must be the main worktree")
	}
	for _, record := range records[1:] {
		if record.IsMain {
			t.Errorf("%s must not be marked main", record.Path)
		}
	}
}

func TestDetachedRecordHasNoBranch(t *testing.T) {
	detached := ParseWorktreePorcelain(mixedPorcelain)[1]
	if !detached.Detached {
		t.Error("record must be detached")
	}
	if detached.HasBranch {
		t.Error("a detached record carries no branch line at all")
	}
	if detached.ShortBranch() != "" {
		t.Errorf("short branch must be empty, got %q", detached.ShortBranch())
	}
}

func TestReasonsAreCapturedForLockedAndPrunable(t *testing.T) {
	records := ParseWorktreePorcelain(mixedPorcelain)
	if !records[2].Prunable || records[2].PrunableReason != "gitdir file points to non-existent location" {
		t.Errorf("prunable reason not captured: %+v", records[2])
	}
	if !records[3].Locked || records[3].LockedReason != "under review" {
		t.Errorf("locked reason not captured: %+v", records[3])
	}
}

func TestBasenameCollisionKeepsRecordsDistinct(t *testing.T) {
	// A nested checkout can share a basename with the primary. Keying anything by
	// basename would merge two unrelated worktrees into one.
	records := ParseWorktreePorcelain(mixedPorcelain)
	primaries := 0
	paths := map[string]bool{}
	for _, record := range records {
		if filepath.Base(record.Path) == "primary" {
			primaries++
		}
		paths[record.Path] = true
	}
	if primaries != 2 {
		t.Errorf("expected two records named primary, got %d", primaries)
	}
	if len(paths) != len(records) {
		t.Error("records collapsed: paths are not distinct")
	}
}

func TestNULSeparatedOutputParsesIdentically(t *testing.T) {
	newlineForm := ParseWorktreePorcelain(mixedPorcelain)
	nulForm := ParseWorktreePorcelain(strings.ReplaceAll(mixedPorcelain, "\n", "\x00"))
	if len(newlineForm) != len(nulForm) {
		t.Fatalf("record counts differ: %d vs %d", len(newlineForm), len(nulForm))
	}
	for index := range newlineForm {
		if newlineForm[index].Path != nulForm[index].Path {
			t.Errorf("record %d differs: %s vs %s", index, newlineForm[index].Path, nulForm[index].Path)
		}
	}
}

func TestBranchPrefixIsStripped(t *testing.T) {
	records := ParseWorktreePorcelain(mixedPorcelain)
	if got := records[3].ShortBranch(); got != "feature/one" {
		t.Errorf("want feature/one, got %q", got)
	}
	if got := records[4].ShortBranch(); got != "local/docker" {
		t.Errorf("want local/docker, got %q", got)
	}
}

func TestEmptyOutputYieldsNoRecords(t *testing.T) {
	if records := ParseWorktreePorcelain(""); len(records) != 0 {
		t.Errorf("expected no records, got %d", len(records))
	}
}
