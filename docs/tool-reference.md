# Tool Reference

jinn exposes 13 tools through a JSON-over-stdin/stdout protocol. You call them by piping a request object:

```bash
echo '{"tool":"read_file","args":{"path":"main.go"}}' | jinn
```

Every tool returns `{"ok": true, "result": "..."}` on success or `{"ok": false, "error": "..."}` on failure.

## Response Envelope

The full response type includes optional fields that carry structured metadata:

| Field | Type | Description |
|-------|------|-------------|
| `ok` | bool | `true` on success, `false` on error |
| `result` | string | Tool output (present when `ok: true`) |
| `error` | string | Error message (present when `ok: false`) |
| `suggestion` | string | One-sentence next-step hint on structured errors |
| `classification` | string | Exit-code class set by `run_shell`: `success`, `expected_nonzero`, `error`, `timeout`, `signal` |
| `risk` | string | Pre-execution risk set by `run_shell`: `safe`, `caution`, `dangerous` |

`suggestion` is present on errors from any tool when jinn can offer a specific recovery action. Always read it before retrying.

---

## File Operations

These tools read, write, and edit files. All file paths are confined to the working directory. See [Security](security.md) for details on path confinement and TOCTOU protection.

### read_file

Read a file with line-numbered output.

```bash
echo '{"tool":"read_file","args":{"path":"main.go"}}' | jinn
```

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `path` | string | Yes | -- | File path relative to working directory |
| `start_line` | int | No | `1` | First line to return |
| `end_line` | int | No | `start_line + 199` | Last line to return |
| `tail` | int | No | `0` (disabled) | Return last N lines. Overrides `start_line`/`end_line` |

**Notes:**

- Files larger than 50 MB are rejected.
- **PDF files** (`.pdf`) return `ok: false` with `suggestion: "convert the PDF to text first (pdftotext, pdftk, or a cloud OCR service) and read the text file"`. Content is never returned.
- **Image files** (`.png`, `.jpg`, `.jpeg`, `.gif`, `.webp`, `.svg`, `.bmp`) return a `data:<mime>;base64,<payload>` URI in `result`. MIME normalization: `.jpg` → `image/jpeg`, `.svg` → `image/svg+xml`. Pass the result directly to a vision model.
- Binary files (null byte in first 512 bytes) return `[binary file: N bytes — use checksum_tree for integrity or skip content reads]` as a success result (not an error).
- Output is line-numbered with right-justified width.
- When the file has more lines than the requested window, jinn appends a hint: `[file has N lines; showing X-Y. Use start_line=Z to continue]`.
- jinn records the file's modification time for TOCTOU protection. See [Security: TOCTOU](security.md#toctou-protection).

Read lines 10 through 20:

```bash
echo '{"tool":"read_file","args":{"path":"main.go","start_line":10,"end_line":20}}' | jinn
```

Read the last 5 lines:

```bash
echo '{"tool":"read_file","args":{"path":"main.go","tail":5}}' | jinn
```

### write_file

Write content to a file atomically.

```bash
echo '{"tool":"write_file","args":{"path":"hello.txt","content":"Hello, world.\n"}}' | jinn
```

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `path` | string | Yes | -- | File path relative to working directory |
| `content` | string | Yes | -- | Content to write |
| `dry_run` | bool | No | `false` | Preview the write without modifying the file |

**Notes:**

