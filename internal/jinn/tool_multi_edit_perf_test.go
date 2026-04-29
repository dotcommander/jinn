package jinn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Regression tests for the multi_edit fast-path diff (generateEditDiff).
//
// History: multi_edit originally called generateDiff which runs O(m×n) LCS
// over the full file. On large files this allocated hundreds of MB and
// froze the process for many seconds. The fix routes diff generation
// through generateEditDiff, which uses applyEdit's matchInfo to emit a
// bounded-region diff in O(edited_region + 2*context).
//
// These tests pin the fix in place and cover correctness at the boundaries
// of the fast-path's known weak spots: file start/end, multi-line edits,
// accumulated-content path on same-file repeats, and diff metadata.

// ---- performance ----

// TestMultiEdit_LargeFileFastPath fails if generateDiff is reintroduced.
// 5 edits to a 10k-line file complete in ~50ms with the fast-path; full LCS
// takes >5s and allocates ~800 MB.
func TestMultiEdit_LargeFileFastPath(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	const lineCount = 10_000
	var lines strings.Builder
	for i := range lineCount {
		fmt.Fprintf(&lines, "line %d content\n", i)
	}
	writeTestFile(t, dir, "big.txt", lines.String())

	edits := make([]interface{}, 0, 5)
	for _, idx := range []int{100, 2500, 5000, 7500, 9900} {
		edits = append(edits, map[string]interface{}{
			"path":     "big.txt",
			"old_text": fmt.Sprintf("line %d content", idx),
			"new_text": fmt.Sprintf("LINE %d CHANGED", idx),
		})
	}

	start := time.Now()
	result, err := e.multiEdit(args("edits", edits))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("multiEdit: %v", err)
	}
	if !strings.Contains(result.Text, "applied 5 edits") {
		t.Errorf("expected 'applied 5 edits', got: %s", result.Text)
	}
	// Generous bound: fast-path completes in ~50 ms on a modern laptop.
	// LCS would take >5 s; 2 s flags the regression without flaking on slow CI.
	if elapsed > 2*time.Second {
		t.Errorf("multiEdit on 10k-line file took %v — fast-path likely regressed to LCS", elapsed)
	}
}

// ---- correctness at boundaries ----

// TestMultiEdit_FastPath_EditAtFileStart exercises ctxStart<0 clamp (line 1).
func TestMultiEdit_FastPath_EditAtFileStart(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "head.txt", "first\nsecond\nthird\n")
	result, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{"path": "head.txt", "old_text": "first", "new_text": "FIRST"},
	}))
	if err != nil {
		t.Fatalf("multiEdit: %v", err)
	}
	edit := result.Meta["edit"].(editDetails)
	if !strings.Contains(edit.Diff, "- first") || !strings.Contains(edit.Diff, "+ FIRST") {
		t.Errorf("expected -first/+FIRST in diff, got:\n%s", edit.Diff)
	}
	if edit.FirstChangedLine != 1 {
		t.Errorf("FirstChangedLine: got %d, want 1", edit.FirstChangedLine)
	}
}

// TestMultiEdit_FastPath_EditAtFileEnd exercises trailingEnd clamp (last line).
func TestMultiEdit_FastPath_EditAtFileEnd(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "tail.txt", "alpha\nbeta\ngamma\n")
	result, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{"path": "tail.txt", "old_text": "gamma", "new_text": "GAMMA"},
	}))
	if err != nil {
		t.Fatalf("multiEdit: %v", err)
	}
	edit := result.Meta["edit"].(editDetails)
	if !strings.Contains(edit.Diff, "- gamma") || !strings.Contains(edit.Diff, "+ GAMMA") {
		t.Errorf("expected -gamma/+GAMMA in diff, got:\n%s", edit.Diff)
	}
	if edit.FirstChangedLine != 3 {
		t.Errorf("FirstChangedLine: got %d, want 3", edit.FirstChangedLine)
	}
}

