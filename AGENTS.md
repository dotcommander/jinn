# AGENTS.md — jinn tool usage guide for AI agents

## run_shell — output truncation

Shell output is truncated to **2000 lines** or **50KB** (whichever is hit first), keeping the tail. When truncated, full output is saved to a temp file and the response includes a notice:

```
[Showing 1847 of 3000 lines (45.2KB of 78.1KB). Full output: /tmp/jinn-shell-xxxxx.log]
```

For very large output (>1MB capture), the subprocess buffer caps at 1MB but the full output is always spilled to a temp file.

---

## run_shell — risk classifier

Before executing a command, jinn classifies it:

| Level | Meaning | Response |
|-------|---------|----------|
| `safe` | Read-only, no side effects | Executed normally |
| `caution` | Modifies state but recoverable | Executed normally |
| `dangerous` | Destructive or irreversible | **Blocked** unless `force: true` |

```json
{"tool": "run_shell", "args": {"command": "rm -rf /tmp/scratch", "force": true}}
```

The `risk` field is always present in the response envelope for `run_shell` calls, even when the command succeeds:

```json
{"ok": true, "result": "...", "risk": "safe", "classification": "success"}
```

Without `force: true`, a dangerous command returns:

```json
{"ok": false, "error": "blocked by risk classifier: dangerous — ...", "suggestion": "pass force:true in args to override, or use a less-destructive command", "risk": "dangerous"}
```

---

## run_shell — exit code classification

Every `run_shell` response includes a `classification` annotation after the output:

```
[exit: N]
<stdout/stderr>
[classification: <class> — <reason>]
```

### Classification values

| Class | Meaning | Action |
|-------|---------|--------|
| `success` | Exit 0 — command completed normally | Proceed |
| `expected_nonzero` | Non-zero exit that is a semantic signal, not a failure | Do NOT retry — read the output for the actual result |
| `error` | Unexpected non-zero exit indicating failure | Diagnose from output, then retry or escalate |
| `timeout` | Command exceeded its time limit | Retry with a higher `timeout` value or a faster command |
| `signal` | Process was terminated by a signal (exit >128 or negative) | Diagnose signal cause; usually an OOM kill or external termination |

### Common expected_nonzero commands

| Command | Exit 1 means |
|---------|-------------|
| `grep`, `rg`, `ag` | Pattern not found — this is success |
| `diff`, `cmp` | Files differ — this is the result, not an error |
| `test`, `[` | Condition is false — this is the result |
| `curl` exit 22 | HTTP 4xx/5xx — server responded, check the body |

`find` returns 0 even when zero files match (no results ≠ failure for `find`), so a nonzero `find` exit is always classified `error`, consistent with `exitTable`.

**Rule**: Always inspect `classification` before deciding whether to retry on a non-zero exit. Retrying an `expected_nonzero` result wastes turns and context.

---

## read_file — error suggestions

When `read_file` fails, the response includes a `suggestion` field with an imperative one-sentence next step:

```json
{"ok": false, "error": "file not found: foo.txt", "suggestion": "verify the path exists with list_dir on the parent, or check for typos"}
```

Follow the suggestion before retrying. Common suggestions map to these error paths:

| Error | Suggestion action |
|-------|------------------|
| file not found | `list_dir` on parent directory |
| not a regular file | Use `list_dir` to enumerate; target a file not a directory |
| file too large | Use `start_line`/`end_line` windowing or `search_files` |
| binary file detected | Use `stat_file` for metadata |
| window past end | Reduce `start_line` to within file length |
| blocked (sensitive path) | Request the specific value from the user instead |
| outside working directory | Supply a path inside the workdir |

---

## read_file — truncation strategy

When a windowed read exceeds the line limit, the `truncate` arg controls what is kept:

| Value | Keeps | Use for |
|-------|-------|---------|
| `head` (default) | First N lines | Paginating top-down with `start_line` |
| `tail` | Last N lines | Logs and command output — the end matters |
| `middle` | Both ends, center elided | Spotting a file's overall shape |
| `none` | Everything (byte cap still applies) | Small files you must see whole |
| `smart` | Cuts at block boundaries | Source files (`.go`/`.rs`/`.ts`/`.js`/`.java`/`.c`/`.cpp`/`.h`/`.hpp`) |

Truncated reads append a hint with the remainder file path so you can continue from where the window ended.

---

## multi_read — batch file reads

Read multiple files in a single call. Returns JSON with `files` (path→content), `errors` (path→error detail), and `truncation` (path→metadata) maps. Partial success: individual failures go to `errors` without failing the entire call. Use when you need 2+ files at once.

