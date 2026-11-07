---
command: prd
version: 3
id: prd-recheck-before-rename
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
    - internal/jinn/tool_write.go
    - internal/jinn/batch_write.go
    - internal/jinn/filelock.go
    - internal/jinn/security.go
    - internal/jinn/tool_write_test.go
    - internal/jinn/batch_write_test.go
    - internal/jinn/partial_apply_test.go
  conditional: []
  forbidden:
    - internal/jinn/tool_undo.go
    - docs/gsi-strengths-port.md
handoff:
  objective_kind: standalone_prd
  workflow_path: null
  audit_run_id: null
terminal_verification: [TG1]
---

# Recheck before rename at the guarded publication seam

## Purpose

Close the preflight-to-rename cooperator window in jinn's guarded file
publication by streaming the target recheck hash, failing exclusive create
closed via an atomic no-replace publish, and proving the in-process target
lock already holds the stage→recheck→rename sequence so the two remaining
cross-process gaps are named rather than hidden.

## Compilation Basis

### User Intent

The `/specs` coordinator selected source item 3 of `docs/gsi-strengths-port.md`
(recorded in `.work/specs/gsi-strengths-port.md`, group G3): port the GSI
`writeFileGuardedContext` decisive rule — stage, sync, close the temp file,
re-hash the live target, then rename under a held lock — into jinn, preserving
that the recheck happens after staging and before rename and that exclusive
create never falls back to overwrite.

### WWGD Findings

None. This is a standalone compilation; there is no WWGD report, finding-ID
membership, or workset lineage. `source_findings` is not applicable.

### ADRs and Accepted Decisions

- jinn's in-process target locks (`filelock.go` `withTargetLocks`) already
  wrap `write_file`, `edit_file`, `apply_patch`, `multi_edit`,
  `search_replace`, and undo restore; this contract reuses them rather than
  adding a directory-scoped lock subsystem.
- Production runs one process per tool call (per `verifyIfChecksum`
  comment), so the in-process lock does not coordinate external processes;
  the contract names this limit instead of claiming cross-process exclusion.
- Guarded publication is the single mutation seam: `snapshotAndWrite`
  (`tool_write.go`) and `commitPreparedWrites` (`batch_write.go`) both funnel
  staged writes through `atomicWriteFileStaged` with a `beforeRename`
  callback; the recheck stays there.

### Source Baseline

HEAD `0370d1ce09860d5f17ead08be3618f9e1930e119`. Worktree dirty only with
untracked `docs/gsi-strengths-port.md` (digest
`0e500169b4a757b20b7894d00d0e93cb455632d2642ea409803ccae6868096c2`).
Task-relevant source fingerprints (all tracked, unmodified at compilation):
`internal/jinn/tool_write.go` (`atomicWriteFileStaged`,
`snapshotAndWrite`, `verifyPreflightState`, `writeFileLocked`),
`internal/jinn/batch_write.go` (`commitPreparedWrites`),
`internal/jinn/filelock.go` (`withTargetLocks`, `withTargetLocksOnly`),
`internal/jinn/security.go` (`readRegularFile`), `internal/jinn/tool_read.go`
(`maxFileSize = 50 << 20`).

## Product Contract

### Problem

`verifyPreflightState` re-reads the whole target (`readRegularFile`,
50 MB cap) plus a buffered SHA-256 before rename — correct but memory-heavy
for large files. Exclusive create (`if_absent:true`) is enforced only at
preflight; `atomicWriteFileStaged` then publishes with `e.root.Rename`, which
replaces a target that appeared between preflight and rename instead of
failing closed.

### Desired State

- **AC1:** The target recheck hash is streamed (`io.Copy`-style) in bounded
  memory in both `deny-rewrite` and overwrite paths without changing when the
  comparison happens (after staging, before rename).
- **AC2:** An exclusive create (`if_absent:true`) publishes atomically with
  no-replace semantics: if the target exists at publish time, the write fails
  with `ErrCodeStaleFile`, the target bytes are untouched, and the staged temp
  is removed.
- **AC3:** Existing target-lock coverage across the stage→recheck→rename
  sequence is proven by test (no new lock subsystem), and the cross-process
  limit of the in-process lock is documented in code at the seam.
- **AC4:** All existing guarded-publication behavior (checksum guards,
  undo snapshots, partial-apply accounting, durability syncs) is preserved
  with no regression in the focused suite.

### In Scope

- Streamed target recheck inside `verifyPreflightState` and its `beforeRename`
  call sites (`snapshotAndWrite`, `commitPreparedWrites`, `writeFileLocked`).
- No-replace publish for exclusive create in `atomicWriteFileStaged`.
- Regression/race tests at `writeFileWithPreCommitHook` and batch entry points.

### Non-Goals

- No new directory-scoped cross-process lock subsystem (in-process locks exist).
- No change to undo journal semantics (G4), multi-edit preflight exhaustiveness
  (G2), or read resume cursors (G1).

### Constraints

