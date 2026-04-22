# Security Policy

## Reporting a Vulnerability

jinn is a sandboxed tool executor — security bugs (sandbox escape, path
traversal, symlink bypass, injection into `run_shell`) are treated as the
highest-priority issues.

**Please report privately** via one of:

- GitHub Security Advisories: https://github.com/dotcommander/jinn/security/advisories/new
- Email: brujah@gmail.com

Please do not open public issues for suspected vulnerabilities until a fix
is available.

## Supported Versions

Only the latest minor version receives security fixes. See [CHANGELOG.md](CHANGELOG.md).

## Scope

In scope:
- Path escape via symlinks, `..`, or absolute paths outside the workDir
- Sandbox escape via `run_shell` (env vars, PATH manipulation, shell metacharacters)
- Sensitive-path reads or writes (`.git/`, `.ssh/`, `.env*`, etc.)
- Denial of service via unbounded reads/writes/shell output

Out of scope:
- Issues in agents or apps that consume jinn — report to those projects.
- Behavior when running jinn outside its designed sandbox (e.g., as root, or against an adversarial filesystem).
