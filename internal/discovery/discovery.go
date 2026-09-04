// Package discovery merges every project source into canonical repositories.
package discovery

import (
	"sort"
	"strings"

	"github.com/gonaas/devctl/internal/adapters"
	"github.com/gonaas/devctl/internal/gitx"
	"github.com/gonaas/devctl/internal/registry"
)

// Finding kinds for directories a source reported that are not usable repositories.
const (
	StaleEntry     = "stale-source-entry"
	NotARepository = "not-a-repository"
)

// Finding is a directory a source reported that is not a usable repository.
type Finding struct {
	Kind      string
	Directory string
	Project   string
	Source    string
	Detail    string
}

// Result is everything one discovery pass produced.
type Result struct {
	Repositories []*gitx.Repository
	Findings     []Finding
	SourceStatus map[string]string
}

// ByProduct groups repositories under the product each belongs to.
func (r Result) ByProduct() map[string][]*gitx.Repository {
	grouped := map[string][]*gitx.Repository{}
	for _, repository := range r.Repositories {
		products := repository.SortedProducts()
		if len(products) == 0 {
			products = []string{repository.Name()}
		}
		for _, product := range products {
			grouped[product] = append(grouped[product], repository)
		}
	}
	return grouped
}

// SortedProducts returns product names in a stable order.
func (r Result) SortedProducts() []string {
	grouped := r.ByProduct()
	names := make([]string, 0, len(grouped))
	for name := range grouped {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ProductFor returns the product a path belongs to under first-match-wins rules.
//
// Order is load-bearing and mirrors shell `case` semantics: a carve-out must
// precede the tree it sits inside. A longest-prefix heuristic is not equivalent,
// because a carve-out says "this is its own product" rather than naming one.
//
// Returns ok=false when a rule claims the path as its own product, and an empty
// string with ok=true when no rule matches at all.
func ProductFor(path string, rules []registry.ProductRule) (product string, ok bool) {
	for _, rule := range rules {
		if path == rule.Prefix || isUnder(path, rule.Prefix) {
			if rule.IsCarveOut() {
				return "", false
			}
			return rule.Product, true
		}
	}
	return "", true
}

func isUnder(path, prefix string) bool {
	if prefix == "" || path == prefix {
		return false
	}
	trimmed := strings.TrimSuffix(prefix, "/")
	return strings.HasPrefix(path, trimmed+"/")
}

func matchesAnyPrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if path == prefix || isUnder(path, prefix) {
			return true
		}
	}
	return false
}

// Discover finds every repository the filesystem and the declared sources know.
//
// The filesystem walk always runs. Declared sources add directories the walk
// cannot reach, plus the project names and activity timestamps that let
// repositories be grouped into products.
func Discover(reg registry.Registry, set adapters.Set, roots []string) Result {
	settings := reg.Discovery
	result := Result{SourceStatus: map[string]string{}}

	searchRoots := roots
	if len(searchRoots) == 0 {
		searchRoots = settings.Roots
	}

	byCommonDir := map[string]*gitx.Repository{}

	register := func(directory, source string) *gitx.Repository {
		commonDir, topLevel, ok := gitx.Canonicalize(directory)
		if !ok {
			return nil
		}
		repository, seen := byCommonDir[commonDir]
		if !seen {
			repository = &gitx.Repository{
				CommonDir:    commonDir,
				MainWorktree: topLevel,
				Worktrees:    gitx.ListWorktrees(topLevel),
				RemoteURL:    gitx.RemoteURL(topLevel, "origin"),
				Sources:      map[string]bool{},
				Products:     map[string]bool{},
			}
			for _, worktree := range repository.Worktrees {
				if worktree.IsMain {
					repository.MainWorktree = worktree.Path
					break
				}
			}
			byCommonDir[commonDir] = repository
		}
		repository.Sources[source] = true
		return repository
	}

	for _, candidate := range gitx.WalkCandidates(searchRoots, settings.MaxDepth, settings.SkipDirectories) {
		register(candidate, "filesystem")
	}

	for _, source := range set.ProjectSources {
		availability := source.Available()
		if availability.Usable {
			result.SourceStatus[source.Name()] = "available"
		} else {
			result.SourceStatus[source.Name()] = availability.Reason
			continue
		}

		for _, record := range source.Projects() {
			directory := gitx.RealPath(record.Directory)

			if matchesAnyPrefix(directory, settings.StalePrefixes) {
				result.Findings = append(result.Findings, Finding{
					Kind: StaleEntry, Directory: directory, Project: record.Project,
					Source: record.Source, Detail: "known-dead path prefix",
				})
				continue
			}
			if !directoryExists(directory) {
				result.Findings = append(result.Findings, Finding{
					Kind: StaleEntry, Directory: directory, Project: record.Project,
					Source: record.Source, Detail: "no longer on disk",
				})
				continue
			}

			repository := register(directory, record.Source)
			if repository == nil {
				// A product folder holding several repositories is a legitimate
				// session directory but is not a repository itself.
				result.Findings = append(result.Findings, Finding{
					Kind: NotARepository, Directory: directory, Project: record.Project,
					Source: record.Source, Detail: "not a git repository",
				})
				continue
			}
			repository.Products[record.Project] = true
			if record.LastActive > repository.LastActive {
				repository.LastActive = record.LastActive
			}
		}
	}

	for _, repository := range byCommonDir {
		mapped, matched := ProductFor(repository.MainWorktree, reg.ProductRules)
		switch {
		case !matched:
			repository.Products = map[string]bool{repository.Name(): true}
		case mapped != "":
			repository.Products = map[string]bool{mapped: true}
		case len(repository.Products) == 0:
			repository.Products = map[string]bool{repository.Name(): true}
		}
		result.Repositories = append(result.Repositories, repository)
	}

	sort.Slice(result.Repositories, func(i, j int) bool {
		return result.Repositories[i].MainWorktree < result.Repositories[j].MainWorktree
	})
	return result
}
