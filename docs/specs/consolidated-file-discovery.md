---
command: prd
version: 3
id: prd-consolidated-file-discovery
supersedes: none
mode: standalone
status: complete
created_at: 2026-11-07T08:37:09Z
source_baseline:
  revision: 0370d1ce09860d5f17ead08be3618f9e1930e119
  worktree: dirty
  wwgd_report: none
  relevant_dirty_paths:
    - docs/gsi-strengths-port.md
scope:
  allowed:
    - internal/jinn/tool_find.go
    - internal/jinn/tool_list.go
    - internal/jinn/tool_find_test.go
    - internal/jinn/tool_list_test.go
  conditional: []
  forbidden:
    - docs/gsi-strengths-port.md
    - docs/specs/recheck-before-rename.md
handoff:
  objective_kind: standalone_prd
  workflow_path: null
  audit_run_id: null
terminal_verification: [TG1]
---

# Consolidated file discovery with symlink metadata and depth budget

## Purpose

Make `find_files` the single bounded discovery call whose metadata record is
trustworthy — returning symlinks as link metadata instead of dropping them,
enforcing a depth budget during traversal, and stating which budget cut a
truncated result — so "what is here" needs one call without follow-up
`stat_file` round trips.

## Compilation Basis

### User Intent

The `/specs` coordinator selected source item 6 of `docs/gsi-strengths-port.md`
(ledger `.work/specs/gsi-strengths-port.md`, group G6). Reconciliation at
compile time narrowed the work: `find_files` with `include_meta=true` already
returns path/type/size/mtime in one call, and the walk already streams via
`fs.WalkDir`; only the remainder below is specified here.

### WWGD Findings

None. Standalone compilation; no WWGD report or finding-ID membership.

### ADRs and Accepted Decisions

- `find_files` remains the consolidated-discovery owner; `list_dir` and
  `stat_file` remain for their distinct single-purpose shapes and are not
  deprecated or removed.
- Symlinks are reported from `lstat` metadata only and are never traversed,
  following `fs.WalkDir`'s no-follow semantics made explicit in the result.
- Truncation reasons reuse the existing `Truncated`/`TotalCountExact`/
  `Hint`/`NextCall` envelope with a reason token distinguishing budgets.

### Source Baseline

HEAD `0370d1ce09860d5f17ead08be3618f9e1930e119`. Worktree dirty only with
untracked `docs/gsi-strengths-port.md` (digest
`0e500169b4a757b20b7894d00d0e93cb455632d2642ea409803ccae6868096c2`).
Task-relevant source (tracked, unmodified at compilation):
`internal/jinn/tool_find.go` (`findFiles`, `walkNativeFiles`,
`findFileEntry`, `findVisitLimit`), `internal/jinn/tool_list.go`
(`listDirContext`, `listParams`), `internal/jinn/tool_stat.go` (`statFile`).

## Product Contract

### Problem

`walkNativeFiles` silently drops symlinks (returns nil for
`ModeSymlink` entries), so discovery under-reports relative to reality;
there is no depth budget (only the 100k visit cap and a timeout), so a deep
tree is walked until the visit cap trips; and a truncated result does not say
which budget (visits, results, timeout) cut it.

### Desired State

- **AC1:** Symlinks appear in results with link metadata (path, `symlink`
  type, link-target size), never traversed, and never followed.
- **AC2:** Traversal enforces a caller-visible depth budget; entries beyond
  it are excluded by depth, not by an accidental visit-cap.
- **AC3:** Truncation carries an explicit reason token naming the budget
  that cut the result (result cap, visit cap, depth, timeout) so callers can
  narrow deterministically.
- **AC4:** Existing glob, offset, `NextCall`, and `include_meta` behavior is
  preserved and the focused suite passes.

### In Scope

- `walkNativeFiles` traversal policy and `findFiles` result construction.
- Result `Hint`/`TruncationReason` wording.
- Tests in `tool_find_test.go` (and `tool_list_test.go` only for shared
  envelope fixtures).

### Non-Goals

- No removal or deprecation of `stat_file`/`list_dir`.
- No change to `search_files` content search, `grepExcludeDirs` policy, or
  the 60s timeout value.

### Constraints

- Symlinks are never traversed and never followed; link metadata only.
- Dot-segment pruning and the exclude set behave exactly as today.
- Streaming dir reads (no unbounded materialization) as today via `WalkDir`.

### Acceptance Criteria

- AC1: A tree with symlinks reports them as `symlink` entries with link
  metadata and does not descend through them.
