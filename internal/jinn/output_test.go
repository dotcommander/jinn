package jinn

import (
	"fmt"
	"strings"
	"testing"
)

// --- truncateOutput ---

func TestTruncateOutput_Short(t *testing.T) {
	t.Parallel()
	input := "line1\nline2\nline3\n"
	got := truncateOutput(input, 10)
	if got != input {
		t.Errorf("short input should pass through unchanged")
	}
}

func TestTruncateOutput_Long(t *testing.T) {
	t.Parallel()
	var lines []string
	for i := range 100 {
		lines = append(lines, "line"+strings.Repeat("x", i))
	}
	input := strings.Join(lines, "\n")
	got := truncateOutput(input, 20)
	if !strings.Contains(got, "[... ") || !strings.Contains(got, "lines omitted") {
		t.Error("truncated output should contain omission marker")
	}
	if !strings.Contains(got, "[truncated:") {
		t.Error("truncated output should contain metadata header")
	}
}

func TestTruncateOutputDetailed_FitsExactly(t *testing.T) {
	t.Parallel()
	// count == limit: all lines fit, no truncation or middle marker.
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = fmt.Sprintf("line%d", i)
	}
	input := strings.Join(lines, "\n") + "\n"
	r := truncateOutputDetailed(input, 10)
	if r.Truncated {
		t.Errorf("Truncated = true, want false when count == limit")
	}
	if strings.Contains(r.Content, "omitted") {
		t.Errorf("Content contains omission marker when count == limit: %q", r.Content)
	}
	if r.ShownLines != 10 {
		t.Errorf("ShownLines = %d, want 10", r.ShownLines)
	}
}

func TestTruncateOutput_Empty(t *testing.T) {
	t.Parallel()
	if got := truncateOutput("", 10); got != "" {
		t.Errorf("empty input should return empty, got %q", got)
	}
}

// --- truncateTail ---

func TestTruncateTail_Short(t *testing.T) {
	t.Parallel()
	input := "line1\nline2\nline3\n"
	got := truncateTail(input, 10)
	if got != input {
		t.Error("short input should pass through unchanged")
	}
}

func TestTruncateTail_Long(t *testing.T) {
	t.Parallel()
	var lines []string
	for i := range 100 {
		lines = append(lines, fmt.Sprintf("line%d", i))
	}
	input := strings.Join(lines, "\n")
	got := truncateTail(input, 20)
	if !strings.Contains(got, "[truncated:") {
		t.Error("should contain truncation header")
	}
	if !strings.Contains(got, "showing last 20") {
		t.Error("should indicate tail-only truncation")
	}
	if !strings.Contains(got, "line99") {
		t.Error("should contain last line")
	}
	if strings.Contains(got, "line0\n") {
		t.Error("should NOT contain first line")
	}
}

func TestTruncateTail_Empty(t *testing.T) {
	t.Parallel()
	if got := truncateTail("", 10); got != "" {
		t.Errorf("empty input should return empty, got %q", got)
	}
}

// --- boundedWriter ---

func TestBoundedWriter_UnderLimit(t *testing.T) {
	t.Parallel()
	w := &boundedWriter{limit: 100}
	n, err := w.Write([]byte("hello"))
	if n != 5 || err != nil {
		t.Errorf("Write = (%d, %v), want (5, nil)", n, err)
	}
	if w.String() != "hello" || w.Truncated() {
		t.Errorf("under-limit: String()=%q Truncated()=%v", w.String(), w.Truncated())
	}
}

func TestBoundedWriter_OverLimit(t *testing.T) {
	t.Parallel()
	w := &boundedWriter{limit: 5}
	n, err := w.Write([]byte("hello world"))
	if n != 11 || err != nil {
		t.Errorf("Write should report full len and nil error: (%d, %v)", n, err)
	}
	if w.String() != "hello" || !w.Truncated() {
		t.Errorf("over-limit: String()=%q Truncated()=%v", w.String(), w.Truncated())
	}
}

func TestBoundedWriter_MultipleWrites(t *testing.T) {
	t.Parallel()
	w := &boundedWriter{limit: 10}
	w.Write([]byte("abc"))
	w.Write([]byte("defghijklmno"))
	if w.String() != "abcdefghij" || !w.Truncated() {
		t.Errorf("multi-write: String()=%q Truncated()=%v", w.String(), w.Truncated())
	}
}

// --- truncateLine ---

func TestTruncateLine_Short(t *testing.T) {
	t.Parallel()
	if got := truncateLine("hello", 200); got != "hello" {
		t.Errorf("short line should pass through, got %q", got)
	}
}

func TestTruncateLine_Long(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 300)
	got := truncateLine(long, 200)
	if !strings.HasSuffix(got, "...") || len(got) != 203 {
		t.Errorf("truncated line: len=%d suffix=%q", len(got), got[len(got)-3:])
	}
}

func TestTruncateLine_Unicode(t *testing.T) {
	t.Parallel()
	input := strings.Repeat("日", 5)
	got := truncateLine(input, 3)
	if got != "日日日..." {
		t.Errorf("unicode truncation = %q, want %q", got, "日日日...")
	}
}

// --- formatSize ---

func TestFormatSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		bytes int
		want  string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0KB"},
		{50 * 1024, "50.0KB"},
		{1536, "1.5KB"},
		{1024 * 1024, "1.0MB"},
	}
	for _, tc := range tests {
		got := formatSize(tc.bytes)
		if got != tc.want {
			t.Errorf("formatSize(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

// --- splitLines ---

func TestSplitLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"one\n", 1},
		{"one\ntwo\n", 2},
		{"one\ntwo", 2},
	}
	for _, tc := range tests {
		got := splitLines(tc.input)
		if len(got) != tc.want {
			t.Errorf("splitLines(%q) = %d lines, want %d", tc.input, len(got), tc.want)
		}
	}
}
