# Similar pi.dev extensions for jinn's conditional tools

This document maps each of jinn's conditional tools to the closest similar pi.dev extensions found in the local corpus, and records the adoption suggestions mined from those donors with the `/compare` prompt.

- **Corpus:** `pie.json` — the github-enriched pi-packages corpus copy of `all_pi_packages_github.json` (star counts, last-update activity, and repo availability as of the snapshot).
- **Verification:** entries were verified against the live pi.dev catalog; ★ means GitHub stars in the snapshot and "active Nd ago" means days since the last upstream update in the snapshot. Treat both as snapshot values, not live metrics.
- **Source depth:** adoption suggestions for `pi-web-access` (v0.47.0) and `@juicesharp/rpiv-ask-user-question` (v2.11.0) are based on the locally installed package sources; the `run_shell` donors (`pi-smart-terminal`, `pi-interactive-shell`, `pi-bg-shell`) and the `run_plan` donors (`@juicesharp/rpiv-workflow`, `pi-subagents`, `pi-plan-execute-auto`, `pi-run-mode`) were source-inspected via `/compare` local clones on 2026-11-07 (see the section status notes under `run_shell` and `run_plan`); the remaining donors were assessed from package metadata/pi.dev descriptions, so their suggestions are qualified accordingly (see the reports in [Reports](#reports)).
- **Standing exclusions:** context-compaction and cache mechanisms were excluded from the comparisons and are not recommended anywhere below.
- **Overall: complete (2026-11-07).** All six tool sections below are reviewed — complete. Open items below are historical learn-only/skip dispositions, not pending work.

## Decision summary

| jinn tool | Closest donor | Best idea mined | Disposition | Discriminating check |
|---|---|---|---|---|
| `run_shell` | `pi-interactive-shell` (closest overall); `pi-smart-terminal` closest in spirit | interactive-shell supervision, PTY sessions + live overlay, background wake-on-exit | reviewed/complete 2026-11-07: 1 adopt (launch-policy exact-match gate), 2 learn-only (PTY sessions, wake-on-exit), rest skip | launch policy: global `[{command:"rm -rf build",decision:"deny"}]` + `run_shell {command:"rm -rf build", force:true}` → blocked with deny reason |
| `run_plan` | `@juicesharp/rpiv-workflow` | per-stage output validation before edge routing | **reviewed/complete + implemented 2026-11-07**: `expect` gate + JSONL audit; remaining donors source-inspected, no compelling adoption | stage emits `{}` with `expect` requiring `status` → node fails, zero edges evaluated, stop names the failed expectation |
| `web_fetch` | `pi-web-access` | typed field extraction with source-line provenance | **implemented 2026-11-07** (`mode:"extract"` + `fields`; grounded value + source line) | request `{price:"number"}` → grounded value + source line, never a full-page dump |
| `web_search` | `pi-web-access` | ordered fallback on `empty` / `domain-miss` + routing deadline | **implemented 2026-11-07** (comma-separated `JINN_WEB_SEARCH_PROVIDER` chain, reasons reported) | first provider returns zero results → next provider is tried, fallback reason reported |
| `list_tools` | `pi-tool-search` (partial) | query/filter on catalog metadata | **reviewed/complete + implemented 2026-11-07** (`risk`/`names`/`query` filters + `describe` mode) | planner asks for `read_only` subset without receiving the full catalog |
| `ask_user_question` | `@juicesharp/rpiv-ask-user-question` | pure validation before UI + input normalization at the boundary | **implemented 2026-11-07** (line-terminator normalization in `DecodeQuestionnaire`) | reserved `"Other\r"` label + duplicate option label → precise error, UI never opens |

> Note: jinn does not currently ship an `ask_user_question` tool — that section is a target-shape reference for the contract the host questionnaire should satisfy.

## run_shell

**Status: reviewed — complete (2026-11-07).** All three donors in this section have been source-inspected via `/compare` (Adopt) at their current HEADs; dispositions below are source-backed, not metadata-only. Section outcome: one supported adoption (pi-interactive-shell launch-policy gate), learn-only for the persistent-session/wake mechanisms, skip for the rest.

- [pi-smart-terminal](https://pi.dev/packages/pi-smart-terminal) — persistent PTY shell sessions for pi, overriding the built-in bash tool with stateful `terminal_*` tools, a live `/term` overlay, and structured output for codemode scripting (verified on pi.dev; v0.5.0). Repository: `pungggi/pi-smart-terminal`. **Closest in spirit to `run_shell`**: per-call exit-code classification, timeouts, and stdout/stderr handling are the featured semantics.
- [pi-interactive-shell](https://pi.dev/packages/pi-interactive-shell) — ★603, active today. Runs AI coding agents inside pi TUI overlays with interactive CLIs, hands-free mode, and dispatch supervision.
- [pi-bg-shell](https://pi.dev/packages/pi-bg-shell) — background shell tasks: start non-blocking commands with `bash_bg` and get woken with the output when they exit, Claude Code style.

### Adoption suggestions (/compare, per item)

- **`pi-smart-terminal` — no compelling adoption (source-inspected 2026-11-07, `pungggi/pi-smart-terminal` @ `e9e094c`).** Persistent PTY sessions with incremental `since` reads, new-output-only pattern wait, and watch are **learn-only**: the smallest adaptation is a session owner that outlives one call, contradicting jinn's no-daemon one-shot design (lesson: byte-offset reads, new-output-only matching, failure-with-data). Shell pinning and the PTY multi-line guard are **skip**: jinn is already deterministic single-shell (`/bin/bash`) and non-PTY, so both problems are absent. PTY core internals live in upstream `smart-terminal-mcp` and were not inspected. Supersedes the earlier metadata-only inconclusive note. (report: [.work/compare-pungggi-pi-smart-terminal-7d2646af02b1-20261107T152208Z.md](.work/compare-pungggi-pi-smart-terminal-7d2646af02b1-20261107T152208Z.md), prior: [.work/compare-pi-smart-terminal-20261107T131500Z.md](.work/compare-pi-smart-terminal-20261107T131500Z.md))
- **`pi-interactive-shell` — supported adoption (source-inspected 2026-11-07, `nicobailon/pi-interactive-shell` @ `3823684`): launch-policy exact-match gate.** A global, exact-string allow/ask/deny policy over opaque commands (deny > ask > allow, unmatched = ask, snapshot+freeze, evaluated before any spawn, fail-closed when confirmation is unavailable) fills a real gap in jinn's classifier+`force` gating seam with a small opt-in slice and no new dependency or state. Headless dispatch/monitor supervision is **learn-only** (session owner + wake hook = new product surface; trigger taxonomy is useful vocabulary for `run_plan` edges). Lean-by-default tool schema is **skip** (jinn already returns capabilities without schema by default). Refines the earlier learn-only note. (report: [.work/compare-pi-interactive-shell-a0f8819046c8-20261107T152828Z.md](.work/compare-pi-interactive-shell-a0f8819046c8-20261107T152828Z.md), prior: [.work/compare-pi-interactive-shell-20261107T130000Z.md](.work/compare-pi-interactive-shell-20261107T130000Z.md))
- **`pi-bg-shell` — no compelling adoption (source-inspected 2026-11-07, `No-World/pi-bg-shell` @ `05500d4`).** Wake-on-exit (exit hook + 100 ms debounce batching + `triggerTurn`) and running-event delivery (`on_pattern`/`report_every`) are **learn-only**: both need a host-resident registry and wake channel jinn deliberately lacks (lessons: debounce-merge concurrent completions, retry-once, capped wake payloads, executor-not-scheduler boundary). The detached-task global pool/subscription model is **skip** (cross-process persistent infrastructure at disproportionate cost). The bounded 1 MiB tail + spill output shape is already equivalent in jinn. Supersedes the earlier metadata-only learn-only note. (report: [.work/compare-pi-bg-shell-539702a034d9-20261107T153755Z.md](.work/compare-pi-bg-shell-539702a034d9-20261107T153755Z.md), prior: [.work/compare-pi-bg-shell-20261107T130000Z.md](.work/compare-pi-bg-shell-20261107T130000Z.md))

## run_plan

**Status: reviewed — complete, implemented 2026-11-07.** All donors in this section have been source-inspected via `/compare` (Adopt) at their current HEADs: `@juicesharp/rpiv-workflow` (closest donor, `juicesharp/rpiv-mono` @ `46dbdfd`, package `packages/rpiv-workflow`) contributed the adopted slices — per-stage `expect` validation before edge evaluation and the best-effort JSONL audit trail, both in the worktree (`internal/jinn/plan_engine.go`, `plan_audit.go`, `plan_expect_test.go`) — and the remaining donors (`pi-subagents`, `pi-plan-execute-auto`, `pi-run-mode`) were source-inspected with no compelling adoption (see Adoption suggestions). Partial match `pi-plans` tracks plan state rather than executing a condition-gated plan tree, so it is not a true `run_plan` equivalent.

- [@juicesharp/rpiv-workflow](https://pi.dev/packages/@juicesharp/rpiv-workflow) — ★946, active 14d ago. Chains skills into typed multi-stage workflows with audited JSONL state, predicate routing, and per-stage output validation. **Closest in spirit to `run_plan`**: condition-gated multi-stage execution with durable state and output validation.
- [pi-subagents](https://pi.dev/packages/pi-subagents) — ★4143, active today. Single-agent delegation and scripted multi-agent workflows (saved workflows, parallel audits, fleet view).
- [pi-plan-execute-auto](https://pi.dev/packages/pi-plan-execute-auto) — plan-and-execute workflow with human-approved phased plans, isolated per-phase execution, and deterministic acceptance.
- [pi-run-mode](https://pi.dev/packages/pi-run-mode) — ask / plan / auto workflow modes for balancing control, planning depth, and execution speed.

**Closest partial match:** [pi-plans](https://pi.dev/packages/pi-plans) — human-in-the-loop planning state (plan path, current task, task ids). It tracks plan state rather than executing a condition-gated plan tree, so it is not a true `run_plan` equivalent.

### Adoption suggestions (/compare)

- **Adopt (best pick) — per-stage output validation before edge evaluation.** Add an optional node `expect` (jsonPath/value or regex, reusing jinn's existing condition primitives — no schema library needed) that must hold on the stage's result before edges are evaluated; on failure the node fails, zero edges fire, and the stop names the failed expectation and the offending value. Source-backed: the donor checks `outputSchema`/`inputSchema` (Standard Schema) at both seams with a bounded produce→validate→retry loop (`maxRetries` clamped 1–3, `onInvalid: retry|halt`, validate timeout) and structured failures (`path`/`expected`/`actual`/`allowed`). Today jinn evaluates edges on the raw last-op result (`plan_engine.go` ~836–930), so a malformed stage (e.g. `{}` where `status` is expected) silently misroutes or ends `no_edge_match`. Cost: one field, one check, tests; no dependency, no state, no safety weakening. (report: [.work/compare-rpiv-workflow-c61098d35050-20261107T154340Z.md](.work/compare-rpiv-workflow-c61098d35050-20261107T154340Z.md), prior: [.work/compare-rpiv-workflow-20261107T120000Z.md](.work/compare-rpiv-workflow-20261107T120000Z.md))
- **Adopt, qualified second slice — audited JSONL plan state.** Append one JSONL event per completed node/op (plus routing-decision rows with notes) so the plan transcript outlives the in-memory result; resume/replay stay out of scope. Durability alone improves post-mortems, but it owns persistent files, confinement, and retention — sequence it after the `expect` gate and decide the write-failure posture (best-effort vs fail-closed) at implementation. (same report as above)
- **Learn-only — predicate taxonomy + retry-until-valid loops.** `gate()`/`match()` are already covered by jinn's six condition kinds for deterministic ops; arbitrary-TypeScript predicates and re-prompt retry loops belong to the donor's skill/LLM execution layer, not jinn's engine. The auditable no-match note is the one transferable idea (partly present via `edges_evaluated`/`edges_matched` + `no_edge_match`). (same report as above)
- Discriminating check: a stage emitting `{}` with `expect` requiring `status` must fail the node, evaluate zero edges, and stop naming the failed expectation and offending value; with `expect` absent, behavior must be identical to today.
- **`pi-subagents` — no compelling adoption (source-inspected 2026-11-07, `nicobailon/pi-subagents` @ `52e68e9`).** Host/agent-session product: model-backed child sessions, scripted multi-agent orchestration, and fleet supervision sit above jinn's deterministic tool-engine layer; the one engine-shaped mechanism (scripted workflows with strict public cutover validation and capacity budgets) is already covered or exceeded by `run_plan`'s declarative graph and fixed clamps. Lessons kept: ownership-scoped child disposal, strict public-boundary validation, async-drain semantics for any future async lanes, configurable-vs-fixed budget policy as an explicit decision. (report: [.work/compare-pi-subagents-324a394aa703-20261107T155031Z.md](.work/compare-pi-subagents-324a394aa703-20261107T155031Z.md))
- **`pi-plan-execute-auto` — no compelling adoption (source-inspected 2026-11-07, `ZyqAlwaysCool/pi-plan-execute-auto` @ `08fbfce`).** Human-in-the-loop, agent-phase, git-committing workflow product; every value-bearing mechanism (approval freeze, Executor/Reviewer sessions, rework loops, per-phase commits, resume) sits outside jinn's deterministic one-shot engine. Its engine-adjacent ideas (structured acceptance, failure fingerprints, pause taxonomy, preflight) are either already equivalent in `run_plan` (exit-code gates, structured stop reasons, plan validation before execution) or have no consumer without rework/resume constructs. (report: [.work/compare-pi-plan-execute-auto-8fef952c47e3-20261107T155307Z.md](.work/compare-pi-plan-execute-auto-8fef952c47e3-20261107T155307Z.md))
- **`pi-run-mode` — no compelling adoption (source-inspected 2026-11-07, `ouzhenkun/pi-run-mode` @ `44c4d7f`).** Host workflow product (agent modes, approval dialogs, per-mode model routing); its engine-adjacent mechanisms are either already present in stronger form in jinn (risk classifier, fail-closed gating, shell modes) or duplicate a concept already adopted from a stronger donor (declarative user policy — the `pi-interactive-shell` launch-policy gate). Lessons kept: safety floor evaluated before policy, headless prompt→block, verified-target exemptions only where the classifier is coarse. (report: [.work/compare-pi-run-mode-755272d81a5a-20261107T155517Z.md](.work/compare-pi-run-mode-755272d81a5a-20261107T155517Z.md))

## web_fetch

**Status: reviewed — complete (2026-11-07).** `web_fetch` extract mode (`mode:"extract"` + `fields`; `{value, line, source}` per field, `not_on_page`/`uncertain`, page content never returned — `internal/webfetch/extract.go`, wired in `internal/jinn/tool_web.go`) implemented, and all four donors accounted for: `pi-web-access` (source-inspected; extract adopted), `pi-fetch` (kotarac/pi-fetch @ `1b8c625`, source-inspected 2026-11-07 — three adopted mechanisms implemented: HTML sniffing fallback, GitHub blob/raw rewrite, ANSI/bidi sanitization), `@johnnywu/pi-webfetch` (metadata note: no compelling adoption vs jinn projections), `@m4ss/pi-web-fetch` (metadata note: clean-Markdown/JSON output already covered by jinn projections/readers). `donsetch` (`dondai44423/donsetch` @ `f02a6c2`, source-inspected 2026-11-07) contributed three further adopted mechanisms, implemented in `internal/webfetch/views.go` and wired in `internal/jinn/tool_web.go`: `toc`/`section` heading-outline views, `must_contain` page probes, and the `chrome_only` extraction-quality signal.

- [pi-web-access](https://pi.dev/packages/pi-web-access) — ★1771, active today. Web search, URL fetching, GitHub repo cloning, PDF extraction, YouTube video understanding, and local video analysis; providers include OpenAI, Brave, Parallel, TinyFish, Search1API, Tavily, Firecrawl, Crawl4AI, Jina, and more. **Strongest overall search+fetch alternative.** (Also listed under `web_search` — one donor, two tools.)
- [@m4ss/pi-web-fetch](https://pi.dev/packages/@m4ss/pi-web-fetch) — ★3, active today. Web-fetch tool for Pi: fetches HTTP(S) URLs, converts HTML to clean Markdown, and pretty-prints JSON responses.
- [@johnnywu/pi-webfetch](https://pi.dev/packages/@johnnywu/pi-webfetch) — fetch web pages and URLs as readable text, Markdown, HTML, or JSON output.
- [pi-fetch](https://pi.dev/packages/pi-fetch) — fetch tool that retrieves a URL and returns page content, truncated to a bounded size.

### Adoption suggestions (/compare)

- **Adopt — typed field extraction with source-line provenance.** Add `mode: "extract"` + typed fields (e.g. `{price: "number"}`) that return the grounded value with the line it was found on, or an explicit `not_on_page`/`uncertain` result. This is the largest capability delta: today the caller must fetch the whole page and locate the value itself.
- **Adopt — pi-fetch mechanisms (source-inspected 2026-11-07, `kotarac/pi-fetch` @ `1b8c625`).** HTML sniffing fallback for mislabeled Content-Type, GitHub `blob`/`raw` → `raw.githubusercontent.com` rewrite, and ANSI/bidi/C0-C1 sanitization on fetched text — all three implemented (`internal/webfetch/defuddle_reader.go`, `internal/webfetch/client.go`, `internal/webfetch/sanitize.go`, wired in `internal/jinn/tool_web.go`).
- **Learn-only — GitHub repo clone mode.** Routing `github.com/owner/repo` to a clone/fetch path (real file contents + local path) beats scraping GitHub HTML, but it touches fetch destination policy, so it is not part of the first slice.
- pi-web-access's own fetch caching was excluded from this comparison by the standing cache rule and is not recommended here.
- No compelling adoption from @johnnywu/pi-webfetch: jinn's projections + metadata already cover its multi-shape output. (reports: [.work/compare-pi-web-access-20261107T120000Z.md](.work/compare-pi-web-access-20261107T120000Z.md), [.work/compare-johnnywu-pi-webfetch-20261107T120000Z.md](.work/compare-johnnywu-pi-webfetch-20261107T120000Z.md), [.work/compare-pi-fetch-6c877553b90e-20261107T163756Z.md](.work/compare-pi-fetch-6c877553b90e-20261107T163756Z.md))

## web_search

**Status: reviewed — complete (2026-11-07).** `JINN_WEB_SEARCH_PROVIDER` accepts an ordered comma-separated list (e.g. `brave,exa`); `Service.Search` tries providers in order, falling back on error (reason = error code) or zero results (`empty`, or `domain-miss` with domain filters), bounded by the service timeout, and reports each fallback (`internal/webfetch/search.go`). All donors accounted for: `@juicesharp/rpiv-web-tools` (source-inspected 2026-11-07 @ `46dbdfd` — adopted: ordered provider fallback with recorded reasons, keyless SearXNG backend, per-provider config precedence; learn-only: per-call provider override, since the operator chain + combine already beat it), `pi-search-multi` (source-inspected 2026-11-07 — adopted: opt-in combine with reciprocal rank fusion and targeted batching, `internal/webfetch/combine.go`; key-injection convention skipped as already covered by jinn's cmd layer), `pi-web-access` (source-inspected earlier; provider/cache mechanisms excluded by the standing cache rule), `pi-web-search` (metadata note: model-based search has no deterministic equivalent in jinn's one-shot contract, no adoption).

- [pi-web-access](https://pi.dev/packages/pi-web-access) — ★1771, active today. Web search, URL fetching, GitHub repo cloning, PDF extraction, YouTube video understanding, and local video analysis; providers include OpenAI, Brave, Parallel, TinyFish, Search1API, Tavily, Firecrawl, Crawl4AI, Jina, and more. **Strongest overall search+fetch alternative.** (Cross-listed from `web_fetch`.)
- [@juicesharp/rpiv-web-tools](https://pi.dev/packages/@juicesharp/rpiv-web-tools) — ★946, active 14d ago. Web search and fetch with pluggable providers (Brave, Tavily, Serper, Exa, You.com, Jina, Firecrawl, Perplexity, SearXNG, Ollama).
- [pi-search-multi](https://pi.dev/packages/pi-search-multi) — unified web search + content extraction with 12 backend providers (DuckDuckGo, Jina AI, Marginalia, and more).
- [pi-web-search](https://pi.dev/packages/pi-web-search) — web search using your currently selected model.

### Adoption suggestions (/compare)

- **Adopt — ordered provider fallback on quality signals.** Add an ordered provider list with fallback reasons `empty`, `domain-miss`, `off-topic` (and the existing transient/quota/network errors) plus a bounded total routing deadline. jinn today tries one provider and returns its empty result; the donor moves on and returns the first usable answer.
- pi-web-access's provider-specific and cache mechanisms were excluded from this comparison by the standing cache rule and are not recommended here.
- Discriminating check: a first provider returning zero results for a domain-filtered query must trigger the next provider, with the fallback reason reported. (report: [.work/compare-pi-web-access-20261107T120000Z.md](.work/compare-pi-web-access-20261107T120000Z.md))

## list_tools

**Status: reviewed — complete (2026-11-07).** No direct equivalent was found. `list_tools` is a meta-capability (enumerating the registered tool surface with metadata), and no pi.dev package in the corpus advertises a tool-catalog/listing tool. The one learn-only suggestion from the pi-tool-search comparison is now implemented: `risk`/`names`/`query` filters narrow the listing and `describe:true` adds one-line descriptions without the full schema payload (`internal/jinn/engine.go`, `internal/jinn/list_tools_test.go`).

**Closest partial match:** [pi-tool-search](https://pi.dev/packages/pi-tool-search) — ★7, last updated 196 days ago per the snapshot. Hides all tools behind a manifest-aware `tool_search` gate — it manages tool visibility (the inverse direction) rather than listing the tool surface.

### Adoption suggestions (/compare)

- **Learn-only — query/filter on catalog metadata.** Add optional `query`/`risk` filters to `list_tools` (`read_only`, `mutating`, `shell`, `network`) so a planner can request a subset without receiving the full catalog. pi-tool-search's gating direction is inverted and its snapshot staleness (196 days) weakens the case for direct adoption. (report: [.work/compare-pi-tool-search-20261107T120000Z.md](.work/compare-pi-tool-search-20261107T120000Z.md))

## ask_user_question

**Status: reviewed — complete (2026-11-07).** `DecodeQuestionnaire` now strips line terminators from all model-supplied strings once at the boundary, before validation (`internal/jinn/questionnaire.go`) — the discriminating check (`"Other\r"` reserved label + normalized-duplicate labels → precise error, UI never opens) is covered by `TestDecodeQuestionnaireNormalizesLineTerminators`. All donors in this section were source-inspected via `/compare` (Adopt); the TUI overlay/layout item is a deliberate skip (jinn is a tool runtime, not a TUI).

- [@thinkrail/pi-ask-user-question](https://www.npmjs.com/package/@thinkrail/pi-ask-user-question) — ★7870, active today (JetBrains ThinkRail monorepo). Adds an `ask_user_question` tool with the same name and contract: structured multiple-choice questions answered in a terminal questionnaire, an RPC host's dialogs, or a host's own UI. **Same tool name, same semantics** — published on npm; `pi install npm:@thinkrail/pi-ask-user-question` (listed on npm, not separately verified on pi.dev).
- [@juicesharp/rpiv-ask-user-question](https://pi.dev/packages/@juicesharp/rpiv-ask-user-question) — ★946, active 14d ago. One tool that opens a terminal dialog of up to four questions with written-out options — the MCP `ask_user_question` questionnaire shape.
- [@dreki-gg/pi-questionnaire](https://pi.dev/packages/@dreki-gg/pi-questionnaire) — questionnaire tool collecting 1–5 structured answers in one interaction.
- [pi-ask-questions](https://pi.dev/packages/pi-ask-questions) — interactive questionnaires with tabs, multi-select, free-form typing, validation, and structured answers.
- [pi-questions](https://pi.dev/packages/pi-questions) — `ask_questions` tool opening a TUI flow and returning structured answers.

### Adoption suggestions (/compare)

- **Adopt — pure validation before any UI opens.** Keep the questionnaire validator pure and UI-independent: question count, duplicate question text, minimum options, reserved labels, duplicate option labels. Errors must be testable without a terminal.
- **Adopt — normalize input at the boundary, before validation.** Strip/repair line terminators in model-supplied strings once at entry so guards compare the text the user will actually see (e.g. `"Other\r"` cannot slip past `reserved_label`).
- **Learn-only — collapsible overlay and adaptive preview layout.** These are host/TUI concerns; jinn is a tool runtime, not a TUI, so only the contract-level rules belong in a jinn tool.
- Discriminating check: a questionnaire with a reserved `"Other\r"` label and a duplicate option label must fail validation with a precise error and never open a UI. (report: [.work/compare-rpiv-ask-user-question-20261107T120000Z.md](.work/compare-rpiv-ask-user-question-20261107T120000Z.md))

## Reports

Full `/compare` findings (including the rerun note) live under `.work/`:

- [.work/compare-pi-smart-terminal-20261107T120000Z.md](.work/compare-pi-smart-terminal-20261107T120000Z.md)
- [.work/compare-rpiv-workflow-20261107T120000Z.md](.work/compare-rpiv-workflow-20261107T120000Z.md)
- [.work/compare-johnnywu-pi-webfetch-20261107T120000Z.md](.work/compare-johnnywu-pi-webfetch-20261107T120000Z.md)
- [.work/compare-pi-web-access-20261107T120000Z.md](.work/compare-pi-web-access-20261107T120000Z.md)
- [.work/compare-pi-tool-search-20261107T120000Z.md](.work/compare-pi-tool-search-20261107T120000Z.md)
- [.work/compare-rpiv-ask-user-question-20261107T120000Z.md](.work/compare-rpiv-ask-user-question-20261107T120000Z.md)
- [.work/compare-pi-interactive-shell-20261107T130000Z.md](.work/compare-pi-interactive-shell-20261107T130000Z.md)
- [.work/compare-pi-bg-shell-20261107T130000Z.md](.work/compare-pi-bg-shell-20261107T130000Z.md)
- [.work/compare-pi-smart-terminal-20261107T131500Z.md](.work/compare-pi-smart-terminal-20261107T131500Z.md)
- [.work/compare-rerun-20261107T121500Z.md](.work/compare-rerun-20261107T121500Z.md)
- [.work/compare-pungggi-pi-smart-terminal-7d2646af02b1-20261107T152208Z.md](.work/compare-pungggi-pi-smart-terminal-7d2646af02b1-20261107T152208Z.md)
- [.work/compare-pi-interactive-shell-a0f8819046c8-20261107T152828Z.md](.work/compare-pi-interactive-shell-a0f8819046c8-20261107T152828Z.md)
- [.work/compare-pi-bg-shell-539702a034d9-20261107T153755Z.md](.work/compare-pi-bg-shell-539702a034d9-20261107T153755Z.md)
- [.work/compare-rpiv-workflow-c61098d35050-20261107T154340Z.md](.work/compare-rpiv-workflow-c61098d35050-20261107T154340Z.md)
- [.work/compare-pi-subagents-324a394aa703-20261107T155031Z.md](.work/compare-pi-subagents-324a394aa703-20261107T155031Z.md)
- [.work/compare-pi-plan-execute-auto-8fef952c47e3-20261107T155307Z.md](.work/compare-pi-plan-execute-auto-8fef952c47e3-20261107T155307Z.md)
- [.work/compare-pi-run-mode-755272d81a5a-20261107T155517Z.md](.work/compare-pi-run-mode-755272d81a5a-20261107T155517Z.md)
