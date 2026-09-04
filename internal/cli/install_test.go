package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// thisBinary is what RunInstall will link: the test binary itself.
func thisBinary(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return resolved
}

func TestOccupiedDestinationIsRefusedAndNamesTheWayOut(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "devctl")
	if err := os.WriteFile(destination, []byte("someone else\n"), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out := &bytes.Buffer{}
	err := RunInstall([]string{"--apply", "--target", destination}, out)
	if err == nil {
		t.Fatal("an occupied destination must not be replaced without being asked")
	}
	if !strings.Contains(err.Error(), "--replace") {
		t.Errorf("the refusal must name the way forward: %s", err)
	}

	content, readErr := os.ReadFile(destination)
	if readErr != nil || string(content) != "someone else\n" {
		t.Error("the refusal must leave the destination untouched")
	}
}

func TestReplaceDryRunChangesNothing(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "devctl")
	if err := os.WriteFile(destination, []byte("someone else\n"), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out := &bytes.Buffer{}
	if err := RunInstall([]string{"--replace", "--target", destination}, out); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(out.String(), "would replace") {
		t.Errorf("a dry run over an occupant must say it would replace: %s", out.String())
	}
	if !strings.Contains(out.String(), "regular file") {
		t.Errorf("a dry run must name what it would destroy: %s", out.String())
	}

	content, readErr := os.ReadFile(destination)
	if readErr != nil || string(content) != "someone else\n" {
		t.Error("a dry run must not touch the destination")
	}
}

func TestReplaceSwapsTheDestinationForALink(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "devctl")
	if err := os.WriteFile(destination, []byte("stale build\n"), 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out := &bytes.Buffer{}
	if err := RunInstall([]string{"--replace", "--apply", "--target", destination}, out); err != nil {
		t.Fatalf("replace: %v", err)
	}

	linked, err := os.Readlink(destination)
	if err != nil {
		t.Fatalf("destination is not a symlink: %v", err)
	}
	if linked != thisBinary(t) {
		t.Errorf("want a link to %s, got %s", thisBinary(t), linked)
	}
	if entries, _ := filepath.Glob(destination + ".devctl-incoming"); len(entries) != 0 {
		t.Error("the staging link must not survive a successful replace")
	}
}

func TestDirectoryDestinationIsNeverReplaced(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "devctl")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := RunInstall([]string{"--replace", "--apply", "--target", destination}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("--replace must not reach a directory")
	}
	if !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("the refusal must say why, not leak an OS error: %s", err)
	}
	if info, statErr := os.Stat(destination); statErr != nil || !info.IsDir() {
		t.Error("the directory must survive")
	}
}

func TestAlreadyLinkedIsNotWorkToRedo(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "devctl")
	if err := os.Symlink(thisBinary(t), destination); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out := &bytes.Buffer{}
	if err := RunInstall([]string{"--apply", "--target", destination}, out); err != nil {
		t.Fatalf("already linked must not be an error: %v", err)
	}
	if !strings.Contains(out.String(), "Already linked") {
		t.Errorf("want the no-op reported, got %s", out.String())
	}
}

// Building straight onto PATH makes the destination the running binary. A
// replace there would point a symlink at itself and destroy the binary doing it.
func TestDestinationThatIsThisBinaryIsNeverReplaced(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "devctl")
	original, err := os.ReadFile(thisBinary(t))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(destination, original, 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out := &bytes.Buffer{}
	if err := RunInstall([]string{"--replace", "--apply", "--target", thisBinary(t)}, out); err != nil {
		t.Fatalf("installing over itself must be a no-op, got %v", err)
	}
	if !strings.Contains(out.String(), "Already installed") {
		t.Errorf("want the no-op reported, got %s", out.String())
	}
	if _, readErr := os.Readlink(thisBinary(t)); readErr == nil {
		t.Fatal("the running binary must not have become a symlink")
	}
}
