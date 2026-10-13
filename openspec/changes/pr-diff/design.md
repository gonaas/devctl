# Design: changes-command

## Architecture decisions

### D1 — Resolve the merge-base explicitly instead of using `base...HEAD`

`git diff base...HEAD` already means merge-base semantics. The command resolves
`git merge-base base HEAD` first and then diffs the resulting commit against HEAD.

Rationale: the two are equivalent in output, but only the explicit form lets the
command *report* the commit it compared. A consumer that renders a view has to be
able to state "this is the range I am showing"; `...` hides it. It also keeps the
semantics visible in the code rather than encoded in three dots, and it makes the
scenario in S2 directly assertable.

Consequence: one extra git call per invocation. Accepted; it is a local read.

### D2 — Target one repository, not the whole registry

Every existing scanning command runs `prepare()`: `requireGit`, the registry load,
and a full `survey.Run` across every registered product. `changes` deliberately
does not. It resolves its target with `gitx.Canonicalize(directory)`
(`internal/gitx/repos.go:174`), which returns the repository's common directory
and top level from any worktree.

Rationale: a per-file diff of *every* product at once is not a report anyone can
read, and the survey cost (a repository sweep, plus `ls-remote` in the base
fallback) is the wrong price for answering "what would this PR contain".

Consequence: `changes` accepts the shared flag vocabulary but is single-target.
`--root` may name one directory; more than one MUST be rejected with a clear
message rather than silently using the first. `--product` does not apply and MUST
be rejected when set, so no flag is silently ignored.

### D3 — NUL-separated parsing

Both `git diff --name-status -z` and `git diff --numstat -z` are parsed with NUL
separators.

Rationale: the default newline-separated form quotes and escapes non-ASCII and
whitespace-bearing paths, so a parser would have to reimplement git's quoting
rules or corrupt names. This is the same reasoning the worktree parser already
applies (`internal/gitx/repos.go:198`).

### D4 — Renames stay one entry

Renames are correlated between the two listings and reported as a single entry
carrying the previous and current path.

Rationale: a rename shown as a deletion plus an addition is the single most
misleading thing a diff view can do to a reviewer. Both listings contain the old
and new path in the rename case, so the correlation is exact.

### D5 — No provider default for base resolution

`changes` calls `ResolveBase(directory, override, "")` — it never consults a forge
adapter for a default branch.

Rationale: it keeps the command on the vendor-agnostic side of the architecture,
avoids loading the registry and the adapter set for a local diff, and still covers
the real cases in order: explicit override, recorded remote HEAD, then the
well-known names through the remote.

Consequence: when the remote's default branch is not one of the well-known names
and no remote HEAD is recorded, resolution fails loudly (S1's third scenario)
rather than guessing. Failing loudly is the established direction: an empty result
must never be indistinguishable from a clean one (`internal/cli/common.go:56`).

### D6 — Read-only by construction

The command has no `--apply` and no mutating path to reach; there is nothing to
gate. Rationale: the safety contract stays provable by inspection, not by a flag
check that could be misread later.

### D7 — One model, two renderings

The command builds one `[]gitx.FileChange` plus its totals, then renders either
the text table or `output.Envelope("changes", …)`. The JSON payload is a superset
of the table, as `branches` already does (`internal/cli/branches.go:52-77`).

## Flow

```
args ──▶ RunChanges
           │
           ├─ reject: >1 --root, --product set        (D2)
           ├─ requireGit()                            ── no git ⇒ hard error
           ├─ target := Canonicalize(root | cwd)      ── (D2)
           ├─ base   := gitx.ResolveBase(target, --base, "")   (D5)
           │     └─ unresolved ⇒ error naming the ref (S1)
           ├─ mb     := gitx.MergeBase(target, base)  ── (D1)
           ├─ files  := gitx.ChangedFiles(target, mb, HEAD)    (D3, D4)
           ├─ patch  := gitx.Patch(target, mb, HEAD)  ── only with --patch (S6)
           │
           └─ render ──▶ text Table (S5)  |  Envelope("changes") (S5)
```

No sequence diagram: the flow is a single linear read with no concurrency,
no external service and no ordering hazard between participants. The three-call
git sequence above is the complete interaction.

## Layer placement

| Concern | Package | Why |
|---|---|---|
| merge-base, changed files, patch | `internal/gitx` | git-only core, the package that already owns `ResolveBase` |
| flag handling, validation, rendering | `internal/cli` | the established command surface |
| dispatch and help | `internal/app` | the established router |
| schema envelope | `internal/output` | the only producer of the versioned contract |

`internal/adapters` is not touched. No vendor appears in the new logic, which
`internal/app/agnosticism_test.go` verifies by scanning every `.go` file and the
rendered help.

## Test strategy

Repo convention: stdlib `testing` only, real git against throwaway repositories in
`t.TempDir()` (`openspec/config.yaml`, `testing.layers`).

- `internal/gitx`: reuse the existing fixture helpers `gitTest`, `writeCommit` and
  `buildRepository` from `internal/gitx/branches_test.go`. A dedicated builder
  creates the shapes this feature needs: a rename, a binary file, a path with a
  space and a non-ASCII character, and a base that has moved ahead of the fork
  point.
- `internal/cli`: direct tests through `RunChanges(arguments, stdout)` against a
  temporary repository, asserting exit status and captured output. This closes the
  declared gap for the command-wiring layer.

Test-first per `strict_tdd: true`: RED with the failing scenario, then the minimum
implementation, then refactor with the suite still green.
