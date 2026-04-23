package jinn

import (
	"encoding/json"
	"fmt"
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

	if _, err := e.checkPath(searchPath); err != nil {
		return "", err
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return "", fmt.Errorf("invalid regex: %s", err)
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
	} else {
		cmd = "grep"
		cmdArgs = []string{"-r", "-n"}
		for _, dir := range grepExcludeDirs {
			cmdArgs = append(cmdArgs, "--exclude-dir="+dir)
		}
		if include, ok := args["include"].(string); ok && include != "" {
			cmdArgs = append(cmdArgs, "--include="+include)
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
		if maxResults > 0 {
			cmdArgs = append(cmdArgs, "-m", strconv.Itoa(maxResults))
		}
		cmdArgs = append(cmdArgs, "-c", "--", pattern, searchPath)

		out := &boundedWriter{limit: 1 << 20}
		c := exec.Command(cmd, cmdArgs...)
		c.Dir = e.workDir
		c.Stdout = out
		c.Stderr = out
		c.Run()

		raw := out.String()
		return parseFilenamesOutput(raw, maxResults), nil
	}

	// Pass a generous cap to the underlying tool (fetch more than we need so
	// we can count total); we enforce maxMatches ourselves post-parse.
	cmdArgs = append(cmdArgs, "--", pattern, searchPath)

	out := &boundedWriter{limit: 1 << 20}
	c := exec.Command(cmd, cmdArgs...)
	c.Dir = e.workDir
	c.Stdout = out
	c.Stderr = out
	c.Run()

	raw := out.String()
	if raw != "" {
		lines := strings.Split(raw, "\n")
		for i, l := range lines {
			lines[i] = truncateLine(l, 200)
		}
		raw = strings.Join(lines, "\n")
	}
	if out.Truncated() {
		raw += "\n[output truncated at 1 MB]"
	}
	if maxResults > 0 {
		raw += fmt.Sprintf("\n(results capped at max_results=%d, more matches may exist)", maxResults)
	}

	if format == "json" {
		shown, total := parseSearchResults(raw, maxMatches)
		truncated := total > maxMatches

		resp := searchFilesResult{Results: shown, Truncated: truncated, TotalCount: total}
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
	return result, nil
}
