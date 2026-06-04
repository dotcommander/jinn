package jinn

import (
	"fmt"
	"strings"
)

// Default limits for tool output truncation, matching pi conventions.
const (
	DefaultMaxLines = 2000
	DefaultMaxBytes = 50 * 1024 // 50KB
)

// formatSize returns a human-readable byte size (e.g. "50.0KB").
func formatSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	} else if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

func truncateOutput(raw string, limit int) string {
	return truncateOutputDetailed(raw, limit).Content
}

// truncateResult is the shared shape returned by the line-truncation helpers:
// the rendered content plus metadata about how many lines were shown.
type truncateResult struct {
	Content    string
	Truncated  bool
	TotalLines int
	ShownLines int
}

// truncationInfo describes how output was truncated.
type truncationInfo struct {
	Truncated   bool `json:"truncated"`
	TotalLines  int  `json:"totalLines"`
	OutputLines int  `json:"outputLines"`
}

// truncateOutputDetailed truncates output and returns both the content
// and structured metadata about the truncation.
func truncateOutputDetailed(raw string, limit int) truncateResult {
	result := truncateResult{}

	lines := splitLines(raw)
	count := len(lines)
	result.TotalLines = count

	if count > limit {
		keep := limit / 4
		shown := keep * 2
		omitted := count - shown
		var b strings.Builder
		fmt.Fprintf(&b, "[truncated: %d lines → %d shown (head %d + tail %d)]\n", count, shown, keep, keep)
		for _, l := range lines[:keep] {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "[... %d lines omitted ...]\n", omitted)
		for _, l := range lines[count-keep:] {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		result.Content = strings.TrimRight(b.String(), "\n")
		result.Truncated = true
		result.ShownLines = shown
		return result
	}
	result.Content = raw
	result.ShownLines = count
	return result
}

// truncateOutputHead keeps the first limit lines and appends a pagination hint.
// The hint format matches pi's convention: agents use start_line=N+1 to continue.
func truncateOutputHead(raw string, limit int) truncateResult {
	return truncateOutputEnd(raw, limit, false)
}

// truncateOutputTail keeps the last limit lines and prepends a marker.
func truncateOutputTail(raw string, limit int) truncateResult {
	return truncateOutputEnd(raw, limit, true)
}

// truncateOutputEnd keeps limit lines from one end of raw. When tail is false it
// keeps the first limit lines and appends a continuation hint; when tail is true
// it keeps the last limit lines and prepends a marker. Behavior and output
// strings match the original head/tail implementations exactly.
func truncateOutputEnd(raw string, limit int, tail bool) truncateResult {
	result := truncateResult{}
	lines := splitLines(raw)
	count := len(lines)
	result.TotalLines = count
	// Head returns raw when count < limit; tail when count <= limit.
	if (tail && count <= limit) || (!tail && count < limit) {
		result.Content = raw
		result.ShownLines = count
		return result
	}

	var b strings.Builder
	if tail {
		fmt.Fprintf(&b, "[truncated: showing last %d of %d lines]\n", limit, count)
		for _, l := range lines[count-limit:] {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		result.Content = strings.TrimRight(b.String(), "\n")
	} else {
		for _, l := range lines[:limit] {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "[truncated: showing first %d of %d lines — use start_line=%d to continue]", limit, count, limit+1)
		result.Content = b.String()
	}
	result.Truncated = true
	result.ShownLines = limit
	return result
}
