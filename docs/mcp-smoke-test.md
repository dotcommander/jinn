# Jinn MCP smoke test

This smoke test exercises the real `jinn --mcp` binary over a long-lived
stdin/stdout pipe. It does not use a network, a project mutation, or an API key.

## Run

```bash
go build -o "${TMPDIR:-/tmp}/jinn-mcp-smoke" ./cmd/jinn
python3 scripts/mcp-smoke-test.py \
  --binary "${TMPDIR:-/tmp}/jinn-mcp-smoke"
```

The script checks both paths:

- Current MCP 2026-07-28 `server/discover`, `tools/list`, and `tools/call`.
- Exactly one exposed tool, `jinn_route`, preserving the context-bloat design.
- JSON Schema 2020-12 declarations and output `$defs`.
- Structured route output plus its mirrored text content.
- Empty stderr and clean shutdown after the client closes stdin.
- Compatibility with the older initialize-based handshake.

The current SDK stdio transport treats EOF as client shutdown and cancels
in-flight requests. A real MCP client must keep stdin open until it has read the
responses it requested.
