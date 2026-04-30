package jinn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestUndoRestore_StaleTrackerRejection verifies that a file externally modified
// since the last tracker record is rejected before restore overwrites it.
func TestUndoRestore_StaleTrackerRejection(t *testing.T) {
	e, workDir := undoEngine(t)
	p := undoMkFile(t, e, workDir, "target.txt", "original")

	// Write once to create a snapshot (tracker records the new mtime).
	if _, err := e.writeFile(args("path", "target.txt", "content", "v2")); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	id := firstSnapshotID(t, e)

	// Advance mtime externally so the tracker thinks the file is stale.
	future := undoFileModTime(t, p).Add(2 * time.Second)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	_, err := e.undoTool(args("action", "restore", "id", id))
	if err == nil {
		t.Fatal("expected stale-file error, got nil")
	}
	if !strings.Contains(err.Error(), "modified since last read") {
		t.Errorf("expected stale message, got: %v", err)
	}
}

// TestUndoRestore_NotFoundID verifies ErrWithSuggestion for an unknown snapshot ID.
func TestUndoRestore_NotFoundID(t *testing.T) {
	e, _ := undoEngine(t)

	_, err := e.undoTool(args("action", "restore", "id", "deadbeef1234"))
	if err == nil {
		t.Fatal("expected error for unknown snapshot id")
	}
	if !strings.Contains(err.Error(), "snapshot not found") {
		t.Errorf("wrong error: %v", err)
	}
}

// TestUndoRestore_AbsPathMismatch verifies the path-mismatch guard.
// This is exercised by mangling an entry's AbsPath after recording it.
func TestUndoRestore_AbsPathMismatch(t *testing.T) {
	e, workDir := undoEngine(t)
	undoMkFile(t, e, workDir, "mismatch.txt", "data")

	if _, err := e.writeFile(args("path", "mismatch.txt", "content", "v2")); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	id := firstSnapshotID(t, e)

	// Mangle the AbsPath in the stored entry so it won't match the resolved path.
	histMu.Lock()
	hf, _ := e.loadHistory()
	hf.Entries[0].AbsPath = filepath.Join(workDir, "different.txt")
	e.saveHistory(hf)
	histMu.Unlock()

	// Also create the differently-named file so checkPath succeeds but mismatch is caught.
	writeTestFile(t, workDir, "different.txt", "unrelated")
	// Force tracker to allow it.
	p2 := filepath.Join(workDir, "different.txt")
	info, _ := os.Stat(p2)
	e.tracker.record(p2, info.ModTime())

	_, err := e.undoTool(args("action", "restore", "id", id))
	if err == nil {
		t.Fatal("expected path-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("expected 'mismatch' in error, got: %v", err)
	}
}

// TestUndoClear_Empty verifies clear on empty history is a no-op success.
func TestUndoClear_Empty(t *testing.T) {
	e, _ := undoEngine(t)

	result, err := e.undoTool(args("action", "clear"))
	if err != nil {
		t.Fatalf("clear empty: %v", err)
	}
	if !strings.Contains(result, "cleared") {
		t.Errorf("expected 'cleared' in result, got: %q", result)
	}
}
