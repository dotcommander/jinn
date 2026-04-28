package jinn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMultiEdit_Basic(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "m1.txt", "aaa\n")
	writeTestFile(t, dir, "m2.txt", "bbb\n")
	edits := []interface{}{
		map[string]interface{}{"path": "m1.txt", "old_text": "aaa", "new_text": "AAA"},
		map[string]interface{}{"path": "m2.txt", "old_text": "bbb", "new_text": "BBB"},
	}
	result, err := e.multiEdit(args("edits", edits))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "applied 2 edits") {
		t.Errorf("expected success, got: %s", result.Text)
	}
	d1, _ := os.ReadFile(filepath.Join(dir, "m1.txt"))
	d2, _ := os.ReadFile(filepath.Join(dir, "m2.txt"))
	if string(d1) != "AAA\n" || string(d2) != "BBB\n" {
		t.Errorf("files not updated: m1=%q m2=%q", d1, d2)
	}
}

func TestMultiEdit_ValidationFailureAbortsAll(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "ok.txt", "good\n")
	writeTestFile(t, dir, "bad.txt", "original\n")
	edits := []interface{}{
		map[string]interface{}{"path": "ok.txt", "old_text": "good", "new_text": "GOOD"},
		map[string]interface{}{"path": "bad.txt", "old_text": "MISSING", "new_text": "x"},
	}
	_, err := e.multiEdit(args("edits", edits))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected validation error, got: %v", err)
	}
	d, _ := os.ReadFile(filepath.Join(dir, "ok.txt"))
	if string(d) != "good\n" {
		t.Errorf("ok.txt should be unchanged after failed multi_edit, got: %q", d)
	}
}

func TestMultiEdit_EmptyEdits(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.multiEdit(args("edits", []interface{}{}))
	if err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Errorf("expected non-empty error, got: %v", err)
	}
}

func TestMultiEdit_FuzzyAndCRLF(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "multi.txt", "aaa\r\nbbb\r\nccc\r\n")
	result, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{
			"path":     "multi.txt",
			"old_text": "bbb",
			"new_text": "BBB",
		},
	}))
	if err != nil {
		t.Fatalf("multi_edit failed: %v", err)
	}
	if strings.Contains(result.Text, "error") {
		t.Fatalf("multi_edit returned error in result: %s", result.Text)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "multi.txt"))
	content := string(data)
	if !strings.Contains(content, "\r\n") {
		t.Error("CRLF should be preserved in multi_edit")
	}
	if !strings.Contains(content, "BBB") {
		t.Error("edit should have been applied")
	}
}

func TestMultiEdit_FuzzyIndent(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	content := "func main() {\n\tx := 1\n\ty := 2\n}\n"
	writeTestFile(t, dir, "mi_indent.go", content)

	result, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{
			"path":         "mi_indent.go",
			"old_text":     "x := 1",
			"new_text":     "x := 42\n\tz := x + 1",
			"fuzzy_indent": true,
		},
	}))
	if err != nil {
		t.Fatalf("multi_edit with fuzzy_indent failed: %v", err)
	}
	if strings.Contains(result.Text, "error") {
		t.Fatalf("multi_edit returned error: %s", result.Text)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "mi_indent.go"))
	s := string(data)
	if !strings.Contains(s, "\tx := 42") {
		t.Errorf("replacement should have tab indent from fuzzy_indent, got:\n%s", s)
	}
	if !strings.Contains(s, "\tz := x + 1") {
		t.Errorf("second line should have tab indent, got:\n%s", s)
	}
	if !strings.Contains(s, "\ty := 2") {
		t.Error("unchanged line should still have tab indent")
	}
}

func TestMultiEdit_FuzzyIndentDefaultFalse(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	content := "func main() {\n\tx := 1\n}\n"
	writeTestFile(t, dir, "mi_noindent.go", content)

	_, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{
			"path":     "mi_noindent.go",
			"old_text": "x := 1",
			"new_text": "a := 1\nb := 2",
		},
	}))
	if err != nil {
		t.Fatalf("multi_edit failed: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "mi_noindent.go"))
	s := string(data)
	if !strings.Contains(s, "a := 1\nb := 2") {
		t.Errorf("replacement should not be re-indented without fuzzy_indent, got:\n%s", s)
	}
}
