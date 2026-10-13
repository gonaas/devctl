# changes-command — Specification delta

Capability: `changes-command`. Requirement IDs (`S#`) are the same identifiers used
by the ODD execution ledger, so a reader can reconcile both documents mechanically.

## ADDED Requirements

### Requirement: S1 — Base resolution

`devctl changes` SHALL compare the working branch against a base ref resolved by
`gitx.ResolveBase`, so the base obeys the same ordered fallback every other
comparison in the tool already obeys: an explicit override, the recorded remote
HEAD, then the well-known names (`develop`, `main`, `master`, `trunk`) through the
remote.

The command MUST accept `--base REF` as an override through the existing shared
flag binding, and MUST NOT introduce a second base-resolution path.

#### Scenario: Explicit override resolves

- **GIVEN** a repository whose remote-tracking ref `origin/develop` exists
- **WHEN** the user runs `devctl changes --base origin/develop`
- **THEN** the command compares against `origin/develop`
- **AND** reports the resolved base in its output

#### Scenario: Explicit override does not resolve

- **GIVEN** a repository without a ref named `origin/nope`
- **WHEN** the user runs `devctl changes --base origin/nope`
- **THEN** the command MUST fail with a non-zero exit status
- **AND** the message MUST name the unresolved ref
- **AND** it MUST NOT silently fall back to another base

#### Scenario: No base can be resolved at all

- **GIVEN** a repository with no remote, no `origin` HEAD and none of the
  well-known branch names
- **WHEN** the user runs `devctl changes`
- **THEN** the command MUST fail with a non-zero exit status
- **AND** report that no base could be resolved, rather than reporting an empty
  change set

### Requirement: S2 — Committed range, merge-base semantics

The reported range SHALL be `merge-base(base, HEAD)..HEAD`, the same range a
pull-request view compares. It MUST NOT be `base..HEAD`, which would also report
commits that landed on the base after the branch point.

The command MUST resolve the merge-base explicitly and MUST report the resulting
commit id as `merge_base`, so a consumer can state exactly what was compared.

#### Scenario: Base has moved ahead of the branch point

- **GIVEN** a branch that forked from `base` at commit `M`
- **AND** `base` has received commits `X` and `Y` after `M`
- **WHEN** the user runs `devctl changes`
- **THEN** the reported `merge_base` is `M`
- **AND** changes introduced by `X` and `Y` MUST NOT appear in the listing

#### Scenario: HEAD is the base itself

- **GIVEN** HEAD equal to the resolved base
- **WHEN** the user runs `devctl changes`
- **THEN** the command succeeds with an empty file list
- **AND** states that there is nothing to compare

### Requirement: S3 — Per-file change listing

The command SHALL list every changed path in the range with its status and its
added and deleted line counts.

The status vocabulary MUST distinguish at least added, modified, deleted,
renamed, copied and type-changed.

`--numstat` renders binary files with literal `-` for both counts: the command
MUST report those as binary with unknown counts, and MUST NOT report `-` as `0`.

#### Scenario: A modified file

- **GIVEN** a file that gained two lines and lost one in the range
- **WHEN** the user runs `devctl changes`
- **THEN** the entry reports status modified
- **AND** reports added 2 and deleted 1

#### Scenario: A renamed file

- **GIVEN** a file renamed with no content change in the range
- **WHEN** the user runs `devctl changes`
- **THEN** the command reports exactly one entry with status renamed
- **AND** reports both the previous and the current path
- **AND** MUST NOT report the rename as one deletion plus one addition

#### Scenario: A binary file

- **GIVEN** a binary file changed in the range
- **WHEN** the user runs `devctl changes`
- **THEN** the entry is marked as binary
- **AND** its line counts are reported as unavailable, not as zero

#### Scenario: A path containing whitespace and non-ASCII characters

- **GIVEN** a changed file whose name contains a space and a non-ASCII character
- **WHEN** the user runs `devctl changes`
- **THEN** the path is reported byte-for-byte, unquoted and unescaped

### Requirement: S4 — Read-only

`devctl changes` SHALL NOT mutate anything. It MUST NOT require `--apply`, MUST
NOT write recovery manifests, and MUST NOT add objects to the repository.

#### Scenario: The repository is byte-identical afterwards

- **GIVEN** a repository and a captured object-store state
- **WHEN** the user runs `devctl changes` with and without `--patch`
- **THEN** the command exits successfully
- **AND** `git status --porcelain` and the set of objects are unchanged

### Requirement: S5 — Two renderings from one model

The command SHALL render the same data as a plain-text table for a person and as a
versioned JSON document for a machine, and the JSON MUST be wrapped by
`output.Envelope` with kind `changes`.

The JSON payload MUST carry the resolved base, the merge-base commit id, the
repository top level, the per-file entries and the totals.

#### Scenario: JSON is versioned and tool-neutral

- **GIVEN** a repository with changes in the range
- **WHEN** the user runs `devctl changes --json`
- **THEN** the document carries `schema` equal to `devctl/changes/1`
- **AND** carries `base`, `merge_base`, `repository` and a `files` array
- **AND** names no vendor and no product

#### Scenario: Text output is readable without a pager

- **GIVEN** the same repository
- **WHEN** the user runs `devctl changes`
- **THEN** the output is a table with one row per file and the added/deleted counts
- **AND** a closing line states the totals and the compared range

### Requirement: S6 — Patch available on request

With `--patch`, the command SHALL include the unified patch for the range so a
consumer can render hunks without reimplementing git plumbing. Without the flag,
no patch content MUST be emitted.

#### Scenario: Patch requested

- **WHEN** the user runs `devctl changes --patch`
- **THEN** the output includes the unified diff of the range

#### Scenario: Patch not requested

- **WHEN** the user runs `devctl changes` without `--patch`
- **THEN** no hunk content appears in either rendering

### Requirement: S7 — Direct command tests

The command wiring layer SHALL gain direct tests, closing the gap declared in
`openspec/config.yaml`. Tests MUST run against a real git repository created in a
temporary directory, never against a mocked git.

#### Scenario: The new command is covered end to end

- **GIVEN** the test suite
- **WHEN** `go test ./...` runs
- **THEN** `internal/cli` exercises the success path, the unresolved-base path and
  the JSON rendering of the new command
- **AND** no test asserts against internal storage instead of command output

### Requirement: S8 — Vendor agnosticism preserved

New logic MUST NOT name a vendor or product. `internal/adapters` SHALL remain the
only package holding concrete integrations.

#### Scenario: The guard test still passes

- **GIVEN** the new files
- **WHEN** `go test ./internal/app/...` runs
- **THEN** `TestNoVendorTokenInLogic` and `TestHelpNamesNoVendor` pass unchanged
