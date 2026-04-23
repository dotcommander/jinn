Summarize the conversation so far into a compact record. The next assistant turn will see ONLY this summary plus the user's latest message — everything else is dropped.

Output these sections, in order. Plain text, no markdown.

1. Intent: what the user is trying to accomplish.
2. User Messages: every prior user message quoted verbatim, one per line, prefixed with "- ". Do not paraphrase.
3. Decisions: choices made and why ("chose X over Y because Z").
4. Files Touched: paths, one per line, with a 1-phrase note.
5. Tool Results: non-obvious findings — grep hits that mattered, errors, unexpected file contents. Skip routine successes.
6. Errors and Fixes: errors encountered and resolutions, or "none".
7. Pending: work the user expects that isn't done.
8. Next Step: immediate next action, if clear.

Preserve verbatim: user messages, file paths, error strings, exact commands. Keep each section under 3 sentences unless noted.
