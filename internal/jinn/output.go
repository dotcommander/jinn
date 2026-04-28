package jinn

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func truncateOutput(raw string, limit int) string {
	return truncateOutputDetailed(raw, limit).Content
}

// truncationInfo describes how output was truncated.
type truncationInfo struct {
	Truncated   bool `json:"truncated"`
	TotalLines  int  `json:"totalLines"`
	OutputLines int  `json:"outputLines"`
}

// truncateOutputDetailed truncates output and returns both the content
// and structured metadata about the truncation.
func truncateOutputDetailed(raw string, limit int) struct {
	Content    string
	Truncated  bool
	TotalLines int
	ShownLines int
} {
	result := struct {
		Content    string
		Truncated  bool
		TotalLines int
		ShownLines int
	}{}

	if raw == "" {
		return result
	}
	lines := strings.Split(raw, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	count := len(lines)
	result.TotalLines = count

	if count >= limit {
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

// truncateTail keeps the last `limit` lines. Better for shell output
// where errors and results appear at the end.
func truncateTail(raw string, limit int) string {
	if raw == "" {
		return ""
	}
	lines := strings.Split(raw, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	count := len(lines)
	if count <= limit {
		return raw
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[truncated: %d lines → showing last %d]\n", count, limit)
	fmt.Fprintf(&b, "[... %d lines omitted ...]\n", count-limit)
	for _, l := range lines[count-limit:] {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// boundedWriter caps output at limit bytes. Always returns len(p), nil
// so the subprocess doesn't die mid-output.
type boundedWriter struct {
	buf   strings.Builder
	limit int
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	n := len(p)
	remaining := w.limit - w.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		w.buf.Write(p)
	}
	return n, nil
}

func (w *boundedWriter) String() string  { return w.buf.String() }
func (w *boundedWriter) Truncated() bool { return w.buf.Len() >= w.limit }

// truncateLine truncates a single line at a rune boundary if it exceeds maxRunes.
func truncateLine(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes]) + "..."
}

// collapseRepeatedLines replaces runs of 3+ identical consecutive lines
// with a single instance plus an annotation.
func collapseRepeatedLines(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) < 3 {
		return s
	}
	var b strings.Builder
	i := 0
	for i < len(lines) {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		run := j - i
		if run >= 3 {
			b.WriteString(lines[i])
			b.WriteByte('\n')
			fmt.Fprintf(&b, "[... %d identical lines collapsed ...]\n", run-1)
		} else {
			for k := i; k < j; k++ {
				b.WriteString(lines[k])
				b.WriteByte('\n')
			}
		}
		i = j
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatTruncatedHint returns the standard hint string appended when list_dir
// or search_files truncates results. shown and total are entry counts;
// narrowHint names the parameter the agent can use to reduce the result set.
func formatTruncatedHint(shown, total int, narrowHint string) string {
	return fmt.Sprintf("[TRUNCATED: %d of %d entries. Use %s to narrow.]", shown, total, narrowHint)
}
