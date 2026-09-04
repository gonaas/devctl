package registry

import (
	"path/filepath"
	"testing"
)

func TestBuiltinRegistryParses(t *testing.T) {
	got, err := Load("", "/home/example")
	if err != nil {
		t.Fatalf("built-in registry must parse: %v", err)
	}
	if len(got.Agents) == 0 {
		t.Fatal("built-in registry declares no agent runtimes")
	}
	for _, agent := range got.Agents {
		if !filepath.IsAbs(agent.SkillsRoot) {
			t.Errorf("agent %s: skills root is not absolute: %s", agent.Name, agent.SkillsRoot)
		}
		if want := "/home/example"; agent.SkillsRoot[:len(want)] != want {
			t.Errorf("agent %s: ${HOME} was not expanded: %s", agent.Name, agent.SkillsRoot)
		}
	}
}

func TestJSONHealthProbesDeclareAStatusPath(t *testing.T) {
	got, _ := Load("", "/home/example")
	for _, tool := range got.Tools {
		if tool.HealthFormat == HealthJSON && tool.StatusPath == "" {
			t.Errorf("tool %s declares json health with no status path", tool.Name)
		}
	}
}

func TestDiscoveryDepthReachesNestedLayouts(t *testing.T) {
	got, _ := Load("", "/home/example")
	// Depth 1 cannot see a repository inside a container directory, a layout
	// this tool is expected to handle.
	if got.Discovery.MaxDepth < 2 {
		t.Errorf("max depth %d cannot reach nested checkouts", got.Discovery.MaxDepth)
	}
}

func TestInvalidEntriesAreRejected(t *testing.T) {
	cases := map[string]string{
		"unknown source kind":      "[project_source.x]\nkind = \"carrier-pigeon\"\n",
		"sqlite without database":  "[project_source.x]\nkind = \"sqlite\"\n",
		"tool without binary":      "[tool.x]\nhealth_format = \"exit_code\"\n",
		"json health without path": "[tool.x]\nbinary = \"b\"\nhealth_format = \"json\"\n",
		"agent without root":       "[agent.x]\n",
		"rule without prefix":      "[[product.rule]]\nproduct = \"p\"\n",
		"non-positive depth":       "[discovery]\nmax_depth = -1\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parse([]byte(body), "/home/example"); err == nil {
				t.Error("expected a validation error, got none")
			}
		})
	}
}

func TestMissingFileYieldsAnEmptyRegistry(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "absent.toml"), "/home/example")
	if err != nil {
		t.Fatalf("a missing registry must not be an error: %v", err)
	}
	if len(got.Tools) != 0 || len(got.Agents) != 0 || len(got.ProjectSources) != 0 {
		t.Error("a missing registry must declare nothing")
	}
}
