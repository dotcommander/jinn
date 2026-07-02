# Changelog

All notable changes to jinn will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and jinn adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.11.0] - 2026-07-02

### Added

- `run_plan` tool: condition-gated plan tree execution — a deterministic
  in-process walk of a `PlanTree` connected by first-match-wins conditional
  edges (`exitCode`, `fileExists`, `jsonPath`, `numeric`, `match`, `always`).
  Phase 1 nodes are read-only (safe shell + allowlisted tools only); Phase 2
  `mutates: true` nodes risk-gate mutations (`caution` runs automatically,
  `dangerous` requires both plan- and node-level `force`). Completed runs
  fire-and-forget a stats row to `~/.config/jinn/stats/run_plan.jsonl`.
- `if_checksum` precondition on `write_file`/`edit_file`: rejects a stale
  overwrite when the file changed since the checksum was read, returning
  `error_code: "stale_file"` so the caller can re-read and retry.
- Cross-process `flock` guard for the history/spill registry read-modify-write,
  replacing an in-process mutex that missed races between concurrent `jinn`
  shells.
- Double-encoded args in the request envelope are now coerced automatically.

### Fixed

- `search_replace`/`multi_edit`/`apply_patch` partial-apply errors enumerate
  already-written files with undo ids so a mid-batch failure can be recovered
  without reapplying successful writes.
- Risk classifier treats `[[ ]]` and `(( ))` comparisons as non-redirection.
- `TestRouteToolsCorpus` paraphrase routing for `search_files`.
- Same-mtime-tick file modifications are now detected by size in the tracker.
- Pre-redesign memory tables are additively migrated (missing columns added)
  and rebuilt without losing existing rows.

### Documentation

- `if_checksum` precondition and partial-apply error shape documented in
  AGENTS.md.
- Memory DB path corrected to `os.UserConfigDir` (macOS Library path).
- `find` exit-code doc corrected — a nonzero exit is always an error.
- README first-use guide improved.

## [0.10.0] - 2026-06-23

### Added

- `jinn_route` (MCP): routing corpus and intent rules covering tools that were
  previously unrouted, so a natural-language "need" maps to the right existing
  tools.
- Inspector web UI (`--inspect`): a jinn-themed interface for browsing the
  available tools and the live schema.

### Fixed

- Unify the binary-detection window to 8192 bytes on the read path for
  consistent file classification.

### Documentation

- Harness integration guide (Claude Code, Codex, and custom agent loops) and a
  pi-extension bridge example.

## [0.9.2] - 2026-06-04

### Removed

- `checksum_tree` tool: low-value for coding agents; `git` and
  `list_dir` (`changed_since`) cover its use cases.
- `related_context` tool and its `config.json` (`related_context.paths`)
  support: value was gated on a local KB/skills corpus that most callers
  lack. The request envelope `client` field is now inert.

### Changed

- Internal refactor pass: split files exceeding the 300-line tripwire (edit,
  `multi_edit`, `read`, `search`, `search_replace`, `lsp`, `compress`, `patch`,
  `output`) and decomposed high-cognitive-complexity functions; consolidated
  duplicate detection/edit/read helpers.

### Fixed

- Escape regex metacharacters in `globToRegex`.
- Make `lspClient.stop` idempotent via `sync.Once`.
- Validate blob paths stay within the history store before reading.
- Harden the LSP client against hostile or buggy language servers.
- Set structured error `Code` on symlink-outside-sandbox and path/LSP/undo
  errors.

## [0.9.1] - 2026-05-30

### Fixed

- Block symlink escape writes when the path crosses a symlink before a missing
  child directory.
- Preserve the actual `run_shell` response tail and full spill file for output
  larger than the in-memory capture limit.
- Initialize LSP sessions with explicit workspace folders so `gopls`
  diagnostics load the full package context.
- Include `risk` and `classification` metadata on `run_shell` dry-run
  responses.

## [0.9.0] - 2026-05-29

### Changed

- Migrate the `memory` tool from a single flat `memory.json` file to a
  per-project scoped SQLite store (WAL mode, `0700` directory permissions),
  enabling safe cross-process concurrent access.
- Memories are now namespaced by scope: omit `scope` to target the current
  project (resolved by walking up to the nearest `.git` ancestor, falling back
  to the working directory), or pass `scope: "global"` for cross-project
  memories.

### Added

- Optional `scope` parameter on `memory` actions.
- Automatic one-time migration of legacy `memory.json` entries into the
  reserved `global` scope on first run.

### Removed

- The 1 MiB total-store cap on the `memory` tool (no longer needed with the
  SQLite-backed store).

## [0.8.14] - 2026-05-29

### Fixed

- Preserve cancellation semantics for subprocess-backed `search_files`,
  `find_files`, and `search_replace` glob expansion.
- Distinguish stalled `find_files` walks from empty no-match results.
- Reject blank `run_shell` commands with a structured invalid-args error.
- Ignore Justfile assignment lines when detecting `just build`, `just test`,
  and `just lint` recipes.

### Changed

- Derive `list_tools` names from the embedded schema to avoid drift.
- Move the OpenAI tool schema into embedded `schema.json`.
- Prefer committed Justfile recipes during project detection.

