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
	os.WriteFile(p, []byte("hi"), 0o644)
	info, _ := os.Stat(p)

	ft := newFileTracker()
	ft.record(p, info.ModTime())

	if err := ft.checkStale(p); err != nil {
		t.Errorf("fresh file should not be stale: %v", err)
	}
}

func TestCheckStale_Modified(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	os.WriteFile(p, []byte("hi"), 0o644)

	ft := newFileTracker()
	ft.record(p, time.Now().Add(-2*time.Second))

	os.WriteFile(p, []byte("changed"), 0o644)
	if err := ft.checkStale(p); err == nil {
		t.Error("modified file should be stale")
	}
}
