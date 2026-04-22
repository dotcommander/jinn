# Changelog

All notable changes to jinn will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and jinn adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/dotcommander/jinn/compare/v0.3.2...HEAD
[0.3.2]: https://github.com/dotcommander/jinn/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/dotcommander/jinn/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/dotcommander/jinn/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/dotcommander/jinn/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/dotcommander/jinn/releases/tag/v0.1.0
