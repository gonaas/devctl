# devctl

A tool-agnostic CLI for git worktree hygiene and agent-stack inspection.

```bash
brew tap gonaas/tap
brew install devctl
```

Or build it: `go build ./cmd/devctl`. Requires Go 1.25+ to build, and `git` to run.
Everything else is optional.

## What it answers

```bash
devctl projects list       # products, repositories, last activity
devctl worktrees status    # every worktree with divergence, conflicts and risk
devctl worktrees doctor    # only what is wrong
devctl worktrees clean     # what is provably safe to delete (dry run)
devctl worktrees rescue    # secure worktrees on purge-prone paths
devctl branches list       # merge status and orphan risk per branch
devctl config doctor       # health of every declared tool
devctl config drift        # repository skills vs installed copies
devctl context             # a compact machine summary for an agent
```

Every command defaults to a dry run. Only `--apply` mutates, and it always writes
a recovery manifest to `~/.devctl/manifests/` first.

## Layers

| Layer | Knows about | Where |
|---|---|---|
| Core | git only | `internal/{gitx,discovery,worktree,cleanup,rescue}` |
| Adapters | one external system each | `internal/adapters` |
| Registry | nothing; it is data | `internal/registry/tools.toml` |
| Presentation | a versioned schema | `internal/output` |

**No vendor name appears in the CLI's logic.** `internal/app/agnosticism_test.go`
enforces that: it scans every Go file and fails the suite if a product name leaks
in outside the adapters and the registry. Replacing the whole agent stack is an
edit to the registry, never a code change.

Override the shipped registry with `~/.config/devctl/tools.toml`, or point
`DEVCTL_REGISTRY` at a file of your own. `devctl config registry --defaults`
prints what is compiled in.

The core runs with no adapter available and no network. Whatever cannot be proved
becomes `UNKNOWN`, and `UNKNOWN` is never deletable — degradation shrinks the set
of things this tool will touch, never grows it.

## Merge detection

Three-valued, and no single source is authoritative. A branch is `MERGED` when any
of these proves it, and every one is verified locally:

| Signal | Check | Needs |
|---|---|---|
| `ancestry` | `git merge-base --is-ancestor <tip> <base>` | nothing |
| `tree-equality` | `git merge-tree --write-tree` returns the base tree | nothing |
| `change-request` | a merged request whose merge commit is in this base **and** whose head commit contains the local tip | a hosting provider |

`git cherry` is deliberately unused: it matches patch ids one to one, so a squash
that collapses several commits into one never matches, and it reports merged work
as unmerged.

`ancestry` and `tree-equality` are positive-only. A negative result never proves a
branch is unmerged, because a squash merge rewrites commit identity.

The provider is asked only after both offline signals come up empty, and its
answer alone is never enough: a request merged under a branch name says nothing
about whether the local tip is that same commit.

## Safety model

The hard gate is reachability, not merge status:

```
git rev-list --count <tip> --not --exclude=<branch> --branches --tags --remotes
```

Zero means deleting the branch loses nothing. Two details are load-bearing, and
getting either wrong reports zero for a branch that would orphan commits:

- `--exclude` only affects the ref-enumerating options that **follow** it.
- Its pattern is matched **relative to each namespace**, so the short branch name
  is required; `refs/heads/<branch>` excludes nothing.

`internal/gitx/branches_test.go` proves both failure modes against a real
repository built for the purpose.

Removal additionally refuses on: the main checkout, the current working directory,
any uncommitted or untracked content, a locked worktree, an interrupted operation,
a branch checked out elsewhere, the base branch, and a detached HEAD on no other
ref. `git worktree remove` and `git branch -d` are used unforced, so git's own
refusals act as two further independent checks at the moment of mutation.

Order is forced by git: the worktree goes first, because a branch checked out in a
linked worktree cannot be deleted at all.

## Notes

- `git merge-tree --write-tree` adds unreachable objects to the object store, so
  this tool is not literally read-only against `.git/objects`. Nothing else is
  written without `--apply`.
- Declared SQLite sources open `mode=ro` with `query_only`: no statement can write
  and no checkpoint can run, so the database stays byte-identical. SQLite does
  create the empty `-shm`/`-wal` side files it needs to read a WAL database while
  another process writes. `immutable=1` would suppress them but asserts the file
  cannot change, risking torn reads from a live writer, so it is not used.
- `config drift` compares content, never timestamps. A copy-based installer resets
  mtimes without changing bytes, so timestamps report drift that does not exist.

## Licence

MIT.
