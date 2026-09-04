package adapters

import (
	"path/filepath"
	"testing"

	"github.com/gonaas/devctl/internal/registry"
)

func TestRemoteParsingClaimsOnlyItsOwnHost(t *testing.T) {
	forge := NewGitHubForge()
	cases := []struct {
		remote  string
		matches bool
		slug    string
	}{
		{"https://github.com/owner/repo.git", true, "owner/repo"},
		{"git@github.com:owner/repo.git", true, "owner/repo"},
		{"ssh://git@github.com/owner/repo", true, "owner/repo"},
		{"https://github.com/owner/repo", true, "owner/repo"},
		{"https://gitlab.com/owner/repo.git", false, ""},
		{"git@bitbucket.org:owner/repo.git", false, ""},
		{"", false, ""},
		{"not a url", false, ""},
	}
	for _, item := range cases {
		if got := forge.Matches(item.remote); got != item.matches {
			t.Errorf("%q: matches=%v, want %v", item.remote, got, item.matches)
		}
		if got := forge.Slug(item.remote); got != item.slug {
			t.Errorf("%q: slug=%q, want %q", item.remote, got, item.slug)
		}
	}
}

func TestForgeForReturnsNothingWhenNoneClaimsTheRemote(t *testing.T) {
	set := Set{Forges: []Forge{NewGitHubForge()}}
	if set.ForgeFor("https://gitlab.com/a/b.git") != nil {
		t.Error("no forge should claim a host it does not serve")
	}
	if (Set{}).ForgeFor("https://github.com/a/b.git") != nil {
		t.Error("an empty set must claim nothing")
	}
}

func TestSQLiteSourceDegradesWhenTheDatabaseIsAbsent(t *testing.T) {
	source := BuildProjectSource(registry.ProjectSource{
		Name:     "store",
		Kind:     registry.SourceSQLite,
		Database: filepath.Join(t.TempDir(), "absent.db"),
		Query:    "SELECT 1",
	})
	availability := source.Available()
	if availability.Usable {
		t.Error("a missing database must not be usable")
	}
	if availability.Reason == "" {
		t.Error("an unusable source must say why")
	}
	if got := source.Projects(); got != nil {
		t.Errorf("an unusable source must report nothing, got %d records", len(got))
	}
}

func TestUnknownSourceKindBuildsNothing(t *testing.T) {
	if BuildProjectSource(registry.ProjectSource{Kind: "carrier-pigeon"}) != nil {
		t.Error("an unknown kind must not produce a source")
	}
}
