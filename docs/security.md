# Security

jinn confines every file operation to the working directory. You cannot read from, write to, or traverse into sensitive paths. There is no disable flag -- security is always on.

## Path Confinement

Every file path goes through two checks before any I/O:

1. **`resolvePath`** joins the path to the working directory and calls `filepath.Clean`. Symlinks in the working directory itself are resolved.
2. **`checkPath`** resolves any symlinks in the requested path, verifies no sensitive segments are present, and confirms the final path stays within the working directory boundary.

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

## TOCTOU Protection

jinn tracks file modification times to prevent **time-of-check-to-time-of-use** races. When you read a file, jinn records its mtime. When you write or edit that file, jinn checks whether the mtime has changed since the read. If it has, the write is rejected.

```bash
# Step 1: Read the file (jinn records mtime)
echo '{"tool":"read_file","args":{"path":"config.yaml"}}' | jinn

# Step 2: Edit the file (jinn verifies mtime hasn't changed)
echo '{"tool":"edit_file","args":{"path":"config.yaml","old_text":"port: 8080","new_text":"port: 9090"}}' | jinn
```

If another process modifies `config.yaml` between steps 1 and 2:

```json
{"ok": false, "error": "file modified since last read (mtime changed). Re-read before writing: config.yaml"}
```

**Exceptions:** New files (never read) and deleted files (stat fails) bypass the TOCTOU check. You can always create a new file or overwrite a deleted one.

The TOCTOU tracker is per-engine instance. Each `jinn` process starts fresh -- there is no global state persisted between invocations.

## Atomic Writes

`write_file`, `edit_file`, and `multi_edit` all use the same atomic write pattern:

1. Write content to a hidden temp file (`.jinn-*` prefix).
2. `chmod` to match existing file permissions (or use default for new files).
3. `fsync` the temp file to ensure durability.
4. `rename` the temp file to the target path.

```bash
echo '{"tool":"write_file","args":{"path":"data.json","content":"{\"status\":\"ok\"}\n"}}' | jinn
```

If the process crashes mid-write, the target file is never left in a partial state. The rename is atomic on all major filesystems. The temp file is cleaned up on error.

## Command Risk Classifier

Before executing any shell command, `run_shell` classifies it by examining the leading verb and flags:

| Level | Behavior | Examples |
|-------|----------|---------|
| `safe` | Executed normally | `ls`, `cat`, `grep`, `find`, `echo` |
| `caution` | Executed normally; modifies state | `cp`, `mv`, `mkdir`, `sed -i`, `curl`, unknown verbs |
| `dangerous` | **Blocked** unless `force: true` | `rm`, `dd`, `sudo`, `kill`, `shutdown`, pipe to `sh`/`bash` |

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
| `PATH` | Finds executables |
| `HOME` | User home directory |
| `LANG` | Locale |
| `LC_ALL` | Locale override |
| `TERM` | Terminal capabilities |
| `USER` | Current username |
| `LOGNAME` | Login name |
| `TMPDIR` | Temp directory |
| `TZ` | Timezone |
| `SHELL` | Shell path |

All other environment variables -- including any API keys, tokens, or secrets you have exported -- are removed before the command runs. This prevents accidental credential leakage through child processes.

## Output Bounds

jinn caps output to prevent unbounded memory growth:

| Boundary | Value | Applies To |
|----------|-------|------------|
| Shell output buffer | 1 MB | `run_shell` |
| Per-line truncation | Truncated at rune boundary + `...` | All tools |
| Repeated line collapse | 3+ identical consecutive lines collapsed | All tools |
| Shell tail truncation | Last N lines kept | `run_shell` |
| Read/search truncation | Head 25% + tail 25% with omitted count | `read_file`, `search_files` |
| File size limit | 50 MB | `read_file` |

When shell output exceeds 1 MB, it spills to a temp file (`jinn-shell-*.log`). jinn keeps the tail of the output so you always see the exit code and final lines.

The repeated line collapse replaces 3 or more identical consecutive output lines with `[... N identical lines collapsed ...]`. This keeps build output and log dumps readable without losing the line count.

## Special File Reads

`read_file` applies type-specific handling before returning content:

| File type | Behavior |
|-----------|---------|
| `.pdf` | Returns `ok: false` with `suggestion: "convert the PDF to text first (pdftotext, pdftk, or a cloud OCR service) and read the text file"` |
| Images (`.png`, `.jpg`, `.jpeg`, `.gif`, `.webp`, `.svg`, `.bmp`) | Returns `data:<mime>;base64,<payload>` so callers can pass it directly to a vision model. MIME normalization: `.jpg` → `image/jpeg`, `.svg` → `image/svg+xml`. |
| Binary (null byte in first 512 bytes) | Returns `[binary file: N bytes — use checksum_tree for integrity or skip content reads]` (success, not error) |

## Memory Persistence

The `memory` tool stores its file at `~/.config/jinn/memory.json` (or `$JINN_CONFIG_DIR/jinn/memory.json` when the env var is set). The directory is created with mode `0700`. The file is written with mode `0600` via atomic temp+rename, so partial writes cannot corrupt the store.

---

## Summary

| Mechanism | Scope | Configurable |
|-----------|-------|-------------|
| Path confinement | All file tools | No |
| Sensitive path blocking | All file tools | No |
| TOCTOU tracking | `read_file` records, `write_file`/`edit_file`/`multi_edit` enforce | No |
| Atomic writes | `write_file`, `edit_file`, `multi_edit` | No |
| Environment scrubbing | `run_shell` | No |
| Risk classifier | `run_shell` | `force: true` overrides dangerous block |
| Output bounds | All tools | No |
| Memory file permissions | `memory` | `$JINN_CONFIG_DIR` relocates storage |

Security in jinn is enforced at the engine level. Path confinement, sensitive path blocking, TOCTOU tracking, and environment scrubbing have no bypass. The risk classifier has one intentional override (`force: true`) for callers that have verified the command is safe for their context.
