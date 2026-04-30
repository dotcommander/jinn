package jinn

import (
	"os"
	"path/filepath"
	"testing"
)

// saveMemory / loadMemory error-path coverage.
// These tests must NOT use t.Parallel because t.Setenv is process-wide.

func TestLoadMemory_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", dir)

	// Write corrupt JSON to the memory file path.
	memDir := filepath.Join(dir, "jinn")
	os.MkdirAll(memDir, 0o755)
	os.WriteFile(filepath.Join(memDir, "memory.json"), []byte("{not valid json}"), 0o600)

	_, err := loadMemory()
	if err == nil {
		t.Fatal("expected error for corrupt JSON, got nil")
	}
}

func TestLoadMemory_ReadError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", dir)

	memDir := filepath.Join(dir, "jinn")
	os.MkdirAll(memDir, 0o755)

	// Make memory.json a directory to trigger a read error.
	memPath := filepath.Join(memDir, "memory.json")
	os.Mkdir(memPath, 0o755)

	_, err := loadMemory()
	if err == nil {
		t.Fatal("expected error reading a directory as a file, got nil")
	}
}

func TestSaveMemory_UnwritableDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", dir)

	memDir := filepath.Join(dir, "jinn")
	os.MkdirAll(memDir, 0o755)
	// Make the jinn dir read-only so atomicWriteJSON → CreateTemp fails.
	os.Chmod(memDir, 0o555)
	t.Cleanup(func() { os.Chmod(memDir, 0o755) })

	m := memoryFile{Version: 1, Entries: map[string]memoryEntry{}}
	err := saveMemory(m)
	if err == nil {
		// Possibly running as root — skip.
		t.Skip("read-only dir not enforced (possibly running as root)")
	}
}

func TestMemoryForget_InvalidKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", dir)
	e, _ := testEngine(t)

	_, err := e.memoryForget(args("key", "invalid/key"))
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestMemoryRecall_InvalidKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", dir)
	e, _ := testEngine(t)

	_, err := e.memoryRecall(args("key", "has space"))
	if err == nil {
		t.Fatal("expected error for invalid key in recall")
	}
}

func TestMemorySave_LoadError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", dir)
	e, _ := testEngine(t)

	// Pre-corrupt the memory file.
	memDir := filepath.Join(dir, "jinn")
	os.MkdirAll(memDir, 0o755)
	os.WriteFile(filepath.Join(memDir, "memory.json"), []byte("!!!"), 0o600)

	_, err := e.memorySave(args("key", "k", "value", "v"))
	if err == nil {
		t.Fatal("expected error when memory file is corrupt")
	}
}

func TestMemoryList_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", dir)
	e, _ := testEngine(t)

	// No memory file written — loadMemory returns empty struct (not found = ok).
	out, err := e.memoryList()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty output for empty list")
	}
}
