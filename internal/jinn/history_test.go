package jinn

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// historyEngine returns an Engine with JINN_CONFIG_DIR isolated to t.TempDir().
// Must NOT call t.Parallel() — t.Setenv is serial only.
func historyEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	return testEngine(t)
}

func TestHistoryDir_PerWorkdir(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e1, _ := testEngine(t)
	e2 := New(t.TempDir(), "dev")
	if e1.historyDir() == e2.historyDir() {
		t.Error("two engines with different workDirs must use different history dirs")
	}
}

func TestHistoryDir_SameWorkdirSameHash(t *testing.T) {
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e, _ := testEngine(t)
	if e.historyDir() != e.historyDir() {
		t.Error("same engine must return same history dir each call")
	}
}

func TestLoadHistory_EmptyOnFirstLoad(t *testing.T) {
	e, _ := historyEngine(t)

	hf, err := e.loadHistory()
	if err != nil {
		t.Fatalf("loadHistory: %v", err)
	}
	if hf.Version != 1 {
		t.Errorf("version: got %d, want 1", hf.Version)
	}
	if len(hf.Entries) != 0 {
		t.Errorf("entries: got %d, want 0", len(hf.Entries))
	}
}

func TestRecordSnapshot_BasicRoundtrip(t *testing.T) {
	e, workDir := historyEngine(t)

	absPath := filepath.Join(workDir, "test.txt")
	preContent := []byte("hello world")
	if err := e.recordSnapshot(absPath, "test.txt", "write_file", preContent); err != nil {
		t.Fatalf("recordSnapshot: %v", err)
	}

	histMu.Lock()
	hf, err := e.loadHistory()
	histMu.Unlock()
	if err != nil {
		t.Fatalf("loadHistory: %v", err)
	}
	if len(hf.Entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(hf.Entries))
	}
	ent := hf.Entries[0]
	if ent.Op != "write_file" {
		t.Errorf("op: got %q, want write_file", ent.Op)
	}
	if ent.DisplayPath != "test.txt" {
		t.Errorf("display_path: got %q, want test.txt", ent.DisplayPath)
	}
	if ent.AbsPath != absPath {
		t.Errorf("abs_path: got %q, want %q", ent.AbsPath, absPath)
	}
	if ent.Created {
		t.Error("created should be false for existing file")
	}
	if ent.BlobHash == "" {
		t.Error("blob_hash should not be empty")
	}
}

func TestRecordSnapshot_NewFile(t *testing.T) {
	e, workDir := historyEngine(t)

	absPath := filepath.Join(workDir, "new.txt")
	if err := e.recordSnapshot(absPath, "new.txt", "write_file", nil); err != nil {
		t.Fatalf("recordSnapshot: %v", err)
	}

	histMu.Lock()
	hf, _ := e.loadHistory()
	histMu.Unlock()
	if len(hf.Entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(hf.Entries))
	}
	if !hf.Entries[0].Created {
		t.Error("created should be true for new file")
	}
	if hf.Entries[0].BlobPath != "" {
		t.Error("blob_path should be empty for new file")
	}
}

func TestEvictHistory_ByEntryCount(t *testing.T) {
	e, workDir := historyEngine(t)

	absPath := filepath.Join(workDir, "f.txt")
	content := []byte("x")
	for i := 0; i < historyMaxEntries+5; i++ {
		time.Sleep(time.Microsecond)
		_ = e.recordSnapshot(absPath, "f.txt", "write_file", content)
	}

	histMu.Lock()
	hf, err := e.loadHistory()
	histMu.Unlock()
	if err != nil {
		t.Fatalf("loadHistory: %v", err)
	}
	if len(hf.Entries) > historyMaxEntries {
		t.Errorf("entries after eviction: got %d, want <= %d", len(hf.Entries), historyMaxEntries)
	}
}

func TestEvictHistory_ByTotalSize(t *testing.T) {
	e, workDir := historyEngine(t)

	absPath := filepath.Join(workDir, "big.txt")
	// Each blob is 4 MiB (just under per-file limit). 6 blobs = 24 MiB > 20 MiB cap.
	chunk := make([]byte, 4*1024*1024)
	for i := 0; i < 6; i++ {
		time.Sleep(time.Microsecond)
		_ = e.recordSnapshot(absPath, "big.txt", "write_file", chunk)
	}

	histMu.Lock()
	hf, err := e.loadHistory()
	histMu.Unlock()
	if err != nil {
		t.Fatalf("loadHistory: %v", err)
	}
	var total int64
	for _, ent := range hf.Entries {
		total += ent.BlobSize
	}
	if total > historyMaxTotalBytes {
		t.Errorf("total blob size after eviction: got %d, want <= %d", total, historyMaxTotalBytes)
	}
}

func TestRecordSnapshot_OversizeBlobSkipped(t *testing.T) {
	e, workDir := historyEngine(t)

	absPath := filepath.Join(workDir, "huge.txt")
	// 6 MiB > historyMaxBlobBytes (5 MiB) — should be silently skipped.
	huge := make([]byte, 6*1024*1024)
	if err := e.recordSnapshot(absPath, "huge.txt", "write_file", huge); err != nil {
		t.Fatalf("recordSnapshot must not error on oversized blob: %v", err)
	}

	histMu.Lock()
	hf, _ := e.loadHistory()
	histMu.Unlock()
	if len(hf.Entries) != 0 {
		t.Errorf("oversize blob should not be recorded, got %d entries", len(hf.Entries))
	}
}

func TestSaveLoadHistory_Roundtrip(t *testing.T) {
	e, workDir := historyEngine(t)

	hf := historyFile{
		Version: 1,
		Entries: []historyEntry{
			{
				ID:          "abc123",
				AbsPath:     filepath.Join(workDir, "x.go"),
				DisplayPath: "x.go",
				Op:          "edit_file",
				Timestamp:   time.Now().UTC().Truncate(time.Second),
			},
		},
	}
	if err := e.saveHistory(hf); err != nil {
		t.Fatalf("saveHistory: %v", err)
	}

	loaded, err := e.loadHistory()
	if err != nil {
		t.Fatalf("loadHistory: %v", err)
	}
	if len(loaded.Entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(loaded.Entries))
	}
	if loaded.Entries[0].ID != "abc123" {
		t.Errorf("id: got %q, want abc123", loaded.Entries[0].ID)
	}
	if loaded.Entries[0].Op != "edit_file" {
		t.Errorf("op: got %q, want edit_file", loaded.Entries[0].Op)
	}
}

func TestAtomicWriteBytes_CreatesAndVerifies(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "blob.dat")
	data := []byte("test blob content")

	if err := atomicWriteBytes(path, data, 0o600); err != nil {
		t.Fatalf("atomicWriteBytes: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("content: got %q, want %q", got, data)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm: got %o, want 600", info.Mode().Perm())
	}
}
