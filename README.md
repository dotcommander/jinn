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

---

## Toolset

`jinn` exposes 13 specialized tools for coding agents:

| Tool | Description |
| :--- | :--- |
| `read_file` | Read windowed chunks of a file with line numbers (max 50MB). PDFs return a structured error; images return a base64 data URI. |
| `write_file` | Atomic full-file write. Creates parent directories automatically. |
| `edit_file` | Targeted text replacement. Handles fuzzy whitespace/quotes, CRLF/BOM preservation, `dry_run` diff preview. |
| `multi_edit` | Apply batch edits across multiple files atomically (2-phase commit). |
| `search_files` | Fast grep/regex search with glob filtering and context lines. |
| `run_shell` | Controlled bash execution with risk classification. Dangerous commands blocked unless `force: true`. |
| `stat_file` | Get metadata (size, lines, mtime) without reading contents. |
| `list_dir` | Recursive directory tree exploration (skips hidden files). |
| `detect_project` | Auto-detect language, frameworks, and build/test/lint commands. |
| `checksum_tree` | Compute SHA-256 hashes for a tree to verify workspace integrity. |
| `list_tools` | Programmatic access to the tool schema from within the protocol. |
| `memory` | Persistent key/value store across sessions. Actions: `save`, `recall`, `list`, `forget`. |
| `lsp_query` | Query a language server for `definition`, `references`, `hover`, or `symbols`. |

---

## Security Model

Security is not an opt-in feature; it is the core of the engine.

1. **Path Confinement:** Every path is resolved via `EvalSymlinks` and checked against the working directory. Traversal attempts (e.g., `../../etc/passwd`) are hard-blocked.
2. **Sensitive Blocklist:** Direct access to `.git`, `.ssh`, `.aws`, `.env`, and `.gnupg` is always denied.
3. **TOCTOU Protection:** `jinn` records file `mtime` during `read_file`. If a file is modified externally before an agent calls `write_file` or `edit_file`, the update is rejected.
4. **Environment Scrubbing:** `run_shell` runs with a minimal allowlist of environment variables (e.g., `PATH`, `LANG`, `TMPDIR`). Your `STRIPE_API_KEY` stays safe.
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

---

## Contributing

`jinn` aims for zero dependencies and maximum reliability. Please ensure `go test -race ./...` passes before submitting PRs.

## License

MIT
