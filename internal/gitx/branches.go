package gitx

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gonaas/devctl/internal/process"
)

// MergedStatus is a three-valued merge verdict. Absence of proof is never proof
// of absence, which is why Unknown exists and is never treated as deletable.
type MergedStatus string

const (
	Merged    MergedStatus = "MERGED"
	NotMerged MergedStatus = "NOT_MERGED"
	Unknown   MergedStatus = "UNKNOWN"
)

// MergeTreeStatus is the outcome of a trial merge that touches nothing.
type MergeTreeStatus string

const (
	MergeClean     MergeTreeStatus = "CLEAN"
	MergeConflicts MergeTreeStatus = "CONFLICTS"
	MergeError     MergeTreeStatus = "ERROR"
)

var baseCandidates = []string{"develop", "main", "master", "trunk"}

const fieldSeparator = "\x09"

// inProgressMarkers are the real signs of an interrupted operation.
//
// MERGE_RR is deliberately absent: it is rerere state that lingers long after a
// merge finishes and would block every worktree that ever hit a conflict.
//
// REBASE_HEAD is absent for the same reason: git writes it when a rebase stops
// on a conflict and never removes it, so it outlives the operation by months.
// Only rebase-merge and rebase-apply prove a rebase is still running.
var inProgressMarkers = []string{
	"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD",
	"BISECT_LOG", "rebase-merge", "rebase-apply",
}

// MergeTreeResult is what a trial merge produced.
type MergeTreeResult struct {
	Status          MergeTreeStatus
	Tree            string
	ConflictedPaths []string
}

// BranchSignal is everything one batch ref walk knows about a branch.
type BranchSignal struct {
	Name         string
	Tip          string
	CommittedAt  int64
	WorktreePath string
	Upstream     string
	Ahead        int
	Behind       int
}

// PullRequest is a hosting provider's change request, reduced to what can be
// proved locally. HeadOID and MergeCommitOID are load-bearing: without them a
// merged flag is only a claim about a branch name.
type PullRequest struct {
	Number         int
	HeadRef        string
	HeadOID        string
	BaseRef        string
	State          string
	MergeCommitOID string
	URL            string
}

// MergedVerdict is a merge verdict plus the evidence that produced it.
type MergedVerdict struct {
	Status            MergedStatus
	Signals           []string
	PullRequestNumber int
	PullRequestURL    string
	Detail            string
}

func gitAt(directory string, timeout time.Duration, arguments ...string) process.Result {
	return process.Git(arguments, process.Options{Dir: directory, Timeout: timeout})
}

// RefExists reports whether a commit-ish resolves in a repository.
func RefExists(directory, reference string) bool {
	return gitAt(directory, 10*time.Second, "rev-parse", "--verify", "-q", reference+"^{commit}").OK()
}

// ResolveBase returns the base ref to compare branches against.
//
// Ordered: an explicit override, the recorded origin HEAD, a provider default,
// the remote's symbolic HEAD, then well-known names. Never derived from
// branch.<name>.merge, which points at the base itself for branches configured
// to track it.
func ResolveBase(directory, override, providerDefault string) string {
	if override != "" {
		if RefExists(directory, override) {
			return override
		}
		return ""
	}

	if symbolic := gitAt(directory, 10*time.Second, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); symbolic.OK() {
		if value := strings.TrimSpace(symbolic.Stdout); value != "" {
			return value
		}
	}

	if providerDefault != "" {
		candidate := "origin/" + providerDefault
		if RefExists(directory, candidate) {
			return candidate
		}
	}

	if remote := gitAt(directory, 30*time.Second, "ls-remote", "--symref", "origin", "HEAD"); remote.OK() {
		for _, line := range remote.Lines() {
			if strings.HasPrefix(line, "ref:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					candidate := "origin/" + strings.TrimPrefix(fields[1], "refs/heads/")
					if RefExists(directory, candidate) {
						return candidate
					}
				}
			}
		}
	}

	for _, name := range baseCandidates {
		candidate := "origin/" + name
		if RefExists(directory, candidate) {
			return candidate
		}
	}
	return ""
}

