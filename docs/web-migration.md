# Web configuration migration: pi-web-access `web-search.json` → jinn

Jinn web tools read configuration from environment variables and CLI flags
(flags override environment, which overrides built-in defaults). Credentials
are never copied automatically; set each provider key explicitly and rotate
any key that was shared with another tool.

| pi-web-access `web-search.json` | jinn equivalent | Status |
|---|---|---|
| `searchRouting.providers: ["exa","brave","tavily","gemini"]` | `JINN_WEB_SEARCH_PROVIDER=exa,brave,tavily` | Mapped (Gemini pending a contract probe; see below) |
| `searchRouting.fallbackOn: [unsupported, transient, quota, network, invalid-response]` | Built-in fallback with normalized categories on each `fallbacks[]` entry | Mapped |
| `workflow: "auto-summary"` | `web_search` with `workflow: "extractive_summary"` | Partial — deterministic digest, not model synthesis |
| `summaryModel: "openai-codex/gpt-6-luna"` | None (no model adapter in jinn core) | Decided 2026-11-08 — extractive + caller synthesis; no model adapter in jinn core |
| `allowBrowserCookies: false` | No cookie import exists; keep unset | Dropped by design |
| Provider API keys (`braveApiKey`, `exaApiKey`, …) | `BRAVE_API_KEY`, `EXA_API_KEY`, `TAVILY_API_KEY`, `SEARXNG_API_KEY`, `JINA_API_KEY` | Mapped (set explicitly; do not copy secrets) |
| Self-hosted endpoints | `JINN_WEB_SEARCH_ENDPOINT`, `JINN_WEB_EXA_SEARCH_ENDPOINT`, `TAVILY_SEARCH_ENDPOINT`, `JINN_WEB_SEARXNG_SEARCH_ENDPOINT` | Mapped |
| `fetch` reader/render/timeout settings | `JINN_WEB_READER`, `JINN_WEB_READER_ENDPOINT`, `JINN_WEB_TIMEOUT`, render `JINN_WEB_RENDER_*` | Mapped |
| Result/content caching | `JINN_WEB_CACHE_TTL`, `JINN_WEB_CACHE_DIR` (URL cache); `JINN_WEB_SNAPSHOT_DIR` (snapshots) | Mapped |
| `/websearch` curator, `/curator`, Ctrl+Shift+S/W | None | Intentionally dropped — accept loss or keep a thin Pi adapter |
| `/google-account`, Gemini Web cookies | None | Intentionally dropped |
| `fetch_content` answer/extract model modes | `web_fetch` `mode: "extract"` (grounded scalars) + caller synthesis | Partial |
| YouTube/local video analysis | None (transcripts deferred) | Deferred — keep pi-web-access or another verified tool |
| `get_search_content` | `web_content` over `persist_snapshot` results | Mapped (24h TTL, max 50/64MiB) |
| `source_check` (semantic) | `source_check` (lexical evidence only) | Partial — no semantic verdict |
| MCP server for other agents | `jinn --mcp --mcp-profile=network` (`jinn_call` exposes the web tools) | Mapped |

## Notes

- Gemini search has no jinn adapter yet. A Tavily-only chain is the current
  parity bar; adding Gemini requires first validating an API that returns
  searchable, attributable evidence.
- Extractive summaries select source-provided snippets with result
  citations and never call a model. Summary contract (decided
  2026-11-08): the caller (Pi/assistant) synthesizes from the cited
  digest and underlying results; jinn core has no model adapter,
  credentials, or synthesis budget.
- Snapshots are local storage: the read-only MCP profile never exposes
  stored web content; only `--mcp-profile=network` does.
