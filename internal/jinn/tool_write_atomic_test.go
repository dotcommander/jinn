package jinn

import (
	"os"
	"path/filepath"
	"testing"
)

// atomicWriteJSON error-path coverage

func TestAtomicWriteJSON_MkdirFailsOnFile(t *testing.T) {
	t.Parallel()
	// Place a regular file where the dir should be — MkdirAll must fail.
	base := t.TempDir()
	blocker := filepath.Join(base, "notadir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// path whose parent is "notadir/sub" — MkdirAll fails because notadir is a file
	target := filepath.Join(blocker, "sub", "data.json")
	err := atomicWriteJSON(target, map[string]string{"k": "v"}, 0o600)
	if err == nil {
		t.Fatal("expected error when parent path is a file, got nil")
	}
}

func TestAtomicWriteJSON_UnmarshalableInput(t *testing.T) {
	t.Parallel()
	// channels cannot be marshalled to JSON.
	ch := make(chan int)
	err := atomicWriteJSON(filepath.Join(t.TempDir(), "x.json"), ch, 0o600)
	if err == nil {
		t.Fatal("expected marshal error for channel, got nil")
	}
}

func TestAtomicWriteJSON_RoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "data.json")
	payload := map[string]string{"hello": "world"}
	if err := atomicWriteJSON(path, payload, 0o600); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data) == 0 {
		t.Error("file is empty")
	}
}

// atomicWriteFile error-path coverage

func TestAtomicWriteFile_WritesToNewFile(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	target := filepath.Join(dir, "fresh.txt")
	if err := e.atomicWriteFile(target, "new content"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "new content" {
		t.Errorf("content = %q, want %q", data, "new content")
	}
}

func TestAtomicWriteFile_OverwritesExisting(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	target := filepath.Join(dir, "existing.txt")
	os.WriteFile(target, []byte("old"), 0o644)
	if err := e.atomicWriteFile(target, "new"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "new" {
		t.Errorf("content = %q, want %q", data, "new")
	}
}

func TestAtomicWriteFile_NoTempLeftover(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	target := filepath.Join(dir, "clean.txt")
	if err := e.atomicWriteFile(target, "data"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, ent := range entries {
		if len(ent.Name()) > 6 && ent.Name()[:6] == ".jinn-" {
			t.Errorf("temp file left behind: %s", ent.Name())
		}
	}
}

func TestAtomicWriteFile_FailsOnReadonlyDir(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Make the target directory read-only so CreateTemp fails.
	roDir := filepath.Join(dir, "ro")
	if err := os.Mkdir(roDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(roDir, 0o755) })

	target := filepath.Join(roDir, "file.txt")
	// Pre-create file so stat doesn't fail; but dir is read-only for writes.
	// atomicWriteFile tries CreateTemp in the same dir — should fail.
	err := e.atomicWriteFile(target, "data")
	if err == nil {
		// On some CI/root environments permissions aren't enforced — skip rather than fail.
		t.Skip("read-only dir not enforced (possibly running as root)")
	}
}
