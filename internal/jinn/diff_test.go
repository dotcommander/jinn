package jinn

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestDiffFilesStopsAtOperationBudget(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var left, right strings.Builder
	for i := range 3000 {
		left.WriteString("left-" + strconv.Itoa(i) + "\n")
		right.WriteString("right-" + strconv.Itoa(i) + "\n")
	}
	writeTestFile(t, dir, "left.txt", left.String())
	writeTestFile(t, dir, "right.txt", right.String())
	_, err := e.diffFiles(args("path_a", "left.txt", "path_b", "right.txt"))
	var suggested *ErrWithSuggestion
	if err == nil || !errors.As(err, &suggested) || suggested.Code != ErrCodeResourceLimit {
		t.Fatalf("diff budget error = %v", err)
	}
}

func TestUnifiedDiff_Identical(t *testing.T) {
	t.Parallel()
	result := unifiedDiff("same\ncontent\n", "same\ncontent\n", "test.txt")
	if result != "[dry-run] no changes" {
		t.Errorf("identical files should return no changes, got: %s", result)
	}
}

func TestUnifiedDiff_Additions(t *testing.T) {
	t.Parallel()
	old := "line1\nline2\n"
	newText := "line1\nline2\nline3\nline4\n"
	result := unifiedDiff(old, newText, "test.txt")
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
	newText := "line1\n"
	result := unifiedDiff(old, newText, "test.txt")
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
	newText := "header\nnew content\nfooter\n"
	result := unifiedDiff(old, newText, "test.txt")
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
	newText := "a\nX\nc\n"
	result := unifiedDiff(old, newText, "test.txt")
	if !strings.Contains(result, "@@") {
		t.Errorf("diff should contain hunk header (@@), got:\n%s", result)
	}
}
