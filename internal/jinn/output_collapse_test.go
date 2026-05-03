package jinn

import (
	"strings"
	"testing"
)

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
	input := "header\nthis is a repeated line\nthis is a repeated line\nthis is a repeated line\nthis is a repeated line\nfooter"
	got := collapseRepeatedLines(input)
	if !strings.Contains(got, "header") || !strings.Contains(got, "footer") {
		t.Errorf("non-repeated lines should survive, got: %q", got)
	}
	if !strings.Contains(got, "[... 3 identical lines collapsed ...]") {
		t.Errorf("expected collapse of 4 repeated lines, got: %q", got)
	}
}

// --- collapseRepeatedLines no-op guard ---

func TestCollapseRepeatedLines_NoOpGuard(t *testing.T) {
	t.Parallel()
	// All unique lines — collapse achieves nothing; original string must be returned.
	input := "apple\nbanana\ncherry\ndate\nelderberry"
	got := collapseRepeatedLines(input)
	if got != input {
		t.Errorf("no-op guard: want original pointer identity, got different string: %q", got)
	}
}

func TestCollapseRepeatedLines_ShortRepeatsNoOp(t *testing.T) {
	t.Parallel()
	// Three "x" lines: the annotation "[... 2 identical lines collapsed ...]" (37 chars)
	// is longer than the two collapsed lines ("x\nx\n" = 4 chars), so the no-op guard
	// must fire and return the original unchanged.
	input := "x\nx\nx"
	got := collapseRepeatedLines(input)
	if got != input {
		t.Errorf("short-repeats no-op guard: want original %q, got %q", input, got)
	}
}

// --- collapseBlankLines ---

func TestCollapseBlankLines_BelowThreshold(t *testing.T) {
	t.Parallel()
	// Exactly threshold=3 consecutive blank lines — no collapse should occur.
	input := "before\n\n\n\nafter"
	got := collapseBlankLines(input, 3)
	if got != input {
		t.Errorf("at-threshold: want unchanged %q, got %q", input, got)
	}
}

func TestCollapseBlankLines_AboveThreshold(t *testing.T) {
	t.Parallel()
	// 5 consecutive blank lines with threshold=3: collapseBlankLines keeps at most
	// threshold blank lines, so 5 collapse to 3 (blankRun <= threshold is inclusive).
	input := "before\n\n\n\n\n\nafter" // 5 blank lines between words
	got := collapseBlankLines(input, 3)
	if strings.Contains(got, "\n\n\n\n\n") {
		t.Errorf("above-threshold: more than 3 consecutive blanks remain in %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Errorf("above-threshold: non-blank content missing in %q", got)
	}
	// threshold=3 keeps exactly 3 blank lines (4 consecutive newlines between words).
	want := "before\n\n\n\nafter"
	if got != want {
		t.Errorf("above-threshold: got %q, want %q", got, want)
	}
}

func TestCollapseBlankLines_WhitespaceOnlyLines(t *testing.T) {
	t.Parallel()
	// Lines containing only spaces/tabs count as blank and must be collapsed.
	input := "start\n   \n\t\n  \t  \n\nend"
	got := collapseBlankLines(input, 2)
	// 4 whitespace/blank lines exceeds threshold=2; result should have at most 2.
	lines := strings.Split(got, "\n")
	maxRun := 0
	run := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 0
		}
	}
	if maxRun > 2 {
		t.Errorf("whitespace-only lines: blank run of %d exceeds threshold 2 in %q", maxRun, got)
	}
}

func TestCollapseBlankLines_MultipleGroups(t *testing.T) {
	t.Parallel()
	// Two separate groups of >threshold blank lines; both must be independently collapsed.
	input := "a\n\n\n\n\nb\n\n\n\n\nc"
	got := collapseBlankLines(input, 2)
	lines := strings.Split(got, "\n")
	maxRun := 0
	run := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 0
		}
	}
	if maxRun > 2 {
		t.Errorf("multiple groups: blank run of %d exceeds threshold 2 in %q", maxRun, got)
	}
	if !strings.Contains(got, "a") || !strings.Contains(got, "b") || !strings.Contains(got, "c") {
		t.Errorf("multiple groups: non-blank content missing in %q", got)
	}
}

func TestCollapseBlankLines_NoBlankLines(t *testing.T) {
	t.Parallel()
	// No blank lines at all — no-op guard must return original unchanged.
	input := "line1\nline2\nline3"
	got := collapseBlankLines(input, 2)
	if got != input {
		t.Errorf("no blanks: want unchanged %q, got %q", input, got)
	}
}

func TestCollapseBlankLines_AllBlank(t *testing.T) {
	t.Parallel()
	// Entirely blank lines (>threshold) — must collapse to a single blank line.
	input := "\n\n\n\n\n"
	got := collapseBlankLines(input, 2)
	lines := strings.Split(got, "\n")
	blankCount := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			blankCount++
		}
	}
	if blankCount > 2 {
		t.Errorf("all-blank: %d blank lines remain (threshold 2) in %q", blankCount, got)
	}
}
