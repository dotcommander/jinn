# GSI strengths — candidates for enriching jinn (later)

> Source: evidence-backed comparison of GSI (`github.com/dotcommander/gsi`,
> `internal/tools/`) against jinn at `0370d1ce` (2026-11-07). This file is a
> port-back list only — jinn already donated the search-pagination rule
> (`formatSearchJSON` offset slicing), which is not offered back. Compaction
> and cache mechanisms are excluded throughout. Each item names the GSI
> owner, the decisive rule, why it could matter to jinn, and what must hold
> if ported.

## 1. Streaming bounded read with full-file digest + resume cursor

- GSI owner: `internal/tools/read.go` (`readOne`), `internal/tools/read_page.go`
  (`pageStream`, `readPageMetadata`).
- Decisive rule: the read streams the file once, hashing every byte for the
  full-file SHA-256 while counting every line, retaining only the requested
  page rows plus a capped window of the current line. Retained memory stays
  independent of input size. The footer emits `next_offset`/`next_column`
  (absolute column continuation for overlong lines) and `total_lines`, so a
  caller resumes without re-reading covered bytes and without a spill file.
- Why jinn could benefit: jinn's `read_window.go` assembles the window from
  buffered content and spills the remainder to a temp file. A streaming
  version would trade that spill file for a digest + cursor, and make
  "continue from last displayed line" exact even for column-truncated lines.
- Preserve if ported: the digest must cover the whole file (not the window);
  resume must use `next_offset`/`next_column` from the stream, never a
  requested bound; oversize lines must be consumed without whole-line
  buffering.

## 2. Multi-file edit: all-or-nothing preflight, then per-file publication with partial/unstarted accounting

- GSI owner: `internal/tools/edit_files.go` (`runEditFiles`, `editFilesFailure`).
- Decisive rule: every member is validated first (parse, duplicate canonical
  destination, guard resolution, checksum, readability, edit applicability);
  any conflict rejects the whole batch with per-file reasons and publishes
  nothing. Publication then proceeds per file, each re-admitted at its own
  boundary; a mid-batch failure reports
  `[batch: partial=…; failed=…; publication=…; unstarted=…]` and every
  previously published member's undo entry. Committed edits are never
  replayed.
- Why jinn could benefit: jinn's `multi_edit` validates-then-applies, and
  `search_replace` aborts all on stale but reports other per-file failures in
  text only. GSI's contract is stricter and more useful: preflight conflicts
  are structured and exhaustive, and post-publication failures name exactly
  which files committed and which never started.
- Preserve if ported: publication must recheck each member at its own write
  (preflight is not a transaction); failure output must never claim
  "not published" after a partial batch.

## 3. Recheck-before-rename at the guarded publication seam

- GSI owner: `internal/tools/mutation.go` (`writeFileGuardedContext`).
- Decisive rule: under a directory-scoped mutation lock, the temp file is
  staged, synced, and closed; then the live target is re-hashed
  (`FileSHA256Context`) and compared to the expected checksum immediately
  before `os.Rename`. Exclusive create uses `os.Link` (atomic no-replace)
  and fails closed instead of falling back to rename.
- Why jinn could benefit: jinn already re-reads before rename via
  `verifyPreflightState` (beforeRename callback). GSI additionally streams
  the hash (bounded memory for large files) and holds a lock across the
  stage→recheck→rename sequence, closing the cooperator window more tightly.
- Preserve if ported: recheck must happen after staging and before rename;
  exclusive create must never fall back to overwrite; hash must stream for
  large files.

## 4. Non-deletable recovery journal with guarded restore

- GSI owner: `internal/tools/file_history_tool.go` (`FileHistoryTool`).
- Decisive rule: history entries are persistent and cannot be deleted or
  cleared by callers; restore requires an explicit current-file guard
  (`if_checksum` or `if_version`, mutually exclusive) and a committed-state
  check. Mutations cannot target the history storage itself.
- Why jinn could benefit: jinn's `undo` supports `clear`, which destroys
  recovery evidence. GSI's rule keeps the journal as durable evidence — a
  restore that lands on a changed file is a decision the caller must make
  explicitly, not an accident.
- Preserve if ported: no caller-reachable delete/clear; restore always
  guarded; journal storage excluded from mutation targets.

## 5. Ranked whole-record search (rows mode)

- GSI owner: `internal/tools/search_rows.go` (`runSearchRows`, `rowsPage`).
- Decisive rule: BM25 ranking over the union of query tokens across whole
  JSON records (JSONL or JSON-array), with a fixed-size page, `offset`
  echo, `matched_rows` total, schema keys, and exact per-record render
  bounds. Oversize corpora are a hard error, not silent truncation.
- Why jinn could benefit: jinn's `search_files` is line-oriented. For journal
  or log files, ranked whole-record retrieval with a total count is a
  distinct, composable capability.
- Preserve if ported: total must be exact (full corpus scan), page size
  fixed and calibrated, oversize input an error rather than truncation.

## 6. Small consolidated file-discovery tool

- GSI owner: `internal/tools/search_files.go` (files mode of `search`).
- Decisive rule: one bounded tool returns path/type/size/mtime with glob,
  depth limit, scan budget, output cap, and explicit truncation reasons —
  covering jinn's `stat_file` + `list_dir` + `find_files` in a single call.
- Why jinn could benefit: fewer round trips for "what's here" questions.
- Preserve if ported: symlinks return link metadata only and are never
  traversed; truncation reasons stay explicit; streaming dir reads (no
  unbounded materialization).

## 7. Replay-safety classification in the tool registry

- GSI owner: `internal/tools/registry.go` (`toolEntry.replaySafe`, `effect`).
- Decisive rule: every advertised tool carries an explicit effect and a
  `replaySafe` flag proving it only reads derived state; unclassified tools
  fail closed. A lost result is recoverable by re-running only for
  replay-safe tools, so retry logic never repeats an external effect.
- Why jinn could benefit: jinn dispatches by name with risk metadata per
  call. A registry-level replay-safety classification would let harness
  retry policies be declared once instead of inferred.
- Preserve if ported: unclassified tools default to fail-closed; the flag
  must be a verified property, not a declaration of intent.

## Explicitly not offered back

- Search offset pagination: donated by jinn, already implemented in GSI.
- Ask/questionnaire: equivalent on both sides.
- `run_shell` risk classification / `run_plan` / LSP / web tools: jinn
  strengths, no GSI counterpart to port.

## Known constraints (shared, not regressions)

- Offset paging (both sides) assumes an unchanged tree across pages; a
  changed tree can shift or duplicate records. Documented at the call site.
- GSI content-mode `offset` counts emitted records (matches + context
  lines); records are self-contained, so a split context group is benign.
- A missing or failing tool, undocumented behavior, or absence in this list
  is not evidence about uninspected code.
