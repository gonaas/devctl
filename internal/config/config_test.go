package config

import (
	"fmt"
	"testing"

	"github.com/gonaas/devctl/internal/registry"
)

// absentTools builds a registry whose tools are guaranteed not to be on PATH,
// so probes stay hermetic: ProbeTool reports "absent" without running anything.
func absentTools(names ...string) registry.Registry {
	tools := make([]registry.Tool, 0, len(names))
	for _, name := range names {
		tools = append(tools, registry.Tool{
			Name:   name,
			Binary: "devctl-test-absent-binary-" + name,
		})
	}
	return registry.Registry{Tools: tools}
}

func TestToolHealthAllKeepsDeclarationOrder(t *testing.T) {
	// Concurrency must not reorder or drop a tool: callers print these rows in
	// declaration order and a shuffled result would silently mislabel health.
	names := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	got := ToolHealthAll(absentTools(names...))

	if len(got) != len(names) {
		t.Fatalf("got %d results, want %d", len(got), len(names))
	}
	for index, name := range names {
		if got[index].Name != name {
			t.Errorf("position %d: got tool %q, want %q", index, got[index].Name, name)
		}
		if got[index].Binary != "devctl-test-absent-binary-"+name {
			t.Errorf("position %d: got binary %q for %s", index, got[index].Binary, name)
		}
		if got[index].Installed || got[index].Status != "absent" {
			t.Errorf("tool %s: want absent and not installed, got installed=%v status=%q",
				name, got[index].Installed, got[index].Status)
		}
	}
}

func TestToolHealthAllReportsEveryToolExactlyOnce(t *testing.T) {
	names := []string{"one", "two", "three", "four", "five", "six", "seven"}
	got := ToolHealthAll(absentTools(names...))

	seen := map[string]int{}
	for _, health := range got {
		seen[health.Name]++
	}
	if len(seen) != len(names) {
		t.Fatalf("got %d distinct tools, want %d", len(seen), len(names))
	}
	for _, name := range names {
		if seen[name] != 1 {
			t.Errorf("tool %s reported %d times, want exactly 1", name, seen[name])
		}
	}
}

// realProbes builds tools that actually spawn a process, so ProbeTool reaches
// process.Run instead of short-circuiting at the PATH lookup.
func realProbes(outcomes ...int) registry.Registry {
	tools := make([]registry.Tool, 0, len(outcomes))
	for index, code := range outcomes {
		tools = append(tools, registry.Tool{
			Name:         fmt.Sprintf("probe-%d", index),
			Binary:       "sh",
			Health:       []string{"-c", fmt.Sprintf("exit %d", code)},
			HealthFormat: registry.HealthExit,
		})
	}
	return registry.Registry{Tools: tools}
}

func TestToolHealthAllRunsRealProbesConcurrently(t *testing.T) {
	// The absent-binary tests stop at process.Available, so the concurrent
	// subprocess path (process.Run, environment building, output capture) would
	// otherwise never run under -race. Spawn real processes here.
	got := ToolHealthAll(realProbes(0, 0, 0, 0, 0, 0, 0, 0))

	if len(got) != 8 {
		t.Fatalf("got %d results, want 8", len(got))
	}
	for index, health := range got {
		if health.Status != "ok" {
			t.Errorf("probe-%d: want ok, got status=%q detail=%q", index, health.Status, health.Detail)
		}
	}
}

func TestToolHealthAllKeepsPerToolResultsInTheirOwnSlot(t *testing.T) {
	// Identical probes cannot reveal a concurrency bug that crossed result
	// slots. Give every tool a distinct outcome and require outcome i in slot i.
	outcomes := []int{0, 3, 0, 7, 2, 0, 1, 9}
	got := ToolHealthAll(realProbes(outcomes...))

	if len(got) != len(outcomes) {
		t.Fatalf("got %d results, want %d", len(got), len(outcomes))
	}
	for index, code := range outcomes {
		want := "degraded"
		if code == 0 {
			want = "ok"
		}
		if got[index].Status != want {
			t.Errorf("slot %d (exit %d): got status=%q detail=%q, want %q",
				index, code, got[index].Status, got[index].Detail, want)
		}
		if got[index].Name != fmt.Sprintf("probe-%d", index) {
			t.Errorf("slot %d: got name %q, want probe-%d", index, got[index].Name, index)
		}
	}
}

func TestToolHealthAllWithNoTools(t *testing.T) {
	got := ToolHealthAll(registry.Registry{})
	if len(got) != 0 {
		t.Fatalf("want no results, got %d", len(got))
	}
	if got == nil {
		t.Error("want a non-nil slice so callers can range over it")
	}
}

func TestToolHealthAllIsDeterministicUnderConcurrency(t *testing.T) {
	// A racy implementation can pass once and permute on the next call. Repeat
	// the probe many times and require byte-identical ordering every time.
	tools := absentTools("a", "b", "c", "d", "e", "f", "g", "h")
	first := ToolHealthAll(tools)

	for attempt := 0; attempt < 50; attempt++ {
		got := ToolHealthAll(tools)
		if len(got) != len(first) {
			t.Fatalf("attempt %d: got %d results, want %d", attempt, len(got), len(first))
		}
		for index := range first {
			if got[index].Name != first[index].Name || got[index].Binary != first[index].Binary {
				t.Fatalf("attempt %d: position %d changed: %q/%q, first was %q/%q",
					attempt, index, got[index].Name, got[index].Binary,
					first[index].Name, first[index].Binary)
			}
		}
	}
}
