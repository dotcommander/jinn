package jinn

import (
	"strings"
	"testing"
)

func TestUnifiedDiff_Identical(t *testing.T) {
	t.Parallel()
	result := unifiedDiff("same\ncontent\n", "same\ncontent\n", "test.txt", 3)
	if result != "[dry-run] no changes" {
		t.Errorf("identical files should return no changes, got: %s", result)
	}
}

func TestUnifiedDiff_Additions(t *testing.T) {
	t.Parallel()
	old := "line1\nline2\n"
	new_ := "line1\nline2\nline3\nline4\n"
	result := unifiedDiff(old, new_, "test.txt", 3)
	if result == "[dry-run] no changes" {
		t.Fatal("expected diff for additions")
	}
	if !strings.Contains(result, "+ line3") {
		t.Errorf("diff should contain '+ line3', got:\n%s", result)
	}
	if !strings.Contains(result, "+ line4") {
		t.Errorf("diff should contain '+ line4', got:\n%s", result)
	}
	if !strings.Contains(result, "[dry-run] diff for test.txt:") {
		t.Errorf("diff should contain label, got:\n%s", result)
	}
}

func TestUnifiedDiff_Deletions(t *testing.T) {
	t.Parallel()
	old := "line1\nline2\nline3\n"
	new_ := "line1\n"
	result := unifiedDiff(old, new_, "test.txt", 3)
	if result == "[dry-run] no changes" {
		t.Fatal("expected diff for deletions")
	}
	if !strings.Contains(result, "- line2") {
		t.Errorf("diff should contain '- line2', got:\n%s", result)
	}
	if !strings.Contains(result, "- line3") {
		t.Errorf("diff should contain '- line3', got:\n%s", result)
	}
}

func TestUnifiedDiff_Replacement(t *testing.T) {
	t.Parallel()
	old := "header\nold content\nfooter\n"
	new_ := "header\nnew content\nfooter\n"
	result := unifiedDiff(old, new_, "test.txt", 3)
	if result == "[dry-run] no changes" {
		t.Fatal("expected diff for replacement")
	}
	if !strings.Contains(result, "- old content") {
		t.Errorf("diff should contain '- old content', got:\n%s", result)
	}
	if !strings.Contains(result, "+ new content") {
		t.Errorf("diff should contain '+ new content', got:\n%s", result)
	}
}

func TestUnifiedDiff_HunkHeader(t *testing.T) {
	t.Parallel()
	old := "a\nb\nc\n"
	new_ := "a\nX\nc\n"
	result := unifiedDiff(old, new_, "test.txt", 3)
	if !strings.Contains(result, "@@") {
		t.Errorf("diff should contain hunk header (@@), got:\n%s", result)
	}
}
