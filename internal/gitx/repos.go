// Package gitx wraps the git plumbing this tool depends on.
//
// Everything here is pure git. No package in gitx knows that hosting providers,
// memory stores or agent runtimes exist.
package gitx

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gonaas/devctl/internal/process"
)

// Worktree is one record from git's worktree registry.
type Worktree struct {
	Path           string
	Head           string
	Branch         string
	HasBranch      bool
	Bare           bool
	Detached       bool
	Locked         bool
	LockedReason   string
	Prunable       bool
	PrunableReason string
	IsMain         bool
}

// ShortBranch returns the branch name without its refs/heads/ prefix.
func (w Worktree) ShortBranch() string {
	if !w.HasBranch {
		return ""
	}
	return strings.TrimPrefix(w.Branch, "refs/heads/")
}

// Exists reports whether the working directory is still on disk.
//
// git's own prunable flag is gated on an expiry time and therefore under-reports;
// the directory itself is the only ground truth.
func (w Worktree) Exists() bool {
	info, err := os.Stat(w.Path)
	return err == nil && info.IsDir()
}

// Repository is a canonical repository, identified by its git common directory.
type Repository struct {
	CommonDir    string
	MainWorktree string
	Worktrees    []Worktree
	RemoteURL    string
	Sources      map[string]bool
	Products     map[string]bool
	LastActive   string
}

// Name returns a human label for the repository.
func (r Repository) Name() string { return filepath.Base(r.MainWorktree) }

// LinkedWorktrees returns every worktree except the main checkout.
func (r Repository) LinkedWorktrees() []Worktree {
	var out []Worktree
	for _, worktree := range r.Worktrees {
		if !worktree.IsMain {
			out = append(out, worktree)
		}
	}
	return out
}

// SortedProducts returns the products this repository belongs to, in order.
func (r Repository) SortedProducts() []string {
	out := make([]string, 0, len(r.Products))
	for product := range r.Products {
		out = append(out, product)
	}
	sort.Strings(out)
	return out
}

// RealPath resolves symlinks so path comparisons are sound.
//
// On macOS /tmp is a symlink to /private/tmp. Comparing unresolved paths makes
// the "never touch the current worktree" guard miss without any sign of failure.
func RealPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if absolute, absErr := filepath.Abs(path); absErr == nil {
			return absolute
		}
		return path
	}
	return resolved
}

// ParseWorktreePorcelain parses `git worktree list --porcelain`, with or without
// -z. Records are separated by a blank line, and the first record is always the
// main worktree.
func ParseWorktreePorcelain(payload string) []Worktree {
	separator := "\n"
	if strings.Contains(payload, "\x00") {
		separator = "\x00"
	}

	type record map[string]string
	var records []record
	current := record{}
	flush := func() {
		if len(current) > 0 {
			records = append(records, current)
			current = record{}
		}
	}

	for _, raw := range strings.Split(payload, separator) {
		line := strings.Trim(raw, "\r\n")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		keyword, remainder, _ := strings.Cut(line, " ")
		remainder = strings.TrimSpace(remainder)
		switch keyword {
		case "worktree":
			flush()
			current["worktree"] = remainder
		case "HEAD", "branch":
			current[keyword] = remainder
		case "bare", "detached":
			current[keyword] = "true"
		case "locked", "prunable":
			current[keyword] = "true"
			current[keyword+"_reason"] = remainder
		}
	}
	flush()

	var worktrees []Worktree
	for index, item := range records {
		location := item["worktree"]
		if location == "" {
			continue
		}
		branch, hasBranch := item["branch"]
		worktrees = append(worktrees, Worktree{
			Path:           RealPath(location),
			Head:           item["HEAD"],
			Branch:         branch,
			HasBranch:      hasBranch,
			Bare:           item["bare"] == "true",
			Detached:       item["detached"] == "true",
			Locked:         item["locked"] == "true",
			LockedReason:   item["locked_reason"],
			Prunable:       item["prunable"] == "true",
			PrunableReason: item["prunable_reason"],
			IsMain:         index == 0,
		})
	}
	return worktrees
}

// Canonicalize returns the common directory and top level of the repository
// containing a directory, or ok=false when it is not inside one.
//
// --path-format=absolute is mandatory: without it --git-common-dir returns the
// literal string ".git" from a primary checkout, which would make every primary
// look like a different repository.
func Canonicalize(directory string) (commonDir, topLevel string, ok bool) {
	result := process.Git(
		[]string{"rev-parse", "--path-format=absolute", "--git-common-dir", "--show-toplevel"},
		process.Options{Dir: directory, Timeout: 15 * time.Second},
	)
	if !result.OK() {
		return "", "", false
	}
	lines := result.Lines()
	if len(lines) < 2 {
		return "", "", false
	}
	return RealPath(lines[0]), RealPath(lines[1]), true
}

// ListWorktrees returns every worktree of the repository containing a directory.
// It works from any worktree, not only the main checkout.
func ListWorktrees(directory string) []Worktree {
	options := process.Options{Dir: directory, Timeout: 30 * time.Second}
	result := process.Git([]string{"worktree", "list", "--porcelain", "-z"}, options)
	if !result.OK() {
		result = process.Git([]string{"worktree", "list", "--porcelain"}, options)
		if !result.OK() {
			return nil
		}
	}
	return ParseWorktreePorcelain(result.Stdout)
}

// RemoteURL returns a remote's URL, or an empty string when it is unconfigured.
func RemoteURL(directory, remote string) string {
	if remote == "" {
		remote = "origin"
	}
	result := process.Git([]string{"remote", "get-url", remote}, process.Options{Dir: directory, Timeout: 15 * time.Second})
	if !result.OK() {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

// WalkCandidates returns directories under the roots that look like a checkout.
//
// A .git directory marks a primary checkout. A .git file marks a linked
// worktree, which is skipped because enumerating from its primary finds it
// anyway. Depth must reach at least 2 or nested container layouts are missed.
func WalkCandidates(roots []string, maxDepth int, skip []string) []string {
	skipSet := make(map[string]bool, len(skip))
	for _, name := range skip {
		skipSet[name] = true
	}

	seen := map[string]bool{}
	var candidates []string

	var descend func(directory string, depth int)
	descend = func(directory string, depth int) {
		if depth > maxDepth {
			return
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			return
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			// Hidden directories under a development root are tool caches, not
			// work: one pre-commit cache alone can hold five vendored clones.
			if strings.HasPrefix(name, ".") || skipSet[name] {
				continue
			}
			child := filepath.Join(directory, name)
			if info, statErr := os.Lstat(child); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			marker := filepath.Join(child, ".git")
			info, markerErr := os.Stat(marker)
			switch {
			case markerErr == nil && info.IsDir():
				resolved := RealPath(child)
				if !seen[resolved] {
					seen[resolved] = true
					candidates = append(candidates, resolved)
				}
			case markerErr == nil:
				// A linked worktree; its primary will enumerate it.
			default:
				descend(child, depth+1)
			}
		}
	}

	for _, root := range roots {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			descend(RealPath(root), 1)
		}
	}
	return candidates
}
