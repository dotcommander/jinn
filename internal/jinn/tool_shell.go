package jinn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
var shellAllowList = []string{"HOME", "GOMODCACHE", "LANG", "LC_ALL", "TERM", "TZ", "USER", "LOGNAME", "TMPDIR", "SHELL"}

// subprocessEnv returns a minimal environment for subprocess tools, preventing
// accidental leakage of host secrets (API keys, credentials). Extra values are
// explicit per-tool overlays, such as a dedicated Go build cache.
func subprocessEnv(extra map[string]string) []string {
	env := []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
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

func (e *Engine) subprocessEnv() []string {
	env := subprocessEnv(nil)
	if len(e.execPath) == 0 {
		return env
	}
	for i, value := range env {
		if strings.HasPrefix(value, "PATH=") {
			env[i] = "PATH=" + strings.Join(e.execPath, string(os.PathListSeparator))
			break
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

const shellCanceledExitCode = 130

type shellRunResult struct {
	exitCode        int
	canceled        bool
	resourceLimited bool
}

func runWithTimeoutControl(ctx context.Context, c *exec.Cmd, timeout int, resourceLimit <-chan struct{}, interrupt func()) shellRunResult {
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
		_ = c.Process.Kill()
		if interrupt != nil {
			interrupt()
		}
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
	case <-resourceLimit:
		killGroup()
		<-done
		return shellRunResult{exitCode: 1, resourceLimited: true}
	}
}

func killCommandGroup(c *exec.Cmd, interrupt func()) {
	if c.Process != nil {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		_ = c.Process.Kill()
	}
	if interrupt != nil {
		interrupt()
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
	content         string
	exitCode        int
	canceled        bool
	resourceLimited bool
	stdout          string
	stderr          string
	durationMs      int64
}

// executeShellCommand sets up the bash subprocess (minimal env, process group,
// bounded capture + stdout/stderr buffers), runs it under the timeout, and shapes
// the captured output. It covers the execution half of runShell; classification,
// hint matching and meta assembly stay in the caller. ctx threads to the subprocess
// via explicit cancellation handling; a nil ctx is a caller bug and panics.
//
//nolint:funlen,gocyclo // process setup, capture wiring, deadline drainage, and cleanup share cancellation ownership.
func (e *Engine) executeShellCommand(ctx context.Context, cmd string, timeout int) shellExecution {
	// Always use a process group so SIGKILL reaches background processes too.
	// Both timeout and parent cancellation must kill -pgid, not only bash.
	// nil ctx is a caller bug — guard with panic so it surfaces in tests rather
	// than masking parent cancellation in production.
	if ctx == nil {
		panic("runShell: nil ctx")
	}
	runDir, err := os.MkdirTemp("", "jinn-run-")
	if err != nil {
		return shellExecution{exitCode: 1, stderr: err.Error()}
	}
	defer func() { _ = os.RemoveAll(runDir) }()
	homeDir := filepath.Join(runDir, "home")
	tmpDir := filepath.Join(runDir, "tmp")
	if mkdirErr := os.MkdirAll(homeDir, 0o700); mkdirErr != nil {
		return shellExecution{exitCode: 1, stderr: mkdirErr.Error()}
	}
	if mkdirErr := os.MkdirAll(tmpDir, 0o700); mkdirErr != nil {
		return shellExecution{exitCode: 1, stderr: mkdirErr.Error()}
	}
	argv := e.shellArgv(cmd, runDir, homeDir, tmpDir)
	c := exec.CommandContext(context.WithoutCancel(ctx), argv[0], argv[1:]...) //nolint:gosec // explicit policy-gated shell execution
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Dir = e.workDir

	capture := newShellOutputCapture(1 << 20) // 1 MB response tail + bounded spill on overflow
	defer capture.Close()
	outBuf := &boundedWriter{limit: 1 << 20} // 1 MB stdout meta buffer
	errBuf := &boundedWriter{limit: 1 << 20} // 1 MB stderr meta buffer
	c.Env = e.shellProcessEnv(homeDir, tmpDir)
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return shellExecution{exitCode: 1, stderr: err.Error()}
	}
	defer func() { _ = stdoutR.Close() }()
	defer func() { _ = stdoutW.Close() }()
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		return shellExecution{exitCode: 1, stderr: err.Error()}
	}
	defer func() { _ = stderrR.Close() }()
	defer func() { _ = stderrW.Close() }()
	c.Stdout = stdoutW
	c.Stderr = stderrW
	copyDone := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(io.MultiWriter(capture, outBuf), stdoutR)
		copyDone <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(io.MultiWriter(capture, errBuf), stderrR)
		copyDone <- struct{}{}
	}()
	interrupt := func() {
		_ = stdoutR.Close()
		_ = stderrR.Close()
		_ = stdoutW.Close()
		_ = stderrW.Close()
	}

	start := time.Now()
	run := runWithTimeoutControl(ctx, c, timeout, capture.LimitReached(), interrupt)
	_ = stdoutW.Close()
	_ = stderrW.Close()
	drained := make(chan struct{})
	go func() {
		<-copyDone
		<-copyDone
		close(drained)
	}()
	// bash can exit while a background descendant still owns its pipes. Keep
	// the same deadline authority until those pipes drain, otherwise a command
	// such as `sleep 30 & echo done` would outlive its requested timeout.
	if run.exitCode != 124 && !run.canceled && !run.resourceLimited {
		remaining := time.Duration(timeout)*time.Second - time.Since(start)
		if remaining < 0 {
			remaining = 0
		}
		timer := time.NewTimer(remaining)
		select {
		case <-drained:
		case <-timer.C:
			killCommandGroup(c, interrupt)
			run.exitCode = 124
			<-drained
		case <-ctx.Done():
			killCommandGroup(c, interrupt)
			run.exitCode = shellCanceledExitCode
			run.canceled = true
			<-drained
		case <-capture.LimitReached():
			killCommandGroup(c, interrupt)
			run.exitCode = 1
			run.resourceLimited = true
			<-drained
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	} else {
		<-drained
	}
	content := shapeShellOutput(capture, cmd, run.exitCode, timeout)

	return shellExecution{
		content:         content,
		exitCode:        run.exitCode,
		canceled:        run.canceled,
		resourceLimited: run.resourceLimited || capture.ResourceLimited(),
		stdout:          outBuf.String(),
		stderr:          errBuf.String(),
		durationMs:      time.Since(start).Milliseconds(),
	}
}

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
//nolint:funlen // request validation and command lifecycle share one response contract.
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
