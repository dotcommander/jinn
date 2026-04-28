package jinn

import (
	"strings"
	"testing"
)

// --- Enhancement 1: line range in success message ---

func TestEditFile_LineRangeInSuccess(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		content    string
		oldText    string
		newText    string
		wantPrefix string
	}{
		{
			name:       "single line match",
			content:    "aaa\nbbb\nccc\n",
			oldText:    "bbb",
			newText:    "BBB",
			wantPrefix: "edited edit.txt: lines 2-2 (1 lines) replaced with 1 lines",
		},
		{
			name:       "multiline match",
			content:    "a\nb\nc\nd\ne\n",
			oldText:    "b\nc",
			newText:    "X",
			wantPrefix: "edited edit.txt: lines 2-3 (2 lines) replaced with 1 lines",
		},
		{
			name:       "match at start of file",
			content:    "first\nsecond\nthird\n",
			oldText:    "first",
			newText:    "FIRST",
			wantPrefix: "edited edit.txt: lines 1-1 (1 lines) replaced with 1 lines",
		},
		{
			name:       "match replacing single line with multiple",
			content:    "a\nb\nc\n",
			oldText:    "b",
			newText:    "x\ny\nz",
			wantPrefix: "edited edit.txt: lines 2-2 (1 lines) replaced with 3 lines",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, dir := testEngine(t)
			writeTestFile(t, dir, "edit.txt", tc.content)
			result, err := e.editFile(args("path", "edit.txt", "old_text", tc.oldText, "new_text", tc.newText))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.HasPrefix(result.Text, tc.wantPrefix) {
				t.Errorf("result = %q, want prefix %q", result.Text, tc.wantPrefix)
			}
		})
	}
}

// --- Enhancement 2+3: fuzzy hint on miss ---

func TestEditFile_ClosestMatchHint(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	content := "package main\n\nfunc foo() error {\n\treturn nil\n}\n"
	writeTestFile(t, dir, "hint.txt", content)

	_, err := e.editFile(args("path", "hint.txt", "old_text", "func foo() err {", "new_text", "x"))
	if err == nil {
		t.Fatal("expected error for non-matching old_text")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "not found") {
		t.Errorf("expected 'not found', got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "Nearest match at line 3") {
		t.Errorf("expected 'Nearest match at line 3', got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "did you mean this?") {
		t.Errorf("expected 'did you mean this?', got: %s", errMsg)
	}
}

func TestEditFile_NotFoundIncludesLineCount(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "lines.txt", "a\nb\nc\nd\ne\n")

	_, err := e.editFile(args("path", "lines.txt", "old_text", "MISSING", "new_text", "x"))
	if err == nil {
		t.Fatal("expected error")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "(5 lines)") {
		t.Errorf("expected '(5 lines)' in error, got: %s", errMsg)
	}
}

func TestEditFile_NotFoundNoHintOnAmbiguous(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "ambig2.txt", "aaa\naaa\n")

	_, err := e.editFile(args("path", "ambig2.txt", "old_text", "aaa", "new_text", "bbb"))
	if err == nil {
		t.Fatal("expected error")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "matches 2 locations") {
		t.Errorf("ambiguous error should not include closest match hint, got: %s", errMsg)
	}
	if strings.Contains(errMsg, "did you mean") {
		t.Errorf("ambiguous error should not have fuzzy hint, got: %s", errMsg)
	}
}

// --- Enhancement 4: show_context ---

func TestEditFile_ShowContext(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	content := "line1\nline2\nline3\nline4\nline5\n"
	writeTestFile(t, dir, "ctx.txt", content)

	result, err := e.editFile(args(
		"path", "ctx.txt",
		"old_text", "line3",
		"new_text", "REPLACED",
		"show_context", float64(1),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "--- context ---") {
		t.Errorf("expected ..., got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "REPLACED") {
		t.Errorf("context should show replaced text, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "3* | REPLACED") {
		t.Errorf("edited line should be marked with *, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "2 | line2") {
		t.Errorf("context line should appear, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "4 | line4") {
		t.Errorf("context line should appear, got: %s", result.Text)
	}
}

func TestEditFile_ShowContextZero(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "noctx.txt", "a\nb\nc\n")

	result, err := e.editFile(args(
		"path", "noctx.txt",
		"old_text", "b",
		"new_text", "B",
		"show_context", float64(0),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Text, "--- context ---") {
		t.Errorf("show_context=0 should not include context, got: %s", result.Text)
	}
}

func TestEditFile_ShowContextMultiline(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	content := "a\nb\nc\nd\ne\nf\n"
	writeTestFile(t, dir, "mlctx.txt", content)

	result, err := e.editFile(args(
		"path", "mlctx.txt",
		"old_text", "b\nc",
		"new_text", "X\nY",
		"show_context", float64(1),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "2* | X") {
		t.Errorf("line 2 should be marked as edited, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "3* | Y") {
		t.Errorf("line 3 should be marked as edited, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "1 | a") {
		t.Errorf("context line 1 should appear, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "4 | d") {
		t.Errorf("context line 4 should appear, got: %s", result.Text)
	}
}

func TestEditFile_ShowContextAtFileBoundary(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "edge.txt", "first\nsecond\nthird\n")

	result, err := e.editFile(args(
		"path", "edge.txt",
		"old_text", "first",
		"new_text", "FIRST",
		"show_context", float64(2),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "1* | FIRST") {
		t.Errorf("line 1 should be marked as edited, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "2 | second") {
		t.Errorf("context line 2 should appear, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "3 | third") {
		t.Errorf("context line 3 should appear, got: %s", result.Text)
	}
}
