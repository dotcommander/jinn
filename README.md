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
context bounded. Discovery exposes 20 tools by default and 21 with an explicit
`sandboxed` or `unsafe` shell mode.

MCP hosts that accept only an executable path may launch `jinn` with no
arguments. Jinn detects MCP JSON-RPC on piped stdin while preserving its
existing one-shot `{tool,args}` protocol. For example:

```bash
apfel --mcp /absolute/path/to/jinn "recommend tools for reviewing a Go package"
```

For an opt-in execution surface that is still safe for review and discovery
work, start `jinn --mcp-profile=read-only --mcp`. It keeps `jinn_route` and adds
`jinn_call`, whose schema enum is generated from the canonical read-only tool
allowlist. File/state mutation, shell execution, `memory`, and `undo` are not
available, and the profile forces shell execution off regardless of the
`--shell-mode` flag. The default `--mcp` profile remains route-only.

For Codex or JCode, use the default route-only profile and expose only the
side-effect-free route lookup so the model can discover tools without widening
execution authority. See the
[Codex CLI configuration](docs/harness-integrations.md#codex-mcp-configuration)
and [JCode configuration](docs/harness-integrations.md#jcode-mcp-configuration).

`--mcp-profile=network` keeps this compact routed surface while allowing only
local read-only tools plus `web_fetch` and `web_search`. Network calls leave the
machine and may consume provider quota; `jinn_call` remains read-only,
non-destructive, and open-world.

```bash
jinn mcp list http://127.0.0.1:8788/mcp
jinn mcp inspect --command jinn --arg --mcp jinn_route
jinn mcp call http://127.0.0.1:8788/mcp jinn_route -a need '"read source"'
```

The explorer follows paginated tool lists, has a `30s` default deadline, and
uses explicit subprocess argv (no shell). HTTP tokens default from
`JINN_MCP_HTTP_TOKEN` and are never printed. `JINN_MCP_LOG_LEVEL=off|error|info|debug`
controls capped, redacted JSONL records under `jinn/logs/mcp.jsonl`.

Current MCP requests use `server/discover`, `tools/list`, and `tools/call` with
`_meta.io.modelcontextprotocol/protocolVersion` set to `2026-07-28` plus
`_meta.io.modelcontextprotocol/clientCapabilities`. Existing clients that send
the older `initialize` handshake are routed through a compatibility path; that
legacy path remains route-only.
The deterministic black-box checks are documented in
[docs/mcp-smoke-test.md](docs/mcp-smoke-test.md).

For clients that require Streamable HTTP, start the separate opt-in broker:

```bash
jinn --mcp-http
# POST http://127.0.0.1:8788/mcp
```

The HTTP endpoint is stateless, uses the same route-only profile by default,
and accepts the MCP 2026-07-28 request headers. Use
`jinn --mcp-profile=read-only --mcp-http` for the bounded `jinn_route` plus
`jinn_call` surface. Requests need `Accept: application/json, text/event-stream`,
`Content-Type: application/json`, `MCP-Protocol-Version: 2026-07-28`, and a
matching `Mcp-Method` header. A
`tools/call` request also needs `Mcp-Name` matching its tool name.

Loopback is the default and does not require authentication. To bind beyond
loopback, set both environment-only controls before starting the process:

```bash
JINN_MCP_HTTP_TOKEN="$TOKEN" \
JINN_MCP_HTTP_ORIGINS="https://agent.example.com" \
jinn --mcp-profile=read-only --mcp-http 0.0.0.0:8788
```

The token is sent as `Authorization: Bearer $TOKEN`. Origins are exact
HTTP(S) origins, and the insecure allow-any-origin mode is never enabled.
Use a non-loopback bind only on a trusted network or behind a TLS-terminating
proxy or tunnel: bearer tokens authenticate requests but do not encrypt them.
HTTP does not add the stdio legacy `initialize` compatibility path. Use the
[HTTP smoke test](docs/mcp-smoke-test.md#streamable-http-smoke-test) to verify
the real endpoint.

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

`jinn` exposes 20 tools by default, or 21 with an enabled shell mode:

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

## Public web tools

`web_fetch` and `web_search` are opt-in network tools in the normal one-shot schema. Fetch can project Markdown, headings, or links and paginate the projection with zero-based `start_line`, `max_bytes`, and `max_lines`; text carries the selected content while snake-case metadata reports document fields, counts, truncation, and `next_start_line`. Search returns compact JSON text plus `provider` and `count` metadata. Both block private targets and unsafe redirects by default. They are never executable from `run_plan`, even in a mutating node.

```bash
jinn web fetch --reader=defuddle https://example.com/article
jinn web fetch --json --max-lines=200 https://example.com/article
jinn web search --include-domain=go.dev "Go context cancellation"
```

The fetch CLI supports human output and flat `--json` success shapes. Human output appends an explicit marker when `--max-bytes` or `--max-lines` truncates content; JSON includes the corresponding byte/line totals and truncation fields.

The CLI uses `JINN_WEB_*` configuration only: `JINN_WEB_READER`, `JINN_WEB_SEARCH_PROVIDER`, `JINN_WEB_CACHE_TTL`, `JINN_WEB_CACHE_DIR`, `JINN_WEB_TIMEOUT`, `JINN_WEB_CHROME_PATH`, render limits, endpoint overrides, and `JINN_WEB_ALLOW_PRIVATE_NETWORKS`. Search credentials remain `JINA_API_KEY`, `BRAVE_API_KEY`, and `EXA_API_KEY`. Reader cache files default to `$XDG_CACHE_HOME/jinn/web/urls`, falling back to `~/.cache/jinn/web/urls`; temporary cache writes use the `.jinn-web-cache-` prefix.

MCP stays route-only by default. `--mcp-profile=read-only` exposes local read-only execution only; `--mcp-profile=network` exposes the same two MCP tools but permits only local read-only tools plus `web_fetch` and `web_search`. In the network profile, local read-only tools default to compression while `web_fetch` and `web_search` default to uncompressed output; `compress` explicitly overrides either default.

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
