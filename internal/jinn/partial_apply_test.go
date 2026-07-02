package jinn

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var undoIDRe = regexp.MustCompile(`undo id=[0-9a-f]{16}`)

// partialFixture creates a/f1.txt, b/f2.txt, c/f3.txt and makes b/
// unwritable so the second write of a batch fails mid-way.
func partialFixture(t *testing.T) (*Engine, string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits")
	}
	e, dir := testEngine(t)
	for _, sub := range []string{"a", "b", "c"} {
		writeTestFile(t, dir, filepath.Join(sub, "f.txt"), "hello world\n")
	}
	roDir := filepath.Join(dir, "b")
	if err := os.Chmod(roDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o755) })
	return e, dir
}

func assertPartialApplyErr(t *testing.T, err error, appliedPath string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a partial-apply error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "partial apply") {
		t.Fatalf("error lacks 'partial apply': %v", err)
	}
	if !strings.Contains(msg, appliedPath) {
		t.Errorf("error does not enumerate applied file %s: %v", appliedPath, err)
	}
	if !undoIDRe.MatchString(msg) {
		t.Errorf("error lacks an undo id: %v", err)
	}
}

func TestApplyPatch_PartialFailureEnumeratesApplied(t *testing.T) {
	t.Parallel()
	e, dir := partialFixture(t)

	patch := "*** Begin Patch\n"
	for _, sub := range []string{"a", "b", "c"} {
		patch += fmt.Sprintf("*** Update File: %s/f.txt\n@@ hello world\n-hello world\n+goodbye world\n", sub)
	}
	patch += "*** End Patch"

	_, err := e.applyPatch(args("patch", patch))
	assertPartialApplyErr(t, err, "a/f.txt")

	got, _ := os.ReadFile(filepath.Join(dir, "c", "f.txt"))
	if string(got) != "hello world\n" {
		t.Errorf("file after the failure point was written: %q", got)
	}
}

func TestMultiEdit_PartialFailureEnumeratesApplied(t *testing.T) {
	t.Parallel()
	e, _ := partialFixture(t)

	edits := []interface{}{}
	for _, sub := range []string{"a", "b", "c"} {
		edits = append(edits, map[string]interface{}{
			"path":     sub + "/f.txt",
			"old_text": "hello",
			"new_text": "goodbye",
		})
	}
	_, err := e.multiEdit(args("edits", edits))
	assertPartialApplyErr(t, err, "a/f.txt")
}

func TestSearchReplace_PartialFailureEnumeratesApplied(t *testing.T) {
	t.Parallel()
	e, _ := partialFixture(t)

	_, err := e.searchReplace(t.Context(), args(
		"pattern", "hello",
		"replacement", "goodbye",
		"files", []interface{}{"a/f.txt", "b/f.txt", "c/f.txt"},
	))
	if err == nil {
		t.Fatal("expected a partial-apply error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "partial apply") {
		t.Fatalf("error lacks 'partial apply': %v", err)
	}
	if !undoIDRe.MatchString(msg) {
		t.Errorf("error lacks an undo id: %v", err)
	}
}
