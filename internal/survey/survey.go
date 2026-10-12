// Package survey builds the full picture of every repository in one pass.
package survey

import (
	"os"
	"sort"
	"sync"
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
	pullOnce     sync.Once
}

// HasProvider reports whether a hosting provider can answer authoritatively.
func (c *Context) HasProvider() bool {
	return c.forge != nil && c.slug != "" && c.forge.Available().Usable
}

// PullRequests returns this repository's change requests, fetching at most once.
//
// Worktrees of one repository are inspected concurrently and each may ask this
// same question, so the lazy fetch is guarded rather than merely idempotent.
func (c *Context) PullRequests() []gitx.PullRequest {
	c.pullOnce.Do(func() {
		if c.HasProvider() {
			c.pullRequests = c.forge.PullRequests(c.slug)
		}
	})
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

// maxSurveyWorkers bounds how many worktrees are inspected at once. Every unit
// of work may spawn a git subprocess, so the bound is what keeps a tree with
// dozens of worktrees from saturating the machine.
const maxSurveyWorkers = 8

// Run inspects every discovered repository and classifies all of its worktrees.
//
// Work is fanned out over a bounded pool, but every result is written back by
// its discovery index, so the reported order is the discovery order rather than
// the order the work happened to finish in.
func Run(reg registry.Registry, set adapters.Set, options Options) Survey {
	result := discovery.Discover(reg, set, options.Roots)
	survey := Survey{Discovery: result, Contexts: map[string]*Context{}}

	current := ""
	if cwd, err := os.Getwd(); err == nil {
		current = gitx.RealPath(cwd)
	}

	selected := make([]*gitx.Repository, 0, len(result.Repositories))
	for _, repository := range result.Repositories {
		if options.Product != "" && !repository.Products[options.Product] {
			continue
		}
		selected = append(selected, repository)
	}

	// One context per repository, built concurrently. Each context is fully
	// owned by one worker until it is published, so no lock is needed here; the
	// map itself is written by this goroutine alone once every worker finished.
	contexts := make([]*Context, len(selected))
	boundedEach(len(contexts), maxSurveyWorkers, func(index int) {
		contexts[index] = buildContext(selected[index], set, options.BaseOverride)
	})
	for index, repository := range selected {
		survey.Contexts[repository.CommonDir] = contexts[index]
	}

	// One task per worktree. Worktrees of a repository may run concurrently and
	// all share that repository's context, which is why Context.PullRequests is
	// guarded by sync.Once.
	type task struct{ repository, worktree int }
	tasks := make([]task, 0, len(selected))
	perRepository := make([][]*worktree.Report, len(selected))
	for repositoryIndex, repository := range selected {
		perRepository[repositoryIndex] = make([]*worktree.Report, len(repository.Worktrees))
		for worktreeIndex := range repository.Worktrees {
			tasks = append(tasks, task{repository: repositoryIndex, worktree: worktreeIndex})
		}
	}

	boundedEach(len(tasks), maxSurveyWorkers, func(index int) {
		assignment := tasks[index]
		repository := selected[assignment.repository]
		context := contexts[assignment.repository]
		item := repository.Worktrees[assignment.worktree]

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
			Product:    productOf(repository),
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

		perRepository[assignment.repository][assignment.worktree] = report
	})

	for repositoryIndex := range perRepository {
		survey.Reports = append(survey.Reports, perRepository[repositoryIndex]...)
	}
	return survey
}

// productOf names the product a repository belongs to, preferring a declared
// product over the repository name.
func productOf(repository *gitx.Repository) string {
	if products := repository.SortedProducts(); len(products) > 0 {
		return products[0]
	}
	return repository.Name()
}

// boundedEach runs work(0..total-1) on at most workers goroutines at a time and
// returns once every call has finished.
func boundedEach(total, workers int, work func(int)) {
	if total <= 0 {
		return
	}
	if workers < 1 {
		workers = 1
	}
	if workers > total {
		workers = total
	}

	indices := make(chan int)
	var group sync.WaitGroup
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer group.Done()
			for index := range indices {
				work(index)
			}
		}()
	}
	for index := 0; index < total; index++ {
		indices <- index
	}
	close(indices)
	group.Wait()
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
