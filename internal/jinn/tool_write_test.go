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
	result, err := e.writeFile(args("path", "out.txt", "content", "hello world"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	result, err := e.writeFile(args("path", "a/b/c.txt", "content", "deep"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	_, err := e.writeFile(args("path", "clean.txt", "content", "data"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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

	result, err := e.writeFile(args("path", "tracked.txt", "content", "v3"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "blocked") {
		t.Errorf("second write should not be blocked after tracker update, got: %s", result)
	}
}

func TestWriteFile_PreservesPermissions(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Create a file with executable permissions.
	p := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho v1"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Read then overwrite via writeFile.
	e.readFile(args("path", "script.sh"))
	_, err := e.writeFile(args("path", "script.sh", "content", "#!/bin/sh\necho v2"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("permissions = %o, want 0755", info.Mode().Perm())
	}
}

func TestWriteFile_NewFileGetsDefault(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	_, err := e.writeFile(args("path", "brand-new.txt", "content", "fresh"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "brand-new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("permissions = %o, want 0644", info.Mode().Perm())
	}
}
