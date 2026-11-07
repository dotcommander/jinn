# Similar pi.dev extensions for jinn's conditional tools

Each candidate below was found in the local pi-packages corpus
(`/Users/vampire/code/ts/pi-packages/all_pi_packages.json`, 5251 entries)
and verified against the live pi.dev catalog. Selections are intentionally
short: one high-signal equivalent per family, plus the closest partial
match where no true equivalent exists.

## run_shell

- [pi-smart-terminal](https://pi.dev/packages/pi-smart-terminal) — persistent PTY shell sessions for pi, overriding the built-in bash tool with stateful `terminal_*` tools, a live `/term` overlay, and structured output for codemode scripting (v0.5.0).
- [pi-bg-shell](https://pi.dev/packages/pi-bg-shell) — background shell tasks: start non-blocking commands with `bash_bg` and get woken with the output when they exit, Claude Code style.

## run_plan

- [pi-plan-execute-auto](https://pi.dev/packages/pi-plan-execute-auto) — plan-and-execute workflow with human-approved phased plans, isolated per-phase execution, and deterministic acceptance.
- [pi-run-mode](https://pi.dev/packages/pi-run-mode) — ask / plan / auto workflow modes for balancing control, planning depth, and execution speed.

**Closest partial match:** [pi-plans](https://pi.dev/packages/pi-plans) — human-in-the-loop planning state (plan path, current task, task ids). It tracks plan state rather than executing a condition-gated plan tree, so it is not a true `run_plan` equivalent.

## web_fetch

- [@johnnywu/pi-webfetch](https://pi.dev/packages/@johnnywu/pi-webfetch) — fetch web pages and URLs as readable text, Markdown, HTML, or JSON output.
- [pi-fetch](https://pi.dev/packages/pi-fetch) — fetch tool that retrieves a URL and returns page content, truncated to a bounded size.

## web_search

- [pi-search-multi](https://pi.dev/packages/pi-search-multi) — unified web search + content extraction with 12 backend providers (DuckDuckGo, Jina AI, Marginalia, and more).
- [pi-web-search](https://pi.dev/packages/pi-web-search) — web search using your currently selected model.

## list_tools

No direct equivalent was found. `list_tools` is a meta-capability (enumerating the registered tool surface with metadata), and no pi.dev package in the corpus advertises a tool-catalog/listing tool.

**Closest partial match:** [pi-tool-search](https://pi.dev/packages/pi-tool-search) — hides non-core tools behind a manifest-aware `tool_search` gate. It manages tool visibility (the inverse direction) rather than listing the tool surface.

## ask_user_question

- [@dreki-gg/pi-questionnaire](https://pi.dev/packages/@dreki-gg/pi-questionnaire) — questionnaire tool collecting 1–5 structured answers in one interaction.
- [pi-ask-questions](https://pi.dev/packages/pi-ask-questions) — interactive questionnaires with tabs, multi-select, free-form typing, validation, and structured answers.
- [pi-questions](https://pi.dev/packages/pi-questions) — `ask_questions` tool opening a TUI flow and returning structured answers.
