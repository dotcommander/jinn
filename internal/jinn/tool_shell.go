package jinn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// shellAllowList is the set of environment variables passed to shell subprocesses.
// The list is an explicit allowlist (not a denylist) so the default for any
// unrecognized host variable is "excluded". Before adding an entry, decide which
// category it falls into:
//
//	(a) Included — minimal-unix essentials. These are non-secret and required for
//	    commands to run correctly: PATH (binary resolution), HOME (home dir),
//	    LANG/LC_ALL/TZ (locale & time formatting), TERM (terminal capabilities),
//	    USER/LOGNAME (identity for tools that branch on it), TMPDIR (scratch space),
//	    SHELL (shell selection). Add a var here only if it is non-secret AND a
//	    common command will misbehave without it.
//
//	(b) Intentionally EXCLUDED — credential-bearing patterns. Never add anything
//	    matching API keys, tokens, secrets, or auth material (e.g. *_API_KEY,
//	    *_TOKEN, *_SECRET, AWS_*, GITHUB_TOKEN, OPENAI_API_KEY, SSH_AUTH_SOCK,
//	    GPG_*). The whole point of this allowlist is to keep host secrets out of
//	    subprocesses; a single such addition defeats it.
//
//	(c) Intentionally EXCLUDED — non-essential XDG / convenience dirs. Variables
//	    like XDG_CONFIG_HOME, XDG_CACHE_HOME, XDG_DATA_HOME, XDG_RUNTIME_DIR are
//	    omitted because they are not required for correctness and can leak host
//	    paths / state into the subprocess. Omit unless a concrete need is proven.

func (e *Engine) shellProcessEnv(homeDir, tmpDir string) []string {
	if e.shellMode == ShellModeUnsafe {
		return e.subprocessEnv()
	}
	path := "/usr/bin:/bin:/usr/sbin:/sbin"
	if len(e.execPath) > 0 {
		path = strings.Join(e.execPath, string(os.PathListSeparator))
	}
	env := []string{
		"PATH=" + path,
		"HOME=" + homeDir,
		"TMPDIR=" + tmpDir,
		"SHELL=/bin/bash",
	}
	for _, key := range []string{"LANG", "LC_ALL", "TERM", "TZ"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func (e *Engine) shellArgv(command, runDir, homeDir, tmpDir string) []string {
	if e.shellMode == ShellModeUnsafe {
		return []string{"/bin/bash", "-c", command}
	}
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		readFilter := `(require-all (require-not (subpath "/etc")) (require-not (subpath "/private/etc"))`
		if home != "" {
			readFilter += ` (require-not (subpath ` + strconv.Quote(home) + `))`
		}
		readFilter += `)`
		profile := `(version 1)(deny default)(allow process-exec)(allow process-fork)` +
			`(allow sysctl-read)(allow file-read-metadata)` +
			`(allow file-read* ` + readFilter + `)` +
			`(allow file-read* (subpath ` + strconv.Quote(e.workDir) + `) (subpath ` + strconv.Quote(runDir) + `))` +
			`(allow file-write* (subpath ` + strconv.Quote(e.workDir) + `) (subpath ` + strconv.Quote(runDir) + `))`
		return []string{e.sandboxBinary, "-p", profile, "/bin/bash", "-c", command}
	}
	args := []string{e.sandboxBinary, "--die-with-parent", "--new-session", "--unshare-net"}
	for _, path := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64"} {
		if _, err := os.Stat(path); err == nil {
			args = append(args, "--ro-bind", path, path)
		}
	}
	return append(args,
		"--proc", "/proc", "--dev", "/dev",
		"--bind", e.workDir, e.workDir,
		"--bind", tmpDir, tmpDir,
		"--bind", homeDir, homeDir,
		"--chdir", e.workDir,
		"/bin/bash", "-c", command,
	)
}

