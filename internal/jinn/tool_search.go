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

// searchResult is a structured match from searchFiles.
type searchResult struct {
	File          string `json:"file"`
	Line          int    `json:"line"`
	Column        int    `json:"column,omitempty"`
	Text          string `json:"text"`
	ContextBefore string `json:"context_before,omitempty"`
	ContextAfter  string `json:"context_after,omitempty"`
}

// parseSearchResults parses grep/rg output into structured results.
// Match lines use ':' separator: "file:line:text" or "file:line:col:text" (rg).
// Context lines use '-' separator: "file-NUM-text".
// Group separators ("--") and binary file warnings are skipped.
func parseSearchResults(raw string) []searchResult {
	var results []searchResult
	var pending *searchResult // current match awaiting context lines
	// Buffer context-before lines that appear before their match.
	var preContext []string
	var preContextFile string

	for _, line := range strings.Split(raw, "\n") {
		if line == "" || line == "--" {
			if pending != nil {
				results = append(results, *pending)
				pending = nil
			}
			preContext = nil
			preContextFile = ""
			continue
		}

		// Try match-line (':' separator): file:line[:col]:text
		// The rest after the first ':' must start with a digit.
		if idx := strings.Index(line, ":"); idx > 0 {
			rest := line[idx+1:]
			if lineNum, after, ok := splitLeadingInt(rest); ok {
				file := line[:idx]
				if pending != nil {
					results = append(results, *pending)
				}
				r := searchResult{File: file, Line: lineNum}
				// after is ":text" or ":col:text".
				// Strip leading ':' then check for optional column number.
				if len(after) > 0 && after[0] == ':' {
					after = after[1:]
					if col, text, ok := splitLeadingInt(after); ok && len(text) > 0 && text[0] == ':' {
						r.Column = col
						r.Text = text[1:]
					} else {
						r.Text = after
					}
				} else {
					r.Text = after
				}
				// Attach buffered context-before lines.
				if preContext != nil && preContextFile == file {
					for i := len(preContext) - 1; i >= 0; i-- {
						r.ContextBefore += preContext[i] + "\n"
					}
				}
				preContext = nil
				preContextFile = ""
				pending = &r
				continue
			}
		}

		// Context line ('-' separator): file-NUM-text
		if idx := strings.Index(line, "-"); idx > 0 {
			rest := line[idx+1:]
			lineNum, text, ok := splitLeadingInt(rest)
			if !ok {
				continue
			}
			// Strip the leading '-' separator between linenum and text.
			if strings.HasPrefix(text, "-") {
				text = text[1:]
			}
			file := line[:idx]
			if pending != nil && pending.File == file {
				if lineNum < pending.Line {
					pending.ContextBefore = text + "\n" + pending.ContextBefore
				} else {
					pending.ContextAfter += text + "\n"
				}
			} else {
				// No pending match yet — buffer as context-before.
				preContext = append(preContext, text)
				preContextFile = file
			}
		}
	}
	if pending != nil {
		results = append(results, *pending)
	}
	if results == nil {
		results = []searchResult{}
	}
	return results
}

// splitLeadingInt splits "42:rest" into (42, "rest", true).
func splitLeadingInt(s string) (int, string, bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(s) {
		return 0, "", false
	}
	n, _ := strconv.Atoi(s[:i])
	return n, s[i:], true
}

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

	maxResults := 0
	if mr, ok := args["max_results"].(float64); ok && int(mr) > 0 {
		maxResults = int(mr)
	}

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

	if maxResults > 0 {
		cmdArgs = append(cmdArgs, "-m", strconv.Itoa(maxResults))
	}
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
		results := parseSearchResults(raw)
		if len(results) > 100 {
			results = results[:100]
		}
		jsonBytes, err := json.Marshal(results)
		if err != nil {
			return "", fmt.Errorf("marshal search results: %w", err)
		}
		return string(jsonBytes), nil
	}

	return truncateOutput(raw, 100), nil
}

// parseFilenamesOutput converts grep -c output ("file:N" or "file:0") into
// "file: N matches" lines. Files with zero matches (from -c when some files
// have matches) are excluded. A max_results note is appended when applicable.
func parseFilenamesOutput(raw string, maxResults int) string {
	var sb strings.Builder
	total := 0
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}
		idx := strings.LastIndex(line, ":")
		if idx < 0 {
			continue
		}
		file := line[:idx]
		countStr := strings.TrimSpace(line[idx+1:])
		count, err := strconv.Atoi(countStr)
		if err != nil || count == 0 {
			continue
		}
		total += count
		if count == 1 {
			fmt.Fprintf(&sb, "%s: %d match\n", file, count)
		} else {
			fmt.Fprintf(&sb, "%s: %d matches\n", file, count)
		}
	}
	if maxResults > 0 && total >= maxResults {
		s := strings.TrimRight(sb.String(), "\n")
		return s + fmt.Sprintf("\n(results capped at max_results=%d, more matches may exist)", maxResults)
	}
	return strings.TrimRight(sb.String(), "\n")
}
