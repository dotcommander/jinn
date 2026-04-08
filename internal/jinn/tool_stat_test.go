package jinn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatFile_Regular(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "stat.txt", "one\ntwo\nthree\n")
	result := e.statFile(args("path", "stat.txt"))
	if !strings.Contains(result, "type: file") {
		t.Errorf("expected type: file, got: %s", result)
	}
	if !strings.Contains(result, "lines: 3") {
		t.Errorf("expected lines: 3, got: %s", result)
	}
	if !strings.Contains(result, "modified:") {
		t.Errorf("expected modified timestamp, got: %s", result)
	}
}

func TestStatFile_Directory(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	os.Mkdir(filepath.Join(dir, "statdir"), 0o755)
	result := e.statFile(args("path", "statdir"))
	if !strings.Contains(result, "type: directory") {
		t.Errorf("expected type: directory, got: %s", result)
	}
}

func TestStatFile_NotFound(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	result := e.statFile(args("path", "ghost.txt"))
	if !strings.Contains(result, "file not found") {
		t.Errorf("expected file not found, got: %s", result)
	}
}
