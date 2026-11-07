# Similar pi.dev extensions for jinn's conditional tools

Candidates were sourced from `pie.json` — the github-enriched pi-packages corpus
copy of `all_pi_packages_github.json` (stars, last-update activity, and repo
availability from the snapshot) — and verified against the live pi.dev catalog.
★ = GitHub stars in the snapshot; activity = days since last upstream update.

## run_shell

- [pi-smart-terminal](https://pi.dev/packages/pi-smart-terminal) — persistent PTY shell sessions for pi, overriding the built-in bash tool with stateful `terminal_*` tools, a live `/term` overlay, and structured output for codemode scripting (verified on pi.dev; v0.5.0). **Closest in spirit to `run_shell`**: per-call exit-code classification, timeouts, and stdout/stderr handling are the featured semantics.
- [pi-interactive-shell](https://pi.dev/packages/pi-interactive-shell) — ★603, active today. Runs AI coding agents inside pi TUI overlays with interactive CLIs, hands-free mode, and dispatch supervision.
- [pi-bg-shell](https://pi.dev/packages/pi-bg-shell) — background shell tasks: start non-blocking commands with `bash_bg` and get woken with the output when they exit, Claude Code style.

## run_plan

- [@juicesharp/rpiv-workflow](https://pi.dev/packages/@juicesharp/rpiv-workflow) — ★946, active 14d ago. Chains skills into typed multi-stage workflows with audited JSONL state, predicate routing, and per-stage output validation. **Closest in spirit to `run_plan`**: condition-gated multi-stage execution with durable state and output validation.
- [pi-subagents](https://pi.dev/packages/pi-subagents) — ★4143, active today. Single-agent delegation and scripted multi-agent workflows (saved workflows, parallel audits, fleet view).
- [pi-plan-execute-auto](https://pi.dev/packages/pi-plan-execute-auto) — plan-and-execute workflow with human-approved phased plans, isolated per-phase execution, and deterministic acceptance.
- [pi-run-mode](https://pi.dev/packages/pi-run-mode) — ask / plan / auto workflow modes for balancing control, planning depth, and execution speed.

**Closest partial match:** [pi-plans](https://pi.dev/packages/pi-plans) — human-in-the-loop planning state (plan path, current task, task ids). It tracks plan state rather than executing a condition-gated plan tree, so it is not a true `run_plan` equivalent.

## web_fetch

- [@m4ss/pi-web-fetch](https://pi.dev/packages/@m4ss/pi-web-fetch) — ★3, active today. Web-fetch tool for Pi: fetches HTTP(S) URLs, converts HTML to clean Markdown, and pretty-prints JSON responses.
- [@johnnywu/pi-webfetch](https://pi.dev/packages/@johnnywu/pi-webfetch) — fetch web pages and URLs as readable text, Markdown, HTML, or JSON output.
- [pi-fetch](https://pi.dev/packages/pi-fetch) — fetch tool that retrieves a URL and returns page content, truncated to a bounded size.

## web_search

- [pi-web-access](https://pi.dev/packages/pi-web-access) — ★1771, active today. Web search, URL fetching, GitHub repo cloning, PDF extraction, YouTube video understanding, and local video analysis; providers include OpenAI, Brave, Parallel, TinyFish, Search1API, Tavily, Firecrawl, Crawl4AI, Jina, and more. **Strongest overall search+fetch alternative.**
- [@juicesharp/rpiv-web-tools](https://pi.dev/packages/@juicesharp/rpiv-web-tools) — ★946, active 14d ago. Web search and fetch with pluggable providers (Brave, Tavily, Serper, Exa, You.com, Jina, Firecrawl, Perplexity, SearXNG, Ollama).
- [pi-search-multi](https://pi.dev/packages/pi-search-multi) — unified web search + content extraction with 12 backend providers (DuckDuckGo, Jina AI, Marginalia, and more).
- [pi-web-search](https://pi.dev/packages/pi-web-search) — web search using your currently selected model.

## list_tools

No direct equivalent was found. `list_tools` is a meta-capability (enumerating the registered tool surface with metadata), and no pi.dev package in the corpus advertises a tool-catalog/listing tool.

**Closest partial match:** [pi-tool-search](https://pi.dev/packages/pi-tool-search) — ★7, last updated 196 days ago per the snapshot. Hides all tools behind a manifest-aware `tool_search` gate — it manages tool visibility (the inverse direction) rather than listing the tool surface.

## ask_user_question

- [@thinkrail/pi-ask-user-question](https://www.npmjs.com/package/@thinkrail/pi-ask-user-question) — ★7870, active today (JetBrains ThinkRail monorepo). Adds an `ask_user_question` tool with the same name and contract: structured multiple-choice questions answered in a terminal questionnaire, an RPC host's dialogs, or a host's own UI. **Same tool name, same semantics** — published on npm; `pi install npm:@thinkrail/pi-ask-user-question` (listed on npm, not separately verified on pi.dev).
- [@juicesharp/rpiv-ask-user-question](https://pi.dev/packages/@juicesharp/rpiv-ask-user-question) — ★946, active 14d ago. One tool that opens a terminal dialog of up to four questions with written-out options — the MCP `ask_user_question` questionnaire shape.
- [@dreki-gg/pi-questionnaire](https://pi.dev/packages/@dreki-gg/pi-questionnaire) — questionnaire tool collecting 1–5 structured answers in one interaction.
- [pi-ask-questions](https://pi.dev/packages/pi-ask-questions) — interactive questionnaires with tabs, multi-select, free-form typing, validation, and structured answers.
- [pi-questions](https://pi.dev/packages/pi-questions) — `ask_questions` tool opening a TUI flow and returning structured answers.
