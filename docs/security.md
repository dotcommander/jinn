# Security

jinn confines every file operation to the working directory. You cannot read
from, write to, or traverse into sensitive paths. Shell execution is disabled
unless an explicit shell mode is selected.

## Path Confinement

Accepted paths are normalized relative to a workspace `os.Root`, and I/O uses
root-relative handles rather than caller-supplied absolute strings. Explicit
reads may follow symlinks only when they remain in the root. Mutations reject
every symlink component, and recursive tools never follow directory symlinks.
Devices, sockets, FIFOs, and other non-regular read targets are rejected.

```bash
# This is blocked -- .ssh is a sensitive segment
echo '{"tool":"read_file","args":{"path":"../.ssh/id_rsa"}}' | jinn
```

```json
{"ok": false, "error": "blocked: sensitive path: ../.ssh/id_rsa"}
```

`..` traversal, symlink escapes, and absolute paths that point outside the working directory are all blocked. The working directory is the root of all file access.

## Sensitive Paths

`checkPath` rejects any path containing these segments:

| Segment | Reason |
|---------|--------|
| `.git` | Repository internals -- refs, hooks, config |
| `.ssh` | SSH keys and configuration |
| `.aws` | AWS credentials |
| `.gnupg` | GPG keyrings |
| `.env` | Environment variable files with secrets |
| `.env.*` | Variant environment files (e.g., `.env.production`) |

The check matches on path segments, so `src/.env` and `deploy/.env.staging` are both blocked regardless of depth.

## Mutation Preconditions and Locking

Mutation tools require an authoritative precondition: `if_checksum` (or an
`if_checksums` map) for an existing target and `if_absent:true` for creation.
SHA-256-keyed cross-process locks are held from the authoritative read through
durable snapshot creation, fsync, and atomic rename.

```bash
# Step 1: Read the file and obtain its checksum
echo '{"tool":"read_file","args":{"path":"config.yaml"}}' | jinn

# Step 2: Edit using that checksum
echo '{"tool":"edit_file","args":{"path":"config.yaml","old_text":"port: 8080","new_text":"port: 9090","if_checksum":"<sha256>"}}' | jinn
```

If another process modifies `config.yaml` between steps 1 and 2:

```json
{"ok": false, "error": "file modified since last read (mtime changed). Re-read before writing: config.yaml"}
```

Creation must instead state `"if_absent":true`. A stale precondition leaves the
target bytes unchanged.

## Atomic Writes

`write_file`, `edit_file`, and batch mutation tools all use the same per-file atomic write pattern:

1. Write content to a hidden temp file (`.jinn-*` prefix).
2. `chmod` to match existing file permissions (or use default for new files).
3. `fsync` the temp file to ensure durability.
4. `rename` the temp file to the target path.

```bash
echo '{"tool":"write_file","args":{"path":"data.json","content":"{\"status\":\"ok\"}\n","if_absent":true}}' | jinn
```

If the process crashes mid-write, the target file is never left in a partial state. The rename is atomic on all major filesystems. The temp file is cleaned up on error.

Batch mutation tools validate all inputs before writing, but they do not roll back earlier successful writes if a later per-file write fails.

## Shell Modes and Command Risk Classifier

`--shell-mode=disabled` is the default and removes `run_shell` from schema,
MCP registration, discovery, and nested plans. `sandboxed` uses
`/usr/bin/sandbox-exec` on macOS or an already-installed `bwrap` on Linux and
fails startup when that facility is unavailable. It denies network and user
configuration while allowing workspace and per-run temporary writes.
`--shell-mode=unsafe` is an explicit, prominently reported unconfined
compatibility mode. `force` never selects a mode or bypasses OS confinement.

Before executing any shell command, `run_shell` classifies it by examining the leading verb and flags:

| Level | Behavior | Examples |
|-------|----------|---------|
| `safe` | Executed normally | `ls`, `cat`, `grep`, `find`, `echo` |
| `caution` | Executed normally; modifies state | `cp`, `mv`, `mkdir`, `sed -i`, `curl`, unknown verbs |
| `dangerous` | **Blocked** unless `force: true` | `rm`, `dd`, `sudo`, pipe to `sh`/`bash`, inline-code/task/package runners (`awk`, `make`, `npx`, `pnpm dlx`, `bunx`) |

The `risk` field is always present in `run_shell` responses. Dangerous commands return an error with `risk: "dangerous"` and a `suggestion` unless `force: true` is set:

```json
{
  "ok": false,
  "error": "blocked by risk classifier: dangerous — removes files — irreversible",
  "suggestion": "pass force:true in args to override, or use a less-destructive command",
  "risk": "dangerous"
}
```

To override the block for a known-safe case:

