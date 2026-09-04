package gitx

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/gonaas/devctl/internal/process"
)

// These behaviours cannot be tested against captured output: the point is that
// git itself does something surprising, so the test has to ask git.

func gitTest(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.email=test@example.invalid",
		"-c", "user.name=devctl test",
		"-c", "commit.gpgsign=false",
	}, arguments...)
	result := process.Git(full, process.Options{Dir: repository})
	if !result.OK() {
		t.Fatalf("git %s failed: %s", strings.Join(arguments, " "), result.Stderr)
	}
	return strings.TrimSpace(result.Stdout)
}

func writeCommit(t *testing.T, repository, name, content, message string) {
	t.Helper()
	if err := writeFile(repository, name, content); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	gitTest(t, repository, "add", name)
	gitTest(t, repository, "commit", "-m", message)
}

// buildRepository shapes one throwaway repository containing every trap at once.
func buildRepository(t *testing.T) (repository, orphanCommit string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repository = t.TempDir()

	gitTest(t, repository, "init", "-q", "-b", "main")
	writeCommit(t, repository, "base.txt", "one\n", "base")

	// feature: two commits that exist on no other ref.
	gitTest(t, repository, "checkout", "-q", "-b", "feature")
	writeCommit(t, repository, "feature.txt", "alpha\n", "feature one")
	writeCommit(t, repository, "feature.txt", "alpha\nbeta\n", "feature two")

	// squashed: the same content landing on main as one rewritten commit, which
	// is exactly what a squash merge produces.
	gitTest(t, repository, "checkout", "-q", "-b", "squashed", "main")
	writeCommit(t, repository, "feature.txt", "alpha\nbeta\n", "squashed feature")

	// conflicting: touches the same file main also changes.
	gitTest(t, repository, "checkout", "-q", "-b", "conflicting", "main")
	writeCommit(t, repository, "base.txt", "theirs\n", "conflicting change")

	gitTest(t, repository, "checkout", "-q", "main")
	writeCommit(t, repository, "base.txt", "ours\n", "main moves on")

	// An orphaned commit made on a detached HEAD: reachable from no branch, so
	// only its worktree reflog still refers to it.
	gitTest(t, repository, "checkout", "-q", "--detach", "main")
	writeCommit(t, repository, "orphan.txt", "stranded\n", "made while detached")
	orphanCommit = gitTest(t, repository, "rev-parse", "HEAD")
	gitTest(t, repository, "checkout", "-q", "main")

	return repository, orphanCommit
}

func TestExcludeMustUseTheShortNameAndComeFirst(t *testing.T) {
	// This is the tool's hard safety gate. A wrong answer here does not raise; it
	// quietly authorises deleting unrecoverable work.
	repository, _ := buildRepository(t)

	if got := ReachabilityCount(repository, "feature", "feature"); got != 2 {
		t.Fatalf("feature's two commits exist on no other ref, want 2, got %d", got)
	}

	wrongNamespace := process.Git([]string{
		"rev-list", "--count", "feature", "--not",
		"--exclude=refs/heads/feature", "--branches", "--tags", "--remotes",
	}, process.Options{Dir: repository})
	if got := strings.TrimSpace(wrongNamespace.Stdout); got != "0" {
		t.Errorf("a fully qualified pattern matches nothing, so the branch cancels itself out; want 0, got %s", got)
	}

	wrongOrder := process.Git([]string{
		"rev-list", "--count", "feature", "--not",
		"--branches", "--tags", "--remotes", "--exclude=feature",
	}, process.Options{Dir: repository})
	if got := strings.TrimSpace(wrongOrder.Stdout); got != "0" {
		t.Errorf("--exclude after --branches affects nothing; want 0, got %s", got)
	}
}

func TestDetachedHeadOnABranchTipIsSafe(t *testing.T) {
	// Sitting on a branch tip is not risk: the branch still holds the commits.
	repository, _ := buildRepository(t)
	tip := gitTest(t, repository, "rev-parse", "feature")
	if got := ReachabilityCount(repository, "", tip); got != 0 {
		t.Errorf("want 0, got %d", got)
	}
}

func TestOrphanedDetachedCommitIsReportedAtRisk(t *testing.T) {
	repository, orphan := buildRepository(t)
	if got := ReachabilityCount(repository, "", orphan); got != 1 {
		t.Errorf("a commit made while detached lives on no ref; want 1, got %d", got)
	}
	if refs := ContainingRefs(repository, orphan); len(refs) != 0 {
		t.Errorf("expected no containing refs, got %v", refs)
	}
}