// BranchSignals returns every local branch with its divergence from base, in one
// ref walk.
//
// A base ref that does not resolve makes %(ahead-behind:) abort the entire walk
// with exit 128, so callers must validate the base first.
func BranchSignals(directory, base string) map[string]BranchSignal {
	format := strings.Join([]string{
		"%(refname:short)",
		"%(objectname)",
		"%(committerdate:unix)",
		"%(worktreepath)",
		"%(upstream:short)",
		fmt.Sprintf("%%(ahead-behind:%s)", base),
	}, fieldSeparator)

	result := gitAt(directory, 60*time.Second, "for-each-ref", "refs/heads/", "--format="+format)
	if !result.OK() {
		return nil
	}

	signals := map[string]BranchSignal{}
	for _, line := range result.Lines() {
		parts := strings.Split(line, fieldSeparator)
		if len(parts) < 6 {
			continue
		}
		// %(ahead-behind:) reports ahead first. git rev-list --left-right --count
		// reports the opposite order; mixing them up inverts every verdict.
		aheadText, behindText, _ := strings.Cut(strings.TrimSpace(parts[5]), " ")
		committed, _ := strconv.ParseInt(parts[2], 10, 64)
		ahead, _ := strconv.Atoi(strings.TrimSpace(aheadText))
		behind, _ := strconv.Atoi(strings.TrimSpace(behindText))
		signals[parts[0]] = BranchSignal{
			Name:         parts[0],
			Tip:          parts[1],
			CommittedAt:  committed,
			WorktreePath: parts[3],
			Upstream:     parts[4],
			Ahead:        ahead,
			Behind:       behind,
		}
	}
	return signals
}

// RemoteBranchNames returns the branch names still present on a remote.
func RemoteBranchNames(directory, remote string) map[string]bool {
	if remote == "" {
		remote = "origin"
	}
	result := gitAt(directory, 30*time.Second, "for-each-ref", "refs/remotes/"+remote+"/", "--format=%(refname:strip=3)")
	names := map[string]bool{}
	if !result.OK() {
		return names
	}
	for _, line := range result.Lines() {
		if line != "HEAD" {
			names[line] = true
		}
	}
	return names
}

// IsAncestor reports whether one commit is reachable from another.
//
// Positive-only: a false result never proves independent work, because a squash
// merge rewrites commit identity.
func IsAncestor(directory, candidate, descendant string) bool {
	if candidate == "" || descendant == "" {
		return false
	}
	return gitAt(directory, 20*time.Second, "merge-base", "--is-ancestor", candidate, descendant).Code == 0
}

// TreeOf returns the tree object id of a commit-ish.
func TreeOf(directory, reference string) string {
	result := gitAt(directory, 15*time.Second, "rev-parse", reference+"^{tree}")
	if !result.OK() {
		return ""
	}
	return result.FirstLine()
}

// MergeTree runs a trial merge without touching the working tree, index or HEAD.
//
// The documented exit status is wrong for one case: an unknown ref also exits 1,
// with empty stdout and the message on stderr. Telling that apart from a genuine
// conflict requires inspecting stdout, never the exit code alone.
//
// Note that --write-tree does add unreachable objects to the object store, so
// this is not literally read-only against .git/objects.
func MergeTree(directory, base, tip string, namesOnly bool) MergeTreeResult {
	arguments := []string{"merge-tree", "--write-tree", "--no-messages"}
	if namesOnly {
		arguments = append(arguments, "--name-only")
	}
	arguments = append(arguments, base, tip)

	result := gitAt(directory, 60*time.Second, arguments...)
	lines := result.Lines()

	if result.Code == 0 {
		tree := ""
		if len(lines) > 0 {
			tree = lines[0]
		}
		return MergeTreeResult{Status: MergeClean, Tree: tree}
	}
	if result.Code == 1 && len(lines) > 0 && isObjectID(lines[0]) {
		return MergeTreeResult{Status: MergeConflicts, Tree: lines[0], ConflictedPaths: lines[1:]}
	}
	return MergeTreeResult{Status: MergeError}
}

