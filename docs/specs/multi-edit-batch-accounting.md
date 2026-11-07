---
command: prd
version: 3
id: prd-multi-edit-batch-accounting
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
    - internal/jinn/tool_multi_edit.go
    - internal/jinn/multi_edit_apply.go
    - internal/jinn/tool_search_replace.go
    - internal/jinn/search_replace_apply.go
    - internal/jinn/batch_write.go
    - internal/jinn/partial_apply.go
    - internal/jinn/tool_multi_edit_test.go
    - internal/jinn/batch_write_test.go
    - internal/jinn/partial_apply_test.go
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

# Multi-file edit with exhaustive preflight and partial accounting

## Purpose

Give `multi_edit`, `apply_patch`, and `search_replace` a single batch
contract — structure-exhaustive all-or-nothing preflight, then per-file
publication that reports exactly which members committed, which failed, and
which never started — so callers can retry only the remainder.

## Compilation Basis

### User Intent

The `/specs` coordinator selected source item 2 of `docs/gsi-strengths-port.md`
(ledger `.work/specs/gsi-strengths-port.md`, group G2): port the GSI
`runEditFiles` decisive rule — validate every member first (parse, duplicate
destination, guard resolution, checksum, readability, edit applicability),
reject the whole batch with per-file reasons publishing nothing, then publish
per file with `[partial; failed; publication; unstarted]` accounting and never
replay committed edits. G2 depends on G3 (`prd-recheck-before-rename`,
digest `38eed22605d04154b55a4b224b14daf7838f089e46524bbb045aeb60404c0fc1`)
for the shared publication seam.

### WWGD Findings

None. Standalone compilation; no WWGD report or finding-ID membership.

### ADRs and Accepted Decisions

- Batch tools keep their distinct preflight pipelines (edit applicability for
  `multi_edit`, candidate expansion for `search_replace`); only the receipt
  shape and the retry contract unify.
- `search_replace`'s stale-abort-all behavior is retained inside preflight;
  external write can still race to publication, which is exactly the case
  partial accounting covers.
- The batch payload stays textual for wire compatibility; each member's
  per-file reason carries its own error code instead of one flattened code.

### Source Baseline

HEAD `0370d1ce09860d5f17ead08be3618f9e1930e119`. Worktree dirty only with
untracked `docs/gsi-strengths-port.md` (digest
`0e500169b4a757b20b7894d00d0e93cb455632d2642ea409803ccae6868096c2`).
Task-relevant source (tracked, unmodified at compilation):
`internal/jinn/tool_multi_edit.go` (`parseAndResolveEdits`, `multiEditLocked`
phases 1a–2), `internal/jinn/multi_edit_apply.go` (`applyPendingEdits`,
`writePendingEdits`), `internal/jinn/batch_write.go`
(`commitPreparedWrites`), `internal/jinn/partial_apply.go` (`partialApplyErr`),
`internal/jinn/tool_search_replace.go`, `internal/jinn/search_replace_apply.go`.

## Product Contract

### Problem

Preflight failures are flattened into one error whose code is always
`ErrCodeEditNotFound` regardless of cause; publication failures report
applied files in prose without naming the unstarted remainder, and nothing
states that a retry must skip already-committed members.

### Desired State

- **AC1:** A preflight conflict rejects the whole batch with one reason and
  one error code per affected member, publishing nothing; causes (parse,
  duplicate destination, guard, checksum, readability, applicability) are
  distinguishable by code.
- **AC2:** After publication begins, a mid-batch failure reports
  `[batch: partial=X; failed=Y; publication=Z; unstarted=W]` naming committed
  members with undo ids, the failing member, and the unstarted set; every
  committed undo entry is listed.
- **AC3:** Committed edits are never replayed — retrying the batch surfaces
  already-committed members as skipped rather than re-writing them.
- **AC4:** Dry-run, overlap, and redundant-edit behavior are unchanged, and
  the focused suite passes.

### In Scope

- `applyPendingEdits` status aggregation and error surfacing.
- `commitPreparedWrites` and `partialApplyErr` receipt construction.
- `search_replace` stale/per-file reporting alignment.
- Tests: `tool_multi_edit_test.go`, `batch_write_test.go`,
  `partial_apply_test.go`.

