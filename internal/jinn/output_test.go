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

// --- collapseRepeatedLines ---

func TestCollapseRepeatedLines_NoRepeats(t *testing.T) {
	t.Parallel()
	input := "a\nb\nc"
	got := collapseRepeatedLines(input)
	if got != input {
		t.Errorf("no repeats should pass through, got: %q", got)
	}
}

func TestCollapseRepeatedLines_ShortRun(t *testing.T) {
	t.Parallel()
	input := "a\na\nb"
	got := collapseRepeatedLines(input)
	if got != input {
		t.Errorf("2 repeats should not collapse, got: %q", got)
	}
}

func TestCollapseRepeatedLines_LongRun(t *testing.T) {
	t.Parallel()
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "same"
	}
	input := strings.Join(lines, "\n")
	got := collapseRepeatedLines(input)
	if !strings.Contains(got, "[... 19 identical lines collapsed ...]") {
		t.Errorf("expected collapse annotation, got: %q", got)
	}
	if strings.Count(got, "same") != 1 {
		t.Errorf("expected exactly 1 instance of 'same', got: %q", got)
	}
}

func TestCollapseRepeatedLines_Mixed(t *testing.T) {
	t.Parallel()
	input := "header\nok\nok\nok\nok\nfooter"
	got := collapseRepeatedLines(input)
	if !strings.Contains(got, "header") || !strings.Contains(got, "footer") {
		t.Errorf("non-repeated lines should survive, got: %q", got)
	}
	if !strings.Contains(got, "[... 3 identical lines collapsed ...]") {
		t.Errorf("expected collapse of 4 'ok' lines, got: %q", got)
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

// --- truncateTailDetailed ---

func TestTruncateTailDetailed_NoTruncation(t *testing.T) {
	t.Parallel()
	input := "line1\nline2\nline3\n"
	content, result := truncateTailDetailed(input, 10, 1024)
	if content != input {
		t.Errorf("short input should pass through unchanged")
	}
	if result.Truncated {
		t.Error("should not be truncated")
	}
	if result.TotalLines != 3 {
		t.Errorf("TotalLines = %d, want 3", result.TotalLines)
	}
}

func TestTruncateTailDetailed_LineLimit(t *testing.T) {
	t.Parallel()
	var lines []string
	for i := range 100 {
		lines = append(lines, fmt.Sprintf("line%d", i))
	}
	input := strings.Join(lines, "\n")
	content, result := truncateTailDetailed(input, 20, 1<<20)
	if !result.Truncated {
		t.Fatal("expected truncation")
	}
	if result.TruncatedBy != "lines" {
		t.Errorf("TruncatedBy = %q, want 'lines'", result.TruncatedBy)
	}
	if result.TotalLines != 100 {
		t.Errorf("TotalLines = %d, want 100", result.TotalLines)
	}
	if result.OutputLines != 20 {
		t.Errorf("OutputLines = %d, want 20", result.OutputLines)
	}
	if !strings.Contains(content, "line99") {
		t.Error("should contain last line")
	}
	if strings.Contains(content, "line0\n") {
		t.Error("should NOT contain first line")
	}
}

func TestTruncateTailDetailed_ByteLimit(t *testing.T) {
	t.Parallel()
	// 100 lines of ~600 bytes each = ~60KB total, well over 50KB limit.
	var lines []string
	for range 100 {
		lines = append(lines, strings.Repeat("x", 599))
	}
	input := strings.Join(lines, "\n")
	content, result := truncateTailDetailed(input, 5000, 50*1024)
	if !result.Truncated {
		t.Fatal("expected truncation")
	}
	if result.TruncatedBy != "bytes" {
		t.Errorf("TruncatedBy = %q, want 'bytes'", result.TruncatedBy)
	}
	if result.OutputBytes > 50*1024 {
		t.Errorf("OutputBytes = %d, should be <= 50KB", result.OutputBytes)
	}
	if !strings.Contains(content, strings.Repeat("x", 10)) {
		t.Error("should contain some content")
	}
	_ = content // used
}

func TestTruncateTailDetailed_Empty(t *testing.T) {
	t.Parallel()
	content, result := truncateTailDetailed("", 10, 1024)
	if content != "" {
		t.Errorf("empty input should return empty, got %q", content)
	}
	if result.Truncated {
		t.Error("empty input should not be truncated")
	}
}

// --- truncateHeadDetailed ---

func TestTruncateHeadDetailed_NoTruncation(t *testing.T) {
	t.Parallel()
	input := "line1\nline2\nline3\n"
	content, result := truncateHeadDetailed(input, 10, 1024)
	if content != input {
		t.Errorf("short input should pass through unchanged")
	}
	if result.Truncated {
		t.Error("should not be truncated")
	}
	if result.TotalLines != 3 {
		t.Errorf("TotalLines = %d, want 3", result.TotalLines)
	}
}

func TestTruncateHeadDetailed_LineLimit(t *testing.T) {
	t.Parallel()
	var lines []string
	for i := range 100 {
		lines = append(lines, fmt.Sprintf("line%d", i))
	}
	input := strings.Join(lines, "\n")
	content, result := truncateHeadDetailed(input, 20, 1<<20)
	if !result.Truncated {
		t.Fatal("expected truncation")
	}
	if result.TruncatedBy != "lines" {
		t.Errorf("TruncatedBy = %q, want 'lines'", result.TruncatedBy)
	}
	if result.TotalLines != 100 {
		t.Errorf("TotalLines = %d, want 100", result.TotalLines)
	}
	if result.OutputLines != 20 {
		t.Errorf("OutputLines = %d, want 20", result.OutputLines)
	}
	if !strings.Contains(content, "line0") {
		t.Error("should contain first line")
	}
	if strings.Contains(content, "line99") {
		t.Error("should NOT contain last line")
	}
}

func TestTruncateHeadDetailed_ByteLimit(t *testing.T) {
	t.Parallel()
	var lines []string
	for range 100 {
		lines = append(lines, strings.Repeat("x", 599))
	}
	input := strings.Join(lines, "\n")
	content, result := truncateHeadDetailed(input, 5000, 50*1024)
	if !result.Truncated {
		t.Fatal("expected truncation")
	}
	if result.TruncatedBy != "bytes" {
		t.Errorf("TruncatedBy = %q, want 'bytes'", result.TruncatedBy)
	}
	if result.OutputBytes > 50*1024 {
		t.Errorf("OutputBytes = %d, should be <= 50KB", result.OutputBytes)
	}
	_ = content
}

func TestTruncateHeadDetailed_Empty(t *testing.T) {
	t.Parallel()
	content, result := truncateHeadDetailed("", 10, 1024)
	if content != "" {
		t.Errorf("empty input should return empty, got %q", content)
	}
	if result.Truncated {
		t.Error("empty input should not be truncated")
	}
}
