package jinn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestApplyPatch_Engine_HappyPaths covers add/delete/update via the engine
// method using the standard testEngine + writeTestFile helpers. Parser-level
// concerns are already covered in patch_test.go; these tests focus on the
// engine's side effects: file writes, result text, and Meta.
func TestApplyPatch_Engine_HappyPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(t *testing.T, dir string)
		patch   string
		check   func(t *testing.T, dir string, result *ToolResult)
		wantSub string // required substring in result.Text
	}{
		{
			name:  "add creates file with expected content",
			setup: func(t *testing.T, dir string) {},
			patch: "*** Begin Patch\n*** Add File: created.txt\n+hello\n+world\n*** End Patch",
			check: func(t *testing.T, dir string, result *ToolResult) {
				data, err := os.ReadFile(filepath.Join(dir, "created.txt"))
				if err != nil {
					t.Fatalf("file not created: %v", err)
				}
				if string(data) != "hello\nworld\n" {
					t.Errorf("content = %q, want %q", data, "hello\nworld\n")
				}
			},
			wantSub: "added created.txt",
		},
		{
			name: "delete removes existing file",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, dir, "doomed.txt", "bye\n")
			},
			patch: "*** Begin Patch\n*** Delete File: doomed.txt\n*** End Patch",
			check: func(t *testing.T, dir string, result *ToolResult) {
				if _, err := os.Stat(filepath.Join(dir, "doomed.txt")); !os.IsNotExist(err) {
					t.Error("file should have been deleted")
				}
			},
			wantSub: "deleted doomed.txt",
		},
		{
			name: "update modifies file content",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, dir, "target.txt", "line1\nline2\nline3\n")
			},
			patch: "*** Begin Patch\n*** Update File: target.txt\n@@ line2\n line2\n-line3\n+LINE3\n*** End Patch",
			check: func(t *testing.T, dir string, result *ToolResult) {
				data, err := os.ReadFile(filepath.Join(dir, "target.txt"))
				if err != nil {
					t.Fatalf("read failed: %v", err)
				}
				if string(data) != "line1\nline2\nLINE3\n" {
					t.Errorf("content = %q, want %q", data, "line1\nline2\nLINE3\n")
				}
				// Meta should carry diff information.
				if result.Meta == nil {
					t.Fatal("expected Meta with diff details")
				}
				edit, ok := result.Meta["edit"].(editDetails)
				if !ok {
					t.Fatalf("expected editDetails in Meta, got %T", result.Meta["edit"])
				}
				if edit.Diff == "" {
					t.Error("expected non-empty diff in Meta")
				}
				if !strings.Contains(edit.Diff, "- line3") {
					t.Errorf("diff should show removed line, got: %s", edit.Diff)
				}
				if !strings.Contains(edit.Diff, "+ LINE3") {
					t.Errorf("diff should show added line, got: %s", edit.Diff)
				}
			},
			wantSub: "updated target.txt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, dir := testEngine(t)
			tc.setup(t, dir)
			result, err := e.applyPatch(args("patch", tc.patch))
			if err != nil {
				t.Fatalf("applyPatch error: %v", err)
			}
			if !strings.Contains(result.Text, tc.wantSub) {
				t.Errorf("result.Text = %q, want substring %q", result.Text, tc.wantSub)
			}
			tc.check(t, dir, result)
		})
	}
}

// TestApplyPatch_Engine_DryRun asserts that a patch with dry_run:true returns
// a preview result and leaves the filesystem completely unchanged.
func TestApplyPatch_Engine_DryRun(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "dry.txt", "alpha\nbeta\ngamma\n")

	patch := "*** Begin Patch\n*** Update File: dry.txt\n@@ beta\n beta\n-gamma\n+GAMMA\n*** End Patch"
	result, err := e.applyPatch(args("patch", patch, "dry_run", true))
	if err != nil {
		t.Fatalf("applyPatch dry_run error: %v", err)
	}
	if !strings.Contains(result.Text, "[dry-run]") {
		t.Errorf("expected [dry-run] prefix, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "would update dry.txt") {
		t.Errorf("expected 'would update dry.txt' in result, got: %s", result.Text)
	}

	// Filesystem must be unchanged.
	data, err := os.ReadFile(filepath.Join(dir, "dry.txt"))
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(data) != "alpha\nbeta\ngamma\n" {
		t.Errorf("file should be unchanged after dry_run, got: %q", data)
	}
}

// TestApplyPatch_Engine_DryRun_Add asserts that an add operation with dry_run
// does not create the file on disk.
func TestApplyPatch_Engine_DryRun_Add(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	patch := "*** Begin Patch\n*** Add File: ghost.txt\n+should not appear\n*** End Patch"
	result, err := e.applyPatch(args("patch", patch, "dry_run", true))
	if err != nil {
		t.Fatalf("applyPatch dry_run error: %v", err)
	}
	if !strings.Contains(result.Text, "[dry-run]") {
		t.Errorf("expected [dry-run] prefix, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "would add ghost.txt") {
		t.Errorf("expected 'would add ghost.txt', got: %s", result.Text)
	}
	if _, err := os.Stat(filepath.Join(dir, "ghost.txt")); !os.IsNotExist(err) {
		t.Error("file must not be created during dry_run")
	}
}

// TestApplyPatch_Engine_StaleFile verifies that an update op is rejected with a
// staleness error when the file has been modified out-of-band after the engine
// last read it. The file must not be modified by the rejected call.
//
// The out-of-band write uses the same content so that Phase 1 preflight
// (which reads the file and validates the patch hunks) still succeeds —
// only Phase 2's checkStale sees the advanced mtime and rejects the write.
func TestApplyPatch_Engine_StaleFile(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	fp := filepath.Join(dir, "stale.txt")
	const original = "line1\noriginal\nline3\n"
	writeTestFile(t, dir, "stale.txt", original)

	// Read the file so the tracker records its mtime.
	e.readFile(args("path", "stale.txt"))

	// Advance mtime without changing content — preflight will still find the
	// patch lines, but checkStale detects the newer mtime and rejects Phase 2.
	time.Sleep(10 * time.Millisecond)
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(fp, future, future); err != nil {
		t.Fatal(err)
	}

	patch := "*** Begin Patch\n*** Update File: stale.txt\n@@ original\n-original\n+ORIGINAL\n*** End Patch"
	_, err := e.applyPatch(args("patch", patch))
	if err == nil {
		t.Fatal("expected staleness error, got nil")
	}
	if !strings.Contains(err.Error(), "modified since last read") {
		t.Errorf("expected 'modified since last read' in error, got: %v", err)
	}

	// File must still contain the original content — not the patch result.
	data, readErr := os.ReadFile(fp)
	if readErr != nil {
		t.Fatalf("read failed: %v", readErr)
	}
	if string(data) != original {
		t.Errorf("file should be unchanged after stale rejection, got: %q", data)
	}
}
