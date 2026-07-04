# jinn

```bash
go install github.com/dotcommander/jinn@latest
echo '{"tool":"read_file","args":{"path":"go.mod"}}' | jinn
echo '{"tool":"run_shell","args":{"command":"go test ./..."}}' | jinn
```

`jinn` is a single-binary tool executor for AI coding agents. It reads one JSON
request on stdin, runs one tool inside the current workspace, and writes one JSON
response on stdout.

Use it when you need a small, deterministic tool layer for an agent loop,
subagent, hook, CI job, or harness integration.

- **No daemon:** spawn `jinn` once per tool call.
- **No runtime dependencies:** built with the Go standard library.
- **Workspace confinement:** paths stay inside the working directory.
- **Safer mutation:** file writes use atomic replacement and undo snapshots.
- **Shell guardrails:** `run_shell` classifies commands as `safe`, `caution`, or
  `dangerous` before execution.

## Install

```bash
go install github.com/dotcommander/jinn@latest
jinn --version
```

Build from source:

```bash
git clone https://github.com/dotcommander/jinn.git
cd jinn
go build -o jinn ./cmd/jinn/
```

## First calls

Read a file:

```bash
echo '{"tool":"read_file","args":{"path":"go.mod"}}' | jinn
```

Run a command:

```bash
echo '{"tool":"run_shell","args":{"command":"go test ./..."}}' | jinn
```

Inspect a command without running it:

```bash
echo '{"tool":"run_shell","args":{"command":"rm -rf build","dry_run":true}}' | jinn
```

The response is always a JSON envelope:

```json
{"ok": true, "result": "..."}
```

`run_shell` also includes risk and exit-code classifications:

```json
{"ok": true, "result": "[dry-run] would execute: rm -rf build", "risk": "dangerous", "classification": "success"}
```

Errors use the same envelope and often include a next-step hint:

```json
{"ok": false, "error": "file not found: missing.go", "suggestion": "verify the path exists with list_dir on the parent, or check for typos"}
```

## Discover tools

Print the OpenAI-compatible function schema:

```bash
jinn --schema
```

Ask from inside the protocol:

```bash
echo '{"tool":"list_tools","args":{"include_schema":false}}' | jinn
```

Start the browser inspector:

```bash
jinn --inspect 127.0.0.1:8787
```

Use MCP discovery mode:

```json
{
  "mcpServers": {
    "jinn": {
      "command": "jinn",
      "args": ["--mcp"]
    }
  }
}
```

`jinn --mcp` exposes one MCP tool, `jinn_route`. It recommends matching jinn
tools for a task and can return lean schemas for only those tools. It does not
execute filesystem or shell operations itself.

## When to use jinn with another harness

Claude Code, Codex, pi, and similar tools already have native read/edit/shell
surfaces. Add jinn for gaps that are useful as one-shot subprocesses:

- `lsp_query` for definition, references, hover, diagnostics, symbols, and
  rename previews without running an MCP server.
- `run_shell` with `dry_run: true` for permission hooks that need semantic risk
  classification before a shell command runs.
- `memory` for scoped SQLite-backed facts, directives, and lessons with optional
  expiry and garbage collection.
- `apply_patch` to validate and atomically apply Codex-style patches outside the
  Codex harness.
- `list_tools` or `--schema` when a custom loop needs a compact tool surface.

Recipes for Claude Code, Codex CLI, pi, and custom loops:
[docs/harness-integrations.md](docs/harness-integrations.md).

---

## Toolset

`jinn` exposes 19 specialized tools for coding agents:

