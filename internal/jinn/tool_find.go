package jinn

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

const findDefaultLimit = 1000

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

func (e *Engine) findFiles(args map[string]interface{}) (string, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("pattern is required"),
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

	if e.fdPath != "" {
		raw, backend = e.findViaFd(pattern, searchPath)
	} else {
		raw, backend = e.findViaFind(pattern, searchPath)
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		res := findFilesResult{Files: []string{}, Truncated: false, TotalCount: 0, Backend: backend}
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
func (e *Engine) findViaFd(pattern, searchPath string) (string, string) {
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
	c := exec.Command(e.fdPath, args...)
	c.Dir = e.workDir
	c.Stdout = out
	c.Stderr = out
	c.Run()
	return out.String(), "fd"
}

// findViaFind uses POSIX find as a fallback when fd is unavailable.
func (e *Engine) findViaFind(pattern, searchPath string) (string, string) {
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
	c := exec.Command("find", findArgs...)
	c.Dir = e.workDir
	c.Stdout = out
	c.Stderr = out
	c.Run()
	return out.String(), "find"
}
