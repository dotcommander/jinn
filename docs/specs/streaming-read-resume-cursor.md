---
command: prd
version: 3
id: prd-streaming-read-resume-cursor
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
    - internal/jinn/tool_read.go
    - internal/jinn/read_text_stream.go
    - internal/jinn/read_window.go
    - internal/jinn/read_text_legacy_test.go
    - internal/jinn/read_text_stream_test.go
  conditional: []
  forbidden:
    - docs/gsi-strengths-port.md
handoff:
  objective_kind: standalone_prd
  workflow_path: null
  audit_run_id: null
terminal_verification: [TG1]
---

# Streaming bounded read with full-file digest and resume cursor

## Purpose

Replace the spill-file continuation in `read_file` with a single streaming
pass that always hashes every byte, retains only the requested window plus a
capped current-line buffer, and returns an exact digest-backed resume cursor
so callers never re-read covered bytes.

## Compilation Basis

### User Intent

The `/specs` coordinator selected source item 1 of `docs/gsi-strengths-port.md`
(ledger `.work/specs/gsi-strengths-port.md`, group G1): port the GSI
`readOne`/`pageStream` decisive rule — one streaming pass hashing every byte
for a full-file SHA-256 while counting lines, retaining only the requested
page rows plus a capped current-line window, emitting `next_offset` and
`next_column` — trading jinn's spill temp file for a digest plus cursor.

### WWGD Findings

None. Standalone compilation; no WWGD report or finding-ID membership.

### ADRs and Accepted Decisions

- The 50 MB `maxFileSize` bound, rooted-FS access, and `stableReadFile`
  drift detection stay; the streaming form is bounded by the existing cap,
  not by input size in any other dimension.
- The opt-in `include_checksum` API stays; the change makes the digest
  unconditional (always computed) in the new read path and keeps the
  `Checksum` field populated, so existing callers see a superset.
- Spill-file continuation is replaced, not layered; the registered spill
  registry and `ByteHint` wording change only where the cursor replaces them.

### Source Baseline

HEAD `0370d1ce09860d5f17ead08be3618f9e1930e119`. Worktree dirty only with
untracked `docs/gsi-strengths-port.md` (digest
`0e500169b4a757b20b7894d00d0e93cb455632d2642ea409803ccae6868096c2`).
Task-relevant source (tracked, unmodified at compilation):
`internal/jinn/tool_read.go` (`readContentResult`, `maxFileSize`,
`readMaxBytes`, `readDefaultLines`), `internal/jinn/read_text_stream.go`
(`scanReadLines`, `readTextWindow`, spill closure, `renderSelectedWindow`),
`internal/jinn/read_window.go` (`assembleReadResult`, `oversizedLineResult`).

## Product Contract

### Problem

`read_file` builds the window from buffered lines and spills the remainder to
a temp file (`TempFile`, `ByteHint`) for continuation; the digest is
opt-in (`needChecksum`); oversize single lines fall back to a shell hint
instead of being consumed in bounded memory. Continuation is therefore a
file the caller must re-read, not a digest-backed cursor.

### Desired State

- **AC1:** One pass hashes every byte of the file and counts every line while
  retaining only the requested window rows plus a capped current-line buffer;
  retained memory is independent of file size (within the 50 MB cap).
- **AC2:** The result carries the full-file digest plus `next_offset`
  (absolute byte offset) and `next_column` (absolute column continuation for
  overlong lines), and resume reuses those values instead of a spill path.
- **AC3:** Oversize lines are consumed without whole-line buffering and are
  reported through the cursor, not a shell fallback.
- **AC4:** Existing window/truncate semantics (head/tail/middle/none/smart,
  line numbers, `TotalLines`) are preserved and the focused suite passes.

### In Scope

- `readTextWindow`, `scanReadLines`, `readContentResult`, `assembleReadResult`
  and the render path (`renderSelectedWindow`).
- Cursor fields in the tool result wiring (`tool_read.go` caller).
- Tests in `read_text_stream_test.go` / `read_text_legacy_test.go`.

### Non-Goals