- Writes are atomic: jinn writes to a hidden temp file, syncs to disk, then renames it into place. See [Security: Atomic Writes](security.md#atomic-writes).
- Parent directories are created automatically.
- If the file already exists, jinn preserves its permissions.
- If you previously read the file, jinn checks that it hasn't changed since. See [Security: TOCTOU](security.md#toctou-protection).
- `dry_run` on an existing file returns a unified diff. On a new file, it returns `[dry-run] would create path (N bytes)`.

Preview a write without applying it:

```bash
echo '{"tool":"write_file","args":{"path":"hello.txt","content":"new content\n","dry_run":true}}' | jinn
```

### edit_file

Replace an exact text match in a file.

```bash
echo '{"tool":"edit_file","args":{"path":"main.go","old_text":"fmt.Println","new_text":"log.Println"}}' | jinn
```

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `path` | string | Yes | -- | File path relative to working directory |
| `old_text` | string | Yes | -- | Text to find (must be unique in the file) |
| `new_text` | string | Yes | -- | Replacement text |
| `dry_run` | bool | No | `false` | Preview the edit as a unified diff |
| `fuzzy_indent` | bool | No | `false` | Detect indentation at match site, re-indent `new_text` to match |
| `show_context` | int | No | `0` | Return N surrounding lines around the edit with `*` markers on changed lines |

**Notes:**

- `old_text` must match exactly once. Zero matches or multiple matches both produce an error.
- If exact match fails, jinn tries fuzzy matching (normalizes whitespace, smart quotes, Unicode dashes). Fuzzy match is used only when it produces exactly one candidate.
- jinn preserves BOM markers and CRLF line endings through the edit.
- When both exact and fuzzy fail, the error message includes the nearest line by character overlap to help you locate the right text.
- `dry_run` returns a unified diff with 3 lines of context.

Edit with context lines:

```bash
echo '{"tool":"edit_file","args":{"path":"main.go","old_text":"old","new_text":"new","show_context":2}}' | jinn
```

Preview an edit:

```bash
echo '{"tool":"edit_file","args":{"path":"config.yaml","old_text":"port: 8080","new_text":"port: 9090","dry_run":true}}' | jinn
```

### multi_edit

Apply multiple edits across files atomically.

```bash
echo '{"tool":"multi_edit","args":{"edits":[{"path":"a.go","old_text":"foo","new_text":"bar"},{"path":"b.go","old_text":"baz","new_text":"qux"}]}}' | jinn
```

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `edits` | array | Yes | -- | Array of edit objects (see below) |

Each edit object:

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `path` | string | Yes | -- | File path relative to working directory |
| `old_text` | string | Yes | -- | Text to find (must be unique in the file) |
| `new_text` | string | Yes | -- | Replacement text |
| `fuzzy_indent` | bool | No | `false` | Detect indentation, re-indent `new_text` |
| `show_context` | int | No | `0` | Return N surrounding lines with markers |

**Notes:**

- **Two-phase commit.** jinn validates every edit (path security, TOCTOU, match uniqueness) before applying any. If any edit fails validation, zero edits are applied.
- Each edit uses the same matching and normalization rules as [`edit_file`](#edit_file).
- Multiple edits to the same file are applied sequentially in array order.

---

## Search and Discovery

### search_files

Search file contents with regex.

```bash
echo '{"tool":"search_files","args":{"pattern":"func main"}}' | jinn
```

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `pattern` | string | Yes | -- | Regex pattern to search for |
| `path` | string | No | `"."` | Directory to search in |
| `format` | string | No | `"text"` | Output format: `text`, `json`, or `filenames` |
| `include` | string | No | -- | Glob filter on filenames (e.g., `"*.go"`) |
| `max_results` | int | No | -- | Cap on number of results |
| `context_lines` | int | No | -- | Surrounding lines to include per match |
| `case_insensitive` | bool | No | `false` | Case-insensitive matching |

**Notes:**

- jinn uses `rg` (ripgrep) if available, otherwise falls back to `grep -r -n`.
- These directories are always excluded: `.git`, `node_modules`, `vendor`, `__pycache__`, `.cache`, `dist`, `build`.
- Invalid regex patterns produce an error before any search runs.
- Output limits: 200 characters per line, 100 lines for `text` format, 100 results for `json` format.

Structured results for programmatic use:

```bash
echo '{"tool":"search_files","args":{"pattern":"func \\w+Handler","format":"json","include":"*.go"}}' | jinn
```

`format: "json"` returns an array of objects with `file`, `line`, `column`, `text`, and optional `context_before`/`context_after` fields.

List files with match counts:

```bash
echo '{"tool":"search_files","args":{"pattern":"TODO","format":"filenames"}}' | jinn
```

### stat_file

Get file metadata without reading content.

```bash
echo '{"tool":"stat_file","args":{"path":"main.go"}}' | jinn
```

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `path` | string | Yes | -- | File path relative to working directory |

**Returns:**

| Field | Description |
|-------|-------------|
| `path` | Resolved file path |
| `type` | `file`, `directory`, or `special` |
| `size` | Size in bytes |
| `lines` | Line count (regular files under 50 MB only) |
| `modified` | Modification time as RFC 3339 |

### list_dir

List files in a directory tree.

```bash
echo '{"tool":"list_dir","args":{"path":".","depth":2}}' | jinn
```

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `path` | string | No | `"."` | Directory to list |
| `depth` | int | No | `3` | Maximum recursion depth (clamped to 1--10) |

**Notes:**

- Hidden files and directories (names starting with `.`) are excluded.
- Output is sorted alphabetically.

---

## Shell Execution

### run_shell

Run a bash command with a timeout.

```bash
echo '{"tool":"run_shell","args":{"command":"go test ./..."}}' | jinn
```

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `command` | string | Yes | -- | Bash command to execute |
| `timeout` | int | No | `30` | Timeout in seconds (max 300) |
| `dry_run` | bool | No | `false` | Print the command without executing it |
| `force` | bool | No | `false` | Execute even when risk classification is `dangerous`. See [Security: Risk Classifier](security.md#command-risk-classifier). |

**Notes:**

- The command runs via `bash -c`, wrapped with `timeout` (or `gtimeout` on macOS) to enforce the deadline.
- Exit code 124 means the command was killed by the timeout.
- Output format: `[exit: N]\n<output>\n[classification: <class> — <reason>]`.
- Every response includes `risk` and `classification` fields in the envelope.
- Dangerous commands (e.g., `rm -rf`, `dd`, `sudo`) are blocked and return `ok: false` with a `suggestion` unless `force: true` is passed.
- The shell environment is scrubbed to a fixed allowlist. See [Security: Shell Environment](security.md#shell-environment-scrubbing).
- Output that exceeds 1 MB spills to a temp file. Long-running output is truncated. See [Security: Output Bounds](security.md#output-bounds).

Run with a longer timeout:

```bash
echo '{"tool":"run_shell","args":{"command":"go build ./...","timeout":120}}' | jinn
```

Preview without executing:

```bash
echo '{"tool":"run_shell","args":{"command":"rm -rf /tmp/test","dry_run":true}}' | jinn
```

---

## Meta Tools

### list_tools

Get the JSON schema for all tools jinn exposes.

```bash
echo '{"tool":"list_tools","args":{}}' | jinn
```

**Parameters:** none.

This returns the same content as `jinn --schema`, but through the protocol. Use it when the calling agent needs to discover available tools at runtime.

### checksum_tree

Compute SHA-256 hashes for every file in a tree.

```bash
echo '{"tool":"checksum_tree","args":{"path":"."}}' | jinn
```

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `path` | string | No | `"."` | Root directory to walk |
| `pattern` | string | No | -- | Glob filter on filenames (e.g., `"*.go"`) |

**Notes:**

- Returns a JSON object mapping relative paths to hex digests: `{"relative/path": "sha256hex", ...}`.
- Skips: `.git`, `node_modules`, `vendor`, `__pycache__`, `.cache`, `dist`, `build`.
- Skips symlinks and non-regular files.
- Individual files larger than 50 MB are skipped.

Filter to Go files only:

```bash
echo '{"tool":"checksum_tree","args":{"path":".","pattern":"*.go"}}' | jinn
```

### detect_project

Detect language, framework, and build commands from project config files.

```bash
echo '{"tool":"detect_project","args":{"path":"."}}' | jinn
```

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `path` | string | No | `"."` | Project root to probe |

**Returns:**

| Field | Description |
|-------|-------------|
| `languages` | Detected languages (e.g., `["Go", "TypeScript"]`) |
| `build_tool` | Build command (e.g., `"go build ./..."`) |
| `test_command` | Test command (e.g., `"go test ./..."`) |
| `linter` | Lint command (e.g., `"golangci-lint run"`) |
| `config_files` | Config files found (e.g., `["go.mod", ".golangci.yml"]`) |
| `frameworks` | Detected frameworks (e.g., `["Next.js"]`) |

**Notes:**

- Probes for: `go.mod`, `package.json`, `bun.lockb`, `Cargo.toml`, `pyproject.toml`, `setup.py`, `requirements.txt`, `composer.json`, `Makefile`, `Taskfile.yml`.
- Secondary detection: `tsconfig.json` upgrades JS to TypeScript. `package.json` scripts override build/test/lint commands. `next.config.js` or `next.config.mjs` triggers Next.js detection.

### memory

Persist key/value pairs across jinn invocations.

```bash
echo '{"tool":"memory","args":{"action":"save","key":"project.notes","value":"auth service uses JWT RS256"}}' | jinn
```

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `action` | string | Yes | -- | `save`, `recall`, `list`, or `forget` |
| `key` | string | Depends | -- | Key name. Required for `save`, `recall`, `forget`. Charset: `[a-zA-Z0-9_.-]`, max 128 chars. |
| `value` | string | For `save` | -- | Value to store. Max 16 KiB. |

**Returns by action:**

| Action | Success result |
|--------|---------------|
| `save` | `"saved: <key>"` |
| `recall` | The stored value string |
| `list` | `{"keys": [...], "count": N}` |
| `forget` | `"forgotten: <key>"` (idempotent — not found is success) |

**Notes:**

- Stored at `~/.config/jinn/memory.json`. Override base dir with `JINN_CONFIG_DIR` env var.
- Total store is capped at 1 MiB. When saving would exceed this, `ok: false` is returned with `suggestion: "use action=\"forget\" on old keys to free space"`.
- Writes are atomic via temp+rename with mode `0600`. Directory created with `0700`.
- `recall` on a missing key returns `ok: false` with `suggestion: "use action=\"list\" to see available keys"`.

Save a value:

```bash
echo '{"tool":"memory","args":{"action":"save","key":"db.host","value":"localhost"}}' | jinn
```

List all keys:

```bash
echo '{"tool":"memory","args":{"action":"list"}}' | jinn
```

Forget a key:

```bash
echo '{"tool":"memory","args":{"action":"forget","key":"db.host"}}' | jinn
```

---

## Language Server

### lsp_query

Query a language server for semantic information at a source location.

```bash
echo '{"tool":"lsp_query","args":{"action":"hover","path":"main.go","line":12,"character":5}}' | jinn
```

**Parameters:**

| Name | Type | Required | Default | Description |
|------|------|----------|---------|-------------|
| `action` | string | Yes | -- | `definition`, `references`, `hover`, or `symbols` |
| `path` | string | Yes | -- | File path relative to working directory |
| `line` | int | For non-`symbols` | -- | 1-based line number of the symbol |
| `character` | int | For non-`symbols` | -- | 1-based character offset within the line |

**Supported extensions and servers:**

| Extension | Server binary | Install hint |
|-----------|--------------|-------------|
| `.go` | `gopls` | `go install golang.org/x/tools/gopls@latest` |
| `.rs` | `rust-analyzer` | `rustup component add rust-analyzer` |
| `.py` | `pylsp` | `pip install python-lsp-server` |
| `.ts`, `.tsx`, `.js`, `.jsx` | `typescript-language-server` | `npm install -g typescript-language-server typescript` |

**Returns by action:**

| Action | Result format |
|--------|--------------|
| `definition` | `file:line:col` of the definition site |
| `references` | One `file:line:col` per reference, up to 100. Truncation noted with `[truncated: showing N of M]`. |
| `hover` | Documentation / type signature string from the server |
| `symbols` | `Kind   Name   (line:col)` table for every symbol in the file |

**Notes:**

- The language server is started, queried, and torn down within a single call. There is no persistent daemon.
- Hard timeout: 10 seconds per query. Slow server startups may cause timeouts on cold runs.
- If the server binary is not on `PATH`, `ok: false` is returned with a `suggestion` containing the install command.
- Path must be inside the working directory (normal path security applies).

Get definition:

```bash
echo '{"tool":"lsp_query","args":{"action":"definition","path":"cmd/jinn/main.go","line":15,"character":12}}' | jinn
```

List symbols:

```bash
echo '{"tool":"lsp_query","args":{"action":"symbols","path":"internal/jinn/engine.go"}}' | jinn
```
