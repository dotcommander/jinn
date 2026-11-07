package jinn

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

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
	group, err := prepareShellProcessGroup(c)
	if err != nil {
		return shellExecution{exitCode: 1, stderr: err.Error()}
	}
	defer group.close()
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
	run := runWithTimeoutControl(ctx, group, timeout, capture.LimitReached(), interrupt)
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
			killCommandGroup(group, interrupt)
			run.exitCode = 124
			<-drained
		case <-ctx.Done():
			killCommandGroup(group, interrupt)
			run.exitCode = shellCanceledExitCode
			run.canceled = true
			<-drained
		case <-capture.LimitReached():
			killCommandGroup(group, interrupt)
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