func TestSquashMergeDefeatsAncestryButNotTreeEquality(t *testing.T) {
	repository, _ := buildRepository(t)
	if IsAncestor(repository, "feature", "squashed") {
		t.Error("a squash rewrites commit identity, so ancestry cannot see it")
	}
	trial := MergeTree(repository, "squashed", "feature", false)
	if trial.Status != MergeClean {
		t.Fatalf("want CLEAN, got %s", trial.Status)
	}
	if want := TreeOf(repository, "squashed"); trial.Tree != want {
		t.Errorf("merging feature into squashed must change nothing: %s != %s", trial.Tree, want)
	}
}

func TestMergeTreeReportsConflictsWithPaths(t *testing.T) {
	repository, _ := buildRepository(t)
	trial := MergeTree(repository, "main", "conflicting", true)
	if trial.Status != MergeConflicts {
		t.Fatalf("want CONFLICTS, got %s", trial.Status)
	}
	found := false
	for _, path := range trial.ConflictedPaths {
		if path == "base.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("base.txt missing from %v", trial.ConflictedPaths)
	}
}

func TestUnknownRefIsAnErrorNotAConflict(t *testing.T) {
	// git exits 1 for a bad ref exactly as it does for a conflict.
	repository, _ := buildRepository(t)
	trial := MergeTree(repository, "main", "no-such-branch", true)
	if trial.Status != MergeError {
		t.Errorf("want ERROR, got %s", trial.Status)
	}
	if len(trial.ConflictedPaths) != 0 {
		t.Errorf("an error must report no conflicted paths, got %v", trial.ConflictedPaths)
	}
}

func TestAheadBehindReportsAheadFirst(t *testing.T) {
	repository, _ := buildRepository(t)
	signals := BranchSignals(repository, "main")
	feature, ok := signals["feature"]
	if !ok {
		t.Fatal("feature missing from the ref walk")
	}
	if feature.Ahead != 2 || feature.Behind != 1 {
		t.Errorf("want ahead=2 behind=1, got ahead=%d behind=%d", feature.Ahead, feature.Behind)
	}
}

func TestUnpushedCountsEverythingWithoutARemote(t *testing.T) {
	repository, _ := buildRepository(t)
	if got := UnpushedCount(repository, "feature"); got <= 0 {
		t.Errorf("want a positive count, got %d", got)
	}
}

func TestMergedVerdictUsesTreeEqualityForASquash(t *testing.T) {
	repository, _ := buildRepository(t)
	verdict := EvaluateMerged(repository, EvaluateMergedInput{
		Branch:   "feature",
		Tip:      "feature",
		Base:     "squashed",
		BaseTree: TreeOf(repository, "squashed"),
	})
	if verdict.Status != Merged {
		t.Fatalf("want MERGED, got %s", verdict.Status)
	}
	if len(verdict.Signals) == 0 || verdict.Signals[0] != "tree-equality" {
		t.Errorf("want tree-equality, got %v", verdict.Signals)
	}
}

func TestUnmergedBranchIsUnknownWithoutAProvider(t *testing.T) {
	// Absence of proof is never proof of absence.
	repository, _ := buildRepository(t)
	verdict := EvaluateMerged(repository, EvaluateMergedInput{
		Branch:   "feature",
		Tip:      "feature",
		Base:     "main",
		BaseTree: TreeOf(repository, "main"),
	})
	if verdict.Status != Unknown {
		t.Errorf("want UNKNOWN, got %s", verdict.Status)
	}
}

func TestProviderClaimIsIgnoredWithoutLocalProof(t *testing.T) {
	repository, _ := buildRepository(t)
	unprovable := PullRequest{
		Number:         1,
		HeadRef:        "feature",
		HeadOID:        strings.Repeat("0", 40),
		BaseRef:        "main",
		State:          "MERGED",
		MergeCommitOID: strings.Repeat("0", 40),
	}
	verdict := EvaluateMerged(repository, EvaluateMergedInput{
		Branch:       "feature",
		Tip:          "feature",
		Base:         "main",
		BaseTree:     TreeOf(repository, "main"),
		PullRequests: func() []PullRequest { return []PullRequest{unprovable} },
		HasProvider:  true,
	})
	if verdict.Status != NotMerged {
		t.Errorf("a merged flag whose commits are not in this base proves nothing; want NOT_MERGED, got %s", verdict.Status)
	}
}

func TestResolveBaseFallsBackToWellKnownNames(t *testing.T) {
	repository, _ := buildRepository(t)
	// No remote at all: every network path must fail closed rather than hang.
	if got := ResolveBase(repository, "", ""); got != "" {
		t.Errorf("want no base without a remote, got %q", got)
	}
	if got := ResolveBase(repository, "main", ""); got != "main" {
		t.Errorf("an explicit override must win, got %q", got)
	}
	if got := ResolveBase(repository, "no-such-ref", ""); got != "" {
		t.Errorf("an override that does not resolve must not be accepted, got %q", got)
	}
}
