package jinn

import (
	"fmt"
	"strings"
	"testing"
)

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
