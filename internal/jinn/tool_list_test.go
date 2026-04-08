package jinn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListDir_Basic(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "")
	writeTestFile(t, dir, "b.txt", "")
	result, err := e.listDir(args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "a.txt") || !strings.Contains(result, "b.txt") {
		t.Errorf("expected both files, got: %s", result)
	}
}

func TestListDir_HiddenExcluded(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, ".hidden", "secret")
	writeTestFile(t, dir, "visible.txt", "hi")
	result, err := e.listDir(args("depth", float64(1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, ".hidden") {
		t.Errorf("hidden files should be excluded, got: %s", result)
	}
	if !strings.Contains(result, "visible.txt") {
		t.Errorf("visible files should be listed, got: %s", result)
	}
}

func TestListDir_DepthClamp(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	// depth < 1 clamps to 1, depth > 10 clamps to 10. Verify no panic.
	e.listDir(args("depth", float64(0)))
	e.listDir(args("depth", float64(99)))
}

func TestListDir_EmptySubdir(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	os.Mkdir(filepath.Join(dir, "emptydir"), 0o755)
	result, err := e.listDir(args("path", "emptydir", "depth", float64(1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("result should not be empty string")
	}
}
