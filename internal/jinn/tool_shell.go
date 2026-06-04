package jinn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
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
var shellAllowList = []string{"PATH", "HOME", "LANG", "LC_ALL", "TERM", "USER", "LOGNAME", "TMPDIR", "TZ", "SHELL"} // tunable: config candidate

// shellEnv returns a minimal environment for shell commands, preventing
// accidental leakage of host secrets (API keys, credentials) to the subprocess.
func shellEnv() []string {
	env := make([]string, 0, len(shellAllowList))
	for _, key := range shellAllowList {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	return env
}

// waitExitCode waits for c and returns its exit code: the process exit code for
// a normal exit error, or 1 for any non-ExitError failure.
func waitExitCode(c *exec.Cmd) int {
	if err := c.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

// runWithTimeout starts c, kills its process group after timeout seconds, and
// returns the exit code: 1 if start fails, 124 on timeout, else the wait code.
func runWithTimeout(c *exec.Cmd, timeout int) int {
	if err := c.Start(); err != nil {
		return 1
	}
	pgid := c.Process.Pid // bash is the group leader (Setpgid=true)
	timer := time.AfterFunc(time.Duration(timeout)*time.Second, func() {
		// Negative pgid targets the whole process group.
		syscall.Kill(-pgid, syscall.SIGKILL) //nolint:errcheck
	})
	exitCode := waitExitCode(c)
	// Stop returns false when the timer already fired.
	if !timer.Stop() {
		return 124 // preserves "timed out after N seconds" message
	}
	return exitCode
}

// shapeShellOutput compresses, truncates and frames captured output, appending a
// spill/truncation annotation and a timeout note (when exitCode is 124).
func shapeShellOutput(capture *shellOutputCapture, cmd string, exitCode, timeout int) string {
	raw := collapseRepeatedLines(capture.String())
	raw = collapseBlankLines(raw, 3)
	// Apply command-aware compression before framing (compress_shell.go dispatches on
	// the last pipeline segment's verb, then falls through to the generic strategy chain).
	raw = compressShellOutput(raw, cmd)

	// Apply tail truncation with line + byte limits (matching pi conventions).
	content, trunc := truncateTailDetailed(raw, DefaultMaxLines, DefaultMaxBytes)
	if capture.Truncated() {
		trunc.TotalBytes = capture.TotalBytes()
		trunc.TotalLines = capture.TotalLines()
	}

	if capture.Truncated() || trunc.Truncated {
		spill := ""
		if tmpPath := capture.EnsureSpill(); tmpPath != "" {
			spill = ". Full output: " + tmpPath
		}
		content += fmt.Sprintf(
			"\n\n[Showing %d of %d lines (%s of %s)%s]",
			trunc.OutputLines, trunc.TotalLines,
			formatSize(trunc.OutputBytes), formatSize(trunc.TotalBytes),
			spill,
		)
	}

	if exitCode == 124 {
		content += fmt.Sprintf("\n\nCommand timed out after %d seconds", timeout)
	}
	return content
}

// runShell executes a shell command and returns (result, meta, error).
// Meta keys: "risk" (pre-execution risk level) and "classification" (exit-code class).
// Dangerous commands are blocked unless args["force"] is true.
func (e *Engine) runShell(ctx context.Context, args map[string]interface{}) (string, map[string]string, error) {
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
	if dryRun, ok := args["dry_run"].(bool); ok && dryRun {
		return fmt.Sprintf("[dry-run] would execute: %s", cmd), map[string]string{
			"risk":           riskLevel.String(),
			"classification": string(ClassSuccess),
		}, nil
	}

	// Block dangerous commands unless force=true.
	if riskLevel == RiskDangerous {
		if force, _ := args["force"].(bool); !force {
			return "", map[string]string{"risk": riskLevel.String()}, &ErrWithSuggestion{
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

	// Always use a process group so SIGKILL reaches background processes too.
	// exec.CommandContext only kills the direct child; our timer kills -pgid.
	// nil ctx is a caller bug — guard with panic so it surfaces in tests rather
	// than masking parent cancellation in production.
	if ctx == nil {
		panic("runShell: nil ctx")
	}
	c := exec.CommandContext(ctx, "bash", "-c", cmd) //nolint:gosec // G204: the shell tool intentionally executes agent-provided commands (its documented purpose)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	capture := newShellOutputCapture(1 << 20) // 1 MB response tail + full spill on overflow
	defer capture.Close()
	outBuf := &boundedWriter{limit: 1 << 20} // 1 MB stdout meta buffer
	errBuf := &boundedWriter{limit: 1 << 20} // 1 MB stderr meta buffer
	c.Env = shellEnv()
	c.Stdout = io.MultiWriter(capture, outBuf)
	c.Stderr = io.MultiWriter(capture, errBuf)

	exitCode := runWithTimeout(c, timeout)
	content := shapeShellOutput(capture, cmd, exitCode, timeout)

	argv0 := extractArgv0(cmd)
	class, reason := classifyExitCode(argv0, exitCode)

	// Expected-nonzero exits return a success envelope (output + annotation)
	// rather than an error, so the LLM sees the command's output alongside
	// the classification and does not misinterpret a semantic non-zero as failure.
	output := fmt.Sprintf("[exit: %d]\n%s", exitCode, content)
	result := fmt.Sprintf("%s\n[classification: %s — %s]", output, class, reason)

	if hint := matchStderrHint(errBuf.String()); hint != "" {
		result += fmt.Sprintf("\n[hint: %s]", hint)
	}

	meta := map[string]string{
		"risk":           riskLevel.String(),
		"classification": string(class),
		"stdout":         outBuf.String(),
		"stderr":         errBuf.String(),
	}
	return result, meta, nil
}
