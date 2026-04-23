package jinn

import (
	"fmt"
	"strconv"
	"strings"
)

// searchResult is a structured match from searchFiles.
type searchResult struct {
	File          string `json:"file"`
	Line          int    `json:"line"`
	Column        int    `json:"column,omitempty"`
	Text          string `json:"text"`
	ContextBefore string `json:"context_before,omitempty"`
	ContextAfter  string `json:"context_after,omitempty"`
}

// searchFilesResult wraps search output with truncation metadata.
type searchFilesResult struct {
	Results    []searchResult `json:"results"`
	Truncated  bool           `json:"truncated"`
	TotalCount int            `json:"total_count"`
}

// parseSearchResults parses grep/rg output into structured results.
// Match lines use ':' separator: "file:line:text" or "file:line:col:text" (rg).
// Context lines use '-' separator: "file-NUM-text".
// Group separators ("--") and binary file warnings are skipped.
//
// If cap > 0, the results slice stops growing at cap items (but the
// total count returned via the second value continues — callers rely
// on accurate TotalCount even when truncated).
func parseSearchResults(raw string, cap int) (results []searchResult, total int) {
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
					pending = nil
				}
				total++
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
				// Attach buffered context-before lines in top-to-bottom order.
				if preContext != nil && preContextFile == file {
					var sb strings.Builder
					for _, s := range preContext {
						sb.WriteString(s)
						sb.WriteByte('\n')
					}
					r.ContextBefore = sb.String()
				}
				preContext = nil
				preContextFile = ""
				// Only append when under cap (cap<=0 means unlimited).
				if cap <= 0 || len(results) < cap {
					pending = &r
				}
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
					// rare: interleaved context line for earlier line number.
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
	return results, total
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
