package jinn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFile_Basic(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "edit.txt", "foo bar baz\n")
	result, err := e.editFile(args("path", "edit.txt", "old_text", "bar", "new_text", "qux"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "edited") {
		t.Errorf("expected 'edited', got: %s", result)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "edit.txt"))
	if string(data) != "foo qux baz\n" {
		t.Errorf("content = %q, want %q", data, "foo qux baz\n")
	}
}

func TestEditFile_NotFound(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.editFile(args("path", "nope.txt", "old_text", "a", "new_text", "b"))
	if err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Errorf("expected 'file not found' error, got: %v", err)
	}
}

func TestEditFile_OldTextMissing(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "miss.txt", "hello world\n")
	_, err := e.editFile(args("path", "miss.txt", "old_text", "MISSING", "new_text", "x"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestEditFile_Ambiguous(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "ambig.txt", "aaa\naaa\n")
	_, err := e.editFile(args("path", "ambig.txt", "old_text", "aaa", "new_text", "bbb"))
	if err == nil || !strings.Contains(err.Error(), "matches 2 locations") {
		t.Errorf("expected ambiguity error, got: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "ambig.txt"))
	if string(data) != "aaa\naaa\n" {
		t.Error("file should be unchanged after ambiguous edit")
	}
}

func TestEditFile_Multiline(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "multi.txt", "line1\nline2\nline3\n")
	result, err := e.editFile(args("path", "multi.txt", "old_text", "line1\nline2", "new_text", "replaced"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "lines 1-2 (2 lines) replaced with 1 lines") {
		t.Errorf("expected line count, got: %s", result)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "multi.txt"))
	if string(data) != "replaced\nline3\n" {
		t.Errorf("content = %q, want %q", data, "replaced\nline3\n")
	}
}

func TestEditFile_FuzzyMatch_SmartQuotes(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "quotes.txt", "fmt.Println(\u201CHello\u201D)\n")
	result, err := e.editFile(args(
		"path", "quotes.txt",
		"old_text", "fmt.Println(\"Hello\")",
		"new_text", "fmt.Println(\"World\")",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "fuzzy match") {
		t.Errorf("expected fuzzy match indicator, got: %s", result)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "quotes.txt"))
	if !strings.Contains(string(data), "World") {
		t.Error("file should contain 'World' after fuzzy edit")
	}
}

func TestEditFile_FuzzyMatch_TrailingWhitespace(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "ws.txt", "hello   \nworld\n")
	result, err := e.editFile(args(
		"path", "ws.txt",
		"old_text", "hello\nworld",
		"new_text", "goodbye\nworld",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "fuzzy match") {
		t.Errorf("expected fuzzy match for trailing whitespace, got: %s", result)
	}
}

func TestEditFile_ExactMatchPreferred(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "exact.txt", "hello\nworld\n")
	result, err := e.editFile(args(
		"path", "exact.txt",
		"old_text", "hello\nworld",
		"new_text", "goodbye\nworld",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "fuzzy") {
		t.Errorf("exact match should not report fuzzy, got: %s", result)
	}
}

func TestEditFile_CRLF_Preserved(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "crlf.txt", "line1\r\nline2\r\nline3\r\n")
	result, err := e.editFile(args(
		"path", "crlf.txt",
		"old_text", "line2",
		"new_text", "replaced",
	))
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	if strings.Contains(result, "error") {
		t.Fatalf("edit returned error in result: %s", result)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "crlf.txt"))
	content := string(data)
	if !strings.Contains(content, "\r\n") {
		t.Error("CRLF should be preserved after edit")
	}
	if strings.Contains(content, "line2") {
		t.Error("old text should be replaced")
	}
}

func TestEditFile_BOM_Preserved(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "bom.txt", "\xEF\xBB\xBFhello world\n")
	result, err := e.editFile(args(
		"path", "bom.txt",
		"old_text", "hello",
		"new_text", "goodbye",
	))
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	if strings.Contains(result, "error") {
		t.Fatalf("edit returned error in result: %s", result)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "bom.txt"))
	if !strings.HasPrefix(string(data), "\xEF\xBB\xBF") {
		t.Error("BOM should be preserved after edit")
	}
	if !strings.Contains(string(data), "goodbye") {
		t.Error("edit should have been applied")
	}
}

func TestEditFile_DryRun(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "dry.txt", "foo bar baz\n")

	result, err := e.editFile(args("path", "dry.txt", "old_text", "bar", "new_text", "qux", "dry_run", true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "[dry-run]") {
		t.Errorf("expected dry-run indicator, got: %s", result)
	}
	if !strings.Contains(result, "- foo bar baz") {
		t.Errorf("dry run should show removed line, got: %s", result)
	}
	if !strings.Contains(result, "+ foo qux baz") {
		t.Errorf("dry run should show added line, got: %s", result)
	}

	// File must be unchanged on disk.
	data, _ := os.ReadFile(filepath.Join(dir, "dry.txt"))
	if string(data) != "foo bar baz\n" {
		t.Errorf("file should be unchanged after dry_run, got: %q", data)
	}
}

