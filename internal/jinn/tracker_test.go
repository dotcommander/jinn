package jinn

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckStale_Untracked(t *testing.T) {
	t.Parallel()
	ft := newFileTracker()
	if err := ft.checkStale("/some/path"); err != nil {
		t.Errorf("untracked path should not be stale: %v", err)
	}
}

func TestCheckStale_Fresh(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(p, []byte("hi"), 0o644)
	info, _ := os.Stat(p)

	ft := newFileTracker()
	ft.record(p, info.ModTime(), info.Size())

	if err := ft.checkStale(p); err != nil {
		t.Errorf("fresh file should not be stale: %v", err)
	}
}

func TestCheckStale_Modified(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(p, []byte("hi"), 0o644)

	ft := newFileTracker()
	ft.record(p, time.Now().Add(-2*time.Second), 2)

	_ = os.WriteFile(p, []byte("changed"), 0o644)
	if err := ft.checkStale(p); err == nil {
		t.Error("modified file should be stale")
	}
}

func TestCheckStale_SameMtimeDifferentSize(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	ft := newFileTracker()
	ft.record(p, info.ModTime(), info.Size())

	// Rewrite with a different length, then force the mtime back to the
	// recorded tick — the mtime check alone would miss this.
	if err := os.WriteFile(p, []byte("rewritten, longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := ft.checkStale(p); err == nil {
		t.Error("expected stale error for same-mtime different-size rewrite")
	}
}
