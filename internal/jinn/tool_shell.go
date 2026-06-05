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

// subprocessEnv returns a minimal environment for subprocess tools, preventing
// accidental leakage of host secrets (API keys, credentials). Extra values are
// explicit per-tool overlays, such as a dedicated Go build cache.
func subprocessEnv(extra map[string]string) []string {
	env := make([]string, 0, len(shellAllowList)+len(extra))
	for _, key := range shellAllowList {
		if v, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+v)
		}
	}
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	return env
}

// shellEnv preserves the run_shell helper name for tests and callers while
// sharing the same policy with LSP subprocesses.
func shellEnv() []string {
	return subprocessEnv(nil)
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

const shellCanceledExitCode = 130

type shellRunResult struct {
	exitCode int
	canceled bool
}

// runWithTimeout starts c, kills its process group after timeout seconds or
// parent cancellation, waits for cleanup, and returns the exit status.
func runWithTimeout(ctx context.Context, c *exec.Cmd, timeout int) shellRunResult {
	if err := ctx.Err(); err != nil {
		return shellRunResult{exitCode: shellCanceledExitCode, canceled: true}
	}
	if err := c.Start(); err != nil {
		return shellRunResult{exitCode: 1}
	}
	pgid := c.Process.Pid // bash is the group leader (Setpgid=true)
	killGroup := func() {
		// Negative pgid targets the whole process group.
		syscall.Kill(-pgid, syscall.SIGKILL) //nolint:errcheck
	}
	done := make(chan int, 1)
	go func() { done <- waitExitCode(c) }()

	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()

	select {
	case exitCode := <-done:
		return shellRunResult{exitCode: exitCode}
	case <-timer.C:
		killGroup()
		<-done
		return shellRunResult{exitCode: 124} // preserves "timed out after N seconds" message
	case <-ctx.Done():
		killGroup()
		<-done
		return shellRunResult{exitCode: shellCanceledExitCode, canceled: true}
	}
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

// shellExecution carries the post-run outputs runShell needs to build its
// result envelope and meta map: the shaped/framed content, the process exit
// code, and the separately-buffered stdout/stderr (stderr also feeds hint matching).
type shellExecution struct {
	content    string
	exitCode   int
	canceled   bool
	stdout     string
	stderr     string
	durationMs int64
}

// executeShellCommand sets up the bash subprocess (minimal env, process group,
// bounded capture + stdout/stderr buffers), runs it under the timeout, and shapes
// the captured output. It covers the execution half of runShell; classification,
// hint matching and meta assembly stay in the caller. ctx threads to the subprocess
// via explicit cancellation handling; a nil ctx is a caller bug and panics.
func executeShellCommand(ctx context.Context, cmd string, timeout int) shellExecution {
	// Always use a process group so SIGKILL reaches background processes too.
	// Both timeout and parent cancellation must kill -pgid, not only bash.
	// nil ctx is a caller bug — guard with panic so it surfaces in tests rather
	// than masking parent cancellation in production.
	if ctx == nil {
		panic("runShell: nil ctx")
	}
	c := exec.CommandContext(context.WithoutCancel(ctx), "bash", "-c", cmd) //nolint:gosec // G204: the shell tool intentionally executes agent-provided commands (its documented purpose)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	capture := newShellOutputCapture(1 << 20) // 1 MB response tail + full spill on overflow
	defer capture.Close()
	outBuf := &boundedWriter{limit: 1 << 20} // 1 MB stdout meta buffer
	errBuf := &boundedWriter{limit: 1 << 20} // 1 MB stderr meta buffer
	c.Env = shellEnv()
	c.Stdout = io.MultiWriter(capture, outBuf)
	c.Stderr = io.MultiWriter(capture, errBuf)

	start := time.Now()
	run := runWithTimeout(ctx, c, timeout)
	content := shapeShellOutput(capture, cmd, run.exitCode, timeout)

	return shellExecution{
		content:    content,
		exitCode:   run.exitCode,
		canceled:   run.canceled,
		stdout:     outBuf.String(),
		stderr:     errBuf.String(),
		durationMs: time.Since(start).Milliseconds(),
	}
}

// runShell executes a shell command and returns (result, meta, error).
// Meta keys: "risk" (pre-execution risk level) and "classification" (exit-code class).
// Dangerous commands are blocked unless args["force"] is true.
func (e *Engine) runShell(ctx context.Context, args map[string]interface{}) (string, map[string]any, error) {
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
	if boolArg(args, "dry_run") {
		return fmt.Sprintf("[dry-run] would execute: %s", cmd), map[string]any{
			"risk":           riskLevel.String(),
			"classification": string(ClassSuccess),
			"timeout_ms":     int64(intArg(args, "timeout", 30) * 1000),
			"duration_ms":    int64(0),
			"exit_code":      0,
		}, nil
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

	res := executeShellCommand(ctx, cmd, timeout)

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
