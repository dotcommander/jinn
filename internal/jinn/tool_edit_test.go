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
