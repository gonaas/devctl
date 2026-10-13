package gitx

import (
	"os/exec"
	"strings"
	"testing"
)

// buildChangeRangeRepository shapes the one range this feature reports on: a fork
// point the base has since moved past, plus every file shape that carries its own
// parsing hazard.
//
// The returned forkPoint is the merge base by construction, captured before the
// base branch moved on, so a merge-base assertion is not circular.
func buildChangeRangeRepository(t *testing.T) (repository, forkPoint string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repository = t.TempDir()

	gitTest(t, repository, "init", "-q", "-b", "main")
	writeCommit(t, repository, "modified.txt", "a\nb\nc\n", "base: modified candidate")
	writeCommit(t, repository, "deleted.txt", "bye\n", "base: deletion candidate")
	writeCommit(t, repository, "renamed.txt", "same\n", "base: rename candidate")
	writeCommit(t, repository, "con espacios ñ.txt", "unicode\n", "base: awkward path")
	writeCommit(t, repository, "binary.bin", "\x00\x01\x02\xff", "base: binary candidate")
	forkPoint = gitTest(t, repository, "rev-parse", "HEAD")

	gitTest(t, repository, "checkout", "-q", "-b", "feature")
	writeCommit(t, repository, "modified.txt", "a\nB\nc\nD\n", "feature: modify")
	if err := writeFile(repository, "added.txt", "new\n"); err != nil {
		t.Fatalf("write added.txt: %v", err)
	}
	gitTest(t, repository, "add", "added.txt")
	gitTest(t, repository, "rm", "-q", "deleted.txt")
	gitTest(t, repository, "mv", "renamed.txt", "renamed-moved.txt")
	if err := writeFile(repository, "con espacios ñ.txt", "unicode plus\n"); err != nil {
		t.Fatalf("write awkward path: %v", err)
	}
	gitTest(t, repository, "add", "con espacios ñ.txt")
	if err := writeFile(repository, "binary.bin", "\x00\x01\x03\xff"); err != nil {
		t.Fatalf("write binary.bin: %v", err)
	}
	gitTest(t, repository, "add", "binary.bin")
	gitTest(t, repository, "commit", "-q", "-m", "feature: every shape at once")

	// The base moves on after the fork. None of this may appear in the range.
	gitTest(t, repository, "checkout", "-q", "main")
	writeCommit(t, repository, "ahead.txt", "ahead\n", "base: new file after the fork")
	writeCommit(t, repository, "modified.txt", "a\nb\nc\nZ\n", "base: modify after the fork")
	gitTest(t, repository, "checkout", "-q", "feature")

	return repository, forkPoint
}

func changeByPath(t *testing.T, changes []FileChange, path string) FileChange {
	t.Helper()
	for _, change := range changes {
		if change.Path == path {
			return change
		}
	}
	t.Fatalf("no change reported for %q; got %v", path, changes)
	return FileChange{}
}

func TestMergeBaseFindsTheForkPointTheBaseHasMovedPast(t *testing.T) {
	repository, forkPoint := buildChangeRangeRepository(t)

	got, err := MergeBase(repository, "main")
	if err != nil {
		t.Fatalf("MergeBase: %v", err)
	}
	if got != forkPoint {
		t.Errorf("merge base = %q, want the fork point %q", got, forkPoint)
	}
}

func TestMergeBaseRejectsAnUnresolvableReference(t *testing.T) {
	repository, _ := buildChangeRangeRepository(t)

	if _, err := MergeBase(repository, "no-such-ref"); err == nil {
		t.Fatal("an unresolvable reference must fail loudly, not return an empty base")
	}
}

func TestChangedFilesReportsEveryShapeOnce(t *testing.T) {
	repository, forkPoint := buildChangeRangeRepository(t)

	changes, err := ChangedFiles(repository, forkPoint, "HEAD")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}

	if len(changes) != 6 {
		t.Fatalf("got %d changes, want 6: %v", len(changes), changes)
	}
	for _, path := range []string{"ahead.txt"} {
		for _, change := range changes {
			if change.Path == path {
				t.Errorf("%s exists only on the base and must not appear: %v", path, change)
			}
		}
	}

	modified := changeByPath(t, changes, "modified.txt")
	if modified.Status != StatusModified || modified.Added != 2 || modified.Deleted != 1 {
		t.Errorf("modified.txt = %+v, want modified +2/-1", modified)
	}

	added := changeByPath(t, changes, "added.txt")
	if added.Status != StatusAdded || added.Added != 1 || added.Deleted != 0 {
		t.Errorf("added.txt = %+v, want added +1/-0", added)
	}

	deleted := changeByPath(t, changes, "deleted.txt")
	if deleted.Status != StatusDeleted || deleted.Added != 0 || deleted.Deleted != 1 {
		t.Errorf("deleted.txt = %+v, want deleted +0/-1", deleted)
	}

	renamed := changeByPath(t, changes, "renamed-moved.txt")
	if renamed.Status != StatusRenamed {
		t.Errorf("renamed-moved.txt status = %q, want %q", renamed.Status, StatusRenamed)
	}
	if renamed.PreviousPath != "renamed.txt" {
		t.Errorf("renamed-moved.txt previous path = %q, want renamed.txt", renamed.PreviousPath)
	}

	awkward := changeByPath(t, changes, "con espacios ñ.txt")
	if awkward.Status != StatusModified || awkward.Added != 1 || awkward.Deleted != 1 {
		t.Errorf("awkward path = %+v, want modified +1/-1 with the name intact", awkward)
	}
	if awkward.Path != "con espacios ñ.txt" {
		t.Errorf("awkward path was rewritten to %q", awkward.Path)
	}

	binary := changeByPath(t, changes, "binary.bin")
	if !binary.Binary {
		t.Errorf("binary.bin must be flagged binary: %+v", binary)
	}
	if !binary.CountsUnavailable {
		t.Errorf("binary.bin must report unavailable counts rather than a real zero: %+v", binary)
	}
}

func TestPatchCarriesTheRangeDiff(t *testing.T) {
	repository, forkPoint := buildChangeRangeRepository(t)

	patch, err := Patch(repository, forkPoint, "HEAD")
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !strings.Contains(patch, "+B") {
		t.Errorf("patch does not carry the added line: %q", patch)
	}
	if strings.Contains(patch, "ahead.txt") {
		t.Errorf("patch leaked a change that belongs to the base: %q", patch)
	}
}
