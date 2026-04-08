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
	result := e.multiEdit(args("edits", edits))
	if !strings.Contains(result, "applied 2 edits") {
		t.Errorf("expected success, got: %s", result)
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
	result := e.multiEdit(args("edits", edits))
	if !strings.Contains(result, "not found") {
		t.Errorf("expected validation error, got: %s", result)
	}
	d, _ := os.ReadFile(filepath.Join(dir, "ok.txt"))
	if string(d) != "good\n" {
		t.Errorf("ok.txt should be unchanged after failed multi_edit, got: %q", d)
	}
}

func TestMultiEdit_EmptyEdits(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	result := e.multiEdit(args("edits", []interface{}{}))
	if !strings.Contains(result, "non-empty") {
		t.Errorf("expected non-empty error, got: %s", result)
	}
}

func TestMultiEdit_FuzzyAndCRLF(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "multi.txt", "aaa\r\nbbb\r\nccc\r\n")
	result := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{
			"path":     "multi.txt",
			"old_text": "bbb",
			"new_text": "BBB",
		},
	}))
	if strings.Contains(result, "error") {
		t.Fatalf("multi_edit failed: %s", result)
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
