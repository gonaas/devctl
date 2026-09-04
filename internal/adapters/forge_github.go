package adapters

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gonaas/devctl/internal/gitx"
	"github.com/gonaas/devctl/internal/process"
)

var remotePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^https?://(?P<host>[^/]+)/(?P<owner>[^/]+)/(?P<repo>[^/]+?)(?:\.git)?/?$`),
	regexp.MustCompile(`^ssh://[^@]+@(?P<host>[^/:]+)(?::\d+)?/(?P<owner>[^/]+)/(?P<repo>[^/]+?)(?:\.git)?/?$`),
	regexp.MustCompile(`^[^@]+@(?P<host>[^:]+):(?P<owner>[^/]+)/(?P<repo>[^/]+?)(?:\.git)?/?$`),
}

// pullRequestFields is not negotiable. A merged flag keyed only on a branch name
// says a change request with that name landed once; it says nothing about
// whether the local tip is that same commit. Both object ids are re-proved with
// merge-base before a branch is ever considered deletable.
const pullRequestFields = "number,headRefName,headRefOid,baseRefName,state,mergedAt,mergeCommit,url"

const bulkLimit = "500"

type githubForge struct {
	once   sync.Once
	cached Availability
	mutex  sync.Mutex
	byslug map[string][]gitx.PullRequest
}

// NewGitHubForge returns the one hosting-provider adapter shipped today.
func NewGitHubForge() Forge {
	return &githubForge{byslug: map[string][]gitx.PullRequest{}}
}

func (g *githubForge) Name() string { return "github" }

func parseRemote(remoteURL string) (host, owner, repo string, ok bool) {
	candidate := strings.TrimSpace(remoteURL)
	for _, pattern := range remotePatterns {
		match := pattern.FindStringSubmatch(candidate)
		if match == nil {
			continue
		}
		values := map[string]string{}
		for index, name := range pattern.SubexpNames() {
			if name != "" {
				values[name] = match[index]
			}
		}
		return values["host"], values["owner"], values["repo"], true
	}
	return "", "", "", false
}

func (g *githubForge) Matches(remoteURL string) bool {
	host, _, _, ok := parseRemote(remoteURL)
	return ok && strings.EqualFold(host, "github.com")
}

func (g *githubForge) Slug(remoteURL string) string {
	host, owner, repo, ok := parseRemote(remoteURL)
	if !ok || !strings.EqualFold(host, "github.com") {
		return ""
	}
	return owner + "/" + repo
}

// Available probes the provider CLI once per run and caches the verdict.
func (g *githubForge) Available() Availability {
	g.once.Do(func() {
		if !process.Available("gh") {
			g.cached = Availability{Reason: "gh not on PATH"}
			return
		}
		status := process.Run([]string{"gh", "auth", "status"}, process.Options{Timeout: 15 * time.Second})
		if status.OK() {
			g.cached = Availability{Usable: true}
			return
		}
		g.cached = Availability{Reason: "gh is not authenticated"}
	})
	return g.cached
}

type pullRequestPayload struct {
	Number      int    `json:"number"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
	BaseRefName string `json:"baseRefName"`
	State       string `json:"state"`
	URL         string `json:"url"`
	MergeCommit *struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
}

func (g *githubForge) PullRequests(slug string) []gitx.PullRequest {
	g.mutex.Lock()
	if cached, ok := g.byslug[slug]; ok {
		g.mutex.Unlock()
		return cached
	}
	g.mutex.Unlock()

	var records []gitx.PullRequest
	if g.Available().Usable && slug != "" {
		result := process.Run([]string{
			"gh", "pr", "list",
			"--repo", slug,
			"--state", "all",
			"--limit", bulkLimit,
			"--json", pullRequestFields,
		}, process.Options{Timeout: 45 * time.Second})

		if result.OK() {
			var payload []pullRequestPayload
			if err := json.Unmarshal([]byte(result.Stdout), &payload); err == nil {
				for _, entry := range payload {
					if entry.HeadRefName == "" {
						continue
					}
					mergeCommit := ""
					if entry.MergeCommit != nil {
						mergeCommit = entry.MergeCommit.OID
					}
					records = append(records, gitx.PullRequest{
						Number:         entry.Number,
						HeadRef:        entry.HeadRefName,
						HeadOID:        entry.HeadRefOid,
						BaseRef:        entry.BaseRefName,
						State:          strings.ToUpper(entry.State),
						MergeCommitOID: mergeCommit,
						URL:            entry.URL,
					})
				}
			}
		}
	}

	g.mutex.Lock()
	g.byslug[slug] = records
	g.mutex.Unlock()
	return records
}

func (g *githubForge) DefaultBranch(slug string) string {
	if !g.Available().Usable || slug == "" {
		return ""
	}
	result := process.Run(
		[]string{"gh", "repo", "view", slug, "--json", "defaultBranchRef"},
		process.Options{Timeout: 20 * time.Second},
	)
	if !result.OK() {
		return ""
	}
	var payload struct {
		DefaultBranchRef *struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil || payload.DefaultBranchRef == nil {
		return ""
	}
	return payload.DefaultBranchRef.Name
}
