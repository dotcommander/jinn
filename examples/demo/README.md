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

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DEMO_API_KEY` | — | API key (required; `OPENROUTER_API_KEY` and `OPENAI_API_KEY` are also accepted) |
| `DEMO_MODEL` | `openai/gpt-5.4-mini` | Model identifier |
| `DEMO_BASE_URL` | OpenRouter chat/completions endpoint | LLM API base URL |
| `DEMO_MAX_TURNS` | `25` | Maximum agent turns per session |
| `JINN_BIN` | `jinn` | Path to jinn binary |
| `DEFUDDLE_BIN` | `defuddle` | Path to defuddle binary (used by `web_fetch`) |

## Usage

One-shot (pipe via stdin):

```bash
echo "list files in the cwd" | demo
```

One-shot (inline prompt):

```bash
demo "list files in the cwd"
```

REPL mode: invoke without a prompt on a TTY to enter the interactive REPL.
