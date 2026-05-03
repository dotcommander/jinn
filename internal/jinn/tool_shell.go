package jinn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// shellAllowList is the set of environment variables passed to shell subprocesses.
// All other host variables (API keys, credentials, tokens) are excluded.
var shellAllowList = []string{"PATH", "HOME", "LANG", "LC_ALL", "TERM", "USER", "LOGNAME", "TMPDIR", "TZ", "SHELL"}

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

// runShell executes a shell command and returns (result, meta, error).
// Meta keys: "risk" (pre-execution risk level) and "classification" (exit-code class).
// Dangerous commands are blocked unless args["force"] is true.
func (e *Engine) runShell(ctx context.Context, args map[string]interface{}) (string, map[string]string, error) {
	cmd, _ := args["command"].(string)
	if dryRun, ok := args["dry_run"].(bool); ok && dryRun {
		return fmt.Sprintf("[dry-run] would execute: %s", cmd), nil, nil
	}

	// Risk classification — block dangerous commands unless force=true.
	riskLevel, riskReason := ClassifyCommand(cmd)
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
	c := exec.CommandContext(ctx, "bash", "-c", cmd)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	outBuf := &boundedWriter{limit: 1 << 20} // 1 MB capture buffer
	errBuf := &boundedWriter{limit: 1 << 20} // 1 MB capture buffer
	c.Env = shellEnv()
	c.Stdout = outBuf
	c.Stderr = errBuf

	exitCode := 0
	timedOut := false

	if err := c.Start(); err != nil {
		exitCode = 1
	} else {
		pgid := c.Process.Pid // bash is the group leader (Setpgid=true)
		timer := time.AfterFunc(time.Duration(timeout)*time.Second, func() {
			// Negative pgid targets the whole process group.
			syscall.Kill(-pgid, syscall.SIGKILL) //nolint:errcheck
		})
		if err := c.Wait(); err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}
		// Stop returns false when the timer already fired.
		if !timer.Stop() {
			timedOut = true
		}
	}

	if timedOut {
		exitCode = 124 // preserves "timed out after N seconds" message below
	}
	raw := collapseRepeatedLines(outBuf.String() + errBuf.String())
	raw = collapseBlankLines(raw, 3)

	// Apply tail truncation with line + byte limits (matching pi conventions).
	content, trunc := truncateTailDetailed(raw, DefaultMaxLines, DefaultMaxBytes)

	if outBuf.Truncated() || errBuf.Truncated() || trunc.Truncated {
		if tmp, err := os.CreateTemp("", "jinn-shell-*.log"); err == nil {
			tmp.WriteString(raw)
			content += fmt.Sprintf(
				"\n\n[Showing %d of %d lines (%s of %s). Full output: %s]",
				trunc.OutputLines, trunc.TotalLines,
				formatSize(trunc.OutputBytes), formatSize(trunc.TotalBytes),
				tmp.Name(),
			)
			tmp.Close()
		} else {
			content += fmt.Sprintf(
				"\n\n[Showing %d of %d lines (%s of %s)]",
				trunc.OutputLines, trunc.TotalLines,
				formatSize(trunc.OutputBytes), formatSize(trunc.TotalBytes),
			)
		}
	}

	if exitCode == 124 {
		content += fmt.Sprintf("\n\nCommand timed out after %d seconds", timeout)
	}

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
