package jinn

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// undoEngine returns an Engine with isolated JINN_CONFIG_DIR.
// Must NOT use t.Parallel() — t.Setenv is serial only.
func undoEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	return testEngine(t)
}

// firstSnapshotID returns the ID of the first (oldest) entry in history.
func firstSnapshotID(t *testing.T, e *Engine) string {
	t.Helper()
	histMu.Lock()
	hf, err := e.loadHistory()
	histMu.Unlock()
	if err != nil {
		t.Fatalf("loadHistory: %v", err)
	}
	if len(hf.Entries) == 0 {
		t.Fatal("history is empty")
	}
	return hf.Entries[0].ID
}

// undoFileModTime returns the mtime of a file, failing the test if stat fails.
func undoFileModTime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.ModTime()
}

// undoMkFile creates a file under workDir/name with content and records its mtime on e.tracker.
func undoMkFile(t *testing.T, e *Engine, workDir, name, content string) string {
	t.Helper()
	p := filepath.Join(workDir, name)
	writeTestFile(t, workDir, name, content)
	e.tracker.record(p, undoFileModTime(t, p))
	return p
}