// runShell executes a shell command and returns (result, meta, error).
// Meta keys: "risk" (pre-execution risk level) and "classification" (exit-code class).
// Dangerous commands are blocked unless args["force"] is true.
//
//nolint:funlen,gocyclo // request validation and command lifecycle share one response contract.
func (e *Engine) runShell(ctx context.Context, args map[string]interface{}) (string, map[string]any, error) {
	if e.shellMode == ShellModeDisabled {
		return "", nil, &ErrWithSuggestion{
			Err:        errors.New("run_shell is disabled"),
			Suggestion: "restart jinn with --shell-mode=sandboxed, or explicitly choose --shell-mode=unsafe",
			Code:       ErrCodeCommandBlocked,
		}
	}
	cmd, _ := args["command"].(string)
	if strings.TrimSpace(cmd) == "" {
		return "", nil, &ErrWithSuggestion{
			Err:        errors.New("command is required"),
			Suggestion: "provide a non-empty shell command",
			Code:       ErrCodeInvalidArgs,
		}
	}
	// Classify before dry-run so the response envelope always includes risk metadata.
	riskLevel, riskReason := ClassifyCommand(cmd)
	// Launch policy gate (opt-in global config): evaluated before dry-run and
	// execution, and before the dangerous-command force check, so deny always
	// blocks and ask/unmatched block fail-closed (one-shot has no confirm UI).
	// Allow proceeds to the existing classifier; absent policy is a no-op.
	if e.launchPolicy != nil {
		switch decision := e.launchPolicy.evaluate(cmd); decision {
		case launchAllow:
			// Explicit allow: proceeds to the classifier and execution below.
		case launchDeny:
			return "", map[string]any{
					"risk":          riskLevel.String(),
					"launch_policy": string(decision),
				}, &ErrWithSuggestion{
					Err:        fmt.Errorf("blocked by launch policy: %s", cmd),
					Suggestion: "remove or change the deny rule in the global launch policy file, or use a different command",
					Code:       ErrCodeCommandBlocked,
				}
		case launchAsk:
			return "", map[string]any{
					"risk":          riskLevel.String(),
					"launch_policy": string(decision),
				}, &ErrWithSuggestion{
					Err:        fmt.Errorf("launch policy requires confirmation for: %s", cmd),
					Suggestion: "add an allow rule for this exact command to the global launch policy file, or run it through a host that can confirm",
					Code:       ErrCodeCommandBlocked,
				}
		}
	}
	if boolArg(args, "dry_run") {
		meta := map[string]any{
			"risk":           riskLevel.String(),
			"classification": string(ClassSuccess),
			"timeout_ms":     int64(intArg(args, "timeout", 30) * 1000),
			"duration_ms":    int64(0),
			"exit_code":      0,
		}
		if e.launchPolicy != nil {
			meta["launch_policy"] = string(e.launchPolicy.evaluate(cmd))
		}
		return fmt.Sprintf("[dry-run] would execute: %s", cmd), meta, nil
	}

	// Block dangerous commands unless force=true.
	if riskLevel == RiskDangerous {
		if force, _ := args["force"].(bool); !force {
			return "", map[string]any{
					"risk": riskLevel.String(),
				}, &ErrWithSuggestion{
					Err:        fmt.Errorf("blocked by risk classifier: %s — %s", riskLevel, riskReason),
					Suggestion: `pass force:true in args to override, or use a less-destructive command`,
					Code:       ErrCodeCommandBlocked,
				}
		}
	}

	timeout := intArg(args, "timeout", 30)
	if timeout > 300 {
		timeout = 300
	}

	res := e.executeShellCommand(ctx, cmd, timeout)

	argv0 := extractArgv0(cmd)
	class, reason := classifyExitCode(argv0, res.exitCode)
	meta := map[string]any{
		"risk":           riskLevel.String(),
		"classification": string(class),
		"stdout":         res.stdout,
		"stderr":         res.stderr,
		"exit_code":      res.exitCode,
		"timeout_ms":     int64(timeout * 1000),
		"duration_ms":    res.durationMs,
		"shell_mode":     string(e.shellMode),
	}
	if e.launchPolicy != nil {
		meta["launch_policy"] = string(e.launchPolicy.evaluate(cmd))
	}
	if e.shellMode == ShellModeUnsafe {
		meta["shell_security"] = "unconfined"
	}
	if res.resourceLimited {
		meta["classification"] = "resource_limit"
		return "", meta, &ErrWithSuggestion{
			Err:        errShellOutputLimit,
			Suggestion: "reduce command output or redirect it to a bounded workspace file",
			Code:       ErrCodeResourceLimit,
		}
	}
	if res.canceled {
		return "", meta, &ErrWithSuggestion{
			Err:        errors.New("run_shell canceled"),
			Suggestion: "retry the command if cancellation was unintended",
			Code:       ErrCodeCanceled,
		}
	}

	// Expected-nonzero exits return a success envelope (output + annotation)
	// rather than an error, so the LLM sees the command's output alongside
	// the classification and does not misinterpret a semantic non-zero as failure.
	output := fmt.Sprintf("[exit: %d]\n%s", res.exitCode, res.content)
	result := fmt.Sprintf("%s\n[classification: %s — %s]", output, class, reason)

	if hint := matchStderrHint(res.stderr); hint != "" {
		result += fmt.Sprintf("\n[hint: %s]", hint)
	}

	return result, meta, nil
}
