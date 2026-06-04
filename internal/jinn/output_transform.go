package jinn

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

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
	result := strings.TrimRight(b.String(), "\n")
	if len(result) >= len(s) {
		return s
	}
	return result
}

// collapseBlankLines collapses runs of more than threshold consecutive blank
// lines (empty or whitespace-only) into a single blank line. If the result
// is not strictly smaller than the input, the original string is returned
// unchanged.
func collapseBlankLines(s string, threshold int) string {
	lines := strings.Split(s, "\n")
	if threshold < 1 {
		threshold = 1
	}
	var b strings.Builder
	blankRun := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blankRun++
			if blankRun <= threshold {
				b.WriteString(line)
				b.WriteByte('\n')
			}
			continue
		}
		blankRun = 0
		b.WriteString(line)
		b.WriteByte('\n')
	}
	result := strings.TrimRight(b.String(), "\n")
	if len(result) >= len(s) {
		return s
	}
	return result
}

// formatTruncatedHint returns the standard hint string appended when list_dir
// or search_files truncates results. shown and total are entry counts;
// narrowHint names the parameter the agent can use to reduce the result set.
func formatTruncatedHint(shown, total int, narrowHint string) string {
	return fmt.Sprintf("[TRUNCATED: %d of %d entries. Use %s to narrow.]", shown, total, narrowHint)
}
