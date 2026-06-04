package jinn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"time"
)

// grepResult holds the bounded output of a single grep/rg run.
type grepResult struct {
	stdout   string
	stderr   string
	exitCode int
}

// runGrep runs cmd with cmdArgs under searchTimeout, bounded to 1 MB of output.
// Returns a grepResult and an error. error is non-nil only on cancellation or
// timeout; non-zero grep/rg exits are returned via grepResult.exitCode.
func (e *Engine) runGrep(ctx context.Context, cmd string, cmdArgs []string) (grepResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	outBuf := &boundedWriter{limit: 1 << 20}
	errBuf := &boundedWriter{limit: 1 << 20}
	cmdCtx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	c := exec.CommandContext(cmdCtx, cmd, cmdArgs...) //nolint:gosec // G204: cmd is the resolved rg/grep binary; cmdArgs built internally
	c.Dir = e.workDir
	c.Stdout = outBuf
	c.Stderr = errBuf
	c.WaitDelay = 2 * time.Second
	runErr := c.Run()

	switch {
	case errors.Is(cmdCtx.Err(), context.Canceled):
		return grepResult{}, &ErrWithSuggestion{
			Err:        errors.New("search_files canceled"),
			Suggestion: "retry the search if cancellation was unintended",
			Code:       ErrCodeCanceled,
		}
	case errors.Is(cmdCtx.Err(), context.DeadlineExceeded):
		return grepResult{}, &ErrWithSuggestion{
			Err:        fmt.Errorf("search_files timed out after %s", searchTimeout),
			Suggestion: "narrow 'path' or add a more specific 'include' glob to reduce scan scope",
			Code:       ErrCodeTimeout,
		}
	}

	out := outBuf.String()
	if outBuf.Truncated() {
		out += "\n[output truncated at 1 MB]"
	}

	var exitCode int
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	return grepResult{stdout: out, stderr: errBuf.String(), exitCode: exitCode}, nil
}

// classifyZeroMatch determines why a grep returned zero results.
func (e *Engine) classifyZeroMatch(pattern, searchPath string, literal bool) string {
	if !literal {
		if _, err := regexp.Compile(pattern); err != nil {
			return "invalid_regex"
		}
	}
	resolved, err := e.checkPath(searchPath)
	if err != nil {
		return "path_not_found"
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "path_not_found"
	}
	if info.IsDir() {
		entries, _ := os.ReadDir(resolved)
		if len(entries) == 0 {
			return "path_is_empty_dir"
		}
	}
	return "pattern_matched_nothing"
}
