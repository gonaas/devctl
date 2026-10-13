# Tasks: changes-command

Hierarchical, phase-grouped, each task completable in one session. `S#` maps to
the requirement IDs in `specs/changes-command/spec.md` and to the same `S#` in the
ODD execution ledger at `odd/tasks/pr-diff.md`.

## 1. Data layer (`internal/gitx`)

- [ ] 1.1 `MergeBase(directory, reference)` resolving `git merge-base reference HEAD` (S2)
- [ ] 1.2 `ChangedFiles(directory, from, to)` parsing `--name-status -z` and `--numstat -z`, with rename correlation, binary handling and odd-path handling (S3)
- [ ] 1.3 `Patch(directory, from, to)` returning the unified diff of the range (S6)
- [ ] 1.4 Fixture builder for the shapes this feature needs: rename, binary file, path with space and non-ASCII, base moved ahead of the fork point (S2, S3)

## 2. Command surface (`internal/cli`)

- [ ] 2.1 `RunChanges`: reject more than one `--root` and any `--product`, require git, canonicalize the target, resolve the base, resolve the merge-base, build the model (S1, S2, S4)
- [ ] 2.2 Read-only text rendering with per-file rows and a closing totals line (S5)
- [ ] 2.3 `--json` rendering through `output.Envelope("changes", …)` (S5)
- [ ] 2.4 `--patch` wiring, emitting hunk content only when requested (S6)

## 3. Wiring and docs

- [ ] 3.1 `internal/app/app.go` dispatch case for `changes`
- [ ] 3.2 `internal/app/help.go` COMMANDS line, vendor-free
- [ ] 3.3 `README.md` "What it answers" entry, and the Layers table if a row is affected

## 4. Verification

- [ ] 4.1 Direct tests for the command-wiring layer: success path, unresolved base, JSON rendering, rejected flags (S7)
- [ ] 4.2 `gofmt -l .`, `go vet ./...`, `go test ./...` all clean
- [ ] 4.3 Manual end-to-end against a real repository with a `develop`-style base, confirming text and JSON output by inspection

## 5. Deferred, with rationale

- [ ] 5.1 Working-tree changes alongside the committed range — deferred: the PR
  range is committed by definition, and their publish flow commits before opening
  the request. Revisit only if someone asks to review before committing.

## Report

Surface when each task closes: `gofmt`/`vet`/`test` results, the observed output of
4.3, and anything the spec promised that was not delivered.
