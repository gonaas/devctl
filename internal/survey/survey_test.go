package survey

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gonaas/devctl/internal/adapters"
	"github.com/gonaas/devctl/internal/process"
	"github.com/gonaas/devctl/internal/registry"
)

func gitIn(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	full := append([]string{
		"-c", "user.email=test@example.invalid",
		"-c", "user.name=devctl test",
		"-c", "commit.gpgsign=false",
	}, arguments...)
	if result := process.Git(full, process.Options{Dir: directory}); !result.OK() {
		t.Fatalf("git %v failed: %s", arguments, result.Stderr)
	}
}

// newRepo makes a throwaway repository with one commit on main, skipping when
// git is unavailable so the suite still runs on a machine without it.
func newRepo(t *testing.T, path string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	gitIn(t, path, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(path, "base.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	gitIn(t, path, "add", "base.txt")
	gitIn(t, path, "commit", "-qm", "base")
	return path
}

func testRegistry(t *testing.T) (registry.Registry, adapters.Set) {
	t.Helper()
	reg, err := registry.Load("", t.TempDir())
	if err != nil {
		t.Fatalf("registry must load: %v", err)
	}
	return reg, adapters.Build(reg)
}

// The pool is the mechanism the whole change rests on: it must run every index
// once, never exceed its bound, and still overlap work.

func TestBoundedEachRunsEveryIndexExactlyOnce(t *testing.T) {
	const total = 50
	var mutex sync.Mutex
	seen := map[int]int{}

	boundedEach(total, 4, func(index int) {
		mutex.Lock()
		seen[index]++
		mutex.Unlock()
	})

	if len(seen) != total {
		t.Fatalf("ran %d distinct indexes, want %d", len(seen), total)
	}
	for index := 0; index < total; index++ {
		if seen[index] != 1 {
			t.Errorf("index %d ran %d times, want exactly 1", index, seen[index])
		}
	}
}

func TestBoundedEachNeverExceedsItsBound(t *testing.T) {
	const (
		workers = 3
		total   = 30
	)
	var inflight, peak int64
	var mutex sync.Mutex

	boundedEach(total, workers, func(int) {
		current := atomic.AddInt64(&inflight, 1)
		mutex.Lock()
		if current > peak {
			peak = current
		}
		mutex.Unlock()
		// Sleeping is what makes overlap observable; the workers overlap in
		// time even on a single-core machine.
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt64(&inflight, -1)
	})

	if peak > workers {
		t.Errorf("peak in-flight work was %d, above the bound of %d", peak, workers)
	}
	if peak < 2 {
		t.Errorf("peak in-flight work was %d: the pool never overlapped anything", peak)
	}
}

func TestBoundedEachEdges(t *testing.T) {
	// The counter must be atomic: the middle case really does run three workers
	// at once, and an unsynchronized increment there is a data race that the
	// race detector reports (this test shipped with exactly that bug once).
	var calls int64

	boundedEach(0, 4, func(int) { atomic.AddInt64(&calls, 1) })
	if got := atomic.LoadInt64(&calls); got != 0 {
		t.Errorf("no work must mean no calls, got %d", got)
	}

	atomic.StoreInt64(&calls, 0)
	boundedEach(3, 99, func(int) { atomic.AddInt64(&calls, 1) })
	if got := atomic.LoadInt64(&calls); got != 3 {
		t.Errorf("more workers than work must still run everything, got %d", got)
	}

	atomic.StoreInt64(&calls, 0)
	boundedEach(2, 0, func(int) { atomic.AddInt64(&calls, 1) })
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Errorf("a non-positive bound must clamp to one worker, got %d calls", got)
	}
}

// divergeConflicting leaves the worktree's branch ahead of main and touching the
// same line main also changed.
//
// `WantConflict: true` already forces one merge-tree per worktree through
// AnalyseBranch. Leaving the branch unmerged into base is what additionally
// triggers the EvaluateMerged tree-equality trial, and the conflicting change is
// what makes that trial write novel objects rather than reuse existing ones.
func divergeConflicting(t *testing.T, primary, worktree, branchSide, mainSide string) {
	t.Helper()
	write := func(directory, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, "base.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write base.txt in %s: %v", directory, err)
		}
	}
	write(worktree, branchSide)
	gitIn(t, worktree, "commit", "-qam", "branch side")
	write(primary, mainSide)
	gitIn(t, primary, "commit", "-qam", "main side")
}

