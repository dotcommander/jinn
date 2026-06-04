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
	Results         []searchResult `json:"results"`
	Truncated       bool           `json:"truncated"`
	TotalCount      int            `json:"total_count"`
	ZeroMatchReason string         `json:"zero_match_reason,omitempty"`
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
	acc := searchAccumulator{cap: cap}
	for _, line := range strings.Split(raw, "\n") {
		switch {
		case line == "" || line == "--":
			acc.flush()
		case acc.addMatchLine(line):
			// handled inside addMatchLine
		default:
			acc.addContextLine(line)
		}
	}
	acc.flush()
	if acc.results == nil {
		acc.results = []searchResult{}
	}
	return acc.results, acc.total
}

// searchAccumulator holds the streaming state while parseSearchResults walks
// output lines: the in-progress match awaiting context and any context-before
// lines buffered before their match arrives.
type searchAccumulator struct {
	cap            int
	results        []searchResult
	total          int
	pending        *searchResult // current match awaiting context lines
	preContext     []string      // context-before lines seen before their match
	preContextFile string
}

// flush commits any pending match and drops buffered context-before lines.
// Called on group separators / blank lines and at end of input.
func (a *searchAccumulator) flush() {
	if a.pending != nil {
		a.results = append(a.results, *a.pending)
		a.pending = nil
	}
	a.preContext = nil
	a.preContextFile = ""
}

// addMatchLine parses line as a match line and, on success, records it as the
// new pending match (subject to the cap). Returns false when line is not a
// match line so the caller can try context-line parsing.
func (a *searchAccumulator) addMatchLine(line string) bool {
	r, ok := parseMatchLine(line)
	if !ok {
		return false
	}
	if a.pending != nil {
		a.results = append(a.results, *a.pending)
		a.pending = nil
	}
	a.total++
	// Attach buffered context-before lines in top-to-bottom order.
	if a.preContext != nil && a.preContextFile == r.File {
		r.ContextBefore = joinLines(a.preContext)
	}
	a.preContext = nil
	a.preContextFile = ""
	// Only retain as pending when under cap (cap<=0 means unlimited).
	if a.cap <= 0 || len(a.results) < a.cap {
		rr := r
		a.pending = &rr
	}
	return true
}

// addContextLine parses line as a context line ("file-NUM-text") and attaches
// it to the pending match, or buffers it as context-before when no match for
// the same file is pending yet. Non-context lines are ignored.
func (a *searchAccumulator) addContextLine(line string) {
	c, ok := parseContextLine(line)
	if !ok {
		return
	}
	if a.pending != nil && a.pending.File == c.file {
		if c.lineNum < a.pending.Line {
			// rare: interleaved context line for earlier line number.
			a.pending.ContextBefore = c.text + "\n" + a.pending.ContextBefore
		} else {
			a.pending.ContextAfter += c.text + "\n"
		}
		return
	}
	// No pending match yet — buffer as context-before.
	a.preContext = append(a.preContext, c.text)
	a.preContextFile = c.file
}

// parseMatchLine parses a match line "file:line:text" or "file:line:col:text".
// The substring after the first ':' must begin with the line number; otherwise
// the line is not a match line and ok is false.
func parseMatchLine(line string) (searchResult, bool) {
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return searchResult{}, false
	}
	lineNum, after, ok := splitLeadingInt(line[idx+1:])
	if !ok {
		return searchResult{}, false
	}
	r := searchResult{File: line[:idx], Line: lineNum}
	// after is ":text" or ":col:text". Strip leading ':' then check for an
	// optional column number (col followed by another ':').
	if len(after) > 0 && after[0] == ':' {
		after = after[1:]
		if col, text, ok := splitLeadingInt(after); ok && len(text) > 0 && text[0] == ':' {
			r.Column = col
			r.Text = truncateLine(text[1:], 200)
			return r, true
		}
	}
	r.Text = truncateLine(after, 200)
	return r, true
}

// contextLine is a parsed "file-NUM-text" context line.
type contextLine struct {
	file    string
	text    string
	lineNum int
}

// parseContextLine parses a context line "file-NUM-text" emitted with the '-'
// separator. ok is false when the segment after the first '-' lacks a line number.
func parseContextLine(line string) (contextLine, bool) {
	idx := strings.Index(line, "-")
	if idx <= 0 {
		return contextLine{}, false
	}
	lineNum, text, ok := splitLeadingInt(line[idx+1:])
	if !ok {
		return contextLine{}, false
	}
	// Strip the leading '-' separator between linenum and text.
	text = strings.TrimPrefix(text, "-")
	return contextLine{file: line[:idx], text: text, lineNum: lineNum}, true
}

// joinLines concatenates lines with a trailing newline after each.
func joinLines(lines []string) string {
	var sb strings.Builder
	for _, s := range lines {
		sb.WriteString(s)
		sb.WriteByte('\n')
	}
	return sb.String()
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
func parseFilenamesOutput(raw string, maxMatches int) string {
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
	if maxMatches > 0 && total >= maxMatches {
		s := strings.TrimRight(sb.String(), "\n")
		return s + fmt.Sprintf("\n(results capped at max_matches=%d, more matches may exist)", maxMatches)
	}
	return strings.TrimRight(sb.String(), "\n")
}
