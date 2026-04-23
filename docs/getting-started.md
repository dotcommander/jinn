# Getting Started

jinn is a sandboxed tool executor for AI coding agents. You pipe it a JSON request, it runs one tool, and it prints a JSON response. Single binary, zero dependencies.

## Installation

Install with Go:

```bash
go install github.com/dotcommander/jinn@latest
```

Or build from source:

```bash
git clone https://github.com/dotcommander/jinn.git
cd jinn
go build -o jinn ./cmd/jinn/
```

## Verify It Works

```bash
jinn --version
```

You'll see the version derived from your VCS tag or module path. To dump all tool definitions as JSON:

```bash
jinn --schema
```

This prints the full [OpenAI function-calling schema](https://platform.openai.com/docs/guides/function-calling) for every tool jinn exposes. You can also get this schema at runtime via the [`list_tools`](tool-reference.md#list_tools) tool.

## Your First Tool Call

jinn reads a single JSON object from stdin and writes a single JSON object to stdout. Read a file:

```bash
echo '{"tool":"read_file","args":{"path":"go.mod"}}' | jinn
```

You'll get a response like:

```json
{
  "ok": true,
  "result": "     1\tmodule github.com/dotcommander/jinn\n     2\t\n     3\tgo 1.22\n"
}
```

If something goes wrong, `ok` is `false` and the response contains an `error` field:

```json
{
  "ok": false,
  "error": "file not found: nonexistent.txt"
}
```

## The Protocol

Every request follows this shape:

```json
{
  "tool": "tool_name",
  "args": { }
}
```

Every response is one of two shapes:

```json
{"ok": true, "result": "..."}
{"ok": false, "error": "..."}
```

Some tools add optional fields to the envelope:

| Field | When present | Description |
|-------|-------------|-------------|
| `suggestion` | On structured errors | One-sentence next-step hint — read it before retrying |
| `classification` | `run_shell` (always) | Exit-code class: `success`, `expected_nonzero`, `error`, `timeout`, `signal` |
| `risk` | `run_shell` (always) | Pre-execution risk: `safe`, `caution`, `dangerous` |

Example with extended fields:

```json
{"ok": true, "result": "[exit: 0]\nok\n[classification: success — exit 0]", "risk": "safe", "classification": "success"}
```

The protocol is **one-shot**: one JSON request on stdin, one JSON response on stdout. jinn is not a daemon. It starts, handles one request, and exits.

If stdin is a terminal (you run `jinn` with no pipe), jinn prints a short help message and exits. You must pipe input or redirect from a file.

## Integration Patterns

### Shell Script

```bash
result=$(echo '{"tool":"read_file","args":{"path":"main.go"}}' | jinn)
ok=$(echo "$result" | jq -r '.ok')
if [ "$ok" = "true" ]; then
  echo "$result" | jq -r '.result'
else
  echo "failed: $(echo "$result" | jq -r '.error')"
fi
```

### Python subprocess

```python
import json, subprocess

def jinn(tool: str, args: dict) -> dict:
    req = json.dumps({"tool": tool, "args": args})
    proc = subprocess.run(
        ["jinn"],
        input=req,
        capture_output=True,
        text=True,
        timeout=60,
    )
    return json.loads(proc.stdout)

# Read a file
resp = jinn("read_file", {"path": "go.mod"})
if resp["ok"]:
    print(resp["result"])
else:
    print(f"error: {resp['error']}")
```

### Shell Loop (Sequential)

```bash
while IFS= read -r line; do
  echo "$line" | jinn
done < requests.jsonl
```

Each line in `requests.jsonl` is a complete JSON request object. jinn processes them one at a time.

## Flags

| Flag | Description |
|------|-------------|
| `--schema` | Print all tool definitions as JSON and exit |
| `--version` | Print the version and exit |
| `--help`, `-h` | Print usage information and exit |

## Persistent Memory

The `memory` tool stores key/value pairs across jinn invocations at `~/.config/jinn/memory.json`:

```bash
# Save a value
echo '{"tool":"memory","args":{"action":"save","key":"db_url","value":"postgres://localhost/myapp"}}' | jinn

# Recall it later
echo '{"tool":"memory","args":{"action":"recall","key":"db_url"}}' | jinn

# List all keys
echo '{"tool":"memory","args":{"action":"list"}}' | jinn
```

Keys must match `[a-zA-Z0-9_.-]` (max 128 chars). Values are capped at 16 KiB; the total store at 1 MiB.

## Language Server Queries

The `lsp_query` tool connects to a running language server to answer semantic questions about source code. The server is auto-selected from the file extension:

```bash
# Jump to definition at line 12, character 5
echo '{"tool":"lsp_query","args":{"action":"definition","path":"main.go","line":12,"character":5}}' | jinn

# List all symbols in a file
echo '{"tool":"lsp_query","args":{"action":"symbols","path":"internal/jinn/engine.go"}}' | jinn
```

Supported actions: `definition`, `references`, `hover`, `symbols`. Supported extensions: `.go`, `.rs`, `.py`, `.ts`, `.tsx`, `.js`, `.jsx`. The server binary must be on `PATH`; if missing, the response includes a `suggestion` with the install command.

## What's Next

- [Tool Reference](tool-reference.md) -- every tool with full parameter tables and examples
- [Security](security.md) -- path confinement, TOCTOU protection, atomic writes, and the risk classifier
