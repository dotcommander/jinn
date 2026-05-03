package jinn

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var grepExcludeDirs = []string{".git", "node_modules", "vendor", "__pycache__", ".cache", "dist", "build"}

const searchDefaultMax = 500

func (e *Engine) searchFiles(args map[string]interface{}) (string, error) {
	pattern, _ := args["pattern"].(string)
	searchPath := "."
	if p, ok := args["path"].(string); ok && p != "" {
		searchPath = p
	}

	literal, _ := args["literal"].(bool)

	if _, err := e.checkPath(searchPath); err != nil {
		var sug *ErrWithSuggestion
		if errors.As(err, &sug) && sug.Code == "" {
			sug.Code = ErrCodePathOutsideSandbox
		}
		return "", err
	}
	if !literal {
		if _, err := regexp.Compile(pattern); err != nil {
			return "", &ErrWithSuggestion{
				Err:        fmt.Errorf("invalid regex: %w", err),
				Suggestion: "check your regex syntax — use literal:true for fixed-string search",
				Code:       ErrCodeInvalidRegex,
			}
		}
	}

	format := "text"
	if f, ok := args["format"].(string); ok && f != "" {
		format = f
	}

	// max_matches: default 500, 0 means use the default (not unlimited).
	maxMatches := intArg(args, "max_matches", searchDefaultMax)

	var cmd string
	var cmdArgs []string

	if e.rgPath != "" {
		cmd = e.rgPath
		cmdArgs = []string{"-n", "--no-heading"}
		for _, dir := range grepExcludeDirs {
			cmdArgs = append(cmdArgs, "--glob=!"+dir)
		}
		if include, ok := args["include"].(string); ok && include != "" {
			cmdArgs = append(cmdArgs, "--glob="+include)
		}
		if literal {
			cmdArgs = append(cmdArgs, "--fixed-strings")
		}
	} else {
		cmd = "grep"
		cmdArgs = []string{"-r", "-n"}
		for _, dir := range grepExcludeDirs {
			cmdArgs = append(cmdArgs, "--exclude-dir="+dir)
		}
		if include, ok := args["include"].(string); ok && include != "" {
			cmdArgs = append(cmdArgs, "--include="+include)
		}
		if literal {
			cmdArgs = append(cmdArgs, "-F")
		}
	}

	// Legacy max_results param (kept for backward compat).
	maxResults := intArg(args, "max_results", 0)

	if ctx, ok := args["context_lines"].(float64); ok && int(ctx) > 0 {
		cmdArgs = append(cmdArgs, "-C", strconv.Itoa(int(ctx)))
	}
	if ci, ok := args["case_insensitive"].(bool); ok && ci {
		cmdArgs = append(cmdArgs, "-i")
	}

	if format == "filenames" {
		if maxMatches > 0 {
			cmdArgs = append(cmdArgs, "-m", strconv.Itoa(maxMatches))
		}
		cmdArgs = append(cmdArgs, "-c", "--", pattern, searchPath)

		stdout := &boundedWriter{limit: 1 << 20}
		stderr := &boundedWriter{limit: 1 << 20}
		c := exec.Command(cmd, cmdArgs...)
		c.Dir = e.workDir
		c.Stdout = stdout
		c.Stderr = stderr
		runErr := c.Run()

		if runErr != nil {
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				if exitErr.ExitCode() != 1 || stdout.String() != "" {
					// Exit code 1 with empty stdout = no matches (not an error).
					// Any other non-zero exit with empty stdout is an error.
					if stdout.String() == "" {
						return "", fmt.Errorf("search failed: %s", stderr.String())
					}
				}
			}
		}

		raw := stdout.String()
		return parseFilenamesOutput(raw, maxMatches), nil
	}

	// Pass -m as a safety cap so grep stops scanning extremely large files
	// early. We use a generous cap (2× requested) rather than maxMatches directly
	// because -m limits per-file matches, and setting it to maxMatches would
	// break total_count accuracy when all matches reside in a single file.
	// The post-hoc Go cap is still needed for an accurate total_count.
	if maxMatches > 0 {
		safetyCap := maxMatches * 2
		if safetyCap < searchDefaultMax {
			safetyCap = searchDefaultMax
		}
		cmdArgs = append(cmdArgs, "-m", strconv.Itoa(safetyCap))
	}
	cmdArgs = append(cmdArgs, "--", pattern, searchPath)

	stdout := &boundedWriter{limit: 1 << 20}
	stderr := &boundedWriter{limit: 1 << 20}
	c := exec.Command(cmd, cmdArgs...)
	c.Dir = e.workDir
	c.Stdout = stdout
	c.Stderr = stderr
	runErr := c.Run()
	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

	raw := stdout.String()
	if stdout.Truncated() {
		raw += "\n[output truncated at 1 MB]"
	}
	if maxResults > 0 {
		raw += fmt.Sprintf("\n(results capped at max_results=%d, more matches may exist)", maxResults)
	}

	errOutput := stderr.String()

	if format == "json" {
		shown, total := parseSearchResults(raw, maxMatches)
		truncated := total > maxMatches

		resp := searchFilesResult{Results: shown, Truncated: truncated, TotalCount: total}
		if total == 0 {
			resp.ZeroMatchReason = e.classifyZeroMatch(pattern, searchPath, literal, exitCode, errOutput)
		}
		jsonBytes, err := json.Marshal(resp)
		if err != nil {
			return "", fmt.Errorf("marshal search results: %w", err)
		}
		result := string(jsonBytes)
		if truncated {
			result += "\n" + formatTruncatedHint(maxMatches, total, "'max_matches' or a more specific pattern")
		}
		return result, nil
	}

	// Text format: enforce line cap — single pass counts and caps simultaneously.
	var kept []string
	count := 0
	for _, l := range strings.Split(raw, "\n") {
		if l == "" {
			kept = append(kept, l)
			continue
		}
		count++
		if count <= maxMatches {
			kept = append(kept, l)
		}
	}
	truncated := count > maxMatches
	result := truncateOutput(strings.Join(kept, "\n"), 100)
	if truncated {
		result += "\n" + formatTruncatedHint(maxMatches, count, "'max_matches' or a more specific pattern")
	}
	if count == 0 {
		reason := e.classifyZeroMatch(pattern, searchPath, literal, exitCode, errOutput)
		result += "[no matches: " + reason + "]"
	}
	return result, nil
}

// classifyZeroMatch determines why a grep returned zero results.
func (e *Engine) classifyZeroMatch(pattern, searchPath string, literal bool, exitCode int, stderr string) string {
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
