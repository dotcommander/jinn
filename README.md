# jinn

```bash
go install github.com/dotcommander/jinn/cmd/jinn@latest
echo '{"tool":"read_file","args":{"path":"go.mod"}}' | jinn
echo '{"tool":"run_shell","args":{"command":"go test ./..."}}' | jinn --shell-mode=sandboxed
```

`jinn` is a single-binary tool executor for AI coding agents. It reads one JSON
request on stdin, runs one tool inside the current workspace, and writes one JSON
response on stdout.

Use it when you need a small, deterministic tool layer for an agent loop,
subagent, hook, CI job, or harness integration.

- **No daemon:** spawn `jinn` once per tool call.
- **Single-binary distribution:** no external runtime services; embedded state
  uses pure-Go SQLite.
- **Workspace confinement:** paths stay inside the working directory.
- **Safer mutation:** file writes use atomic replacement and undo snapshots.
- **Shell is opt-in:** the default `disabled` mode omits `run_shell`. Choose
  `sandboxed` for OS confinement or `unsafe` only for explicit compatibility.

## Install

```bash
go install github.com/dotcommander/jinn/cmd/jinn@latest
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
echo '{"tool":"run_shell","args":{"command":"go test ./..."}}' | jinn --shell-mode=sandboxed
```

Inspect a command without running it:

```bash
echo '{"tool":"run_shell","args":{"command":"rm -rf build","dry_run":true}}' | jinn --shell-mode=unsafe
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

`jinn --mcp` speaks MCP 2026-07-28 through the official Go SDK and exposes one
MCP tool, `jinn_route`. It recommends matching jinn tools for a task and can
return lean schemas for only those tools. It does not execute filesystem or
shell operations itself. The one-tool surface is intentional: it keeps model
context bounded. Discovery exposes 18 tools by default and 19 with an explicit
`sandboxed` or `unsafe` shell mode.

For an opt-in execution surface that is still safe for review and discovery
work, start `jinn --mcp-profile=read-only --mcp`. It keeps `jinn_route` and adds
`jinn_call`, whose schema enum is generated from the canonical read-only tool
allowlist. File/state mutation, shell execution, `memory`, and `undo` are not
available, and the profile forces shell execution off regardless of the
`--shell-mode` flag. The default `--mcp` profile remains route-only.

Current MCP requests use `server/discover`, `tools/list`, and `tools/call` with
`_meta.io.modelcontextprotocol/protocolVersion` set to `2026-07-28` plus
`_meta.io.modelcontextprotocol/clientCapabilities`. Existing clients that send
the older `initialize` handshake are routed through a compatibility path; that
legacy path remains route-only.
The deterministic black-box checks are documented in
[docs/mcp-smoke-test.md](docs/mcp-smoke-test.md).

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

`jinn` exposes 18 tools by default, or 19 with an enabled shell mode:

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
| `find_files` | Native bounded glob traversal. Basename patterns match anywhere; slash patterns match normalized relative paths; `**` spans segments. It does not interpret `.gitignore`. |
| `diff_files` | Unified diff between two files with `is_identical` and `first_changed_line` metadata. |
| `detect_project` | Auto-detect language, frameworks, and build/test/lint commands. |
| `list_tools` | Programmatic tool capability metadata; can include the compact schema on request. |
| `memory` | Persistent, project-scoped key/value store across sessions. Actions: `save`, `recall`, `list`, `forget`. |
| `undo` | Browse, preview, and restore file snapshots captured automatically before every mutation. Existing files over 5 MiB are rejected before mutation so the undo guarantee is preserved. |
| `lsp_query` | Query a language server for `definition`, `references`, `hover`, `symbols`, `diagnostics`, or `rename`. |

---

## Security Model

Security is not an opt-in feature; it is the core of the engine.

1. **Rooted File Access:** File operations use a workspace `os.Root`; traversal attempts and special-file reads are blocked. Recursive tools never follow directory symlinks.
2. **Sensitive Blocklist:** Direct access to `.git`, `.ssh`, `.aws`, `.env`, and `.gnupg` is always denied.
3. **Mutation Preconditions:** Existing targets require `if_checksum`; creation requires `if_absent:true`. Cross-process locks cover validation, durable snapshot, and atomic replacement.
4. **Shell Modes:** Shell is disabled by default. Sandboxed mode denies network and user configuration; unsafe mode is explicitly unconfined.
5. **Risk Classifier:** `force` overrides only the advisory dangerous-command block; it never enables shell execution or removes confinement.
6. **Output Caps:** Shell capture uses 1 MiB memory and at most 16 MiB spill before the process group is killed with `resource_limit`.

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

`jinn` prioritizes single-binary distribution and maximum reliability. Run
`just test` before submitting PRs; it is the race-enabled gate for both the
root module and the nested example module.

## License

MIT
