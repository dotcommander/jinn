# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

jinn is a sandboxed tool executor for AI coding agents. Single binary, zero external dependencies — stdlib only. It exposes 9 tools via a one-shot JSON-over-stdin/stdout protocol compatible with OpenAI function calling.

## Build / Test / Install

```bash
go build ./cmd/jinn/          # produces ./jinn
go test -race ./...            # 97 tests
go install github.com/dotcommander/jinn@latest
jinn --schema                  # emit tool definitions as JSON
jinn --version                 # version from ldflags or VCS info
echo '{"tool":"read_file","args":{"path":"go.mod"}}' | jinn
```

No linter config, no Makefile — intentionally minimal.

## Tools

| Tool | Description |
|------|-------------|
| `run_shell` | Bash with timeout (default 30s, max 300s), `dry_run` flag |
| `read_file` | Line-numbered output, windowing, 50 MB gate, binary detection |
| `write_file` | Atomic temp+rename, parent dir creation, TOCTOU check |
| `edit_file` | old_text/new_text replacement, uniqueness enforcement, fuzzy fallback, CRLF/BOM preservation |
| `multi_edit` | Array of edits across files — validates all first, applies atomically, same fuzzy/CRLF/BOM as edit_file |
| `search_files` | Grep with `--exclude-dir`, regex validation, per-line truncation |
| `stat_file` | File metadata (size/lines/mtime/type) without reading content |
| `list_dir` | Recursive find with depth control, hidden files excluded |
| `list_tools` | Returns the JSON schema for all tools jinn exposes — same content as `jinn --schema`, but accessible in-protocol |

## Architecture

```
cmd/jinn/main.go                # ~50 lines: flags, signal, JSON I/O, wires to Engine
internal/jinn/
  engine.go                      # Engine struct, New(workDir), Dispatch(), ResolveVersion()
  schema.go                      # Schema const (OpenAI function-calling JSON) + Request/Response types
  security.go                    # (e) resolvePath, checkPath, sensitiveSegments
  tracker.go                     # fileTracker struct — records mtime on read, blocks stale writes
  normalize.go                   # stripBom, detectLineEnding, normalizeToLF, fuzzy match, shellescape
  output.go                      # truncateOutput, truncateTail, boundedWriter, collapseRepeatedLines, truncateLine
  tool_shell.go                  # (e) runShell
  tool_read.go                   # (e) readFile + maxFileSize
  tool_write.go                  # (e) writeFile
  tool_edit.go                   # (e) editFile (exact + fuzzy, CRLF/BOM preservation)
  tool_multi_edit.go             # (e) multiEdit (validate-all-then-apply, same normalization)
  tool_search.go                 # (e) searchFiles + grepExcludeDirs
  tool_stat.go                   # (e) statFile
  tool_list.go                   # (e) listDir
```

Key design:

- **Engine struct** absorbs all state (`workDir string`, `tracker *fileTracker`). No mutable package globals. Constructor: `New(workDir)`.
- **Dispatch** routes tool name → unexported method. Single entry point for callers.
- **Path security** (`resolvePath`/`checkPath`): Engine methods. Confine all file ops to workDir. Block sensitive paths (`.git/`, `.ssh/`, `.aws/`, `.gnupg/`, `.env*`), detect symlink escapes, reject `..` traversal.
- **TOCTOU tracker**: Per-engine instance. Records mtime on read, blocks stale writes. No global state.
- **Text normalization**: Edit tools strip BOM, normalize CRLF→LF for matching, restore after edit. Fuzzy fallback normalizes smart quotes, dashes, spaces when exact match fails.
- **Output pipeline**: `collapseRepeatedLines` → `boundedWriter` (1 MB cap, spills to temp file) → `truncateTail` (shell) / `truncateOutput` (read/search).
- **All tests parallel**: `testEngine(t)` returns isolated `(*Engine, string)` per test. No `os.Chdir`, no global reset.

## Design Constraints

- **Zero dependencies** — `go.mod` has no `require` block. Only stdlib imports.
- **Security first** — every file path must go through `resolvePath` → `checkPath` before use. New tools must follow this pattern.
- **No interactive I/O** — stdin is consumed as a single JSON blob; stdout is a single JSON response. The calling agent handles all user interaction.
- **Atomic writes** — all file mutations use temp+rename with error cleanup. Never write directly to the target path.
- **No mutable package globals** — `var version` lives in `cmd/jinn/main.go` (ldflags target). `internal/jinn/` has zero mutable globals.
