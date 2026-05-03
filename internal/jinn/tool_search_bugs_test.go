package jinn

// Regression tests for search_files bug fixes:
//   1. truncateLine now runs on the Text field after field extraction, not on
//      the raw "file:line:text" grep line.
//   2. Safety cap (-m flag) fires on the default path (not just explicit max_matches).
//   3. filenames format surfaces errors instead of silently returning empty string.

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSearchFiles_FilenamesError verifies that a path outside the sandbox
// surfaces an error through the filenames branch. Regression: when stdout and
// stderr were merged into a single writer, exit code 1 with empty stdout was
// indistinguishable from a normal no-match result.
func TestSearchFiles_FilenamesError(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.searchFiles(args("pattern", "x", "format", "filenames", "path", "/etc/passwd"))
	if err == nil {
		t.Error("expected error for path outside sandbox, got nil")
	}
}

// TestSearchFiles_LongMatchTruncation verifies that a match line >200 runes has
// its Text field truncated while File and Line remain intact. Regression:
// truncateLine previously ran on the raw "file:line:text" grep line before
// field extraction, which could corrupt the colon-delimited structure and
// produce wrong File/Line values.
func TestSearchFiles_LongMatchTruncation(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// 250 x's between two markers — well over the 200-rune limit.
	longLine := "STARTMARKER" + strings.Repeat("x", 250) + "ENDMARKER"
	writeTestFile(t, dir, "long.txt", longLine+"\n")

	result, err := e.searchFiles(args("pattern", "STARTMARKER", "format", "json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp struct {
		Results []searchResult `json:"results"`
	}
	if err := json.NewDecoder(strings.NewReader(result)).Decode(&resp); err != nil {
		t.Fatalf("failed to parse JSON: %v\ninput: %s", err, result)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one result")
	}
	r := resp.Results[0]
	if !strings.HasSuffix(r.File, "long.txt") {
		t.Errorf("File field corrupted — expected suffix long.txt, got: %s", r.File)
	}
	if r.Line == 0 {
		t.Errorf("Line field corrupted — expected non-zero line number")
	}
	// 200 runes of content + 3-rune "..." suffix = 203 total.
	runeCount := len([]rune(r.Text))
	if runeCount > 203 {
		t.Errorf("expected Text ≤203 runes, got %d: %q", runeCount, r.Text)
	}
	if !strings.HasSuffix(r.Text, "...") {
		t.Errorf("expected Text to end with '...', got: %q", r.Text)
	}
	// ENDMARKER is past the 200-rune cut point; it must not appear.
	if strings.Contains(r.Text, "ENDMARKER") {
		t.Errorf("ENDMARKER should be truncated away, got: %q", r.Text)
	}
}

// TestSearchFiles_SafetyCapDefault verifies accurate total_count when the 2×
// safety cap allows all matches to be counted. Regression: the -m flag was
// only appended when maxMatches > 0 in the non-filenames branch, meaning the
// default path (maxMatches==searchDefaultMax after intArg) never got -m.
//
// With 600 lines and default max_matches=500, grep -m 1000 (2× cap) lets all
// 600 through; total_count must be 600 and truncated must be true.
func TestSearchFiles_SafetyCapDefault(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var b strings.Builder
	for range 600 {
		b.WriteString("captest\n")
	}
	writeTestFile(t, dir, "cap.txt", b.String())

	result, err := e.searchFiles(args("pattern", "captest", "format", "json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp struct {
		Results    []searchResult `json:"results"`
		Truncated  bool           `json:"truncated"`
		TotalCount int            `json:"total_count"`
	}
	if err := json.NewDecoder(strings.NewReader(result)).Decode(&resp); err != nil {
		t.Fatalf("failed to parse JSON: %v\ninput: %s", err, result)
	}
	if !resp.Truncated {
		t.Errorf("expected truncated=true for 600 matches with default cap %d", searchDefaultMax)
	}
	if resp.TotalCount != 600 {
		t.Errorf("expected total_count=600, got %d", resp.TotalCount)
	}
	if len(resp.Results) != searchDefaultMax {
		t.Errorf("expected %d results (default cap), got %d", searchDefaultMax, len(resp.Results))
	}
}