- No change to the 50 MB cap or truncation-mode set.
- No rows-mode ranked search (G5), discovery tool (G6), or history changes (G4).

### Constraints

- Resume must use the emitted `next_offset`/`next_column` from the stream,
  never a caller-supplied bound.
- The digest covers the whole file, not the window.
- Rooted-FS and `stableReadFile` drift checks remain.

### Acceptance Criteria

- AC1: A >50 KB single-line file reads without whole-line buffering and a
  near-cap file streams in bounded memory (allocator-observed in test).
- AC2: A two-page read resumes exactly at the cursor (byte-identical
  concatenated pages) with no spill file created.
- AC3: An overlong line cut mid-line resumes at the exact continuation column.
- AC4: `go test ./internal/jinn/` passes including existing truncate and
  spill-registry tests (re-pointed at cursors where the contract requires).

## Closed Decisions

These implementation decisions are closed for this contract.

- The cursor is exposed as `next_offset` and `next_column` integer fields
  alongside the existing `NextLine`; `TempFile` stays deprecated-empty rather
  than being removed, so pinned callers fail soft (no path) instead of
  breaking on a missing key.
- The digest is computed on every read (no `needChecksum` gate); the flag
  then only controls whether the caller asked to *see* it.
- `oversizedLineResult`'s shell-fallback text is replaced by the cursor
  report; the `ByteHint` remains for byte-cap collapses only.

## Technical Contract

### Ownership

- Behavior: `Engine.readTextWindow` (`read_text_stream.go`).
- State: none persistent; the pass is stateless given (offset, column).
- Transactions: none (read-only).
- Retries: resume is idempotent — re-issuing with the emitted cursor returns
  the same continuation bytes given an unchanged file.

### Capability and Package Structure

No package or directory changes. Read path stays in package `jinn`.

### Interfaces and Reuse

- `readContentResult` gains `NextOffset int64` and `NextColumn int64`
  (0 when the window covers the file end).
- `readFileContent(resolved, args, needChecksum)` signature unchanged; the
  digest is always computed inside.
- No new interface; the stream visitor shape from `scanReadLines` is reused.

### State and Lifetime

- The cursor is derived state (digest + offset + column), not persisted.
- If the file changes between pages, `stableReadFile` (size/mtime) rejects
  the stale continuation; the digest mismatch is the backstop.
- Current-line buffer is capped at `readMaxBytes`; overflow advances the
  column cursor without growing the buffer.

### Repeat Safety

- Same cursor + unchanged file → same continuation (deterministic).
- Changed file → typed stale error, never silent re-read of covered bytes.

### Runtime and Compatibility

- Result JSON gains two integer fields; no field is removed.
- Spill-file code paths (`writeStreamReadRemainder`, spill registry use in
  the read path) become unreachable and are removed; the shell-spill feature
  for `run_shell` output is untouched.
- `gofmt` clean; stdlib only.

### Generalization

Not applicable: this narrows one read mechanism; no engine or framework is
introduced.

### Migration and Deletion

- Callers using `TempFile` fall back to the cursor; the field is kept
  empty for one release cycle, then removed by a successor (out of scope).
- Documentation under `docs/` that describes spill continuation is updated
  in the same task that removes the path (allowed path adds `docs/tool-reference.md`
  only if it names the spill file; otherwise a successor owns it).

## Target Shape

```text
internal/jinn/
  tool_read.go         (result struct gains cursor fields)
  read_text_stream.go  (single-pass digest + cursor read)
  read_window.go       (assembleReadResult emits cursor, drops spill)
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

- **M1 Ownership:** the read path stays owned by `Engine` in package `jinn`;
  the cursor is derived, not a second owner.
- **M2 Cohesion:** streaming, hashing, and cursor emission extend the existing
  `scanReadLines`/`readTextWindow` seam in place; split only at the
  consume/emit boundary if a function exceeds the structure threshold.
- **M3 Compatibility:** window/truncate/line-number semantics preserved;
  public result gains fields only.
- **M4 Evidence:** proofs come from the source-owned gates below.
- **M5 Preservation:** spill registry for `run_shell` and unrelated tools
  untouched; no state outside the read result changes.

## Execution Plan

### T1 — Single-pass digest with bounded retention

```yaml
id: T1
objective: every read hashes the whole file while retaining only the window plus a capped line
depends_on: []
owners: [internal/jinn/read_text_stream.go readTextWindow scanReadLines]
may_touch: [internal/jinn/read_text_stream.go, internal/jinn/tool_read.go]
required_changes:
  - compute the full-file SHA-256 on the single pass for every read; populate Checksum unconditionally
  - retain only window rows and cap the current-line buffer at readMaxBytes so memory stays independent of input size
  - consume oversize lines through the capped buffer instead of whole-line accumulation
