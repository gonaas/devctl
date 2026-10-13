package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gonaas/devctl/internal/process"
)

// newChangeRepository builds a repository shaped like the one this command was
// written for: a feature branch that forked from a base which is still a local
// branch, and no remote at all, so base detection has to be explicit.
func newChangeRepository(t *testing.T) (repository, forkPoint string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repository = t.TempDir()

	runGit := func(arguments ...string) string {
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
	commit := func(name, content, message string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repository, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		runGit("add", name)
		runGit("commit", "-q", "-m", message)
	}

	runGit("init", "-q", "-b", "main")
	commit("base.txt", "one\n", "base")
	forkPoint = runGit("rev-parse", "HEAD")
	runGit("checkout", "-q", "-b", "feature")
	commit("feature.txt", "new\n", "feature: add")
	commit("base.txt", "one\ntwo\n", "feature: extend")

	return repository, forkPoint
}

func runChanges(t *testing.T, arguments ...string) (string, error) {
	t.Helper()
	var stdout bytes.Buffer
	err := RunChanges(arguments, &stdout)
	return stdout.String(), err
}

func decodeChanges(t *testing.T, rendered string) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(rendered), &document); err != nil {
		t.Fatalf("output is not a JSON document: %v\n%s", err, rendered)
	}
	return document
}

// canonicalPath is what the command reports: it resolves symlinks, which on macOS
// turns the temporary directory into its /private form.
func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	return resolved
}

func TestRunChangesRendersTextAndTotals(t *testing.T) {
	repository, forkPoint := newChangeRepository(t)

	rendered, err := runChanges(t, "--base", "main", "--root", repository)
	if err != nil {
		t.Fatalf("RunChanges: %v", err)
	}
	for _, want := range []string{"feature.txt", "added", "base.txt", "modified", "2 files"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("text output does not mention %q:\n%s", want, rendered)
		}
	}
	if !strings.Contains(rendered, "main") {
		t.Errorf("text output does not name the compared range:\n%s", rendered)
	}
	if !strings.Contains(rendered, forkPoint[:8]) {
		t.Errorf("text output does not report the merge base %s:\n%s", forkPoint[:8], rendered)
	}
}

func TestRunChangesEmitsTheVersionedEnvelope(t *testing.T) {
	repository, forkPoint := newChangeRepository(t)

	rendered, err := runChanges(t, "--base", "main", "--root", repository, "--json")
	if err != nil {
		t.Fatalf("RunChanges: %v", err)
	}
	document := decodeChanges(t, rendered)

	if document["schema"] != "devctl/changes/1" {
		t.Errorf("schema = %v, want devctl/changes/1", document["schema"])
	}
	if document["base"] != "main" {
		t.Errorf("base = %v, want main", document["base"])
	}
	if document["merge_base"] != forkPoint {
		t.Errorf("merge_base = %v, want %s", document["merge_base"], forkPoint)
	}
	if document["repository"] != canonicalPath(t, repository) {
		t.Errorf("repository = %v, want %s", document["repository"], canonicalPath(t, repository))
	}

	entries, ok := document["files"].([]any)
	if !ok || len(entries) != 2 {
		t.Fatalf("files = %#v, want two entries", document["files"])
	}
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("file entry is not an object: %#v", raw)
		}
		for _, field := range []string{"path", "status", "binary", "counts_unavailable", "added", "deleted"} {
			if _, present := entry[field]; !present {
				t.Errorf("file entry %v has no %q field", entry["path"], field)
			}
		}
	}
}

func TestRunChangesRejectsAnUnresolvableBase(t *testing.T) {
	repository, _ := newChangeRepository(t)

	_, err := runChanges(t, "--base", "no-such-ref", "--root", repository)
	if err == nil {
		t.Fatal("an unresolvable base must fail rather than report an empty range")
	}
	if !strings.Contains(err.Error(), "no-such-ref") {
		t.Errorf("error does not name the ref that failed to resolve: %v", err)
	}
}

func TestRunChangesFailsWhenNoBaseCanBeResolved(t *testing.T) {
	repository, _ := newChangeRepository(t)

	// No remote and no well-known base branch: silence here would be
	// indistinguishable from a branch with nothing in it.
	_, err := runChanges(t, "--root", repository)
	if err == nil {
		t.Fatal("an unresolvable base must fail, not report an empty change set")
	}
}

func TestRunChangesRejectsSeveralRootsAndProduct(t *testing.T) {
	repository, _ := newChangeRepository(t)

	if _, err := runChanges(t, "--base", "main", "--root", repository, "--root", repository); err == nil {
		t.Error("more than one --root must be rejected: a range belongs to one repository")
	}
	if _, err := runChanges(t, "--base", "main", "--root", repository, "--product", "anything"); err == nil {
		t.Error("--product must be rejected rather than silently ignored")
	}
}

func TestRunChangesCarriesThePatchOnlyWhenAsked(t *testing.T) {
	repository, _ := newChangeRepository(t)

	plain, err := runChanges(t, "--base", "main", "--root", repository)
	if err != nil {
		t.Fatalf("RunChanges: %v", err)
	}
	if strings.Contains(plain, "+new") {
		t.Errorf("hunk content appeared without --patch:\n%s", plain)
	}

	withPatch, err := runChanges(t, "--base", "main", "--root", repository, "--patch")
	if err != nil {
		t.Fatalf("RunChanges --patch: %v", err)
	}
	if !strings.Contains(withPatch, "+new") {
		t.Errorf("--patch did not carry the range diff:\n%s", withPatch)
	}
}

func TestRunChangesReportsNothingToCompareWhenHeadIsTheBase(t *testing.T) {
	repository, _ := newChangeRepository(t)
	if result := process.Git([]string{"checkout", "-q", "main"}, process.Options{Dir: repository}); !result.OK() {
		t.Fatalf("checkout main failed: %s", result.Stderr)
	}

	rendered, err := runChanges(t, "--base", "main", "--root", repository)
	if err != nil {
		t.Fatalf("an empty range is a success, not a failure: %v", err)
	}
	if !strings.Contains(rendered, "no committed changes") {
		t.Errorf("empty range was not reported as such:\n%s", rendered)
	}
}
