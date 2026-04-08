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
	result := e.editFile(args("path", "edit.txt", "old_text", "bar", "new_text", "qux"))
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
	result := e.editFile(args("path", "nope.txt", "old_text", "a", "new_text", "b"))
	if !strings.Contains(result, "file not found") {
		t.Errorf("expected 'file not found', got: %s", result)
	}
}

func TestEditFile_OldTextMissing(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "miss.txt", "hello world\n")
	result := e.editFile(args("path", "miss.txt", "old_text", "MISSING", "new_text", "x"))
	if !strings.Contains(result, "not found") {
		t.Errorf("expected 'not found', got: %s", result)
	}
}

func TestEditFile_Ambiguous(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "ambig.txt", "aaa\naaa\n")
	result := e.editFile(args("path", "ambig.txt", "old_text", "aaa", "new_text", "bbb"))
	if !strings.Contains(result, "matches 2 locations") {
		t.Errorf("expected ambiguity error, got: %s", result)
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
	result := e.editFile(args("path", "multi.txt", "old_text", "line1\nline2", "new_text", "replaced"))
	if !strings.Contains(result, "replaced 2 lines with 1 lines") {
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
	result := e.editFile(args(
		"path", "quotes.txt",
		"old_text", "fmt.Println(\"Hello\")",
		"new_text", "fmt.Println(\"World\")",
	))
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
	result := e.editFile(args(
		"path", "ws.txt",
		"old_text", "hello\nworld",
		"new_text", "goodbye\nworld",
	))
	if !strings.Contains(result, "fuzzy match") {
		t.Errorf("expected fuzzy match for trailing whitespace, got: %s", result)
	}
}

func TestEditFile_ExactMatchPreferred(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "exact.txt", "hello\nworld\n")
	result := e.editFile(args(
		"path", "exact.txt",
		"old_text", "hello\nworld",
		"new_text", "goodbye\nworld",
	))
	if strings.Contains(result, "fuzzy") {
		t.Errorf("exact match should not report fuzzy, got: %s", result)
	}
}

func TestEditFile_CRLF_Preserved(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "crlf.txt", "line1\r\nline2\r\nline3\r\n")
	result := e.editFile(args(
		"path", "crlf.txt",
		"old_text", "line2",
		"new_text", "replaced",
	))
	if strings.Contains(result, "error") {
		t.Fatalf("edit failed: %s", result)
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
	result := e.editFile(args(
		"path", "bom.txt",
		"old_text", "hello",
		"new_text", "goodbye",
	))
	if strings.Contains(result, "error") {
		t.Fatalf("edit failed: %s", result)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "bom.txt"))
	if !strings.HasPrefix(string(data), "\xEF\xBB\xBF") {
		t.Error("BOM should be preserved after edit")
	}
	if !strings.Contains(string(data), "goodbye") {
		t.Error("edit should have been applied")
	}
}
