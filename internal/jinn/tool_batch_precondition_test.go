package jinn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func requireStaleFileError(t *testing.T, err error) {
	t.Helper()
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) || sErr.Code != ErrCodeStaleFile {
		t.Fatalf("expected %s error, got: %v", ErrCodeStaleFile, err)
	}
}

func TestMultiEditIfChecksumRejectsExternalChange(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "external\n")

	edits := []interface{}{map[string]interface{}{
		"path":        "a.txt",
		"old_text":    "external",
		"new_text":    "agent",
		"if_checksum": sha256Hex([]byte("earlier\n")),
	}}
	_, err := e.multiEdit(args("edits", edits))
	requireStaleFileError(t, err)
	assertExternalContent(t, filepath.Join(dir, "a.txt"))
}

func TestApplyPatchIfChecksumsRejectsExternalChange(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "external\n")
	patch := "*** Begin Patch\n*** Update File: a.txt\n@@\n-external\n+agent\n*** End Patch"

	_, err := e.applyPatch(args(
		"patch", patch,
		"if_checksums", map[string]interface{}{"a.txt": sha256Hex([]byte("earlier\n"))},
	))
	requireStaleFileError(t, err)
	assertExternalContent(t, filepath.Join(dir, "a.txt"))
}

func TestSearchReplaceIfChecksumsRejectsExternalChange(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "external\n")

	_, err := e.searchReplace(context.Background(), args(
		"pattern", "external",
		"replacement", "agent",
		"files", "a.txt",
		"if_checksums", map[string]interface{}{"a.txt": sha256Hex([]byte("earlier\n"))},
	))
	requireStaleFileError(t, err)
	assertExternalContent(t, filepath.Join(dir, "a.txt"))
}

func TestBatchChecksumMatchesAllowMutation(t *testing.T) {
	t.Parallel()

	t.Run("multi_edit", func(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)
		writeTestFile(t, dir, "a.txt", "old\n")
		_, err := e.multiEdit(args("edits", []interface{}{map[string]interface{}{
			"path": "a.txt", "old_text": "old", "new_text": "new", "if_checksum": sha256Hex([]byte("old\n")),
		}}))
		if err != nil {
			t.Fatalf("matching checksum: %v", err)
		}
	})

	t.Run("apply_patch", func(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)
		writeTestFile(t, dir, "a.txt", "old\n")
		patch := "*** Begin Patch\n*** Update File: a.txt\n@@\n-old\n+new\n*** End Patch"
		_, err := e.applyPatch(args("patch", patch, "if_checksums", map[string]interface{}{
			"a.txt": sha256Hex([]byte("old\n")),
		}))
		if err != nil {
			t.Fatalf("matching checksum: %v", err)
		}
	})

	t.Run("search_replace", func(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)
		writeTestFile(t, dir, "a.txt", "old\n")
		_, err := e.searchReplace(context.Background(), args(
			"pattern", "old", "replacement", "new", "files", "a.txt",
			"if_checksums", map[string]interface{}{"a.txt": sha256Hex([]byte("old\n"))},
		))
		if err != nil {
			t.Fatalf("matching checksum: %v", err)
		}
	})
}

func TestSearchReplaceStaleChecksumRejectsWholeBatch(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "stale.txt", "old\n")
	writeTestFile(t, dir, "valid.txt", "old\n")

	_, err := e.searchReplace(context.Background(), args(
		"pattern", "old", "replacement", "new",
		"files", []interface{}{"stale.txt", "valid.txt"},
		"if_checksums", map[string]interface{}{
			"stale.txt": sha256Hex([]byte("earlier\n")),
			"valid.txt": sha256Hex([]byte("old\n")),
		},
	))
	requireStaleFileError(t, err)
	if got, readErr := os.ReadFile(filepath.Join(dir, "valid.txt")); readErr != nil || string(got) != "old\n" {
		t.Fatalf("valid target changed despite stale batch peer: content=%q err=%v", got, readErr)
	}
}

func TestApplyPatchRejectsChangeAfterPreflight(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	path := filepath.Join(dir, "a.txt")
	writeTestFile(t, dir, "a.txt", "preflight\n")
	resolved := []resolvedOp{{op: patchOperation{kind: "update", path: "a.txt"}, resolved: path}}
	preflights := []preflightResult{{oldContent: "preflight\n", newContent: "agent\n"}}
	writeTestFile(t, dir, "a.txt", "external\n")

	_, err := e.applyPatchOps(resolved, preflights)
	requireStaleFileError(t, err)
	assertExternalContent(t, path)
}

func TestApplyPatchAddRejectsCreationAfterPreflight(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	path := filepath.Join(dir, "a.txt")
	resolved := []resolvedOp{{op: patchOperation{kind: "add", path: "a.txt"}, resolved: path}}
	preflights := []preflightResult{{newContent: "agent\n"}}
	writeTestFile(t, dir, "a.txt", "external\n")

	_, err := e.applyPatchOps(resolved, preflights)
	requireStaleFileError(t, err)
	assertExternalContent(t, path)
}

func TestApplyPatchAddRejectsEmptyFileCreatedAfterPreflight(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	path := filepath.Join(dir, "a.txt")
	resolved := []resolvedOp{{op: patchOperation{kind: "add", path: "a.txt"}, resolved: path}}
	preflights := []preflightResult{{newContent: "agent\n"}}
	writeTestFile(t, dir, "a.txt", "")

	_, err := e.applyPatchOps(resolved, preflights)
	requireStaleFileError(t, err)
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read externally created file: %v", readErr)
	}
	if len(got) != 0 {
		t.Fatalf("externally created empty file was overwritten: %q", got)
	}
}

func TestMultiEditRejectsChangeAfterPreflight(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	path := filepath.Join(dir, "a.txt")
	writeTestFile(t, dir, "a.txt", "preflight\n")
	pending := []pendingEdit{{path: "a.txt", resolved: path, preContent: []byte("preflight\n"), updated: "agent\n"}}
	writeTestFile(t, dir, "a.txt", "external\n")

	_, err := e.writePendingEdits(pending)
	requireStaleFileError(t, err)
	assertExternalContent(t, path)
}

func TestSearchReplaceRejectsChangeAfterPreflight(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	path := filepath.Join(dir, "a.txt")
	writeTestFile(t, dir, "a.txt", "preflight\n")
	pending := []searchReplacePending{{
		candidate: searchReplaceCandidate{path: "a.txt", resolved: path},
		preData:   []byte("preflight\n"),
		updated:   "agent\n",
		replaced:  1,
	}}
	writeTestFile(t, dir, "a.txt", "external\n")

	_, err := e.srApplyWrites(nil, pending)
	requireStaleFileError(t, err)
	assertExternalContent(t, path)
}

func assertExternalContent(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != "external\n" {
		t.Fatalf("content = %q, want external content", got)
	}
}