// TestMultiEdit_FastPath_MultiLineReplace covers oldCount/newCount > 1.
func TestMultiEdit_FastPath_MultiLineReplace(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "ml.txt", "before\nold-1\nold-2\nold-3\nafter\n")
	result, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{
			"path":     "ml.txt",
			"old_text": "old-1\nold-2\nold-3",
			"new_text": "NEW-A\nNEW-B",
		},
	}))
	if err != nil {
		t.Fatalf("multiEdit: %v", err)
	}
	edit := result.Meta["edit"].(editDetails)
	for _, want := range []string{"- old-1", "- old-2", "- old-3", "+ NEW-A", "+ NEW-B"} {
		if !strings.Contains(edit.Diff, want) {
			t.Errorf("diff missing %q, got:\n%s", want, edit.Diff)
		}
	}
	if edit.FirstChangedLine != 2 {
		t.Errorf("FirstChangedLine: got %d, want 2", edit.FirstChangedLine)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "ml.txt"))
	if string(got) != "before\nNEW-A\nNEW-B\nafter\n" {
		t.Errorf("file content: got %q", got)
	}
}

// TestMultiEdit_FastPath_SameFileAccumulated verifies per-edit diffs are
// computed from the right baseline when multiple edits target the same file.
// preContent for edit 2 is the post-edit-1 state (from accumulatedContent),
// so each diff should show only its own change, not a cumulative delta.
func TestMultiEdit_FastPath_SameFileAccumulated(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "acc.txt", "one\ntwo\nthree\nfour\nfive\n")
	result, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{"path": "acc.txt", "old_text": "two", "new_text": "TWO"},
		map[string]interface{}{"path": "acc.txt", "old_text": "four", "new_text": "FOUR"},
	}))
	if err != nil {
		t.Fatalf("multiEdit: %v", err)
	}
	edit := result.Meta["edit"].(editDetails)
	for _, want := range []string{"- two", "+ TWO", "- four", "+ FOUR"} {
		if !strings.Contains(edit.Diff, want) {
			t.Errorf("diff missing %q, got:\n%s", want, edit.Diff)
		}
	}
	// First edit anchors FirstChangedLine.
	if edit.FirstChangedLine != 2 {
		t.Errorf("FirstChangedLine: got %d, want 2", edit.FirstChangedLine)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "acc.txt"))
	if string(got) != "one\nTWO\nthree\nFOUR\nfive\n" {
		t.Errorf("file content: got %q", got)
	}
}

// TestMultiEdit_FastPath_DiffParity asserts that for a small file, the fast-
// path output matches what the LCS path would produce. Pins behavioral parity
// so future fast-path changes can't silently drift from the diff format.
func TestMultiEdit_FastPath_DiffParity(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	const content = "a\nb\nc\nd\ne\n"
	writeTestFile(t, dir, "p.txt", content)

	result, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{"path": "p.txt", "old_text": "c", "new_text": "C"},
	}))
	if err != nil {
		t.Fatalf("multiEdit: %v", err)
	}
	gotFast := result.Meta["edit"].(editDetails).Diff
	wantLCS := generateDiff(content, "a\nb\nC\nd\ne\n", "p.txt", 3).Diff
	if gotFast != wantLCS {
		t.Errorf("fast-path diff diverged from LCS reference\nfast:\n%s\nLCS:\n%s", gotFast, wantLCS)
	}
}

// TestMultiEdit_FastPath_DiffParityMultiLine covers multi-line replace parity.
func TestMultiEdit_FastPath_DiffParityMultiLine(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	const content = "x\nold-1\nold-2\ny\nz\n"
	writeTestFile(t, dir, "pm.txt", content)

	result, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{
			"path":     "pm.txt",
			"old_text": "old-1\nold-2",
			"new_text": "NEW",
		},
	}))
	if err != nil {
		t.Fatalf("multiEdit: %v", err)
	}
	gotFast := result.Meta["edit"].(editDetails).Diff
	wantLCS := generateDiff(content, "x\nNEW\ny\nz\n", "pm.txt", 3).Diff
	if gotFast != wantLCS {
		t.Errorf("fast-path diff diverged from LCS reference\nfast:\n%s\nLCS:\n%s", gotFast, wantLCS)
	}
}
