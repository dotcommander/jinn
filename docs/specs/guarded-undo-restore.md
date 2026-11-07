---
command: prd
version: 3
id: prd-guarded-undo-restore
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
    - internal/jinn/tool_undo.go
    - internal/jinn/history.go
    - internal/jinn/security.go
    - internal/jinn/tool_undo_test.go
    - internal/jinn/tool_undo_helpers_test.go
    - internal/jinn/history_test.go
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

# Guarded undo restore and journal exclusion

## Purpose

Close the remaining gaps between jinn's undo journal and the GSI
non-deletable-journal rule — a version-based restore guard, explicit journal
exclusion from mutation targets, and tests pinning the already-present
non-deletable plus mandatory-guard behavior — so the recovery journal stays
durable evidence and every restore is an explicit guarded decision.

## Compilation Basis

### User Intent

The `/specs` coordinator selected source item 4 of `docs/gsi-strengths-port.md`
(ledger `.work/specs/gsi-strengths-port.md`, group G4). Reconciliation at
compile time narrowed the work: most of the source item is already true at
HEAD and only the remainder below is specified here.

### WWGD Findings

None. Standalone compilation; no WWGD report or finding-ID membership.

### ADRs and Accepted Decisions

- Already true and only pinned (not rebuilt): `undo` rejects `clear` with
  "history is non-deletable recovery evidence"; `preview`/`restore` require
  `historyStateCommitted`; restore requires `if_checksum`/`if_absent` by
  default because `requireMutationPreconditions` defaults to true
  (`engine.go:91`, `!config.UnsafeAllowMutationWithoutPreconditions`).
- The `if_version` guard maps to the snapshot's recorded pre-state identity
  (`historyEntry.Before` hash), so no new schema field is required.
- The unsafe opt-out (`UnsafeAllowMutationWithoutPreconditions`) is preserved
  as the only unguarded path, now named explicitly in the restore error text.

### Source Baseline

HEAD `0370d1ce09860d5f17ead08be3618f9e1930e119`. Worktree dirty only with
untracked `docs/gsi-strengths-port.md` (digest
`0e500169b4a757b20b7894d00d0e93cb455632d2642ea409803ccae6868096c2`).
Task-relevant source (tracked, unmodified at compilation):
`internal/jinn/tool_undo.go` (`undoTool`, `undoRestore`, `undoRestoreLocked`,
`verifyUndoChecksum`), `internal/jinn/history.go` (`historyDir`,
`historyEntry`, state constants), `internal/jinn/engine.go:91`
(`requireMutationPreconditions` default), `internal/jinn/security.go`
(mutation path checks).

## Product Contract

### Problem

Restore guards exist only as checksum/absent; callers that recorded a snapshot
digest cannot assert the pre-state identity directly. The journal lives under
`JINN_CONFIG_DIR/jinn/history/<wdHash>`, which is inside the workspace when
the workspace root is an ancestor of the config dir (e.g. a home-root
workspace), so a mutation could in principle target journal storage and
destroy recovery evidence.

### Desired State

- **AC1:** `undo restore` accepts `if_version` (the snapshot's recorded
  pre-state hash) as a guard alternative to `if_checksum`; `if_version` and
  `if_checksum` together are mutually exclusive.
- **AC2:** Mutation entry points reject any target inside `historyDir()`
  (index and blobs), regardless of workspace-root nesting.
- **AC3:** Non-deletable history, committed-state restore, and mandatory
  default guards are pinned by tests, including the unsafe opt-out path
  being the only unguarded route.
- **AC4:** Existing restore, preview, and list behavior plus the focused
  suite pass unchanged.

### In Scope

- Restore guard handling in `tool_undo.go`.
- Journal-exclusion check at mutation path validation (`security.go` seam).
- Tests for guards, exclusion, and the already-present invariants.

### Non-Goals

- No change to snapshot capture, blob codec, ring-buffer compaction, or
  retention caps.
- No migration of existing journal files.

### Constraints

- Restore without a satisfied guard must keep failing in default mode; the
  unsafe opt-out stays the single exception and names itself in errors.
- Journal exclusion must not break reads of legitimate workspace files.

### Acceptance Criteria

- AC1: Restore with matching `if_version` succeeds; mismatched version is
  stale-rejected; combined version+checksum args are invalid-args rejected.
- AC2: A mutation targeting a path inside `historyDir()` is rejected with a
  typed error and writes nothing, including when the config dir nests inside
  the workspace root.
- AC3: `clear` stays rejected, non-committed snapshots stay unrestorable,
  and unguarded restore stays rejected in default mode.
- AC4: `go test ./internal/jinn/` passes.

## Closed Decisions

These implementation decisions are closed for this contract.

- `if_version` compares against `historyEntry.Before.Hash` for replacement
  restores and against absence (`Before.Exists == false`) for recreation
  restores; no new stored field.
- Journal exclusion checks the resolved target against the journal directory
  by rooted-path prefix at every mutation entry point, using the same
  `checkPathForMutation` funnel.
- Error for journal targeting uses `ErrCodeInvalidArgs` with a message that
  names the recovery-evidence reason; no silent skip.

## Technical Contract

### Ownership

- Behavior: `Engine` undo and mutation paths in package `jinn`.
- State: journal files under `historyDir()` own the recovery evidence; no
  new state.
- Transactions: none across entries.
- Retries: guard failures are retryable after re-read, as today.

### Capability and Package Structure