```
echo '{"tool":"multi_read","args":{"files":[{"path":"a.go"},{"path":"b.go"}]}}' | jinn
```

Per-file windowing via `start_line`/`end_line`/`tail` on each entry. Max 20 files per call.

---

## list_dir and search_files — entry limits

Both tools enforce a default cap of **500 entries/matches** to prevent context window overflow.

When results are truncated, the response includes:
- `"truncated": true`
- `"total_count": N` — the actual number of available entries
- A hint string: `[TRUNCATED: 500 of 12847 entries. Use 'max_entries' or 'pattern' to narrow.]`

### Parameters

| Tool | Parameter | Default | Cap |
|------|-----------|---------|-----|
| `list_dir` | `max_entries` | 500 | 10000 |
| `search_files` | `max_matches` | 500 | — |

When you receive `truncated: true`, narrow the request using `pattern` (for `list_dir`) or a more specific regex (for `search_files`), or increase the cap explicitly.

---

## find_files — glob-based file search

`find_files` locates files by name pattern. Uses `fd` when available (respects `.gitignore`, fast), falls back to POSIX `find`. Returns matching paths relative to workdir.

### Parameters

| Parameter | Required | Default | Description |
|-----------|----------|---------|-------------|
| `pattern` | **yes** | — | Glob pattern: `*.go`, `**/*.json`, `src/**/*_test.go` |
| `path` | no | `.` | Directory to search in |
| `limit` | no | 1000 | Max results before truncation |

### Response

```json
{
  "files": ["src/main.go", "src/util.go"],
  "truncated": false,
  "total_count": 2,
  "limit_used": 1000,
  "backend": "fd"
}
```

When truncated, a hint is appended: `[TRUNCATED: 1000 of 5421 files. Use a more specific pattern or increase limit.]`

### Excluded directories

Both backends automatically exclude: `.git`, `node_modules`, `vendor`, `__pycache__`, `.cache`, `dist`, `build`.

### Pattern behavior

- Simple patterns (`*.go`) match basenames.
- Patterns with `/` (`src/**/*.test.ts`) match against full paths.
- Use `find_files` for "which files match this name?" and `search_files` for "which files contain this text?"

---

## memory — persistent key/value store

`memory` persists values across jinn invocations. The store is a SQLite database at `~/.config/jinn/memory.db` (override base dir: `JINN_CONFIG_DIR`). Keys are scoped per project (auto-detected from the nearest `.git` ancestor; falls back to the working dir). Writes use WAL journaling with a 5s busy_timeout. A legacy `memory.json` is migrated once into the `"global"` scope then renamed `memory.json.migrated`.

### Actions

| Action | Required args | Returns |
|--------|--------------|---------|
| `save` | `key`, `value` | `"saved: <key>"` |
| `recall` | `key` | stored value string |
| `list` | — | `{"keys": [...], "count": N}` |
| `forget` | `key` | `"forgotten: <key>"` (idempotent — not-found is success) |

All actions accept an optional `scope` arg: omit for the current project, `"global"` for the cross-project bucket, or an absolute path for a specific project.

### Constraints

| Limit | Value |
|-------|-------|
| Key charset | `[a-zA-Z0-9_.-]` |
| Key max length | 128 characters |
| Value max size | 16 KiB |

When a key is not found, `recall` returns `ok: false` with `suggestion: "use action=\"list\" to see available keys"`.

```bash
echo '{"tool":"memory","args":{"action":"save","key":"last_branch","value":"feat/auth"}}' | jinn
echo '{"tool":"memory","args":{"action":"recall","key":"last_branch"}}' | jinn
echo '{"tool":"memory","args":{"action":"list"}}' | jinn
echo '{"tool":"memory","args":{"action":"forget","key":"last_branch"}}' | jinn
```

---

## lsp_query — language server semantic queries

`lsp_query` auto-selects a language server from the file extension and runs one semantic query. The server is started, used, and torn down in a single call (10s timeout).

### Supported file extensions

| Extension | Server binary |
|-----------|--------------|
| `.go` | `gopls` |
| `.rs` | `rust-analyzer` |
| `.py` | `pylsp` |
| `.ts`, `.tsx`, `.js`, `.jsx` | `typescript-language-server` |
| `.c`, `.h`, `.cpp`, `.cc`, `.cxx`, `.hpp` | `clangd` |
| `.java` | `jdtls` |
| `.lua` | `lua-language-server` |
| `.zig` | `zls` |

If the binary is not on `PATH`, the response includes a `suggestion` with the install command.

### Actions

