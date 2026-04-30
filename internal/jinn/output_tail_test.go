package jinn

import (
	"strings"
	"testing"
)

// truncateOutputTail coverage: the trailing-newline strip and exact-limit case.

func TestTruncateOutputTail_Empty(t *testing.T) {
	t.Parallel()
	result := truncateOutputTail("", 10)
	if result.Content != "" || result.Truncated || result.TotalLines != 0 {
		t.Errorf("empty input: got %+v", result)
	}
}

func TestTruncateOutputTail_FitsExactly(t *testing.T) {
	t.Parallel()
	// count == limit: nothing is omitted, so Truncated must be false.
	lines := strings.Repeat("line\n", 5)
	result := truncateOutputTail(lines, 5)
	if result.Truncated {
		t.Error("Truncated = true, want false when count == limit")
	}
	if strings.Contains(result.Content, "truncated:") {
		t.Errorf("unexpected truncation header in content: %q", result.Content)
	}
	if result.TotalLines != 5 {
		t.Errorf("TotalLines = %d, want 5", result.TotalLines)
	}
	if result.ShownLines != 5 {
		t.Errorf("ShownLines = %d, want 5", result.ShownLines)
	}
}

func TestTruncateOutputTail_OverLimit(t *testing.T) {
	t.Parallel()
	var sb strings.Builder
	for i := 1; i <= 20; i++ {
		sb.WriteString("line\n")
	}
	result := truncateOutputTail(sb.String(), 5)
	if !result.Truncated {
		t.Error("should be truncated when count > limit")
	}
	if result.TotalLines != 20 {
		t.Errorf("TotalLines = %d, want 20", result.TotalLines)
	}
	if result.ShownLines != 5 {
		t.Errorf("ShownLines = %d, want 5", result.ShownLines)
	}
	if !strings.Contains(result.Content, "truncated: showing last 5 of 20 lines") {
		t.Errorf("header missing in: %q", result.Content)
	}
}

func TestTruncateOutputTail_UnderLimit(t *testing.T) {
	t.Parallel()
	result := truncateOutputTail("a\nb\nc\n", 100)
	if result.Truncated {
		t.Error("should not be truncated when count < limit")
	}
	if result.TotalLines != 3 {
		t.Errorf("TotalLines = %d, want 3", result.TotalLines)
	}
}

func TestTruncateOutputTail_NoTrailingNewline(t *testing.T) {
	t.Parallel()
	// Input without trailing newline — should handle the same way.
	result := truncateOutputTail("a\nb\nc", 100)
	if result.Truncated {
		t.Error("should not be truncated")
	}
	if result.TotalLines != 3 {
		t.Errorf("TotalLines = %d, want 3", result.TotalLines)
	}
}

// countLines edge-case coverage.

func TestCountLines_EmptyString(t *testing.T) {
	t.Parallel()
	// Empty string has 1 "line" by the n+1 formula (0 newlines → 1).
	// This matches strings.Split("", "\n") = [""], len = 1.
	if got := countLines(""); got != 1 {
		t.Errorf("countLines('') = %d, want 1", got)
	}
}

func TestCountLines_TrailingNewline(t *testing.T) {
	t.Parallel()
	// "a\nb\n" has 2 lines (trailing newline terminates last line).
	if got := countLines("a\nb\n"); got != 2 {
		t.Errorf("countLines('a\\nb\\n') = %d, want 2", got)
	}
}

func TestCountLines_NoTrailingNewline(t *testing.T) {
	t.Parallel()
	// "a\nb" has 2 lines (1 newline + 1 = 2).
	if got := countLines("a\nb"); got != 2 {
		t.Errorf("countLines('a\\nb') = %d, want 2", got)
	}
}

func TestCountLines_SingleLine(t *testing.T) {
	t.Parallel()
	if got := countLines("hello"); got != 1 {
		t.Errorf("countLines('hello') = %d, want 1", got)
	}
}
