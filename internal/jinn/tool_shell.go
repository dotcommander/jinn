package jinn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

func (e *Engine) runShell(ctx context.Context, args map[string]interface{}) (string, error) {
	cmd, _ := args["command"].(string)
	if dryRun, ok := args["dry_run"].(bool); ok && dryRun {
		return fmt.Sprintf("[dry-run] would execute: %s", cmd), nil
	}

	timeout := 30
	if t, ok := args["timeout"].(float64); ok && t >= 1 {
		timeout = int(t)
	}
	if timeout > 300 {
		timeout = 300
	}

	var shellCmd string
	timeoutBin, _ := exec.LookPath("timeout")
	if timeoutBin == "" {
		timeoutBin, _ = exec.LookPath("gtimeout")
	}
	if timeoutBin != "" {
		shellCmd = fmt.Sprintf("%s %d bash -c %s", timeoutBin, timeout, shellescape(cmd))
	} else {
		shellCmd = fmt.Sprintf("bash -c %s", shellescape(cmd))
	}

	out := &boundedWriter{limit: 1 << 20} // 1 MB cap
	c := exec.CommandContext(ctx, "bash", "-c", shellCmd)
	c.Env = shellEnv()
	c.Stdout = out
	c.Stderr = out
	exitCode := 0
	if err := c.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	raw := collapseRepeatedLines(out.String())
	if out.Truncated() {
		if tmp, err := os.CreateTemp("", "jinn-shell-*.log"); err == nil {
			tmp.WriteString(out.String())
			raw += fmt.Sprintf("\n[output truncated at 1 MB — spilled to: %s]", tmp.Name())
			tmp.Close()
		} else {
			raw += "\n[output truncated at 1 MB]"
		}
	}
	if exitCode == 124 {
		raw += fmt.Sprintf("\n[killed: exceeded %ds timeout]", timeout)
	}
	return fmt.Sprintf("[exit: %d]\n%s", exitCode, truncateTail(raw, 200)), nil
}
