# AGENTS.md — jinn tool usage guide for AI agents

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
| `find` | No files found or minor warning |
| `curl` exit 22 | HTTP 4xx/5xx — server responded, check the body |

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
| binary file detected | Use `checksum_tree` for integrity checks |
| window past end | Reduce `start_line` to within file length |
| blocked (sensitive path) | Request the specific value from the user instead |
| outside working directory | Supply a path inside the workdir |

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

## memory — persistent key/value store

`memory` persists values across jinn invocations. The store lives at `~/.config/jinn/memory.json` (override: `JINN_CONFIG_DIR` env var). Writes are atomic (temp+rename).

### Actions

| Action | Required args | Returns |
|--------|--------------|---------|
| `save` | `key`, `value` | `"saved: <key>"` |
| `recall` | `key` | stored value string |
| `list` | — | `{"keys": [...], "count": N}` |
| `forget` | `key` | `"forgotten: <key>"` (idempotent — not-found is success) |

### Constraints

| Limit | Value |
|-------|-------|
| Key charset | `[a-zA-Z0-9_.-]` |
| Key max length | 128 characters |
| Value max size | 16 KiB |
| Total file size | 1 MiB |

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

If the binary is not on `PATH`, the response includes a `suggestion` with the install command.

### Actions

| Action | Required args | Returns |
|--------|--------------|---------|
| `definition` | `path`, `line`, `character` | `file:line:col` of the definition |
| `references` | `path`, `line`, `character` | One `file:line:col` per reference (capped at 100) |
| `hover` | `path`, `line`, `character` | Documentation / type info string |
| `symbols` | `path` | `Kind   Name   (line:col)` table |

`line` and `character` are 1-based. `symbols` does not require a position.

```bash
echo '{"tool":"lsp_query","args":{"action":"hover","path":"main.go","line":12,"character":5}}' | jinn
echo '{"tool":"lsp_query","args":{"action":"symbols","path":"internal/jinn/engine.go"}}' | jinn
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
