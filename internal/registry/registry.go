// Package registry loads the declarative file that names every external system.
//
// This is the only package in the tool that is allowed to know that external
// systems have names. Everything else reads these structures.
package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// EnvironmentVariable points the tool at a registry other than the built-in one.
const EnvironmentVariable = "DEVCTL_REGISTRY"

// Source kinds and health formats the loader accepts.
const (
	SourceSQLite  = "sqlite"
	SourceCommand = "command"
	HealthJSON    = "json"
	HealthExit    = "exit_code"
)

// ProjectSource is one declared way of learning which projects exist.
type ProjectSource struct {
	Name            string
	Kind            string   `toml:"kind"`
	Database        string   `toml:"database"`
	Query           string   `toml:"query"`
	Binary          string   `toml:"binary"`
	Arguments       []string `toml:"arguments"`
	OutputFormat    string   `toml:"output_format"`
	RequiredColumns []string `toml:"required_columns"`
	Timestamps      string   `toml:"timestamps"`
}

// Tool is one external tool with a declared health probe.
type Tool struct {
	Name         string
	Binary       string   `toml:"binary"`
	Health       []string `toml:"health"`
	HealthFormat string   `toml:"health_format"`
	StatusPath   string   `toml:"status_path"`
	OkValues     []string `toml:"ok_values"`
}

// Agent is one agent runtime with a skills root to compare against.
type Agent struct {
	Name        string
	SkillsRoot  string   `toml:"skills_root"`
	ConfigFiles []string `toml:"config_files"`
}

// ProductRule maps a path prefix to a product, evaluated in declared order.
type ProductRule struct {
	Prefix  string `toml:"prefix"`
	Product string `toml:"product"`
}

// IsCarveOut reports whether the rule only prevents absorption into a parent
// tree rather than assigning a product name.
func (r ProductRule) IsCarveOut() bool { return r.Product == "" }

// Discovery bounds the filesystem walk and classifies paths.
type Discovery struct {
	Roots             []string `toml:"roots"`
	MaxDepth          int      `toml:"max_depth"`
	SkipDirectories   []string `toml:"skip_directories"`
	TemporaryPrefixes []string `toml:"temporary_prefixes"`
	StalePrefixes     []string `toml:"stale_prefixes"`
}

// Skills says where the repository copy of the skills lives. A shipped binary
// has no repository beside it, so this cannot be inferred and must be declared.
type Skills struct {
	Repository string `toml:"repository"`
}

// Registry is every externally declared system, loaded from data.
type Registry struct {
	ProjectSources []ProjectSource
	Tools          []Tool
	Agents         []Agent
	ProductRules   []ProductRule
	Discovery      Discovery
	Skills         Skills
	SourcePath     string
}

type document struct {
	ProjectSource map[string]ProjectSource `toml:"project_source"`
	Tool          map[string]Tool          `toml:"tool"`
	Agent         map[string]Agent         `toml:"agent"`
	Product       struct {
		Rule []ProductRule `toml:"rule"`
	} `toml:"product"`
	Discovery Discovery `toml:"discovery"`
	Skills    Skills    `toml:"skills"`
}

// ExpandHome replaces the single supported placeholder with a home directory.
func ExpandHome(value, home string) string {
	return strings.ReplaceAll(value, "${HOME}", home)
}

func expandAll(values []string, home string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	for index, value := range values {
		out[index] = ExpandHome(value, home)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// Load reads a registry, expanding ${HOME} and validating every entry.
//
// The path comes from the argument, then DEVCTL_REGISTRY, then the built-in
// default. A missing file yields an empty registry on purpose: the core must
// keep working with nothing declared at all.
func Load(path, home string) (Registry, error) {
	if path == "" {
		path = os.Getenv(EnvironmentVariable)
	}
	if path == "" {
		return builtin(home)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Registry{}, nil
		}
		return Registry{}, err
	}
	registry, err := parse(raw, home)
	if err != nil {
		return Registry{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	registry.SourcePath = path
	return registry, nil
}

func parse(raw []byte, home string) (Registry, error) {
	var doc document
	if err := toml.Unmarshal(raw, &doc); err != nil {
		return Registry{}, err
	}

	registry := Registry{Discovery: Discovery{MaxDepth: 3}}

	for _, name := range sortedKeys(doc.ProjectSource) {
		source := doc.ProjectSource[name]
		source.Name = name
		switch source.Kind {
		case SourceSQLite:
			if source.Database == "" {
				return Registry{}, fmt.Errorf("project_source.%s: sqlite sources require a database path", name)
			}
		case SourceCommand:
			if source.Binary == "" {
				return Registry{}, fmt.Errorf("project_source.%s: command sources require a binary", name)
			}
		default:
			return Registry{}, fmt.Errorf("project_source.%s: kind must be %q or %q", name, SourceSQLite, SourceCommand)
		}
		source.Database = ExpandHome(source.Database, home)
		source.Query = strings.TrimSpace(source.Query)
		if source.Timestamps == "" {
			source.Timestamps = "utc"
		}
		registry.ProjectSources = append(registry.ProjectSources, source)
	}

	for _, name := range sortedKeys(doc.Tool) {
		tool := doc.Tool[name]
		tool.Name = name
		if tool.Binary == "" {
			return Registry{}, fmt.Errorf("tool.%s: binary is required", name)
		}
		switch tool.HealthFormat {
		case HealthJSON:
			if tool.StatusPath == "" {
				return Registry{}, fmt.Errorf("tool.%s: json health probes require status_path", name)
			}
		case HealthExit:
		default:
			return Registry{}, fmt.Errorf("tool.%s: health_format must be %q or %q", name, HealthJSON, HealthExit)
		}
		registry.Tools = append(registry.Tools, tool)
	}

	for _, name := range sortedKeys(doc.Agent) {
		agent := doc.Agent[name]
		agent.Name = name
		if agent.SkillsRoot == "" {
			return Registry{}, fmt.Errorf("agent.%s: skills_root is required", name)
		}
		agent.SkillsRoot = ExpandHome(agent.SkillsRoot, home)
		agent.ConfigFiles = expandAll(agent.ConfigFiles, home)
		registry.Agents = append(registry.Agents, agent)
	}

	for index, rule := range doc.Product.Rule {
		if rule.Prefix == "" {
			return Registry{}, fmt.Errorf("product.rule[%d]: prefix is required", index)
		}
		rule.Prefix = ExpandHome(rule.Prefix, home)
		registry.ProductRules = append(registry.ProductRules, rule)
	}

	discovery := doc.Discovery
	if discovery.MaxDepth == 0 {
		discovery.MaxDepth = 3
	}
	if discovery.MaxDepth < 1 {
		return Registry{}, fmt.Errorf("discovery.max_depth must be a positive integer")
	}
	discovery.Roots = expandAll(discovery.Roots, home)
	discovery.TemporaryPrefixes = expandAll(discovery.TemporaryPrefixes, home)
	discovery.StalePrefixes = expandAll(discovery.StalePrefixes, home)
	registry.Discovery = discovery
	registry.Skills = Skills{Repository: ExpandHome(doc.Skills.Repository, home)}

	return registry, nil
}