- `maxFileSize` (50 MB) bound and rooted-FS (`os.Root`) restrictions remain.
- Windows: directory-fsync behavior unchanged (`syncMutationDirectory`).
- Stale-write error code stays `ErrCodeStaleFile` with a re-read/reconcile
  suggestion.

### Acceptance Criteria

- AC1: A near-50 MB target can be rechecked without loading the whole file
  into memory (no new `readRegularFile` on the recheck path; a stream hasher
  proves it under test).
- AC2: `write_file` with `if_absent:true` fails when the target appears after
  preflight, leaves the target byte-identical, and returns `ErrCodeStaleFile`.
- AC3: A deterministic race test (pre-commit hook creating the target after
  preflight) proves the stage→recheck→rename order fails fast.
- AC4: `go test ./internal/jinn/` passes unmodified in scope (including
  `batch_write_test.go`, `partial_apply_test.go`).

## Closed Decisions

These implementation decisions are closed for this contract.

- The recheck stays in the existing `beforeRename` callbacks; no new seam is
  introduced.
- Streaming reuses `io.Copy`-to-hasher over a rooted-file reader; the 50 MB
  preflight cap still rejects oversize inputs before staging.
- No-replace publish uses `os.Link`(atomic no-replace) followed by temp
  unlink on success, fenced by the existing `withTargetLocks`; on platforms
  where `os.Link` via the rooted FS is unavailable, publish must fail closed
  (never fall back to rename-overwrite for exclusive create).
- No cross-process lock is added; the code comment at the seam records that
  target-visibility changes from another process are detected at recheck and
  rejected, not prevented.

## Technical Contract

### Ownership

- Behavior/policy: `Engine` in `internal/jinn` (`tool_write.go`).
- State: staged temp files under the workspace root; committed history via
  the undo owner (unchanged here).
- Transactions: `atomicWriteFileStaged` owns stage→recheck→rename order;
  `withTargetLocks` owns in-process mutual exclusion.
- Retries/idempotency: stale-write errors are retryable after re-read; the
  no-replace publish never overwrites an existing target.

### Capability and Package Structure

No package or directory changes. The mutation seam lives in package `jinn`
(files listed in Scope). Structure follows the maintainer contract below.

### Interfaces and Reuse

- `verifyPreflightState(path, expected, expectedExists)` keeps its signature;
  its internals switch from buffered re-read to streamed hash comparison.
- `atomicWriteFileStaged(resolved, content, beforeRename)` gains no new
  public surface; exclusive-create signaling is derived from the existing
  preflight `exists` fact passed by callers.
- No new interface is introduced (single consumer-owner pair at the seam).

### State and Lifetime

- Staged temps: created `O_CREATE|O_EXCL`, removed on any failure, synced
  before close; rename/link is the only publication event, after which the
  parent directory is synced (existing behavior).
- The streamed recheck opens the live target for reading; a target that
  changes size/mtime mid-hash is treated as a preflight mismatch (existing
  `readRegularFile` stability semantics preserved via `stableReadFile`-style
  re-stat).

### Repeat Safety

- Overwrite path: repeat calls after a stale rejection behave as before
  (re-read, reconcile, retry with a fresh checksum).
- Exclusive create: repeat calls after the first success hit the exists
  branch and reject (`verifyIfChecksum`/exists check); after a no-replace
  failure the target is untouched, so a retry reconciles rather than
  corrupts.

### Runtime and Compatibility

- Rooted FS (`os.Root`) operations only; no new syscall surface beyond
  `os.Link` (guarded; failure fails closed for exclusive create).
- Existing error shape preserved: `ErrWithSuggestion` with `ErrCodeStaleFile`
  and re-read/reconcile suggestion text.
- `gofmt` clean; no new dependencies (stdlib `io`, `crypto/sha256`).

### Generalization

Not applicable: this port narrows an existing mechanism; no new seam or
generalized engine is introduced.

### Migration and Deletion

None. No caller, config, schema, or surface changes.

## Target Shape

```text
internal/jinn/
  tool_write.go      (staged publish, streamed recheck, no-replace create)
  batch_write.go     (recheck via existing hook; unchanged accounting)
  filelock.go        (unchanged in-process target locks)
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

- **M1 Ownership:** the guarded-publication seam stays owned by `Engine` in
  package `jinn`; no behavior or mutable fact moves to a second owner.
- **M2 Cohesion:** the streamed hash and no-replace branch extend the existing
  `atomicWriteFileStaged`/`verifyPreflightState` functions in place; no new
  file or abstraction unless the function exceeds the structure review
  threshold, in which case split at the hash/publish seam only.
- **M3 Compatibility:** public behavior, persistence, failure handling, and
  accepted outcomes are preserved except AC1–AC3; consequential choices are
  closed above without a separate approval turn.
- **M4 Evidence:** observable results are proven by the source-owned gates
  below; unrun checks are reported, never claimed.
- **M5 Preservation:** unrelated work, index state, and path/effect authority
  are untouched; uncertain side effects are reconciled before retry.

## Execution Plan

### T1 — Stream the target recheck hash

```yaml
id: T1
objective: verifyPreflightState hashes the live target in bounded memory
depends_on: []
owners: [internal/jinn/tool_write.go verifyPreflightState, internal/jinn/security.go readRegularFile]
may_touch: [internal/jinn/tool_write.go, internal/jinn/security.go]
required_changes:
  - replace the whole-file recheck read with a streamed SHA-256 over a rooted reader; keep the 50 MB cap, existence comparison, and checksum-mismatch error identical
  - keep all three call sites (snapshotAndWrite, commitPreparedWrites, writeFileLocked pre-commit) ordered so recheck happens after staging and before rename
