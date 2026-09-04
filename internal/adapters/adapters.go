// Package adapters holds the optional, read-only bridges to external systems.
//
// The core never imports a concrete adapter. It asks for whatever the registry
// declared and treats an empty result as normal rather than as an error.
package adapters

import (
	"github.com/gonaas/devctl/internal/gitx"
	"github.com/gonaas/devctl/internal/registry"
)

// Availability says whether an adapter can be used, and why not when it cannot.
type Availability struct {
	Usable bool
	Reason string
}

// ProjectRecord is one project-to-directory association reported by a source.
type ProjectRecord struct {
	Project    string
	Directory  string
	Weight     int
	LastActive string
	Source     string
}

// RuntimeResource is a service-runtime artefact that may outlive the checkout
// that created it.
type RuntimeResource struct {
	Name      string
	Kind      string
	SizeBytes int64
	BoundPath string
	Detail    string
}

// ProjectSource reports which projects exist and where they live.
type ProjectSource interface {
	Name() string
	Available() Availability
	Projects() []ProjectRecord
}

// Forge answers change-request questions for one hosting provider.
type Forge interface {
	Name() string
	Matches(remoteURL string) bool
	Available() Availability
	Slug(remoteURL string) string
	PullRequests(slug string) []gitx.PullRequest
	DefaultBranch(slug string) string
}

// Runtime lists service-runtime resources and the paths they came from.
type Runtime interface {
	Name() string
	Available() Availability
	Resources() []RuntimeResource
}

// Set is everything the registry made available for one run.
type Set struct {
	ProjectSources []ProjectSource
	Forges         []Forge
	Runtimes       []Runtime
}

// ForgeFor returns the first forge that claims a remote, or nil.
func (s Set) ForgeFor(remoteURL string) Forge {
	if remoteURL == "" {
		return nil
	}
	for _, forge := range s.Forges {
		if forge.Matches(remoteURL) {
			return forge
		}
	}
	return nil
}

// BuildProjectSource constructs the generic implementation a registry entry names.
func BuildProjectSource(definition registry.ProjectSource) ProjectSource {
	switch definition.Kind {
	case registry.SourceSQLite:
		return newSQLiteSource(definition)
	case registry.SourceCommand:
		return newCommandSource(definition)
	default:
		return nil
	}
}

// Build instantiates every adapter the registry declares, plus the built-in
// forge and runtime. Construction never probes anything; availability resolves
// lazily and is cached, so a run needing no forge pays for no forge.
func Build(reg registry.Registry) Set {
	set := Set{Forges: []Forge{NewGitHubForge()}, Runtimes: []Runtime{NewComposeRuntime()}}
	for _, definition := range reg.ProjectSources {
		if source := BuildProjectSource(definition); source != nil {
			set.ProjectSources = append(set.ProjectSources, source)
		}
	}
	return set
}
