# Harness Integrations

If you run an agent harness — Claude Code, Codex CLI, pi, or your own loop — you already have a tool executor. jinn does not compete with your harness's built-in read/edit/shell tools. It fills the gaps harnesses leave open:

- **Semantic code queries without an MCP server.** [`lsp_query`](tool-reference.md#lsp_query) answers definition/references/hover/diagnostics as a one-shot subprocess. No daemon to babysit, no long-lived server entry in your config.
- **Risk classification without execution.** `run_shell` with `dry_run: true` classifies a command as `safe`/`caution`/`dangerous` and returns without running it — exactly the signal a permission hook needs.
- **Memory that can expire.** Markdown memory files grow forever. jinn's [`memory`](tool-reference.md#memory) tool is a project-scoped SQLite store with `kind`, `pin`, `expires_in`, and `gc`.
- **A uniform tool layer for the rest of your fleet.** Subagents, cron jobs, and cheap worker models don't inherit your harness's tooling. jinn gives any model that can emit JSON the same sandboxed surface.

Pick your depth:

| Depth | Mechanism | Best for |
| :--- | :--- | :--- |
| One-shot calls | `echo '{...}' \| jinn` from the harness's shell tool | `lsp_query`, `memory`, `detect_project` from inside a session |
| Permission hook | `run_shell` + `dry_run` risk classifier | semantic guard on shell commands |
| MCP broker | `jinn --mcp` → `jinn_route` | tool discovery at minimal context cost |
| Full executor | `--schema` + one subprocess per call | custom loops, subagent fleets, worker models |

## Claude Code

Claude Code's own Read/Edit/Bash already run behind its permission system — you don't need jinn for those. Reach for jinn where the harness has no native equivalent.

### LSP queries from the Bash tool

Finding references with grep is a heuristic; a language server is ground truth. jinn makes it a one-liner Claude can run from the Bash tool, with no MCP server configured:

```bash
echo '{"tool":"lsp_query","args":{"action":"references","path":"internal/jinn/engine.go","line":42,"character":10}}' | jinn
```

The server binary (`gopls`, `rust-analyzer`, `typescript-language-server`, …) is auto-selected from the file extension; if it's missing, the error response includes a `suggestion` with the install command.

To make Claude reach for it on its own, add a note to your project's `CLAUDE.md`:

```markdown
## Semantic code navigation
For find-references, go-to-definition, hover types, and diagnostics, prefer jinn over grep:
echo '{"tool":"lsp_query","args":{"action":"references","path":"<file>","line":N,"character":N}}' | jinn
```

### A risk-aware PreToolUse hook

Claude Code's permission rules are pattern-based; jinn's classifier is semantic. `dry_run: true` returns the `risk` field without executing, so a hook can escalate dangerous commands to a confirmation prompt:

```bash
#!/usr/bin/env bash
# ~/.claude/hooks/jinn-risk-guard.sh
input=$(cat)
cmd=$(echo "$input" | jq -r '.tool_input.command // empty')
[ -z "$cmd" ] && exit 0
risk=$(jq -n --arg c "$cmd" '{tool:"run_shell",args:{command:$c,dry_run:true}}' | jinn | jq -r '.risk // "safe"')
if [ "$risk" = "dangerous" ]; then
  jq -n '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"ask",permissionDecisionReason:"jinn classified this command as dangerous"}}'
fi
exit 0
```

Register it in `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "~/.claude/hooks/jinn-risk-guard.sh" }]
      }
    ]
  }
}
```

### Persistent memory with expiry

Claude Code's memory is markdown the model re-reads every session — it only grows. jinn's `memory` tool gives sessions a store that expires and garbage-collects:

```bash
# Save a fact that self-destructs in 30 days
echo '{"tool":"memory","args":{"action":"save","key":"staging_db","value":"postgres://staging:5432/app","kind":"fact","expires_in":"30d"}}' | jinn

# Recall everything for this project at session start
echo '{"tool":"memory","args":{"action":"list","include_values":true}}' | jinn
```

Scoping is automatic (nearest `.git` ancestor), so the same commands work in every repo.

### MCP discovery broker

If you prefer MCP, `jinn --mcp` costs exactly one tool schema in context. `jinn_route` recommends jinn tools for a stated need — deterministically, with no LLM and no network — and never executes anything itself. Add to `.mcp.json`:

```json
{
  "mcpServers": {
    "jinn": { "command": "jinn", "args": ["--mcp"] }
  }
}
```

## Codex CLI

Two natural fits:

**`apply_patch` speaks Codex's native format.** Patches wrapped in `*** Begin Patch … *** End Patch` are validated first, applied with per-file atomic writes, and previewable with `dry_run: true`. That lets you replay or validate Codex-generated patches outside the harness — in CI, in review tooling, or when a different agent applies a Codex model's output:

```bash
jq -n --rawfile p change.patch '{tool:"apply_patch",args:{patch:$p,dry_run:true}}' | jinn
```

**MCP discovery** via `~/.codex/config.toml`:

```toml
[mcp_servers.jinn]
command = "jinn"
args = ["--mcp"]
```

## pi and custom agent loops

This is the deep integration: jinn as the entire tool layer, so your loop stays thin.

- `jinn --schema` emits OpenAI-compatible function-calling definitions — feed them to any provider verbatim.
- One subprocess per call means no daemon lifecycle, no connection state, no cleanup in your loop.
- The envelope is uniform: on failure you get `error_code` plus a one-sentence `suggestion` — pipe the suggestion straight back to the model for self-repair instead of writing retry heuristics.
- Path confinement, TOCTOU protection, atomic writes, and `undo` snapshots come free; your loop never needs to implement "don't let the model write to `.ssh`".

A minimal loop is just: model emits a tool call → serialize to jinn's request shape → spawn `jinn` → return the response to the model. Working subprocess wrappers for Python, TypeScript/Bun, Go, PHP, and shell live in [getting-started.md](getting-started.md#integration-patterns).

## What's Next

- [Tool Reference](tool-reference.md) — every tool with full parameter tables and examples
- [Security](security.md) — path confinement, TOCTOU protection, atomic writes, and the risk classifier