### Non-Goals

- No change to edit-applicability semantics, overlap rules, or fuzzy matching.
- No journal changes (G4) or publication-seam mechanics from G3 beyond the
  consumed recheck contract.

### Constraints

- Preflight conflicts are structured and exhaustive; failure output must never
  claim "not published" after a partial batch.
- Publication still re-admits each member at its own boundary (preflight is
  not a transaction).
- `partialApplyErr` wording stays parseable by existing tests that match
  the `partial apply — N of M` prefix.

### Acceptance Criteria

- AC1: A batch with one stale and one non-applicable member reports both
  codes and publishes zero files.
- AC2: A mid-batch failure lists partial, failed, unstarted, and every
  committed undo id; the `[batch: ...]` line is always present on partial
  failures.
- AC3: A retry of the same batch does not rewrite already-committed files.
- AC4: Dry-run previews and redundant-edit skips behave as before.

## Closed Decisions

These implementation decisions are closed for this contract.

- The receipt line is emitted once per partial failure as the literal prefix
  `[batch: partial=<n>; failed=<member>; publication=<n>; unstarted=<list>]`
  appended to the existing `partial apply` error, preserving old matches.
- Per-member preflight codes reuse the existing taxonomy (`ErrCodeEditNotFound`,
  `ErrCodeEditNoChange`, `ErrCodeStaleFile`, `ErrCodeConflict`, …) instead
  of inventing new codes.
- "Never replay" is enforced by the existing `verifyPreflightState` staleness
  gate plus the receipt; no new persisted batch journal is added.
- `applyPatch` receipts follow the same rule via
  `commitPreparedWrites`/`tool_patch.go` receipts.

## Technical Contract

### Ownership

- Behavior: `Engine` batch entry points in package `jinn`.
- State: committed writes are the persisted state; the receipt is its
  in-band summary.
- Transactions: none across the batch (per-file guarded writes only).
- Retries: caller retries only `unstarted` + `failed` members.

### Capability and Package Structure

No package or directory changes. Files consolidate in `internal/jinn`.

### Interfaces and Reuse

- `editStatus` already carries per-edit code and error; preflight aggregates
  them into the batch error instead of flattening to a single code.
- `partialApplyErr` gains the unstarted set and the `[batch: ...]` receipt;
  its signature is unchanged (the unstarted set derives from the ordered
  `writes` list minus applied minus failed).
- No new public interface.

### State and Lifetime

- Each member holds its pre-content until its own publication boundary;
  committed undo ids are valid for the process lifetime and beyond via the
  journal.
- The unstarted set is exact because `commitPreparedWrites` walks the same
  ordered list it reports.

### Repeat Safety

- Retry with the full batch: members whose target matches the committed
  post-state are reported skipped; members that drift are stale-rejected;
  unstarted members proceed. No member is written twice.

### Runtime and Compatibility

- Wire format: the receipt is an appended line, backward-compatible with
  prefix-matching consumers; `ErrWithSuggestion.Code` carries the failing
  member's code.
- `gofmt` clean; no new dependencies.

### Generalization

Not applicable: this closes one receipt contract across the existing batch
tools; it does not add a batch engine or framework.

### Migration and Deletion

None. `tool_patch.go` receipts automatically follow `commitPreparedWrites`.

## Target Shape

```text
internal/jinn/
  tool_multi_edit.go       (preflight aggregation)
  multi_edit_apply.go      (per-member statuses preserved)
  batch_write.go           (receipt construction at the seam)
  partial_apply.go         ([batch: ...] line)
  tool_search_replace.go   (aligned reporting)
  search_replace_apply.go  (stale abort stays inside preflight)
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

- **M1 Ownership:** batch accounting stays with the `Engine` entry points; no
  second owner for receipts.
- **M2 Cohesion:** receipt construction joins the existing
  `commitPreparedWrites`/`partialApplyErr` seam; no new abstraction unless
  the function exceeds the structure threshold.
- **M3 Compatibility:** dry-run, overlap, fuzzy, and redundant-edit behavior
  preserved; receipt is additive.
- **M4 Evidence:** proofs come from the source-owned gates below.
- **M5 Preservation:** G3's seam contract is consumed, not rewritten; other
  tools untouched.

## Execution Plan

### T1 — Structured exhaustive preflight reasons

```yaml
id: T1
objective: preflight rejects the whole batch with one code per affected member
depends_on: []
owners: [internal/jinn/tool_multi_edit.go parseAndResolveEdits, internal/jinn/multi_edit_apply.go applyPendingEdits]
may_touch: [internal/jinn/tool_multi_edit.go, internal/jinn/multi_edit_apply.go, internal/jinn/tool_search_replace.go]
required_changes:
  - preserve per-edit status codes through aggregation instead of collapsing to ErrCodeEditNotFound
  - cover duplicate canonical destination and guard-resolution failures with their own codes before applicability
  - keep search_replace stale-abort-all inside this preflight phase
