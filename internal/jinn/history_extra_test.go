package jinn

import (
	"os"
	"path/filepath"
	"testing"
)

// loadHistory: corrupt JSON branch (75% → cover error path).
func TestLoadHistory_CorruptJSON(t *testing.T) {
	e, _ := historyEngine(t)

	// Write corrupt JSON to the index file.
	if err := os.MkdirAll(filepath.Dir(e.indexPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(e.indexPath(), []byte("{bad json"), 0o600)

	_, err := e.loadHistory()
	if err == nil {
		t.Fatal("expected error for corrupt index JSON, got nil")
	}
}

// loadHistory: read error (directory in place of file).
func TestLoadHistory_ReadError(t *testing.T) {
	e, _ := historyEngine(t)

	// Place a directory at the index path so ReadFile fails.
	indexPath := e.indexPath()
	os.MkdirAll(indexPath, 0o755) // directory, not a file

	_, err := e.loadHistory()
	if err == nil {
		t.Fatal("expected error reading directory as index file, got nil")
	}
}

// saveHistory: unwritable directory.
func TestSaveHistory_UnwritableDir(t *testing.T) {
	e, _ := historyEngine(t)

	// Create the history dir and then make it unwritable.
	histDir := e.historyDir()
	if err := os.MkdirAll(histDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.Chmod(histDir, 0o555)
	t.Cleanup(func() { os.Chmod(histDir, 0o755) })

	err := e.saveHistory(historyFile{Version: 1, Entries: []historyEntry{}})
	if err == nil {
		t.Skip("read-only dir not enforced (possibly running as root)")
	}
}

// atomicWriteBytes: round-trip.
func TestAtomicWriteBytes_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "blob.bin")
	data := []byte("binary content")
	if err := atomicWriteBytes(path, data, 0o600); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

// atomicWriteBytes: write to read-only dir.
func TestAtomicWriteBytes_UnwritableDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Read-only dir — CreateTemp should fail.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	err := atomicWriteBytes(filepath.Join(dir, "data.bin"), []byte("data"), 0o600)
	if err == nil {
		t.Skip("read-only dir not enforced (possibly running as root)")
	}
}

// recordSnapshot: file too large to snapshot — no-op, no error.
func TestRecordSnapshot_TooLargeSkipsSilently(t *testing.T) {
	e, workDir := historyEngine(t)

	absPath := filepath.Join(workDir, "large.bin")
	big := make([]byte, historyMaxBlobBytes+1)
	err := e.recordSnapshot(absPath, "large.bin", "write_file", big)
	if err != nil {
		t.Fatalf("recordSnapshot with oversized content returned error: %v", err)
	}
	// Index should be empty — snapshot was silently skipped.
	histMu.Lock()
	hf, _ := e.loadHistory()
	histMu.Unlock()
	if len(hf.Entries) != 0 {
		t.Errorf("expected 0 entries (skipped), got %d", len(hf.Entries))
	}
}
