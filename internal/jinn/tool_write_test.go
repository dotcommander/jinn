package jinn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFile_Basic(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	result := e.writeFile(args("path", "out.txt", "content", "hello world"))
	if !strings.Contains(result, "wrote 11 bytes") {
		t.Errorf("expected 'wrote 11 bytes', got: %s", result)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "out.txt"))
	if string(data) != "hello world" {
		t.Errorf("content = %q, want %q", data, "hello world")
	}
}

func TestWriteFile_CreatesParentDirs(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	result := e.writeFile(args("path", "a/b/c.txt", "content", "deep"))
	if !strings.Contains(result, "wrote") {
		t.Errorf("expected success, got: %s", result)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "a/b/c.txt"))
	if string(data) != "deep" {
		t.Errorf("content = %q, want %q", data, "deep")
	}
}

func TestWriteFile_NoLeftoverTemp(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	e.writeFile(args("path", "clean.txt", "content", "data"))
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".jinn-") {
			t.Errorf("leftover temp file: %s", entry.Name())
		}
	}
}

func TestWriteFile_UpdatesTracker(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "tracked.txt", "v1")

	e.readFile(args("path", "tracked.txt"))
	e.writeFile(args("path", "tracked.txt", "content", "v2"))

	result := e.writeFile(args("path", "tracked.txt", "content", "v3"))
	if strings.Contains(result, "blocked") {
		t.Errorf("second write should not be blocked after tracker update, got: %s", result)
	}
}
