package jinn

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

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
	c.Stdout = out
	c.Stderr = out
	exitCode := 0
	if err := c.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
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
