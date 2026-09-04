// Package survey builds the full picture of every repository in one pass.
package survey

import (
	"os"
	"sort"
	"time"

	"github.com/gonaas/devctl/internal/adapters"
	"github.com/gonaas/devctl/internal/discovery"
	"github.com/gonaas/devctl/internal/gitx"
	"github.com/gonaas/devctl/internal/registry"
	"github.com/gonaas/devctl/internal/worktree"
)

// Context holds per-repository facts resolved once and reused by every branch.
//
// Change requests are fetched lazily. Most branches are settled by the two
// offline signals, so a repository whose questions all answer locally never pays
// a network round trip.
type Context struct {
	Repository     *gitx.Repository
	Base           string
	BaseTree       string
	Signals        map[string]gitx.BranchSignal
	RemoteBranches map[string]bool
	ForgeName      string
	ForgeDetail    string

	forge        adapters.Forge
	slug         string
	pullRequests []gitx.PullRequest
	fetched      bool
}

// HasProvider reports whether a hosting provider can answer authoritatively.
func (c *Context) HasProvider() bool {
	return c.forge != nil && c.slug != "" && c.forge.Available().Usable
}

// PullRequests returns this repository's change requests, fetching at most once.
func (c *Context) PullRequests() []gitx.PullRequest {
	if !c.fetched {
		c.fetched = true
		if c.HasProvider() {
			c.pullRequests = c.forge.PullRequests(c.slug)
		}
	}
	return c.pullRequests
}

// Survey is everything one inspection pass produced.
type Survey struct {
	Discovery discovery.Result
	Contexts  map[string]*Context
	Reports   []*worktree.Report
}

// BaseByRepository returns each repository's resolved base ref.
func (s Survey) BaseByRepository() map[string]string {
	bases := map[string]string{}
	for key, context := range s.Contexts {
		bases[key] = context.Base
	}
	return bases
}

// Options tunes how much work one pass does.
type Options struct {
	Roots        []string
	BaseOverride string
	WantConflict bool
	WantSize     bool
	Product      string
}

func buildContext(repository *gitx.Repository, set adapters.Set, baseOverride string) *Context {
	context := &Context{Repository: repository}
	main := repository.MainWorktree

	if forge := set.ForgeFor(repository.RemoteURL); forge != nil {
		context.forge = forge
		context.ForgeName = forge.Name()
		context.slug = forge.Slug(repository.RemoteURL)
		if context.slug == "" {
			context.ForgeDetail = "remote URL yields no repository slug"
		} else if availability := forge.Available(); !availability.Usable {
			context.ForgeDetail = availability.Reason
		}
	} else {
		context.ForgeDetail = "no forge claims this remote"
	}

	// The recorded origin HEAD answers this offline for almost every clone, so a
	// hosting provider is only asked when local git genuinely cannot say.
	base := gitx.ResolveBase(main, baseOverride, "")
	if base == "" && context.HasProvider() {
		base = gitx.ResolveBase(main, baseOverride, context.forge.DefaultBranch(context.slug))
	}
	if base == "" {
		if context.ForgeDetail == "" {
			context.ForgeDetail = "base branch could not be resolved"
		}
		return context
	}

	context.Base = base
	context.BaseTree = gitx.TreeOf(main, base)
	context.Signals = gitx.BranchSignals(main, base)
	context.RemoteBranches = gitx.RemoteBranchNames(main, "origin")
	return context
}

// AnalyseBranch produces the full verdict for one branch inside a context.
func AnalyseBranch(context *Context, branch, tip string, wantConflict bool) *worktree.Analysis {
	main := context.Repository.MainWorktree
	signal, ok := context.Signals[branch]
	if !ok {
		signal = gitx.BranchSignal{Name: branch, Tip: tip}
	}

	analysis := &worktree.Analysis{
		Signal:       signal,
		Reachability: gitx.ReachabilityCount(main, branch, tip),
		Unpushed:     gitx.UnpushedCount(main, tip),
		HasRemote:    context.RemoteBranches[branch],
	}

	if context.Base == "" {
		analysis.Merged = gitx.MergedVerdict{Status: gitx.Unknown, Detail: "no base branch resolved"}
		return analysis
	}

	if branch == "" {
		analysis.Merged = gitx.MergedVerdict{
			Status: gitx.Unknown,
			Detail: "detached HEAD has no branch to compare",
		}
	} else {
		analysis.Merged = gitx.EvaluateMerged(main, gitx.EvaluateMergedInput{
			Branch:       branch,
			Tip:          tip,
			Base:         context.Base,
			BaseTree:     context.BaseTree,
			PullRequests: context.PullRequests,
			HasProvider:  context.HasProvider(),
		})
	}

	if wantConflict && tip != "" {
		conflicts := gitx.MergeTree(main, context.Base, tip, true)
		analysis.Conflicts = &conflicts
	}
	return analysis
}

// Run inspects every discovered repository and classifies all of its worktrees.
func Run(reg registry.Registry, set adapters.Set, options Options) Survey {
	result := discovery.Discover(reg, set, options.Roots)
	survey := Survey{Discovery: result, Contexts: map[string]*Context{}}

	current := ""
	if cwd, err := os.Getwd(); err == nil {
		current = gitx.RealPath(cwd)
	}

	for _, repository := range result.Repositories {
		if options.Product != "" && !repository.Products[options.Product] {
			continue
		}
		context := buildContext(repository, set, options.BaseOverride)
		survey.Contexts[repository.CommonDir] = context

		product := repository.Name()
		if products := repository.SortedProducts(); len(products) > 0 {
			product = products[0]
		}

		for _, item := range repository.Worktrees {
			state := worktree.ReadState(item.Path)
			branch := item.ShortBranch()
			tip := item.Head
			if tip == "" {
				tip = state.HeadOID
			}

			report := &worktree.Report{
				Repository: repository,
				Worktree:   item,
				State:      state,
				Analysis:   AnalyseBranch(context, branch, tip, options.WantConflict),
				Product:    product,
				SizeBytes:  -1,
			}
			if options.WantSize {
				report.SizeBytes = worktree.SizeBytes(item.Path)
			}
			worktree.Classify(report, worktree.ClassifyInput{
				TemporaryPrefixes: reg.Discovery.TemporaryPrefixes,
				RemoteBranches:    context.RemoteBranches,
				CurrentDirectory:  current,
			})
			survey.Reports = append(survey.Reports, report)
		}
	}
	return survey
}

// SortedContexts returns contexts in a stable order for reporting.
func (s Survey) SortedContexts() []*Context {
	contexts := make([]*Context, 0, len(s.Contexts))
	for _, context := range s.Contexts {
		contexts = append(contexts, context)
	}
	sort.Slice(contexts, func(i, j int) bool {
		return contexts[i].Repository.MainWorktree < contexts[j].Repository.MainWorktree
	})
	return contexts
}

var _ = time.Second