- AC2: With a depth budget set, deeper entries are excluded and the result
  says `depth` rather than a generic truncation.
- AC3: Each truncation kind produces its distinct reason token; a complete
  walk reports no truncation.
- AC4: Existing find/list expectations pass unchanged apart from the
  additive reason token.

## Closed Decisions

These implementation decisions are closed for this contract.

- The reason token is a string field `TruncationReason` on
  `findFilesResult` set to one of `result_cap`, `visit_cap`, `depth`,
  `timeout`, or empty when untruncated; `Hint` keeps human wording.
- The depth budget default preserves current behavior (no silent reduction
  of existing reach); callers opt into shallower budgets via the new arg
  defaulting to an effectively unbounded depth within the visit cap.
- Symlink size is the link target's own `lstat` size (not the referent);
  no target resolution is performed.
- Directory entries remain excluded from file results except where
  `include_meta` already reports them via `IsDir`.

## Technical Contract

### Ownership

- Behavior: `Engine.findFiles`/`walkNativeFiles` in package `jinn`.
- State: none persistent.
- Transactions: none (read-only).
- Retries: identical args give identical results on an unchanged tree (the
  documented offset-paging assumption is unchanged).

### Capability and Package Structure

No package or directory changes. Files consolidate in `internal/jinn`.

### Interfaces and Reuse

- `findFileEntry` gains link metadata for symlinks without changing its
  shape for files/directories.
- `findFilesResult` gains `TruncationReason` with the closed token set.
- `listDirResult` keeps its envelope untouched; only a shared reason-token
  vocabulary link is documented in a comment.
- No new interface.

### State and Lifetime

- The walk streams directory entries; only the result page is retained.
- No caching of directory listings (fresh walk per call, as today).

### Repeat Safety

- Read-only and deterministic per tree state.

### Runtime and Compatibility

- Additive fields only; existing JSON keys and `NextCall` behavior adapt to
  the reason token without breaking prefix consumers.
- `gofmt` clean; stdlib only.

### Generalization

Not applicable: this narrows one discovery traversal; it does not add a
query engine or framework.

### Migration and Deletion

None. `stat_file` and `list_dir` keep full behavior.

## Target Shape

```text
internal/jinn/
  tool_find.go        (symlink metadata, depth budget, reason tokens)
  tool_list.go        (unchanged envelope; vocabulary comment only if needed)
```

## Scope and Authority

Allowed/conditional/forbidden paths are frozen in frontmatter. This contract
grants:

```yaml
local_commit:
  permission: not_granted
  permitted_phases: []
  required_phases: []
  authority_basis: []
```

Maintainer contract (from `lib/engineering/maintainer.md`, carried into every
task):

- **M1 Ownership:** discovery stays with the `Engine` find path; `list_dir`
  and `stat_file` keep their separate owners and are not folded in.
- **M2 Cohesion:** symlink, depth, and reason logic join the existing walk
  callback in place; split only at the traversal-policy boundary if the
  function exceeds the structure threshold.
- **M3 Compatibility:** glob, exclude, offset, `NextCall`, and timeout
  behavior preserved; additive result fields only.
- **M4 Evidence:** proofs come from the source-owned gates below.
- **M5 Preservation:** content search, timeout value, and exclude policy
  untouched.

## Execution Plan

### T1 — Symlinks as link metadata without traversal

```yaml
id: T1
objective: symlink entries are reported with lstat metadata and never traversed
depends_on: []
owners: [internal/jinn/tool_find.go walkNativeFiles findFiles]
may_touch: [internal/jinn/tool_find.go]
required_changes:
  - include symlink candidates in results with symlink type and link metadata instead of dropping them
  - keep WalkDir no-follow semantics so symlinked directories are never descended
  - preserve dot-segment pruning and the exclude set for real entries
must_preserve:
  - glob matching and offset paging over the enlarged result set
must_not:
  - follow or stat through symlink targets
success:
  - id: T1-S1
    acceptance: [AC1]
    criterion: symlink fixture tree reports symlink entries with link metadata and no traversal through them
    gate_ids: [T1-S1]
stop_if:
  - lstat through the rooted FS cannot reach link metadata for symlinks; record the platform limit instead of following links
```

### T2 — Depth budget and explicit truncation reasons