### Documentation

- Sync README, getting-started, tool reference, and architecture docs to the
  current 20-tool surface.

### Removed

- Remove the obsolete demo module and keep `CLAUDE.md` local-only.

## [0.8.9] - 2026-05-15

### Added

- `related_context`: rank local KB, skills, agents, commands, and configured
  context paths for a prompt or tool failure.
- Request envelope `client` field so callers can declare `claude`, `codex`, or
  `pi` and receive only the matching client-specific skill context.

### Changed

- Store `related_context` indexes in per-client cache files to avoid rebuild
  churn when multiple clients use jinn on the same machine.

## [0.8.8] - 2026-05-15

### Fixed

- `apply_patch`: reject `*** Add File:` when the target already exists, including dry runs.
- `search_replace`: expand documented glob targets in `files`.
- `read_file`, `multi_read`: treat empty text files as successful empty reads.

### Documentation

- Document validate-first/per-file atomic write semantics for batch mutation tools.
- Update README and tool reference coverage for the current 19-tool schema.

### Added

- `read_file`: `truncate` parameter — strategy when windowed output exceeds the line limit: `head` (default, paginate with `start_line`), `tail` (keep last N, useful for logs), `middle` (keep both ends, elide center), `none` (defer to byte cap only)
- `read_file`: `line_numbers` parameter — set `false` to receive raw content without line-number prefixes (default: `true`)
- `read_file`: content-based MIME detection via `http.DetectContentType` — images without a recognized extension (e.g., a PNG renamed to no extension) are now correctly identified and returned as base64 content blocks
- `read_file`: uniform truncation hint — `[Showing lines X-Y of Z. Use start_line=N to continue. Remainder saved to <path>.]` — remainder written to an XDG cache temp file for seamless continuation
- `run_shell`: native process-group kill via `Setpgid: true` + `syscall.Kill(-pgid, SIGKILL)` — background children spawned by the command are killed on timeout; no external `timeout` binary required
- `edit_file`, `multi_edit`: empty `old_text` guard — returns an error with a suggestion rather than silently matching the empty string everywhere
- `edit_file`, `multi_edit`: no-op edit guard — returns an error when `old_text` and `new_text` are equivalent (including after fuzzy normalization)
- `multi_edit`: overlap detection — edits targeting overlapping byte ranges in the same file are caught in the validation phase; error names the conflicting edit indices
- `search_files`: `literal` flag — treats `pattern` as a fixed string rather than a regex (passes `-F` to grep / `--fixed-strings` to rg)
- `list_dir`: directories now suffixed with `/` in the `entries` array to distinguish them from files
- `security.resolvePath`: `~` and `~/` prefix expansion — paths beginning with `~` resolve to the user home directory before sandbox boundary checks

## [0.3.2] - 2026-04-18

### Added
- LICENSE (MIT), CHANGELOG.md, expanded `.gitignore` for public release
- `docs/architecture.yaml` — repoflow-rendered architecture diagram source

## [0.3.1] - 2026-04-10

### Fixed

- Detect TTY and print help instead of blocking on stdin
- Whitelist shell subprocess environment variables to prevent leaking secrets

## [0.3.0] - 2026-04-08

### Fixed

- Call fsync before rename to ensure atomic writes survive crashes
- Preserve original file permissions on write
- Prefer `rg` over `grep` for `search_files` when available

## [0.2.0] - 2026-04-08

### Added

- `list_tools` endpoint and structured error semantics
- Symlink-safe work directory initialization

### Fixed

- Resolve symlinks before boundary check to prevent path traversal via symlinks

## [0.1.0] - 2026-04-08

### Added

- Initial release: sandboxed tool executor for AI coding agents
- 8 tools: `run_shell`, `read_file`, `write_file`, `edit_file`, `multi_edit`, `search_files`, `stat_file`, `list_dir`
- Path confinement with symlink escape detection and sensitive path blocking (`.git/`, `.ssh/`, `.aws/`, `.gnupg/`, `.env*`)
- Text normalization: BOM stripping, CRLF handling, fuzzy matching for edit operations
- Output pipeline: repeated line collapse, bounded writer (1 MB cap), truncation
- Atomic writes with TOCTOU protection (temp file + rename)
- Zero dependencies — stdlib only, single binary
- OpenAI function calling compatible JSON-over-stdin/stdout protocol
- `--schema` flag to emit tool definitions
- `--version` flag with ldflags and VCS fallback

[Unreleased]: https://github.com/dotcommander/jinn/compare/v0.9.1...HEAD
[0.9.1]: https://github.com/dotcommander/jinn/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/dotcommander/jinn/compare/v0.8.14...v0.9.0
[0.8.14]: https://github.com/dotcommander/jinn/compare/v0.8.13...v0.8.14
[0.8.9]: https://github.com/dotcommander/jinn/compare/v0.8.8...v0.8.9
[0.8.8]: https://github.com/dotcommander/jinn/compare/v0.8.7...v0.8.8
[0.3.2]: https://github.com/dotcommander/jinn/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/dotcommander/jinn/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/dotcommander/jinn/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/dotcommander/jinn/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/dotcommander/jinn/releases/tag/v0.1.0