| Tool | Description |
| :--- | :--- |
| `read_file` | Read windowed chunks of a file with line numbers (max 50MB). Supports `tail`, `line_numbers`, and a `truncate` strategy (`head`/`tail`/`middle`/`none`). Images detected by content; PDFs return a structured error. |
| `multi_read` | Read up to 20 files in one call with per-file windows, partial success, and structured per-file errors. |
| `write_file` | Atomic full-file write. Creates parent directories automatically. |
| `edit_file` | Targeted text replacement. Handles fuzzy whitespace/quotes, CRLF/BOM preservation, `dry_run` diff preview. Rejects empty `old_text` and no-op edits. |
| `multi_edit` | Apply batch edits with validate-first semantics and per-file atomic writes. Detects overlapping regions, rejects empty or no-op entries. |
| `apply_patch` | Apply a Codex-style patch (`*** Begin Patch … *** End Patch`) to create, delete, or update files. Validates all operations first; writes are per-file atomic. |
| `search_files` | Fast grep/regex search with glob filtering, context lines, and a `literal` flag for fixed-string matching. |
| `search_replace` | Regex search-and-replace across explicit files or glob patterns. Supports capture groups, dry runs, and per-file atomic writes. |
| `run_shell` | Controlled bash execution with risk classification. Process-group kill ensures background children are also terminated on timeout. Dangerous commands blocked unless `force: true`. |
| `run_plan` | Execute a condition-gated plan tree of tool/shell operations in one deterministic engine walk. Read-only nodes by default; mutating nodes are risk-gated behind plan- and node-level `force`. |
| `stat_file` | Get metadata (size, lines, mtime) without reading contents. |
| `list_dir` | Recursive directory tree exploration (skips hidden files). Directories suffixed with `/` in output. |
| `find_files` | Find files by glob pattern. Uses `fd` when available (respects `.gitignore`), falls back to POSIX `find`. |
| `diff_files` | Unified diff between two files with `is_identical` and `first_changed_line` metadata. |
| `detect_project` | Auto-detect language, frameworks, and build/test/lint commands. |
| `list_tools` | Programmatic tool capability metadata; can include the compact schema on request. |
| `memory` | Persistent, project-scoped key/value store across sessions. Actions: `save`, `recall`, `list`, `forget`. |
| `undo` | Browse, preview, and restore file snapshots captured automatically before every mutation. |
| `lsp_query` | Query a language server for `definition`, `references`, `hover`, `symbols`, `diagnostics`, or `rename`. |

---

## Security Model

Security is not an opt-in feature; it is the core of the engine.

1. **Path Confinement:** Every path is resolved via `EvalSymlinks` and checked against the working directory. Traversal attempts (e.g., `../../etc/passwd`) are hard-blocked.
2. **Sensitive Blocklist:** Direct access to `.git`, `.ssh`, `.aws`, `.env`, and `.gnupg` is always denied.
3. **TOCTOU Protection:** `jinn` records file `mtime` during `read_file`. If a file is modified externally before an agent calls `write_file` or `edit_file`, the update is rejected.
4. **Environment Scrubbing:** `run_shell` runs with a minimal allowlist of environment variables (e.g., `PATH`, `LANG`, `TMPDIR`). API keys and tokens are not inherited by child commands.
5. **Risk Classifier:** Every `run_shell` command is classified as `safe`, `caution`, or `dangerous`. Dangerous commands (e.g., `rm -rf`, `dd`, `sudo`) are blocked outright unless the caller passes `"force": true`.
6. **Output Caps:** Stdout/stderr is capped at 1MB. Excess output spills to a temp file, and the agent receives a truncated tail.

---

## Integration Example (Python)

```python
import subprocess, json

def call_jinn(tool: str, args: dict):
    req = json.dumps({"tool": tool, "args": args})
    # Run as a subprocess — no daemon or server needed
    proc = subprocess.run(["jinn"], input=req, capture_output=True, text=True)
    return json.loads(proc.stdout)

# Automate a refactor
project = call_jinn("detect_project", {})
if "Go" in project["languages"]:
    call_jinn("run_shell", {"command": "go mod tidy"})
```

For TypeScript, Go, PHP, and shell-script integrations, see [docs/getting-started.md](docs/getting-started.md#integration-patterns).

---

## Contributing

`jinn` aims for zero dependencies and maximum reliability. Please ensure `go test -race ./...` passes before submitting PRs.

## License

MIT
