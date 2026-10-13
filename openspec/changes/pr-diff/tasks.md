# Tasks: changes-command

Hierarchical, phase-grouped, each task completable in one session. `S#` maps to
the requirement IDs in `specs/changes-command/spec.md` and to the same `S#` in the
ODD execution ledger at `odd/tasks/pr-diff.md`.

## 1. Data layer (`internal/gitx`) — commit dabadfc

- [x] 1.1 `MergeBase(directory, reference)` resolving `git merge-base reference HEAD` (S2)
- [x] 1.2 `ChangedFiles(directory, from, to)` parsing `--name-status -z` and `--numstat -z`, with rename correlation, binary handling and odd-path handling (S3)
- [x] 1.3 `Patch(directory, from, to)` returning the unified diff of the range (S6)
- [x] 1.4 Fixture builder for the shapes this feature needs: rename, binary file, path with space and non-ASCII, base moved ahead of the fork point (S2, S3)

## 2. Command surface (`internal/cli`) — commit 1bdde4f

- [x] 2.1 `RunChanges`: reject more than one `--root` and any `--product`, require git, canonicalize the target, resolve the base, resolve the merge-base, build the model (S1, S2, S4)
- [x] 2.2 Read-only text rendering with per-file rows and a closing totals line (S5)
- [x] 2.3 `--json` rendering through `output.Envelope("changes", …)` (S5)
- [x] 2.4 `--patch` wiring, emitting hunk content only when requested (S6)

## 3. Wiring and docs — commit 1bdde4f

- [x] 3.1 `internal/app/app.go` dispatch case for `changes`
- [x] 3.2 `internal/app/help.go` COMMANDS line, vendor-free
- [x] 3.3 `README.md` "What it answers" entry; the Layers table needed no change because the new code lands in `internal/gitx`, already listed as Core

## 4. Verification — commit 1bdde4f

- [x] 4.1 Direct tests for the command-wiring layer: success path, unresolved base, JSON rendering, rejected flags (S7)
- [x] 4.2 `gofmt -l .`, `go vet ./...`, `go test ./...` all clean
- [x] 4.3 Manual end-to-end against a real repository with a `develop`-style base, confirming text and JSON output by inspection

## 5. Deferred, with rationale

- [ ] 5.1 Working-tree changes alongside the committed range — deferred: the PR
  range is committed by definition, and their publish flow commits before opening
  the request. Revisit only if someone asks to review before committing.

## Report

Observed results at closure:

| Check | Command | Result |
|---|---|---|
| Formatting | `gofmt -l .` | no output |
| Static analysis | `go vet ./...` | clean |
| Full suite | `go test ./...` | ok across 14 packages, 2 with no test files |
| New tests | `go test ./internal/cli/ -run TestRunChanges -v` | 7 of 7 pass |
| Data layer | `go test ./internal/gitx/ -run 'TestMergeBase\|TestChangedFiles\|TestPatch' -v` | 4 of 4 pass |
| End to end | `devctl changes --base main` on this repository | 7 files, +887/-0, merge base `9e60d1a9` |
| Auto-detection | `devctl changes` with no `--base` | resolved `origin/main` |

Not delivered, by decision: 5.1. The end-to-end run made the boundary observable —
files still uncommitted at that moment did not appear in the range, which is the
intended semantics and also the one case a pre-commit reviewer would miss.

Found and not fixed, outside this change's scope: `internal/cli/branches.go:14`
advertises a `--repo PATH` flag that no command binds.
