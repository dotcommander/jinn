package jinn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const findDefaultLimit = 1000

// findTimeout caps each fd/find invocation so a slow filesystem walk cannot
// hang an agent tool call indefinitely. Declared as var so tests may shorten
// it; not part of the public API.
var findTimeout = 60 * time.Second

// Directories excluded from both fd and find backends.
var findExcludeDirs = []string{".git", "node_modules", "vendor", "__pycache__", ".cache", "dist", "build"}

// findFilesResult is the structured response for find_files.
type findFilesResult struct {
	Files      []string `json:"files"`
	Truncated  bool     `json:"truncated"`
	TotalCount int      `json:"total_count"`
	LimitUsed  int      `json:"limit_used"`
	Backend    string   `json:"backend"` // "fd" or "find"
}

func (e *Engine) findFiles(ctx context.Context, args map[string]interface{}) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "", &ErrWithSuggestion{
			Err:        errors.New("pattern is required"),
			Suggestion: "provide a glob pattern like '*.go' or '**/*.test.ts'",
		}
	}

	searchPath := "."
	if p, ok := args["path"].(string); ok && p != "" {
		searchPath = p
	}

	if _, err := e.checkPath(searchPath); err != nil {
		return "", err
	}

	limit := intArg(args, "limit", findDefaultLimit)
	if limit < 1 {
		limit = findDefaultLimit
	}

	var raw string
	var backend string
	var runErr error

	if e.fdPath != "" {
		raw, backend, runErr = e.findViaFd(ctx, pattern, searchPath)
	} else {
		raw, backend, runErr = e.findViaFind(ctx, pattern, searchPath)
	}

	// Distinguish timeout from no-match: a stalled walk must not look like
	// an empty result set to the caller.
	if errors.Is(runErr, context.DeadlineExceeded) {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("find_files timed out after %s (backend=%s)", findTimeout, backend),
			Suggestion: "narrow 'path' or use a more specific glob to reduce walk scope",
			Code:       ErrCodeTimeout,
		}
	}
	if errors.Is(runErr, context.Canceled) {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("find_files canceled (backend=%s)", backend),
			Suggestion: "retry the file search if cancellation was unintended",
			Code:       ErrCodeCanceled,
		}
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		res := findFilesResult{Files: []string{}, Truncated: false, TotalCount: 0, LimitUsed: limit, Backend: backend}
		b, _ := json.Marshal(res)
		return string(b), nil
	}

	// Relativize paths: fd outputs paths relative to searchPath,
	// find outputs paths starting with searchPath.
	// Normalize everything to clean relative paths from workDir.
	lines := strings.Split(raw, "\n")
	relativized := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Handle paths like "./foo.go" — strip leading "./"
		for strings.HasPrefix(line, "./") {
			line = line[2:]
		}
		// If path is absolute or relative to cwd, make relative to workDir.
		if filepath.IsAbs(line) {
			if rel, err := filepath.Rel(e.workDir, line); err == nil {
				line = rel
			}
		}
		line = filepath.ToSlash(line)
		if line == "" {
			continue
		}
		relativized = append(relativized, line)
	}

	total := len(relativized)
	truncated := total > limit
	shown := relativized
	if truncated {
		shown = relativized[:limit]
	}

	res := findFilesResult{
		Files:      shown,
		Truncated:  truncated,
		TotalCount: total,
		LimitUsed:  limit,
		Backend:    backend,
	}
	b, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("find_files: marshal: %w", err)
	}
	result := string(b)
	if truncated {
		result += "\n" + fmt.Sprintf(
			"[TRUNCATED: %d of %d files. Use a more specific pattern or increase limit.]",
			len(shown), total,
		)
	}
	return result, nil
}

// findViaFd uses fd (fast, respects .gitignore) to find files.
// Does not use --max-results so we can count the true total for truncation.
// Returns (output, "fd", err). err is context.DeadlineExceeded on timeout.
func (e *Engine) findViaFd(ctx context.Context, pattern, searchPath string) (output string, tool string, err error) {
	// fd --glob matches basenames by default.
	// If pattern contains /, switch to --full-path and prepend **/ for intuitive matching.
	args := []string{
		"--glob",
		"--color=never",
		"--type", "f",
	}

	// Exclude common junk directories (fd also respects .gitignore by default).
	for _, dir := range findExcludeDirs {
		args = append(args, "--exclude", dir)
	}

	effectivePattern := pattern
	if strings.Contains(pattern, "/") {
		args = append(args, "--full-path")
		if !strings.HasPrefix(pattern, "/") && !strings.HasPrefix(pattern, "**/") && pattern != "**" {
			effectivePattern = "**/" + pattern
		}
	}
	args = append(args, effectivePattern, searchPath)

	out := &boundedWriter{limit: 1 << 20}
	ctx, cancel := context.WithTimeout(ctx, findTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, e.fdPath, args...)
	c.Dir = e.workDir
	c.Stdout = out
	c.Stderr = out
	c.WaitDelay = 2 * time.Second
	_ = c.Run()
	return out.String(), "fd", ctx.Err()
}

// findViaFind uses POSIX find as a fallback when fd is unavailable.
// Returns (output, "find", err). err is context.DeadlineExceeded on timeout.
func (e *Engine) findViaFind(ctx context.Context, pattern, searchPath string) (output string, tool string, err error) {
	var findArgs []string

	if strings.Contains(pattern, "/") {
		findArgs = []string{searchPath, "-type", "f", "-path", pattern}
	} else {
		findArgs = []string{searchPath, "-type", "f", "-name", pattern}
	}

	for _, dir := range findExcludeDirs {
		findArgs = append(findArgs, "-not", "-path", "*/"+dir+"/*")
	}

	out := &boundedWriter{limit: 1 << 20}
	ctx, cancel := context.WithTimeout(ctx, findTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "find", findArgs...)
	c.Dir = e.workDir
	c.Stdout = out
	c.Stderr = out
	c.WaitDelay = 2 * time.Second
	_ = c.Run()
	return out.String(), "find", ctx.Err()
}