must_preserve:
  - TotalLines counting, stableReadFile drift rejection, and all truncate modes
must_not:
  - widen the 50 MB cap or add a second pass over the file
success:
  - id: T1-S1
    acceptance: [AC1]
    criterion: large-line and near-cap tests stream in bounded memory and the digest covers the whole file
    gate_ids: [T1-S1]
stop_if:
  - stream hashing cannot share the single pass with line counting; then halt rather than adding a second read
```

### T2 — Digest-backed resume cursor without spill

```yaml
id: T2
objective: next_offset and next_column replace the spill-file continuation
depends_on: [T1]
owners: [internal/jinn/read_window.go assembleReadResult, internal/jinn/tool_read.go readContentResult]
may_touch: [internal/jinn/read_window.go, internal/jinn/tool_read.go, internal/jinn/read_text_stream.go]
required_changes:
  - emit next_offset and next_column on every truncated result and resolve continuation from those values
  - make the read-path spill unreachable and remove its temp-file creation while keeping TempFile empty for compatibility
  - replace oversizedLineResult shell fallback with the cursor report for overlong lines
must_preserve:
  - existing NextLine semantics for sequential modes
must_not:
  - leave a path that re-reads covered bytes on resume
success:
  - id: T2-S1
    acceptance: [AC2, AC3]
    criterion: two-page reads concatenate byte-identically with no spill file and mid-line resume lands on the exact column
    gate_ids: [T2-S1]
stop_if:
  - a caller in-repo depends on TempFile bytes; record it as a separate finding instead of breaking it silently
```

### T3 — Cursor contract and regression suite

```yaml
id: T3
objective: cursor semantics pinned by tests and the focused suite passes
depends_on: [T2]
owners: [internal/jinn/read_text_stream_test.go, internal/jinn/read_text_legacy_test.go]
may_touch: [internal/jinn/read_text_stream_test.go, internal/jinn/read_text_legacy_test.go]
required_changes:
  - re-point spill-registry tests at cursor equivalence where the contract displaces them
  - add stale-file cursor rejection coverage using the digest plus size and mtime checks
must_preserve:
  - truncate-mode expectations from the legacy suite
must_not:
  - delete failing expectations without a cursor-based replacement
success:
  - id: T3-S1
    acceptance: [AC4]
    criterion: focused suite passes including truncate and cursor tests
    gate_ids: [T3-S1]
stop_if:
  - an expectation cannot be expressed as a cursor contract; record the open coverage gap instead of weakening the suite
```

## Verification Contract

### Task Gates

```yaml
id: T1-S1
kind: task
check:
  command: go test ./internal/jinn/ -run 'TestReadStream|TestScanReadLines|TestLargeLine' -count=1
  working_directory: .
pass: streaming and bounded-memory tests pass with whole-file digest coverage
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
  command: go test ./internal/jinn/ -run 'TestReadCursor|TestNextOffset|TestSpill' -count=1
  working_directory: .
pass: cursor resume tests pass with no spill file created and exact column continuation
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
pass: focused suite passes including truncate and cursor tests
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
pass: read surface still advertises the same tools with the enlarged result schema
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
FINALIZATION_UNRESOLVED. Expected exposure: SOURCE_DRIFT if the read seam
changed at HEAD; OUT_OF_SCOPE_DEPENDENCY if a caller depends on `TempFile`.

## Exit Criteria

- AC1–AC4 proven by TG1; the spill path is unreachable in the read flow and
  resume uses only digest-backed cursors.

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
