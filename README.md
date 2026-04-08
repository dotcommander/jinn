# jinn

Sandboxed tool executor for AI coding agents. Single binary, zero dependencies, stdlib only.

jinn exposes 8 file/shell tools via a one-shot JSON protocol compatible with OpenAI function calling. An agent sends a JSON request on stdin and gets a JSON response on stdout. No server, no daemon, no config.

## Install

```bash
go install github.com/dotcommander/jinn@latest
```

## Usage

```bash
# Emit tool definitions (OpenAI function-calling format)
jinn --schema

# Execute a tool
echo '{"tool":"read_file","args":{"path":"main.go"}}' | jinn
# → {"ok":true,"result":"1\tpackage main\n..."}

echo '{"tool":"run_shell","args":{"command":"go version"}}' | jinn
# → {"ok":true,"result":"[exit: 0]\ngo version go1.26.2 ..."}
```

## Protocol

**Request** (stdin):
```json
{"tool": "<name>", "args": {}}
```

**Response** (stdout):
```json
{"ok": true, "result": "..."}
{"ok": false, "error": "..."}
```

One invocation, one request, one response. The calling agent handles all user interaction.

## Tools

| Tool | Description |
|------|-------------|
| `run_shell` | Execute bash with timeout (default 30s, max 300s) and optional `dry_run` |
| `read_file` | Read with line numbers, `start_line`/`end_line` windowing, 50 MB limit, binary detection |
| `write_file` | Atomic write via temp+rename, auto-creates parent dirs |
| `edit_file` | Replace text — exact match first, fuzzy fallback (smart quotes, whitespace). Preserves CRLF and BOM. Atomic |
| `multi_edit` | Batch edits across files — validates all first, applies atomically |
| `search_files` | Grep with regex, glob filter, context lines, case-insensitive option |
| `stat_file` | File metadata (size, lines, mtime, type) without reading content |
| `list_dir` | Recursive directory listing with depth control, hidden files excluded |

## Security

All file operations are confined to the working directory:

- **Path confinement** — every path is resolved and checked against CWD. `..` traversal and symlink escapes are rejected.
- **Sensitive path blocking** — `.git/`, `.ssh/`, `.aws/`, `.gnupg/`, and `.env*` are always blocked.
- **TOCTOU detection** — file mtime is recorded on read. Writes are rejected if the file changed since last read.
- **Atomic writes** — all mutations use temp file + rename. No partial writes on crash or interrupt.
- **No escape hatch** — sandboxing is always on. There is no flag to disable path confinement.

## Text Normalization

Edit operations handle encoding differences between LLMs and files:

- **Fuzzy matching** — if exact match fails, normalizes smart quotes (`""''` → `"'`), Unicode dashes/spaces to ASCII, and strips trailing whitespace, then retries. Exact match always preferred.
- **CRLF preservation** — detects original line endings, normalizes to LF for matching, restores after edit.
- **BOM handling** — strips UTF-8 BOM before matching (models never produce BOMs), restores after edit.

## Output Pipeline

jinn minimizes token consumption for the calling agent:

1. **Repeated line collapse** — runs of 3+ identical lines become `[... N identical lines collapsed ...]`
2. **Bounded writer** — output capped at 1 MB; when exceeded, full output spills to a temp file (path included in response)
3. **Tail truncation** — shell output keeps the last N lines (where errors and results live) with a metadata header

## Integration

Any agent that speaks OpenAI function calling can use jinn as a subprocess:

```python
import subprocess, json

def call_jinn(tool, args):
    req = json.dumps({"tool": tool, "args": args})
    result = subprocess.run(["jinn"], input=req, capture_output=True, text=True)
    return json.loads(result.stdout)

call_jinn("read_file", {"path": "main.go"})
call_jinn("edit_file", {"path": "config.yaml", "old_text": "timeout: 30", "new_text": "timeout: 60"})
call_jinn("run_shell", {"command": "go test ./..."})
```

Or in a shell-based agent loop:

```bash
SCHEMA=$(jinn --schema)
# ... send $SCHEMA as tools to LLM API ...
RESULT=$(echo "$TOOL_CALL_JSON" | jinn)
# ... feed $RESULT back as tool message ...
```

## Design Decisions

- **Zero dependencies** — `go.mod` has no `require` block. Stdlib only. No supply chain risk, trivial builds.
- **Single file** — everything in `main.go` (~860 lines). No packages to navigate.
- **One-shot** — no persistent server, no state between invocations. The calling agent manages session state.
- **Security by default** — not opt-in. Every path goes through confinement checks before any I/O.

## License

MIT
