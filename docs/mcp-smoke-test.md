# Jinn MCP smoke test

The stdio smoke test exercises the real `jinn --mcp` binary over a long-lived
stdin/stdout pipe. It does not use a network, a project mutation, or an API key.

## Run

```bash
go build -o "${TMPDIR:-/tmp}/jinn-mcp-smoke" ./cmd/jinn
python3 scripts/mcp-smoke-test.py \
  --binary "${TMPDIR:-/tmp}/jinn-mcp-smoke"
```

The script checks three paths:

- Current MCP 2026-07-28 `server/discover`, `tools/list`, and `tools/call`.
- Exactly one exposed tool, `jinn_route`, preserving the context-bloat design.
- JSON Schema 2020-12 declarations and output `$defs`.
- Structured route output plus its mirrored text content.
- Empty stderr and clean shutdown after the client closes stdin.
- Compatibility with the older initialize-based handshake.
- Executable-only host startup with no `--mcp` argument.
- The opt-in `read-only` profile, including its two-tool surface, generated
  read-only allowlist, successful `read_file` execution, and rejected
  `write_file` execution. Its legacy `initialize` request is rejected by the
  current SDK before any route or tool dispatch.

- The opt-in `network` profile's two-tool surface and its absence of mutating
  or shell execution. The smoke does not invoke real web providers.
- The local explicit-argv explorer path (`jinn mcp list --command ... --mcp`)
  with stable JSON and subprocess cleanup.

The current SDK stdio transport treats EOF as client shutdown and cancels
in-flight requests. A real MCP client must keep stdin open until it has read the
responses it requested.

## Codex discoverability smoke (provider-backed)

This opt-in gate invokes Codex up to three times and consumes OpenAI quota. It
does not persist Codex sessions or MCP configuration. Build the task-owned
binary, authenticate Codex, then run:

```bash
go build -o "${TMPDIR:-/tmp}/jinn-codex-discovery" ./cmd/jinn
python3 scripts/codex-discoverability-smoke-test.py \
  --binary "${TMPDIR:-/tmp}/jinn-codex-discovery"
```

For repeatable certification, pin the same model used in production:

```bash
python3 scripts/codex-discoverability-smoke-test.py \
  --binary "${TMPDIR:-/tmp}/jinn-codex-discovery" \
  --model "$CODEX_MODEL"
```

The cold prompts never name Jinn, `jinn_route`, or `jinn_call`. The test uses
Jinn's route-only compatibility surface without enabling legacy execution. Each
isolated Codex run uses the same temporary two-file project as the JCode gate:
`.jcode/mcp.json` and a short route-first `AGENTS.md`. It ignores user config and
exec-policy rules, disables documented native tool families and discovered skill
instructions, and uses the exact `AGENTS.md` bytes as Codex's
`model_instructions_file` replacement while suppressing a duplicate project-doc
read. Only the configured Jinn route tool remains available as far as Codex's
host controls permit. The shared instruction caps each lookup at
`max_tools=1`. The gate requires:

- One route lookup for each of two distinct capability decisions.
- No Jinn call for an unrelated arithmetic question.
- No shell execution, failed MCP call, persistent Codex session, or persistent
  MCP configuration.

Each case reports a shared normalized usage schema: reported counter scope, a
lower bound on model requests, total/cached/uncached input, cache-write input,
total output, and reasoning output. Codex's `turn.completed` usage is cumulative
for the turn and does not report cache writes, so that field is `null`. Neither
host exposes a portable provider-request breakdown; the harness does not invent
one.

Codex's `code_mode_host` feature must remain enabled for the configured MCP tool
to reach the model. The harness retains that host layer, includes its overhead
in the reported usage, and disables `tool_suggest` plus the unrelated native
feature families listed in the context inventory.

Use `--case route-only`, `--case ambiguous-route`, or
`--case negative-control` to isolate a failure without rerunning passing cases.

## JCode discoverability smoke (provider-backed)

This opt-in gate invokes JCode up to three times and consumes the configured
provider's quota. Build the task-owned binary, authenticate JCode, and pin the
provider and model used in production:

```bash
go build -o "${TMPDIR:-/tmp}/jinn-jcode-discovery" ./cmd/jinn
python3 scripts/jcode-discoverability-smoke-test.py \
  --binary "${TMPDIR:-/tmp}/jinn-jcode-discovery" \
  --provider "$JCODE_PROVIDER" \
  --model "$JCODE_MODEL"
```

The harness creates the same temporary two-file project used by the Codex gate,
containing `.jcode/mcp.json` and a route-first `AGENTS.md`. It exposes only
`mcp__jinn__jinn_route`, disables
JCode's built-in tools and automatic follow-up turns, and reads JCode's NDJSON
tool lifecycle plus final usage record. The temporary project is removed after
the run. JCode still writes its normal session records and may update its MCP
schema cache under the active JCode home; the output includes each session ID.

The cold prompts and acceptance criteria match the Codex gate:

- Exactly one completed route lookup for each capability decision.
- No tool call and the exact answer `42` for the arithmetic negative control.
- No other tool, failed or incomplete tool lifecycle, auto-poke turn, or
  malformed NDJSON event.
- A final provider, model, session ID, and token-usage record for every case.

JCode reports the same normalized usage keys as Codex. Its scope is explicitly
`done_event_reported`, and its cache-read and cache-creation counters map to the
shared cache fields. `model_requests_lower_bound` is derived from validated tool
calls plus the final response; it is a lower bound, not a claim about hidden
provider retries or host-side turns. Both context inventories include the same
instruction hash and record the host-native delivery channel.

Omit `--provider` or `--model` to use JCode's configured selection, but that is
not a repeatable certification. Use `--case route-only`,
`--case ambiguous-route`, or `--case negative-control` to isolate a failure.
`--mcp-wait-ms` defaults to `10000` for a cold MCP schema cache, and `--timeout`
defaults to `180` seconds per provider call.

## Streamable HTTP smoke test

The HTTP smoke test starts a real subprocess on a temporary loopback port. It
does not expose a remote listener, write project files, or print a token. It
checks route-only discovery, read-only execution, required MCP headers, bearer
auth, exact origin allow/reject behavior (including unconfigured localhost), the
MCP `Accept: application/json, text/event-stream` request header, the `/mcp`
path boundary, clean SIGTERM shutdown,
empty stdout, and the single expected startup line on stderr.

```bash
go build -o "${TMPDIR:-/tmp}/jinn-mcp-http-smoke" ./cmd/jinn
python3 scripts/mcp-http-smoke-test.py \
  --binary "${TMPDIR:-/tmp}/jinn-mcp-http-smoke"
```

The HTTP command defaults to `127.0.0.1:8788` when no address is supplied:

```bash
jinn --mcp-http
jinn --mcp-profile=read-only --mcp-http 127.0.0.1:8788
```

For a non-loopback bind, set both environment-only controls. The token is sent
as a bearer header, and the origin list must contain exact HTTP(S) origins:

```bash
JINN_MCP_HTTP_TOKEN="$TOKEN" \
JINN_MCP_HTTP_ORIGINS="https://agent.example.com" \
jinn --mcp-profile=read-only --mcp-http 0.0.0.0:8788
```

HTTP is stateless and does not use the stdio legacy `initialize` compatibility
path. The pinned SDK owns JSON body, protocol header, method, and origin
validation after the authorization boundary.