No package or directory changes.

### Interfaces and Reuse

- `undoRestore` gains the `if_version` arg alongside `if_checksum`/`if_absent`.
- `verifyUndoChecksum` gains a version sibling reusing `historyEntry.Before`.
- No new interface; the mutation-path funnel is the single exclusion point.

### State and Lifetime

- Journal files keep their layout and retention; exclusion changes no
  lifetimes.
- Guard evaluation reads the live target afresh under `withTargetLocks`.

### Repeat Safety

- A satisfied guard is stable per target state; a mismatch is a typed
  rejection, never a partial restore.

### Runtime and Compatibility

- Wire compatibility: new optional arg only; all existing args behave as
  before.
- `gofmt` clean; stdlib only.

### Generalization

Not applicable: this narrows journal guards; no rule engine is introduced.

### Migration and Deletion

None. Existing journals keep working; `clear` stays rejected as today.

## Target Shape

```text
internal/jinn/
  tool_undo.go    (if_version guard at restore)
  security.go     (journal-exclusion at the mutation-path funnel)
  history.go      (unchanged layout; exclusion reads historyDir)
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

- **M1 Ownership:** undo and mutation paths keep their single owners; the
  journal stays its own evidence owner.
- **M2 Cohesion:** guard and exclusion logic join the existing seam
  functions in place; split only at the guard-evaluation boundary if a
  function exceeds the structure threshold.
- **M3 Compatibility:** list/preview/restore shapes and the non-deletable
  rule preserved; additive args only.
- **M4 Evidence:** proofs come from the source-owned gates below.
- **M5 Preservation:** snapshot capture, blob codec, and compaction untouched.

## Execution Plan

### T1 — Version-based restore guard

```yaml
id: T1
objective: restore accepts if_version as a guard alternative to if_checksum
depends_on: []
owners: [internal/jinn/tool_undo.go undoRestoreLocked verifyUndoChecksum]
may_touch: [internal/jinn/tool_undo.go]
required_changes:
  - evaluate if_version against the snapshot recorded pre-state identity for both replacement and recreation restores
  - reject combined if_version with if_checksum as invalid args
  - preserve mandatory default guards and the committed-state check
must_preserve:
  - ErrCodeStaleFile semantics on guard mismatch
must_not:
  - add a new journal schema field
success:
  - id: T1-S1
    acceptance: [AC1]
    criterion: matching version restores, mismatched version is stale-rejected, combined args are rejected
    gate_ids: [T1-S1]
stop_if:
  - Before identity cannot express the needed pre-state assertion; then record the alternative instead of widening the schema silently
```

### T2 — Journal exclusion from mutation targets

```yaml
id: T2
objective: no mutation can target journal storage
depends_on: [T1]
owners: [internal/jinn/security.go mutation path funnel, internal/jinn/history.go historyDir]
may_touch: [internal/jinn/security.go, internal/jinn/history.go]
required_changes:
  - reject resolved mutation targets inside historyDir with a typed error at the shared funnel
  - cover the nested-config-dir workspace layout in tests
must_preserve:
  - legitimate workspace mutation reachability
must_not:
  - move the journal directory
success:
  - id: T2-S1
    acceptance: [AC2]
    criterion: journal-targeting mutation is rejected in both nested and standard layouts with nothing written
    gate_ids: [T2-S1]
stop_if:
  - the funnel cannot know historyDir for all entry points; halt and name the missing entry point
```

### T3 — Pin the durable-evidence invariants

```yaml
id: T3
objective: non-deletable, committed-only, mandatory-guard behavior locked by tests
depends_on: [T2]
owners: [internal/jinn/tool_undo_test.go, internal/jinn/history_test.go]
may_touch: [internal/jinn/tool_undo_test.go, internal/jinn/tool_undo_helpers_test.go, internal/jinn/history_test.go]
required_changes:
  - pin clear rejection and committed-state restore enforcement
  - pin mandatory default guards and the unsafe opt-out as the sole unguarded route
must_preserve:
  - existing list and preview expectations
must_not:
  - weaken any invariant to make a test pass
success:
  - id: T3-S1
    acceptance: [AC3, AC4]
    criterion: invariant tests pass and the focused suite is green
    gate_ids: [T3-S1]
stop_if:
  - an invariant contradicts a test expectation; preserve the invariant and record the stale expectation
```

## Verification Contract

### Task Gates

```yaml
id: T1-S1
kind: task
check:
  command: go test ./internal/jinn/ -run 'TestUndoRestoreGuard|TestIfVersion' -count=1
  working_directory: .
pass: version-guard tests pass for match, mismatch, and combined-args cases
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
  command: go test ./internal/jinn/ -run 'TestJournalExclusion|TestHistoryTarget' -count=1
  working_directory: .
pass: journal-targeting mutations are rejected with nothing written in both layouts
covers:
  tasks: [T2]
  success: [T2-S1]
  acceptance: [AC2]
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
pass: invariant tests pass and the focused suite is green
covers:
  tasks: [T3]
  success: [T3-S1]
  acceptance: [AC3, AC4]
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
pass: undo tool surface unchanged except the additive guard arg
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
FINALIZATION_UNRESOLVED. Expected exposure: SOURCE_DRIFT if the undo seam
changed at HEAD.

## Exit Criteria

- AC1–AC4 proven by TG1; the journal remains non-deletable, restore is
  guarded by default, and journal storage is unreachable by mutations.

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