```yaml
id: T2
objective: traversal enforces a depth budget and names the cutting budget on truncation
depends_on: [T1]
owners: [internal/jinn/tool_find.go findFiles findFilesResult]
may_touch: [internal/jinn/tool_find.go]
required_changes:
  - add the depth budget arg with a default that preserves current reach
  - set the reason token per cutting budget on every truncated result and leave it empty on complete walks
  - keep Hint wording while adding the token field
must_preserve:
  - visit cap, timeout, and result-cap behavior
must_not:
  - change the 60s timeout or exclude policy
success:
  - id: T2-S1
    acceptance: [AC2, AC3]
    criterion: depth and each budget cut produce their distinct reason tokens and complete walks report none
    gate_ids: [T2-S1]
stop_if:
  - depth cannot be tracked within the streaming walk callback; halt rather than materializing listings
```

### T3 — Regression suite and envelope compatibility

```yaml
id: T3
objective: existing discovery behavior pinned around the additive changes
depends_on: [T2]
owners: [internal/jinn/tool_find_test.go, internal/jinn/tool_list_test.go]
may_touch: [internal/jinn/tool_find_test.go, internal/jinn/tool_list_test.go]
required_changes:
  - pin glob, offset, NextCall, and timeout behavior as unchanged
  - add symlink and depth fixture coverage beside the existing find suites
must_preserve:
  - current Truncated and Hint expectations except the additive token
must_not:
  - weaken existing expectations to fit the new fields
success:
  - id: T3-S1
    acceptance: [AC4]
    criterion: focused suite passes with the new fixture coverage green
    gate_ids: [T3-S1]
stop_if:
  - an existing expectation only holds because symlinks were dropped; update it deliberately and name the consequence
```

## Verification Contract

### Task Gates

```yaml
id: T1-S1
kind: task
check:
  command: go test ./internal/jinn/ -run 'TestFindSymlink|TestWalkNativeFiles' -count=1
  working_directory: .
pass: symlink fixture tests pass with link metadata and no traversal
covers:
  tasks: [T1]
  success: [T1-S1]
  acceptance: [AC1]
run_when:
  kind: task_candidate
  task: T1
```

```yaml
id: T2-S1
kind: task
check:
  command: go test ./internal/jinn/ -run 'TestFindTruncationReason|TestFindDepth' -count=1
  working_directory: .
pass: depth and truncation-reason tests pass with distinct tokens per budget
covers:
  tasks: [T2]
  success: [T2-S1]
  acceptance: [AC2, AC3]
run_when:
  kind: task_candidate
  task: T2
```

```yaml
id: T3-S1
kind: task
check:
  command: go test ./internal/jinn/ -count=1
  working_directory: .
pass: focused suite passes with new fixture coverage green
covers:
  tasks: [T3]
  success: [T3-S1]
  acceptance: [AC4]
run_when:
  kind: task_candidate
  task: T3
```

### Integration Gates

```yaml
id: IG1
kind: integration
check:
  command: go run ./cmd/jinn list_tools
  working_directory: .
pass: discovery tool surface unchanged with additive result fields
covers:
  tasks: [T1, T2, T3]
  success: [T1-S1, T2-S1, T3-S1]
  acceptance: [AC1, AC2, AC3, AC4]
run_when:
  kind: boundary
  after_completed_tasks: [T3]
  before_starting_tasks: []
```

### Terminal Gates

```yaml
id: TG1
kind: terminal
check:
  command: go test ./internal/jinn/ -count=1 && go build ./...
  working_directory: .
pass: exit 0 with gofmt clean
covers:
  tasks: [T1, T2, T3]
  success: [T1-S1, T2-S1, T3-S1]
  acceptance: [AC1, AC2, AC3, AC4]
run_when:
  kind: contract_candidate
```

## Execution Block Conditions

Explicitly defined terminal block classes: SOURCE_DRIFT,
SPEC_CONTRADICTION, MISSING_DECISION, OUT_OF_SCOPE_DEPENDENCY,
AMBIENT_WORK_CONFLICT, EXTERNAL_AUTHORITY_REQUIRED, GATE_FAILED_UNRESOLVED,
FINALIZATION_UNRESOLVED. Expected exposure: SOURCE_DRIFT if the find walk
changed at HEAD.

## Exit Criteria

- AC1–AC4 proven by TG1; one `find_files` call answers "what is here" with
  trustworthy metadata, a depth budget, and explicit truncation reasons.

## Workflow State

```yaml
implementation_approval: pending
execution_protocol: 3
state_revision: 0
execution_status: not_started
current_task: null
tasks:
  T1: pending
  T2: pending
  T3: pending
completed: []
blocked: []
verification_receipts: []
finalization: null
repair_attempts:
  task_max: 4
  run_max: 12
  consumed_per_task:
    T1: 0
    T2: 0
    T3: 0
  consumed_run: 0
objective: null
```
