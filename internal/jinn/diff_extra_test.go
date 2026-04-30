package jinn

import (
	"strings"
	"testing"
)

// formatEditPreview: fuzzy=false (normal diff).
func TestFormatEditPreview_NoFuzzy(t *testing.T) {
	t.Parallel()
	old := "line1\nline2\n"
	updated := "line1\nchanged\n"
	result := formatEditPreview(old, updated, "test.go", false)
	if !strings.Contains(result, "[dry-run] diff for test.go:") {
		t.Errorf("missing header in: %q", result)
	}
	if strings.Contains(result, "fuzzy match") {
		t.Errorf("should not contain 'fuzzy match' for non-fuzzy, got: %q", result)
	}
}

// formatEditPreview: fuzzy=true appends "(fuzzy match)" annotation.
func TestFormatEditPreview_FuzzyAnnotation(t *testing.T) {
	t.Parallel()
	old := "  original\n"
	updated := "  replaced\n"
	result := formatEditPreview(old, updated, "file.py", true)
	if !strings.Contains(result, "fuzzy match") {
		t.Errorf("expected 'fuzzy match' annotation for fuzzy=true, got: %q", result)
	}
}

// formatEditPreview: identical content returns the no-changes sentinel + fuzzy annotation.
func TestFormatEditPreview_IdenticalFuzzy(t *testing.T) {
	t.Parallel()
	result := formatEditPreview("same\n", "same\n", "x.txt", true)
	// unifiedDiff returns "[dry-run] no changes" for identical content.
	if !strings.Contains(result, "no changes") {
		t.Errorf("expected 'no changes', got: %q", result)
	}
	if !strings.Contains(result, "fuzzy match") {
		t.Errorf("expected 'fuzzy match' annotation, got: %q", result)
	}
}
