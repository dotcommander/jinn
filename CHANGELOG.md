# Changelog

All notable changes to jinn will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and jinn adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/dotcommander/jinn/compare/v0.8.9...HEAD
[0.8.9]: https://github.com/dotcommander/jinn/compare/v0.8.8...v0.8.9
[0.8.8]: https://github.com/dotcommander/jinn/compare/v0.8.7...v0.8.8
[0.3.2]: https://github.com/dotcommander/jinn/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/dotcommander/jinn/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/dotcommander/jinn/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/dotcommander/jinn/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/dotcommander/jinn/releases/tag/v0.1.0
