package jinn

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// goDiagnostics runs the stable gopls check command for Go diagnostics. The
// one-shot pull-diagnostics session can return stale package-load errors while
// gopls is still building workspace state; gopls check is the CLI contract for
// this exact question and matches what Go developers run directly.
func (e *Engine) goDiagnostics(ctx context.Context, req lspRequest, timeout int) (string, error) {
	checkCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	outBuf := &boundedWriter{limit: 1 << 20}
	errBuf := &boundedWriter{limit: 1 << 20}
	c := exec.CommandContext(checkCtx, "gopls", "check", req.absPath) //nolint:gosec // G204: gopls binary is fixed; path was sandbox-checked.
	c.Dir = e.workDir
	c.Env = subprocessEnv(nil)
	c.Stdout = outBuf
	c.Stderr = errBuf
	runErr := c.Run()

	switch {
	case errors.Is(checkCtx.Err(), context.Canceled):
		return "", &ErrWithSuggestion{
			Err:        errors.New("lsp_query diagnostics canceled"),
			Suggestion: "retry diagnostics if cancellation was unintended",
			Code:       ErrCodeCanceled,
		}
	case errors.Is(checkCtx.Err(), context.DeadlineExceeded):
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("lsp_query diagnostics timed out after %ds", timeout),
			Suggestion: "retry with a smaller file or after the language server cache settles",
			Code:       ErrCodeTimeout,
		}
	}

	out := strings.TrimSpace(outBuf.String())
	errOut := strings.TrimSpace(errBuf.String())
	if outBuf.Truncated() {
		out += "\n[stdout truncated at 1 MB]"
	}
	if errBuf.Truncated() {
		errOut += "\n[stderr truncated at 1 MB]"
	}

	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return "", fmt.Errorf("gopls check: %w", runErr)
		}
	}

	combined := strings.TrimSpace(strings.Join(nonEmptyStrings(out, errOut), "\n"))
	if combined == "" {
		return "no diagnostics found", nil
	}

	lines := strings.Split(combined, "\n")
	return fmt.Sprintf("%d diagnostic(s) found:\n\n%s", len(lines), combined), nil
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}
