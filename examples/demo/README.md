# demo

A minimal coding-agent REPL built on jinn's tool-executor protocol. Demonstrates how to consume jinn as a subprocess.

**This is an example, not a dependency.** jinn itself is stdlib-only. This demo lives in its own nested module (`examples/demo/go.mod`) so it can pull in `charmbracelet/glamour` for markdown rendering without polluting jinn's zero-dep guarantee. If you only build `jinn`, you get nothing from this directory.

## Build

```bash
cd examples/demo && go build -o demo .
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
| `--compact-every` | `3` | Compact history every N user turns (`0` disables) |
| `--compact-prompt-file` | — | Path to custom compaction prompt (defaults to embedded) |
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
| `DEMO_COMPACT_EVERY` | `3` | Compact history every N user turns (`0` disables) |
| `DEMO_COMPACT_PROMPT_FILE` | — | Path to custom compaction prompt |
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

## Compaction

Instead of a fixed message-window, demo periodically asks the LLM to summarize the conversation and replaces older turns with that summary. Default: every 3 user inputs. The summary preserves intent, decisions, files touched, pending work, and the next step.

- `--compact-every 0` disables compaction (unbounded history — your tokens, your problem).
- `--compact-prompt-file path/to/prompt.md` overrides the built-in prompt.
- `/reset` in the REPL clears conversation *and* resets the compaction counter.
- If a summarization call fails, demo logs a warning to stderr and proceeds with full history. The next trigger retries.

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

- **Markdown rendering** — full CommonMark + GFM via `charmbracelet/glamour`: syntax-highlighted code blocks, tables, task lists, wrapped paragraphs, styled headings and blockquotes. Theme auto-selected from `$TERM` / `COLORFGBG`.
- **Spinner** — braille-dot animation while waiting for the LLM
- **Tool calls** — highlighted with name, args, and elapsed time
- Colors respect `NO_COLOR` and `TERM=dumb`