| Action | Required args | Returns |
|--------|--------------|---------|
| `definition` | `path`, `line`, `character` | `file:line:col` of the definition |
| `references` | `path`, `line`, `character` | One `file:line:col` per reference (capped at 100) |
| `hover` | `path`, `line`, `character` | Documentation / type info string |
| `symbols` | `path` | `Kind   Name   (line:col)` table |
| `diagnostics` | `path` | Pull diagnostics for the file as `file:line:col severity source/code: message` |
| `rename` | `path`, `line`, `character`, `new_name` | Preview of rename edits across files — does not write |

`line` and `character` are 1-based. `symbols` and `diagnostics` do not require a position. Pass `symbol` (the identifier name) instead of `character` to let jinn resolve the column automatically.

```bash
echo '{"tool":"lsp_query","args":{"action":"hover","path":"main.go","line":12,"character":5}}' | jinn
echo '{"tool":"lsp_query","args":{"action":"symbols","path":"internal/jinn/engine.go"}}' | jinn
echo '{"tool":"lsp_query","args":{"action":"diagnostics","path":"main.go"}}' | jinn
```

---

## Response envelope — extended fields

Every jinn response may include these fields beyond `ok`, `result`, and `error`:

| Field | Set by | Values |
|-------|--------|--------|
| `suggestion` | Any tool on error | Free-form one-sentence next-step hint |
| `classification` | `run_shell` always | `success`, `expected_nonzero`, `error`, `timeout`, `signal` |
| `risk` | `run_shell` always | `safe`, `caution`, `dangerous` |

`suggestion` appears on structured errors to tell the agent exactly what to try next. Always read it before retrying.

---

## edit_file / multi_edit — disambiguation on multi-match

When `old_text` matches multiple locations, the error includes line numbers of all matches (up to 10):

```
old_text matches 3 locations (lines: 12, 47, 89) — must be unique. Add surrounding context to disambiguate
```

To fix: extend `old_text` to include a few surrounding lines that are unique to the target location. No separate `search_files` call is needed — the line numbers tell you where the matches are.

---

## search_replace — regex replace across many files

`search_replace` applies one regex substitution across explicit files, globs, or directories in a single call. Use it for repo-wide renames instead of looping `edit_file`.

| Parameter | Required | Description |
|-----------|----------|-------------|
| `pattern` | yes | Regex to match |
| `replacement` | yes | Replacement text; `$1`, `$2`, … expand capture groups. Empty string deletes matches. |
| `files` | yes | Path, glob, directory, or array of them — resolves to at most 50 files |
| `include` | no | Glob filter applied after `files` expansion (e.g. `"*.go"`) |
| `case_insensitive` | no | Case-insensitive matching (default false) |
| `multiline` | no | `^`/`$` match line boundaries (default true) |
| `dry_run` | no | Preview per-file diffs and match counts without writing |

Every file is validated before any write; writes are per-file atomic. Binary files are skipped with structured per-file errors. Always run with `dry_run: true` first on a wide pattern to confirm the match count before committing.

```json
{"tool": "search_replace", "args": {"pattern": "oldName", "replacement": "newName", "files": "**/*.go", "dry_run": true}}
```

---

## apply_patch — Codex-style patch format

Applies a multi-file patch in Codex format (`*** Begin Patch ... *** End Patch`). Supports three operations:

| Operation | Syntax | Effect |
|-----------|--------|--------|
| Add file | `*** Add File: path` | Create new file. Lines prefixed with `+`. |
| Delete file | `*** Delete File: path` | Delete existing file. |
| Update file | `*** Update File: path` | In-place edits via hunks. |

### Update hunks

Each hunk starts with an optional `@@ context` marker (a line that must be found in the file for positioning), followed by lines prefixed with ` ` (context), `-` (remove), or `+` (add):

```
*** Begin Patch
*** Update File: main.go
@@ func main() {
 func main() {
-	fmt.Println("old")
+	fmt.Println("new")
 }
*** End Patch
```

### Fuzzy matching

When exact line matching fails, `apply_patch` applies progressive fuzzy matching (rstrip → trim → Unicode-normalized) to locate hunks. This handles whitespace differences and smart quotes automatically.

### Atomicity

All operations are validated in a preflight pass before any file is mutated. If any operation fails validation (e.g., context not found, file doesn't exist), the entire patch is rejected. Undo snapshots are recorded for each mutated file.

### Parameters

| Parameter | Required | Description |
|-----------|----------|-------------|
| `patch` | yes | Codex-style patch payload |
| `dry_run` | no | Preview without writing (default: false) |

### When to use

- Multi-file changes that should be validated together before per-file atomic writes (create + update + delete in one call)
- Hunk-based edits where context lines are more natural than old_text/new_text pairs
- Interoperability with tools that emit Codex-style patches