func TestEditFile_FuzzyIndent(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	content := "func main() {\n\tfmt.Println(\"hello\")\n}\n"
	writeTestFile(t, dir, "indent.go", content)

	// Agent provides replacement without tabs, but fuzzy_indent fixes it.
	result, err := e.editFile(args(
		"path", "indent.go",
		"old_text", "fmt.Println(\"hello\")",
		"new_text", "fmt.Println(\"world\")\nfmt.Println(\"again\")",
		"fuzzy_indent", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "edited") {
		t.Errorf("expected 'edited', got: %s", result)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "indent.go"))
	s := string(data)
	if !strings.Contains(s, "\tfmt.Println(\"world\")") {
		t.Errorf("new_text should be indented with tab, got:\n%s", s)
	}
	if !strings.Contains(s, "\tfmt.Println(\"again\")") {
		t.Errorf("second line should also be indented, got:\n%s", s)
	}
}

func TestEditFile_FuzzyIndentPreservesContent(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	content := "func main() {\n    x := 1\n    y := 2\n}\n"
	writeTestFile(t, dir, "spaces.go", content)

	result, err := e.editFile(args(
		"path", "spaces.go",
		"old_text", "x := 1",
		"new_text", "x := 42\n    z := x + 1",
		"fuzzy_indent", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "edited") {
		t.Errorf("expected 'edited', got: %s", result)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "spaces.go"))
	s := string(data)
	if !strings.Contains(s, "    x := 42") {
		t.Errorf("replacement should have 4-space indent, got:\n%s", s)
	}
	if !strings.Contains(s, "    z := x + 1") {
		t.Errorf("second line should have 4-space indent, got:\n%s", s)
	}
	if !strings.Contains(s, "    y := 2") {
		t.Error("unchanged line should still have 4-space indent")
	}
}

// --- Feature 8: line-number disambiguation tests ---

func TestEditFile_Ambiguous_LineNumbers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		content     string
		oldText     string
		wantLines   string // expected substring in error, e.g. "lines: 1, 3"
		wantCount   string // e.g. "matches 2 locations"
		wantNoMatch bool   // test no-match path instead
	}{
		{
			name:      "single match succeeds",
			content:   "aaa\nbbb\naaa\n",
			oldText:   "bbb",
			wantLines: "", // no error
		},
		{
			name:      "two matches report line numbers",
			content:   "aaa\nbbb\naaa\n",
			oldText:   "aaa",
			wantCount: "matches 2 locations",
			wantLines: "lines: 1, 3",
		},
		{
			name: "fifteen matches truncated at 10",
			content: func() string {
				var b strings.Builder
				for range 15 {
					b.WriteString("dup\n")
				}
				return b.String()
			}(),
			oldText:   "dup",
			wantCount: "matches 15 locations",
			wantLines: "... and 5 more",
		},
		{
			name:        "no match error unchanged",
			content:     "hello world\n",
			oldText:     "MISSING",
			wantNoMatch: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, dir := testEngine(t)
			writeTestFile(t, dir, "dis.txt", tc.content)
			_, err := e.editFile(args("path", "dis.txt", "old_text", tc.oldText, "new_text", "X"))
			if tc.wantLines == "" && tc.wantCount == "" && !tc.wantNoMatch {
				// Single-match: success expected.
				if err != nil {
					t.Fatalf("expected success, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			msg := err.Error()
			if tc.wantNoMatch {
				if !strings.Contains(msg, "not found") {
					t.Errorf("expected 'not found', got: %s", msg)
				}
				return
			}
			if tc.wantCount != "" && !strings.Contains(msg, tc.wantCount) {
				t.Errorf("expected %q in error, got: %s", tc.wantCount, msg)
			}
			if tc.wantLines != "" && !strings.Contains(msg, tc.wantLines) {
				t.Errorf("expected %q in error, got: %s", tc.wantLines, msg)
			}
			if !strings.Contains(msg, "Add surrounding context to disambiguate") {
				t.Errorf("expected disambiguation hint, got: %s", msg)
			}
		})
	}
}

func TestMultiEdit_AmbiguousLineNumbers(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "me.txt", "dup\nother\ndup\n")
	// Read the file so tracker records it.
	e.readFile(args("path", "me.txt"))

	edits := []interface{}{
		map[string]interface{}{
			"path":     "me.txt",
			"old_text": "dup",
			"new_text": "replaced",
		},
	}
	_, err := e.multiEdit(args("edits", edits))
	if err == nil {
		t.Fatal("expected error for ambiguous match, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "matches 2 locations") {
		t.Errorf("expected 'matches 2 locations' in error, got: %s", msg)
	}
	if !strings.Contains(msg, "lines:") {
		t.Errorf("expected 'lines:' in error, got: %s", msg)
	}
}
