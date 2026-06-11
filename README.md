# jinn 🧞

**Secure, atomic tool execution for AI coding agents.**

`jinn` is a sandboxed executor that provides AI agents with a safe, standardized interface to the filesystem and shell. It handles the "boring but dangerous" parts of tool execution—path sanitization, race condition prevention, and output management—so you can focus on building your agent.

- **Zero dependencies.** Built with Go's standard library only.
- **Single binary.** Trivial to distribute and run.
- **Security by default.** Path confinement, TOCTOU protection, and a risk classifier that blocks destructive commands.

---

## Why jinn?

Giving an LLM direct access to `os.system()` or `open()` is risky and error-prone. `jinn` solves the common pitfalls of AI tool use:

- **Path Hallucinations:** Prevents agents from traversing into `.git`, `.ssh`, or outside the workspace.
- **Race Conditions:** Uses **TOCTOU protection** (Time-of-Check to Time-of-Use) to ensure an agent doesn't overwrite changes made by a human while the agent was "thinking."
- **Encoding Hell:** Automatically handles CRLF/LF normalization, UTF-8 BOMs, and "fuzzy" whitespace matching when an LLM makes minor formatting mistakes.
- **Token Bloat:** Intelligently collapses repeated output lines and truncates huge files to keep your context window lean.
- **Atomic Reliability:** Every write is atomic (temp file + rename). No partial or corrupted files if the process is interrupted.

---

## Already Using Claude Code, Codex, or Another Harness?

Your harness ships its own read/edit/shell tools — jinn complements them instead of competing:

- **`lsp_query`** — definition/references/hover/diagnostics as a one-shot subprocess; no MCP server to run.
- **Risk classification without execution** — `run_shell` + `dry_run: true` powers semantic permission hooks (e.g., a Claude Code `PreToolUse` guard).
- **`memory` with expiry** — project-scoped SQLite store with `kind`, `pin`, `expires_in`, and `gc`. Memory that doesn't grow forever.
- **`apply_patch`** — validates and atomically applies Codex-format patches (`*** Begin Patch … *** End Patch`) outside the Codex harness.
- **A tool layer for your fleet** — subagents and cheap worker models get the same sandboxed surface your harness gives its main model.

Recipes for Claude Code, Codex CLI, and custom loops: [docs/harness-integrations.md](docs/harness-integrations.md).

---

## Install

```bash
go install github.com/dotcommander/jinn@latest
```

---

## Quick Start

`jinn` follows a simple **one-shot protocol**: one JSON request on `stdin` → one JSON response on `stdout`.

### 1. Get the Schema
Tell your LLM what tools are available. `jinn` emits a full OpenAI-compatible function-calling schema.

```bash
jinn --schema
```

### 2. Execute a Tool
Pipe a JSON request to `jinn`.

```bash
# Read a file
echo '{"tool":"read_file","args":{"path":"main.go"}}' | jinn

# Run a shell command (scrubbed environment, 30s timeout)
echo '{"tool":"run_shell","args":{"command":"go test ./..."}}' | jinn
```

### 3. Open the Inspector
Start a local browser UI for trying tool requests and viewing JSON responses.

```bash
jinn --inspect 127.0.0.1:8787
```

Then open `http://127.0.0.1:8787`. The inspector loads tool definitions from the live schema and runs requests through the same engine as the stdin protocol.

### 4. Use MCP Discovery Mode
`jinn --mcp` starts a stdio MCP server that exposes exactly one tool: `jinn_route`.
It is a discovery broker, not a full MCP adapter. `jinn_route` recommends the
existing jinn tools for a task -- deterministically, with no LLM and no network --
and can optionally include lean schemas only for the matched tools, keeping MCP
context small.

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

---

## Toolset

`jinn` exposes 18 specialized tools for coding agents:

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