func isObjectID(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, char := range value {
		isHex := (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

// ReachabilityCount returns how many commits on a tip exist on no other ref.
//
// Zero means deleting the branch loses nothing. This is the hard safety gate: it
// needs no network, no hosting provider, and no assumption about how a change
// landed.
//
// Two details are load-bearing, and getting either wrong fails in the dangerous
// direction by reporting 0 for a branch that would in fact orphan commits:
//
//  1. --exclude only affects the ref-enumerating options that FOLLOW it, so it
//     must precede --branches.
//  2. Its pattern is matched RELATIVE to each enumerator's namespace, so the
//     short branch name is required. Passing "refs/heads/<branch>" excludes
//     nothing, leaving the branch's own ref in the --not set to cancel itself out.
//
// Branch names cannot contain the glob metacharacters *, ? or [, so no escaping
// is needed. A same-named tag would also be excluded, which can only raise the
// count and therefore only errs toward caution.
func ReachabilityCount(directory, branch, tip string) int {
	if tip == "" {
		return -1
	}
	arguments := []string{"rev-list", "--count", tip, "--not"}
	if branch != "" {
		arguments = append(arguments, "--exclude="+branch)
	}
	arguments = append(arguments, "--branches", "--tags", "--remotes")

	result := gitAt(directory, 60*time.Second, arguments...)
	if !result.OK() {
		return -1
	}
	value, err := strconv.Atoi(result.FirstLine())
	if err != nil {
		return -1
	}
	return value
}

// UnpushedCount returns how many commits on a tip are on no remote-tracking ref.
//
// Works for a detached HEAD, unlike @{upstream}..HEAD, and does not depend on
// the upstream being configured correctly.
func UnpushedCount(directory, tip string) int {
	if tip == "" {
		return -1
	}
	result := gitAt(directory, 60*time.Second, "rev-list", "--count", tip, "--not", "--remotes")
	if !result.OK() {
		return -1
	}
	value, err := strconv.Atoi(result.FirstLine())
	if err != nil {
		return -1
	}
	return value
}

// ContainingRefs returns refs that contain a commit, used to judge a detached HEAD.
func ContainingRefs(directory, commit string) []string {
	if commit == "" {
		return nil
	}
	result := gitAt(directory, 60*time.Second, "for-each-ref", "--contains", commit, "--format=%(refname)")
	if !result.OK() {
		return nil
	}
	return result.Lines()
}

// InProgressOperation returns the name of an interrupted git operation, or "".
func InProgressOperation(directory string) string {
	for _, marker := range inProgressMarkers {
		result := gitAt(directory, 10*time.Second, "rev-parse", "--git-path", marker)
		if !result.OK() {
			continue
		}
		if pathExists(result.FirstLine()) {
			return marker
		}
	}
	return ""
}

// EvaluateMergedInput carries everything a merge verdict needs.
type EvaluateMergedInput struct {
	Branch       string
	Tip          string
	Base         string
	BaseTree     string
	PullRequests func() []PullRequest
	HasProvider  bool
}

// EvaluateMerged decides whether a branch's content already lives in the base.
//
// Three independent positive signals, any of which is sufficient:
//
//	A  ancestry — the tip is reachable from base
//	B  tree equality — merging the tip into base would change nothing, which is
//	   what actually matters and the only offline signal that survives a squash
//	C  a merged change request, but only after its merge commit is proved to be
//	   in this base and the local tip proved to be at or behind what merged
//
// git cherry is deliberately absent: it compares patch ids one to one, so a
// squash that collapses several commits into one never matches, and it reports
// merged work as unmerged.
//
// PullRequests is a function so that C is only reached, and its network cost only
// paid, when both offline signals have already come up empty.
func EvaluateMerged(directory string, input EvaluateMergedInput) MergedVerdict {
	var signals []string

	if IsAncestor(directory, input.Tip, input.Base) {
		signals = append(signals, "ancestry")
	}

	if len(signals) == 0 && input.BaseTree != "" {
		trial := MergeTree(directory, input.Base, input.Tip, false)
		if trial.Status == MergeClean && trial.Tree == input.BaseTree {
			signals = append(signals, "tree-equality")
		}
	}

	verdict := MergedVerdict{}
	if len(signals) == 0 && input.PullRequests != nil {
		_, baseName, found := strings.Cut(input.Base, "/")
		if !found {
			baseName = input.Base
		}
		best := PullRequest{Number: -1}
		for _, entry := range input.PullRequests() {
			if entry.HeadRef != input.Branch || entry.State != "MERGED" {
				continue
			}
			if entry.BaseRef != input.Base && entry.BaseRef != baseName {
				continue
			}
			if entry.Number > best.Number {
				best = entry
			}
		}
		if best.Number > 0 && best.MergeCommitOID != "" && best.HeadOID != "" &&
			IsAncestor(directory, best.MergeCommitOID, input.Base) &&
			IsAncestor(directory, input.Tip, best.HeadOID) {
			signals = append(signals, "change-request")
			verdict.PullRequestNumber = best.Number
			verdict.PullRequestURL = best.URL
		}
	}

	if len(signals) > 0 {
		verdict.Status = Merged
		verdict.Signals = signals
		return verdict
	}
	if input.HasProvider {
		return MergedVerdict{
			Status: NotMerged,
			Detail: "no ancestry, no tree equality, and no merged change request",
		}
	}
	return MergedVerdict{
		Status: Unknown,
		Detail: "no positive signal and no complete change-request history",
	}
}
