# demo

A minimal coding-agent REPL built on jinn's tool-executor protocol. Demonstrates how to consume jinn as a subprocess.

**This is an example, not a dependency.** jinn itself is stdlib-only. This demo links an HTTP client, SSE parser, and REPL — if you build `demo`, you are opting into those via stdlib; if you only build `jinn`, you get nothing from this directory.

## Build

```bash
go build -o demo ./examples/demo/
```

## Requirements

- `jinn` binary on `$PATH`
- `defuddle` CLI on `$PATH` (used by the `web_fetch` tool inside the agent loop)

## Usage

**REPL** — invoke without a prompt on a TTY:

```bash
demo
```

**One-shot** — inline prompt or piped stdin:

```bash
demo "list files in the cwd"
echo "list files in the cwd" | demo
```

**Local LLM** — auto-detect a local server (probes ports 8080, 8000, 1234, 11434):

```bash
demo --local
```

**Resume a session:**

```bash
demo --resume --session s-1713873600
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--model` | `openai/gpt-5.4-mini` | LLM model identifier |
| `--base-url` | OpenRouter endpoint | Chat/completions URL |
| `--local` | off | Auto-detect local LLM server |
| `--max-turns` | `25` | Maximum agent turns per session |
| `--max-history` | `40` | Max messages kept in conversation history (system prompt always preserved) |
| `--max-tool-output` | `32768` | Max tool output bytes before truncation |
| `--temperature` | `1.0` | LLM sampling temperature |
| `--top-p` | `1.0` | LLM top-p sampling |
| `--max-tokens` | `4096` | Max tokens in completion |
| `--dry-run` | off | Intercept destructive tools (write_file, edit_file, multi_edit, run_shell) and report intent without executing |
| `--session` | auto-generated | Session ID for save/resume |
| `--resume` | off | Resume the named session |
| `--session-dir` | `~/.local/share/demo/sessions/` | Session storage directory |
| `--quiet` | off | Suppress tool previews |
| `--jinn-bin` | `jinn` | Path to jinn binary |
| `--defuddle-bin` | `defuddle` | Path to defuddle binary |
| `--version` | — | Print version |

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DEMO_API_KEY` | — | API key (required; `OPENROUTER_API_KEY` and `OPENAI_API_KEY` are also accepted) |
| `DEMO_MODEL` | `openai/gpt-5.4-mini` | Model identifier |
| `DEMO_BASE_URL` | OpenRouter endpoint | LLM API base URL |
| `DEMO_MAX_TURNS` | `25` | Maximum agent turns per session |
| `DEMO_MAX_HISTORY` | `40` | Max messages in history (overrides via `--max-history`) |
| `DEMO_MAX_TOOL_OUTPUT` | `32768` | Max tool output bytes before truncation |
| `JINN_BIN` | `jinn` | Path to jinn binary |
| `DEFUDDLE_BIN` | `defuddle` | Path to defuddle binary |

## Custom System Prompt (AGENTS.md)

Drop an `AGENTS.md` file in your working directory and `demo` will use it as the system prompt instead of the embedded default. This is the same convention used by other agentic CLIs.

Two template tokens are substituted at runtime:

- `{{workdir}}` — current working directory
- `{{os}}` — `runtime.GOOS` value (e.g. `darwin`, `linux`)

If `AGENTS.md` is missing, the embedded default ships with the binary. If it exists but cannot be read (permission denied, etc.), `demo` prints a stderr warning and falls back to the embedded default.

The embedded default is itself the contents of `examples/demo/AGENTS.md` in this repo — start by copying that file and editing it to taste.

## REPL Commands

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/reset` | Clear conversation (keeps system prompt) |
| `/clear` | Clear the screen |
| `/exit` | Quit (or press Ctrl-D) |

Ctrl-C cancels the current turn and re-prompts.

## Tools

The agent has access to 11 tools — 10 from jinn plus `web_fetch` via defuddle:

| Tool | Description |
|------|-------------|
| `read_file` | Line-numbered file reads with windowing |
| `stat_file` | File metadata (size, lines, mtime, type) |
| `list_dir` | Recursive directory listing with depth control |
| `search_files` | Grep with regex, glob filter, context lines |
| `edit_file` | Single text replacement (old_text must be unique) |
| `multi_edit` | Batch edits across files — validates all, applies atomically |
| `write_file` | Atomic full-file writes (creates parent dirs) |
| `run_shell` | Bash with timeout (default 30s, max 300s) |
| `checksum_tree` | SHA-256 hashes for a file tree with glob filter |
| `detect_project` | Detect language, framework, build/test/lint commands |
| `web_fetch` | Retrieve HTTP(S) URL as markdown (via defuddle) |

## Display

In REPL mode the output is ANSI-formatted:

- **Markdown rendering** — bold, inline code, fenced code blocks, headers, and blockquotes are styled in the terminal
- **Spinner** — braille-dot animation while waiting for the LLM
- **Tool calls** — highlighted with name, args, and elapsed time
- Colors respect `NO_COLOR` and `TERM=dumb`