must_preserve:
  - zero publication on any preflight conflict
  - redundant-edit skip and no-change detection semantics
must_not:
  - implement publication for partially valid batches
success:
  - id: T1-S1
    acceptance: [AC1]
    criterion: mixed-cause batch reports every member with its own code and writes nothing
    gate_ids: [T1-S1]
stop_if:
  - duplicate-destination detection cannot reuse the resolved-path map; halt and record the smallest alternative
```

### T2 — Partial and unstarted accounting at publication

```yaml
id: T2
objective: mid-batch failures name committed, failed, and unstarted members
depends_on: [T1]
owners: [internal/jinn/batch_write.go commitPreparedWrites, internal/jinn/partial_apply.go partialApplyErr]
may_touch: [internal/jinn/batch_write.go, internal/jinn/partial_apply.go, internal/jinn/search_replace_apply.go]
required_changes:
  - derive the unstarted set from the ordered writes minus applied minus failed
  - append the literal batch receipt line with every committed undo id while keeping the legacy prefix
  - generate the same receipt from search_replace publication failures
must_preserve:
  - per-file re-admission at each write boundary
  - undo snapshot per committed member
must_not:
  - emit a not-published claim after a partial batch
success:
  - id: T2-S1
    acceptance: [AC2]
    criterion: mid-batch failure prints the batch receipt naming all three sets and every committed undo id
    gate_ids: [T2-S1]
stop_if:
  - the unstarted set cannot be computed exactly from the ordered list; halt rather than reporting an approximate list
```

### T3 — Never-replay retry contract

```yaml
id: T3
objective: retried batches skip committed members instead of re-writing them
depends_on: [T2]
owners: [internal/jinn/tool_multi_edit_test.go, internal/jinn/batch_write_test.go]
may_touch: [internal/jinn/tool_multi_edit_test.go, internal/jinn/batch_write_test.go, internal/jinn/partial_apply_test.go]
required_changes:
  - add retry coverage proving committed members are skipped on batch retry while unstarted members proceed
  - pin dry-run and redundant-edit behavior as unchanged in the same suites
must_preserve:
  - existing undo-id and partial-apply expectations
must_not:
  - add a persisted batch journal
success:
  - id: T3-S1
    acceptance: [AC3, AC4]
    criterion: retry test shows no double-write and the focused suite passes with dry-run intact
    gate_ids: [T3-S1]
stop_if:
  - staleness gating cannot distinguish committed from drifted members; record the open case instead of forcing a rewrite
```

## Verification Contract

### Task Gates

```yaml
id: T1-S1
kind: task
check:
  command: go test ./internal/jinn/ -run 'TestMultiEditPreflight|TestSearchReplaceStale' -count=1
  working_directory: .
pass: mixed-cause preflight tests report per-member codes with zero publication
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
  command: go test ./internal/jinn/ -run 'TestPartialApply|TestBatchWrite' -count=1
  working_directory: .
pass: partial-apply tests show the batch receipt with committed, failed, and unstarted sets
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
pass: focused suite passes including retry and dry-run coverage
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
pass: batch tool surface unchanged with the additive receipt
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
FINALIZATION_UNRESOLVED. Expected exposure: SOURCE_DRIFT if the batch seam
changed at HEAD; MISSING_DECISION if a retry-skip policy needs a durable
boundary not closed here.

## Exit Criteria

- AC1–AC4 proven by TG1 on top of the G3 seam; no partial failure hides an
  unstarted set and no retry replays a committed edit.

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
