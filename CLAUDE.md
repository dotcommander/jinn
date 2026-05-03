# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

jinn is a sandboxed tool executor for AI coding agents. Single binary, zero external dependencies — stdlib only. It exposes 17 tools via a one-shot JSON-over-stdin/stdout protocol compatible with OpenAI function calling.

## Build / Test / Install

```bash
go build ./cmd/jinn/          # produces ./jinn
go test -race ./...            # 633 tests
go install github.com/dotcommander/jinn@latest
jinn --schema                  # emit tool definitions as JSON
jinn --version                 # version from ldflags or VCS info
echo '{"tool":"read_file","args":{"path":"go.mod"}}' | jinn
```

No linter config, no Makefile — intentionally minimal.

## Tools

| Tool | Description |
|------|-------------|
| `run_shell` | Bash with timeout (default 30s, max 300s), `dry_run` flag; process-group kill (`Setpgid`+`SIGKILL`) covers all child processes |
| `read_file` | Line-numbered output (`line_numbers`), windowing, `tail` arg, `truncate` strategy (`head`/`tail`/`middle`/`none`), 50 MB gate, content-based MIME detection for images, uniform truncation hint with remainder temp file |
| `write_file` | Atomic temp+rename, parent dir creation, TOCTOU check, `dry_run` preview |
| `edit_file` | old_text/new_text replacement, uniqueness enforcement, empty `old_text` guard, no-op guard, fuzzy fallback, `fuzzy_indent` re-indentation, `dry_run` preview, CRLF/BOM preservation |
| `multi_edit` | Array of edits across files — validates all first (empty `old_text` guard, no-op guard, overlap detection), applies atomically, same fuzzy/CRLF/BOM as edit_file |
| `apply_patch` | Codex-style patch format (`*** Begin Patch … *** End Patch`); supports add/delete/update-file operations |
| `search_files` | Grep/ripgrep search with regex validation, literal flag, include glob filter, context_lines, case_insensitive, max_matches (default 500), three formats (text/json/filenames), zero_match_reason diagnostics |
| `stat_file` | File metadata (size/lines/mtime/type) without reading content |
| `list_dir` | Recursive find with depth control, hidden files excluded, directories suffixed with `/` |
| `find_files` | Glob-pattern file search; uses `fd` when available (respects `.gitignore`), falls back to POSIX `find` |
| `list_tools` | Returns the JSON schema for all tools jinn exposes — same content as `jinn --schema`, but accessible in-protocol |
| `checksum_tree` | SHA-256 hashes for a file tree, with optional glob filter |
| `detect_project` | Detect language, framework, build/test/lint commands from config files |
| `memory` | Persistent key/value store at `~/.config/jinn/memory.json`; actions: `save`, `recall`, `list`, `forget` |
| `undo` | Snapshot history for all file mutations; actions: `list`, `preview`, `restore`, `clear` |
| `diff_files` | Unified diff between two files |
| `lsp_query` | Language server queries (gopls, rust-analyzer, pylsp, typescript-language-server, clangd, jdtls, lua-language-server, zls): `definition`, `references`, `hover`, `symbols`, `rename`; `symbol` name auto-detect for column resolution |

## Architecture

```
cmd/jinn/main.go                # ~110 lines: flags, signal, TTY detection, JSON I/O, wires to Engine
internal/jinn/
  command_risk.go                # RiskLevel type, riskTable, ClassifyCommand — command risk classifier for run_shell
  command_risk_parse.go          # splitOnOperators, containsSubshell, firstVerb — bash syntax parsing for risk classifier
  engine.go                      # Engine struct, New(workDir), Dispatch(), ResolveVersion()
  lsp_client.go                  # lspClient, lspLauncher — LSP JSON-RPC wire protocol, session lifecycle, result formatting
  schema.go                      # Schema const (OpenAI function-calling JSON) + Request/Response types
  search_parse.go                # searchResult, parseSearchResults, parseFilenamesOutput — structured grep output parsers
  security.go                    # (e) resolvePath (~/ expansion + sandbox check), checkPath, sensitiveSegments
  tracker.go                     # fileTracker struct — records mtime on read, blocks stale writes
  normalize.go                   # stripBom, detectLineEnding, normalizeToLF, restoreLineEndings, fuzzy match, closestLine
  output.go                      # truncateOutput, truncateTail, boundedWriter, collapseRepeatedLines, collapseBlankLines, truncateLine
  tool_shell.go                  # (e) runShell — Setpgid process-group kill, scrubbed env
  tool_read.go                   # (e) readFile + maxFileSize; line_numbers, tail, truncate strategy, content-based MIME detection, writeTruncationRemainder
  tool_write.go                  # (e) writeFile
  tool_edit.go                   # (e) editFile — exact + fuzzy, empty/no-op guards, CRLF/BOM preservation
  tool_multi_edit.go             # (e) multiEdit — validate-all-then-apply, overlap detection, empty/no-op guards
  tool_patch.go                  # (e) applyPatch — Codex-style patch format
  tool_search.go                 # (e) searchFiles — rg/grep backend, three output formats, classifyZeroMatch
  tool_stat.go                   # (e) statFile
  tool_list.go                   # (e) listDir — directory "/" suffix
  tool_find.go                   # (e) findFiles — fd/find glob search
  tool_checksum.go               # (e) checksumTree
  tool_detect.go                 # (e) detectProject
  tool_memory.go                 # (e) memory — persistent key/value store
  tool_undo.go                   # (e) undo — snapshot list/preview/restore/clear
  tool_diff.go                    # (e) diffFiles — unified diff between two files
  tool_lsp.go                    # (e) lspQuery — 8 language servers, symbol auto-detect, rename preview
  errors.go                      # error code constants + ErrWithSuggestion type (structured errors with codes and hints)
  diff.go                        # unifiedDiff, formatEditPreview
```

Key design:

- **Engine struct** absorbs all state (`workDir string`, `tracker *fileTracker`). No mutable package globals. Constructor: `New(workDir)`.
- **Dispatch** routes tool name → unexported method. Single entry point for callers.
- **Path security** (`resolvePath`/`checkPath`): Engine methods. `resolvePath` expands `~` and `~/` before joining to workDir. `checkPath` resolves symlinks, checks sensitive segments (`.git/`, `.ssh/`, `.aws/`, `.gnupg/`, `.env*`), and enforces the workDir boundary.
- **TOCTOU tracker**: Per-engine instance. Records mtime on read, blocks stale writes. No global state.
- **Text normalization**: Edit tools strip BOM, normalize CRLF→LF for matching, restore after edit. Fuzzy fallback normalizes smart quotes, dashes, spaces when exact match fails.
- **Output pipeline**: `collapseRepeatedLines` → `boundedWriter` (1 MB cap, spills to temp file) → `truncateTail` (shell) / `truncateOutput`/`truncateOutputHead`/`truncateOutputTail`/`truncateOutputDetailed` (read, driven by `truncate` param).
- **All tests parallel**: `testEngine(t)` returns isolated `(*Engine, string)` per test. No `os.Chdir`, no global reset.

## Design Constraints

- **Zero dependencies** — `go.mod` has no `require` block. Only stdlib imports.
- **Security first** — every file path must go through `resolvePath` → `checkPath` before use. New tools must follow this pattern.
- **No interactive I/O** — stdin is consumed as a single JSON blob; stdout is a single JSON response. The calling agent handles all user interaction.
- **Atomic writes** — all file mutations use temp+rename with error cleanup. Never write directly to the target path.
- **No mutable package globals** — `var version` lives in `cmd/jinn/main.go` (ldflags target). `internal/jinn/` has zero mutable globals.
