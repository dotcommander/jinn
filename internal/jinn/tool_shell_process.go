package jinn

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

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

func runWithTimeoutControl(ctx context.Context, group *shellProcessGroup, timeout int, resourceLimit <-chan struct{}, interrupt func()) shellRunResult {
	if err := ctx.Err(); err != nil {
		return shellRunResult{exitCode: shellCanceledExitCode, canceled: true}
	}
	if err := group.start(); err != nil {
		return shellRunResult{exitCode: 1}
	}
	killGroup := func() {
		group.kill()
		if interrupt != nil {
			interrupt()
		}
	}
	done := make(chan int, 1)
	go func() { done <- waitExitCode(group.command) }()

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

func killCommandGroup(group *shellProcessGroup, interrupt func()) {
	group.kill()
	if interrupt != nil {
		interrupt()
	}
}

// shapeShellOutput compresses, truncates and frames captured output, appending a
// spill/truncation annotation and a timeout note (when exitCode is 124).
