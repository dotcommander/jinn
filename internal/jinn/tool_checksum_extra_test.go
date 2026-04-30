package jinn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChecksumTree_SingleFile(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "hello.txt", "hello world")

	out, err := e.checksumTree(args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "hello.txt") {
		t.Errorf("expected hello.txt in output, got: %s", out)
	}
}

func TestChecksumTree_PatternFilter(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.go", "package a")
	writeTestFile(t, dir, "b.txt", "text")

	out, err := e.checksumTree(args("pattern", "*.go"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "a.go") {
		t.Errorf("expected a.go in output, got: %s", out)
	}
	if strings.Contains(out, "b.txt") {
		t.Errorf("b.txt should be excluded by pattern, got: %s", out)
	}
}

func TestChecksumTree_NotADirectory(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "plain.txt", "content")

	_, err := e.checksumTree(args("path", "plain.txt"))
	if err == nil {
		t.Fatal("expected error for non-directory path, got nil")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("expected 'not a directory', got: %v", err)
	}
}

func TestChecksumTree_PathNotFound(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	_, err := e.checksumTree(args("path", "nonexistent"))
	if err == nil {
		t.Fatal("expected error for missing path, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found', got: %v", err)
	}
}

func TestHashFile_OpenError(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	// Pass a path that does not exist — hashFile should return an error.
	_, err := e.hashFile("/nonexistent/path/file.txt")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestHashFile_Deterministic(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	p := filepath.Join(dir, "deterministic.txt")
	os.WriteFile(p, []byte("constant content"), 0o644)

	h1, err := e.hashFile(p)
	if err != nil {
		t.Fatalf("first hash: %v", err)
	}
	h2, err := e.hashFile(p)
	if err != nil {
		t.Fatalf("second hash: %v", err)
	}
	if h1 != h2 {
		t.Errorf("hash not deterministic: %q vs %q", h1, h2)
	}
}

func TestChecksumTree_SkipsSymlinks(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "real.txt", "real content")

	// Create a symlink inside the workdir.
	target := filepath.Join(dir, "link.txt")
	os.Symlink(filepath.Join(dir, "real.txt"), target)

	out, err := e.checksumTree(args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Symlinks are skipped; only real.txt should appear.
	if strings.Contains(out, "link.txt") {
		t.Errorf("symlink should be excluded from checksum tree, got: %s", out)
	}
}