// assertObjectStoreIntact asks git whether the shared object store is still
// coherent after worktrees of one repository ran
// `git merge-tree --write-tree` concurrently.
//
// `--full` is deliberate rather than `--connectivity-only`: the latter never
// reads blob contents, so it passes on exactly the failure this guards against,
// a partially written object. Measured: corrupting a loose blob that only an
// unreachable merge tree references makes `--full` fail while
// `--connectivity-only` still reports success. Packed objects are not covered.
func assertObjectStoreIntact(t *testing.T, repository string) {
	t.Helper()
	result := process.Git([]string{"fsck", "--no-progress", "--full"}, process.Options{Dir: repository})
	if !result.OK() {
		t.Errorf("object store is not coherent after concurrent merges: %s", result.Stderr)
	}
}

// The order of Reports is user-visible in worktrees, clean, rescue, doctor and
// context output, so concurrency must not reorder it.

func TestRunCoversEveryDiscoveredWorktreeInDiscoveryOrder(t *testing.T) {
	root := t.TempDir()
	first := newRepo(t, filepath.Join(root, "alpha"))
	topic := filepath.Join(root, "alpha-wt")
	gitIn(t, first, "worktree", "add", "-q", "-b", "topic", topic)
	divergeConflicting(t, first, topic, "topic side\n", "main side\n")
	newRepo(t, filepath.Join(root, "beta"))

	reg, set := testRegistry(t)
	got := Run(reg, set, Options{Roots: []string{root}, BaseOverride: "main", WantConflict: true})

	var want []string
	for _, repository := range got.Discovery.Repositories {
		for _, item := range repository.Worktrees {
			want = append(want, item.Path)
		}
	}
	var have []string
	for _, report := range got.Reports {
		have = append(have, report.Worktree.Path)
	}

	if len(want) == 0 {
		t.Fatal("discovery found no worktrees; the fixture is wrong")
	}
	if len(have) != len(want) {
		t.Fatalf("reported %d worktrees, discovery found %d", len(have), len(want))
	}
	for index := range want {
		if have[index] != want[index] {
			t.Errorf("position %d: reported %s, discovery order wants %s", index, have[index], want[index])
		}
	}
	if len(got.Contexts) != len(got.Discovery.Repositories) {
		t.Errorf("got %d contexts for %d repositories", len(got.Contexts), len(got.Discovery.Repositories))
	}
}

func TestRunOrderIsStableAcrossPasses(t *testing.T) {
	// A pool that returned completion order would pass sometimes and permute
	// other times. Repeat the pass and require byte-identical ordering.
	root := t.TempDir()
	first := newRepo(t, filepath.Join(root, "alpha"))
	topic := filepath.Join(root, "alpha-wt")
	other := filepath.Join(root, "alpha-wt2")
	gitIn(t, first, "worktree", "add", "-q", "-b", "topic", topic)
	gitIn(t, first, "worktree", "add", "-q", "-b", "other", other)
	// Two worktrees of ONE repository diverge on purpose, so a single pass has
	// several merge-tree writers racing into the same object store. Diverging
	// only one of them would serialize the novel writes and weaken the test.
	divergeConflicting(t, first, topic, "topic side\n", "main side\n")
	divergeConflicting(t, first, other, "other side\n", "main again\n")
	newRepo(t, filepath.Join(root, "beta"))

	reg, set := testRegistry(t)
	options := Options{Roots: []string{root}, BaseOverride: "main", WantConflict: true}

	reference := order(Run(reg, set, options))
	if len(reference) < 3 {
		t.Fatalf("fixture produced only %d worktrees", len(reference))
	}
	for pass := 0; pass < 10; pass++ {
		got := order(Run(reg, set, options))
		if len(got) != len(reference) {
			t.Fatalf("pass %d: %d reports, want %d", pass, len(got), len(reference))
		}
		for index := range reference {
			if got[index] != reference[index] {
				t.Fatalf("pass %d: position %d changed to %s, want %s", pass, index, got[index], reference[index])
			}
		}
	}

	assertObjectStoreIntact(t, first)
}

func order(s Survey) []string {
	paths := make([]string, 0, len(s.Reports))
	for _, report := range s.Reports {
		paths = append(paths, report.Worktree.Path)
	}
	return paths
}
