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
Jinn's route-only compatibility surface without enabling legacy execution. The
isolated Codex run receives the same route-first `developer_instructions` rule
documented in the
[Codex CLI setup](harness-integrations.md#codex-mcp-configuration). The gate
requires:

- One route lookup for each of two distinct capability decisions.
- No Jinn call for an unrelated arithmetic question.
- No shell execution, failed MCP call, persistent Codex session, or persistent
  MCP configuration.

Use `--case route-only`, `--case ambiguous-route`, or
`--case negative-control` to isolate a failure without rerunning passing cases.

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
