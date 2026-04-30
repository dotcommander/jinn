package jinn

import (
	"strings"
	"testing"
)

// Change 1: empty old_text guard in edit_file.
func TestEditFile_RejectsEmptyOldText(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "e.txt", "hello\n")
	_, err := e.editFile(args("path", "e.txt", "old_text", "", "new_text", "world"))
	if err == nil {
		t.Fatal("expected error for empty old_text, got nil")
	}
	if !strings.Contains(err.Error(), "old_text cannot be empty") {
		t.Errorf("expected 'old_text cannot be empty', got: %v", err)
	}
	s, ok := err.(*ErrWithSuggestion)
	if !ok {
		t.Fatalf("expected *ErrWithSuggestion, got %T", err)
	}
	if !strings.Contains(s.Suggestion, "non-empty string") {
		t.Errorf("expected suggestion about non-empty string, got: %s", s.Suggestion)
	}
}

// Change 1: empty old_text guard in multi_edit cites the edit index.
func TestMultiEdit_RejectsEmptyOldTextWithIndex(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "me.txt", "hello\n")
	edits := []interface{}{
		map[string]interface{}{"path": "me.txt", "old_text": "hello", "new_text": "hi"},
		map[string]interface{}{"path": "me.txt", "old_text": "", "new_text": "world"},
	}
	_, err := e.multiEdit(args("edits", edits))
	if err == nil {
		t.Fatal("expected error for empty old_text in edits[1], got nil")
	}
	if !strings.Contains(err.Error(), "edits[1]") {
		t.Errorf("expected 'edits[1]' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "old_text cannot be empty") {
		t.Errorf("expected 'old_text cannot be empty', got: %v", err)
	}
}

// Change 2: overlapping regions in multi_edit.
func TestMultiEdit_RejectsOverlappingEdits(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// edits[0] targets "hello world", edits[1] targets "hello" — subset → overlapping.
	writeTestFile(t, dir, "overlap.txt", "hello world\n")
	edits := []interface{}{
		map[string]interface{}{"path": "overlap.txt", "old_text": "hello world", "new_text": "hi there"},
		map[string]interface{}{"path": "overlap.txt", "old_text": "hello", "new_text": "hey"},
	}
	_, err := e.multiEdit(args("edits", edits))
	if err == nil {
		t.Fatal("expected error for overlapping edits, got nil")
	}
	if !strings.Contains(err.Error(), "overlapping regions") {
		t.Errorf("expected 'overlapping regions', got: %v", err)
	}
	s, ok := err.(*ErrWithSuggestion)
	if !ok {
		t.Fatalf("expected *ErrWithSuggestion, got %T", err)
	}
	if !strings.Contains(s.Suggestion, "separate multi_edit") {
		t.Errorf("expected suggestion about separate calls, got: %s", s.Suggestion)
	}
}

// Change 3: no-op guard in edit_file.
func TestEditFile_RejectsNoOpEdit(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "noop.txt", "hello world\n")
	// old_text == new_text → replacement produces identical content.
	_, err := e.editFile(args("path", "noop.txt", "old_text", "hello", "new_text", "hello"))
	if err == nil {
		t.Fatal("expected error for no-op edit, got nil")
	}
	if !strings.Contains(err.Error(), "edit produced no changes") {
		t.Errorf("expected 'edit produced no changes', got: %v", err)
	}
	s, ok := err.(*ErrWithSuggestion)
	if !ok {
		t.Fatalf("expected *ErrWithSuggestion, got %T", err)
	}
	if !strings.Contains(s.Suggestion, "equivalent") {
		t.Errorf("expected suggestion about equivalence, got: %s", s.Suggestion)
	}
}
