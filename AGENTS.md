# AGENTS.md — jinn tool usage guide for AI agents

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

## edit_file / multi_edit — disambiguation on multi-match

When `old_text` matches multiple locations, the error includes line numbers of all matches (up to 10):

```
old_text matches 3 locations (lines: 12, 47, 89) — must be unique. Add surrounding context to disambiguate
```

To fix: extend `old_text` to include a few surrounding lines that are unique to the target location. No separate `search_files` call is needed — the line numbers tell you where the matches are.
