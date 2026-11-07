package jinn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchReceipt_ListsPartialFailedUnstarted(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	paths := []string{"a.txt", "b.txt", "c.txt"}
	for _, path := range paths {
		writeTestFile(t, dir, path, "alpha beta\n")
	}
	prepared := 0
	e.snapshotPreparedHook = func() {
		prepared++
		if prepared == 2 {
			writeTestFile(t, dir, paths[prepared-1], "external\n")
		}
	}
	_, _, err := e.Dispatch(t.Context(), "multi_edit", preparedWriteToolArgs("multi_edit", paths, false))
	if err == nil {
		t.Fatal("expected a partial-apply failure")
	}
	receipt := "[batch: partial=1; failed=b.txt; publication=3; unstarted=c.txt]"
	if !strings.Contains(err.Error(), receipt) {
		t.Fatalf("error lacks receipt %q: %v", receipt, err)
	}
	history, historyErr := e.loadHistoryLocked()
	if historyErr != nil {
		t.Fatal(historyErr)
	}
	if len(history.Entries) == 0 || history.Entries[0].ID == "" {
		t.Fatal("committed member lacks an undo id")
	}
	if !strings.Contains(err.Error(), "(undo id="+history.Entries[0].ID+")") {
		t.Fatalf("receipt omits the committed undo id: %v", err)
	}
}

func TestMultiEditPreflight_ExhaustiveCodesPublishesNothing(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "ok.txt", "hello world\n")
	writeTestFile(t, dir, "stale.txt", "stale content\n")

	staleSum := sha256Hex([]byte("earlier content"))
	_, _, err := e.Dispatch(t.Context(), "multi_edit", args("edits", []interface{}{
		args("path", "ok.txt", "old_text", "hello", "new_text", "goodbye"),
		args("path", "ok.txt", "old_text", "", "new_text", "x"),
		args("path", "stale.txt", "old_text", "stale", "new_text", "fresh", "if_checksum", staleSum),
	}))
	if err == nil {
		t.Fatal("expected a preflight failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "2 of 3 edits failed validation") {
		t.Fatalf("error does not aggregate every failure: %v", err)
	}
	for _, want := range []string{"[old_text_empty]", "stale.txt [stale_file]", "ok.txt"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("preflight error lacks %q: %v", want, err)
		}
	}
	// Exhaustive all-or-nothing: the valid edit must not be published.
	got, readErr := os.ReadFile(filepath.Join(dir, "ok.txt"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "hello world\n" {
		t.Fatalf("valid member was published despite preflight conflicts: %q", got)
	}
	got, _ = os.ReadFile(filepath.Join(dir, "stale.txt"))
	if string(got) != "stale content\n" {
		t.Fatalf("stale member was modified: %q", got)
	}
}

func TestBatchRetry_NeverReplaysCommitted(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	paths := []string{"a.txt", "b.txt", "c.txt"}
	for _, path := range paths {
		writeTestFile(t, dir, path, "alpha beta\n")
	}
	prepared := 0
	e.snapshotPreparedHook = func() {
		prepared++
		if prepared == 2 {
			writeTestFile(t, dir, paths[prepared-1], "external\n")
		}
	}
	input := preparedWriteToolArgs("multi_edit", paths, false)
	if _, _, err := e.Dispatch(t.Context(), "multi_edit", input); err == nil {
		t.Fatal("expected a partial-apply failure")
	}
	e.snapshotPreparedHook = nil

	// A full-batch retry uses the same inputs: the committed member's
	// old_text no longer matches, so it surfaces as failed validation and
	// nothing is rewritten — committed state is never replayed.
	_, _, err := e.Dispatch(t.Context(), "multi_edit", input)
	if err == nil {
		t.Fatal("expected the retry to fail validation on the committed member")
	}
	if !strings.Contains(err.Error(), "a.txt [stale_file]") {
		t.Fatalf("retry does not name the committed member: %v", err)
	}
	got, readErr := os.ReadFile(filepath.Join(dir, "a.txt"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "new new\n" {
		t.Fatalf("committed member rewritten on retry: %q", got)
	}
}