must_preserve:
  - ErrCodeStaleFile + re-read/reconcile suggestion on mismatch
  - buffered-preflight behavior for the phase-1 read (only the recheck streams)
must_not:
  - introduce cross-process locking
  - change the recheck timing away from before-rename
success:
  - id: T1-S1
    acceptance: [AC1]
    criterion: a large-file test proves the recheck path allocates no whole-file buffer (stream hasher observed; no readRegularFile call on the recheck path)
    gate_ids: [T1-S1]
stop_if:
  - the rooted FS cannot stream-open the live target without violating maxFileSize; use the bounded-read error path instead of buffering
```

### T2 — Fail exclusive create closed (no-replace publish)

```yaml
id: T2
objective: if_absent create never overwrites a target that appeared after preflight
depends_on: [T1]
owners: [internal/jinn/tool_write.go atomicWriteFileStaged, internal/jinn/tool_write.go writeFileLocked]
may_touch: [internal/jinn/tool_write.go]
required_changes:
  - thread the phase-1 exists fact into atomicWriteFileStaged; exclusive-create publishes with atomic no-replace (os.Link) and unlinks the staged temp afterwards
  - on existing target at publish time return ErrCodeStaleFile, remove the staged temp, leave target bytes untouched
  - overwrite and non-exclusive paths keep rename semantics
must_preserve:
  - durability syncs (file sync, parent dir sync) and mtime tracking
  - undo snapshot recording before mutation
must_not:
  - fall back to rename-overwrite for exclusive create on any platform
  - change dry-run or if_checksum behavior
success:
  - id: T2-S1
    acceptance: [AC2]
    criterion: deterministic race test creates the target after preflight; write fails with ErrCodeStaleFile, target is byte-identical, staged temp is gone
    gate_ids: [T2-S1]
stop_if:
  - os.Link is unavailable through the rooted FS on a supported platform; then exclusive create must fail closed with a typed error, never silently use rename
```

### T3 — Prove lock coverage and record the cross-process limit

```yaml
id: T3
objective: existing target locks provably serialize stage-recheck-rename; limit documented
depends_on: [T2]
owners: [internal/jinn/filelock.go withTargetLocks, internal/jinn/tool_write.go seam comments]
may_touch: [internal/jinn/filelock.go, internal/jinn/tool_write.go, internal/jinn/tool_write_test.go, internal/jinn/batch_write_test.go]
required_changes:
  - add a comment at atomicWriteFileStaged recording that withTargetLocks serializes in-process cooperators while cross-process changes are detected (not prevented) by the recheck
  - add blocked-at-publish regression coverage via writeFileWithPreCommitHook proving failures before rename
must_preserve:
  - batch partial-apply accounting and undo-id reporting
must_not:
  - add a new lock subsystem
success:
  - id: T3-S1
    acceptance: [AC3, AC4]
    criterion: the pre-commit race tests fail fast in both single-write and batch paths and the focused suite passes unchanged
    gate_ids: [T3-S1]
stop_if:
  - coverage shows a mutation entry that publishes outside withTargetLocks; then halt and record it as a separate finding instead of silently widening scope
```

## Verification Contract

### Task Gates

```yaml
id: T1-S1
kind: task
check:
  command: go test ./internal/jinn/ -run 'TestVerifyPreflight|TestRecheck' -count=1
  working_directory: .
pass: new streaming-recheck tests pass and the recheck path demonstrably streams (test observes bounded allocation)
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
  command: go test ./internal/jinn/ -run 'TestWriteFileIfAbsent|TestExclusiveCreate' -count=1
  working_directory: .
pass: race test shows ErrCodeStaleFile, byte-identical target, and cleaned temp
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
pass: focused suite passes including pre-commit and batch partial-apply tests
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
  command: go run ./cmd/jinn --help
  working_directory: .
pass: CLI surface and docs unchanged (no new flags)
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
FINALIZATION_UNRESOLVED (see `lib/prd/contract.md`). Applicable and expected:
SOURCE_DRIFT if the write seam changed at HEAD; OUT_OF_SCOPE_DEPENDENCY if a
mutation entry publishes outside `withTargetLocks`.

## Exit Criteria

- All of AC1–AC4 are proven by TG1.
- The recheck still happens after staging and before rename; exclusive create
  fails closed on every supported platform.
- No unfinished investigation tasks.

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