```bash
echo '{"tool":"run_shell","args":{"command":"rm -rf /tmp/build-cache","force":true}}' | jinn
```

Pipelines return the maximum risk of any component (`cmd1 | cmd2` inherits the higher classification). Pipe-to-shell (`cmd | bash`) is always `dangerous`. Unknown verbs default to `caution`, not `safe`.

---

## Shell Environment Scrubbing

`run_shell` does not inherit your full shell environment. jinn scrubs the environment down to an allowlist before executing the command:

| Variable | Why it's kept |
|----------|---------------|
| `PATH` | Sanitized launch-time host search path containing only absolute existing directories |
| `HOME` | Synthetic per-run directory in `sandboxed`; allowlisted host value in `unsafe` |
| `GOMODCACHE` | Allowlisted host Go module cache in `unsafe`; omitted in `sandboxed` |
| `LANG` | Locale |
| `LC_ALL` | Locale override |
| `TERM` | Terminal capabilities |
| `USER` | Current username |
| `LOGNAME` | Login name |
| `TMPDIR` | Synthetic per-run directory in `sandboxed`; allowlisted host value in `unsafe` |
| `TZ` | Timezone |
| `SHELL` | Fixed `/bin/bash` in `sandboxed`; allowlisted host value in `unsafe` |

All other environment variables -- including any API keys, tokens, or secrets you have exported -- are removed before the command runs. This prevents accidental credential leakage through child processes.

## Output Bounds

jinn caps output to prevent unbounded memory growth:

| Boundary | Value | Applies To |
|----------|-------|------------|
| Shell output buffer | 1 MB | `run_shell` |
| Per-line truncation | Truncated at rune boundary + `...` | All tools |
| Repeated line collapse | 3+ identical consecutive lines collapsed | All tools |
| Shell tail truncation | Last N lines kept | `run_shell` |
| Read truncation | Configurable strategy (`head`/`tail`/`middle`/`none`/`smart`); default keeps first N lines. `smart` uses brace-depth heuristic for C-syntax files, cutting at block boundaries. Truncation hint appended: `[Showing lines X-Y of Z. Use start_line=N to continue. Remainder saved to <path>.]` | `read_file` |
| File size limit | 50 MB | `read_file` |

When shell output exceeds 1 MiB, it spills to a temp file
(`jinn-shell-*.log`). Capture is limited to 16 MiB total; exceeding that kills
the process group and reports `resource_limit`. Expired spill files are deleted,
not merely removed from the registry.

The repeated line collapse replaces 3 or more identical consecutive output lines with `[... N identical lines collapsed ...]`. This keeps build output and log dumps readable without losing the line count.

## Special File Reads

`read_file` applies type-specific handling before returning content:

| File type | Behavior |
|-----------|---------|
| `.pdf` | Returns `ok: false` with `suggestion: "convert the PDF to text first (pdftotext, pdftk, or a cloud OCR service) and read the text file"` |
| Images | Detected by content (via `http.DetectContentType` on the first 512 bytes) rather than extension alone. A PNG renamed without an extension is still identified and handled correctly. Returns a base64-encoded content block with the detected MIME type. SVG files (which read as `text/xml` by the content detector) fall back to extension-based detection and return `image/svg+xml`. |
| Binary (null byte in first 512 bytes) | Returns `[binary file: N bytes — use stat_file for metadata or skip content reads]` (success, not error) |

## Memory Persistence

The `memory` tool stores its data in a SQLite database at `~/Library/Application Support/jinn/memory.db` on macOS (`~/.config/jinn/memory.db` on Linux), or at `$JINN_CONFIG_DIR/jinn/memory.db` when that env var is set. The directory is created with mode `0700`. Writes use WAL journaling with a 5s busy_timeout, providing cross-process safety so concurrent jinn invocations cannot corrupt the store. Keys are isolated per project scope.

---

## Summary

| Mechanism | Scope | Configurable |
|-----------|-------|-------------|
| Path confinement | All file tools | No |
| Sensitive path blocking | All file tools | No |
| Checksums and cross-process target locks | All mutation tools | No |
| Atomic writes | Per-file writes in mutation tools | No |
| Shell mode | `run_shell` | `disabled`, `sandboxed`, or explicit `unsafe` |
| Risk classifier | `run_shell` | `force: true` overrides dangerous block |
| Output bounds | All tools | No |
| Memory DB directory permissions | `memory` | `$JINN_CONFIG_DIR` relocates storage |

Security in jinn is enforced at the engine level. Rooted access, sensitive-path
blocking, mutation preconditions, locks, and environment scrubbing have no
`force` bypass. The classifier override is advisory and does not enable shell
execution or convert sandboxed execution to unsafe execution.
