package jinn

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// restoreFixture creates target.txt with "original", performs the mutating
// write to "v2" (recording the snapshot), and returns the snapshot id plus
// the recorded before-hash. Must NOT use t.Parallel() (t.Setenv).
func restoreFixture(t *testing.T, name string) (*Engine, string, string, string) {
	t.Helper()
	e, workDir := undoEngine(t)
	p := undoMkFile(t, e, workDir, name, "original")
	if _, err := e.writeFile(args("path", name, "content", "v2")); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	id := firstSnapshotID(t, e)
	return e, workDir, p, id
}

func setLive(t *testing.T, e *Engine, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	e.tracker.record(p, info.ModTime(), info.Size())
}

func TestUndoRestore_IfVersionAccept(t *testing.T) {
	e, _, p, id := restoreFixture(t, "v-accept.txt")
	// No observed change since the snapshot: live still matches the recorded
	// pre-state identity, and the guard accepts.
	setLive(t, e, p, "original")
	version := sha256HexBytes([]byte("original"))
	res, err := e.undoTool(args("action", "restore", "id", id, "if_version", version))
	if err != nil {
		t.Fatalf("restore with matching if_version: %v", err)
	}
	if !strings.Contains(res, "restored:") {
		t.Errorf("unexpected result: %s", res)
	}
}

func TestUndoRestore_IfVersionWrongSnapshot(t *testing.T) {
	e, _, p, id := restoreFixture(t, "v-wrong.txt")
	setLive(t, e, p, "original")
	_, err := e.undoTool(args("action", "restore", "id", id, "if_version", strings.Repeat("0", 64)))
	if err == nil {
		t.Fatal("expected error for a version the snapshot never had")
	}
	var werr *ErrWithSuggestion
	errors.As(err, &werr)
	if werr == nil || werr.Code != ErrCodeInvalidArgs {
		t.Errorf("expected invalid_args, got: %v", err)
	}
}

func TestUndoRestore_IfVersionStale(t *testing.T) {
	e, _, p, id := restoreFixture(t, "v-stale.txt")
	// An unobserved change since the snapshot: the live digest no longer
	// matches the recorded identity.
	setLive(t, e, p, "v3-unobserved")
	version := sha256HexBytes([]byte("original"))
	_, err := e.undoTool(args("action", "restore", "id", id, "if_version", version))
	if err == nil {
		t.Fatal("expected stale rejection")
	}
	if !strings.Contains(err.Error(), "changed since the snapshot was recorded") {
		t.Errorf("expected recorded-identity stale message, got: %v", err)
	}
}

func TestUndoRestore_IfVersionMutualExclusion(t *testing.T) {
	e, _, _, id := restoreFixture(t, "v-mutex.txt")
	_, err := e.undoTool(args("action", "restore", "id", id, "if_version", "x", "if_checksum", "y"))
	if err == nil {
		t.Fatal("expected mutual-exclusion error")
	}
	var werr *ErrWithSuggestion
	errors.As(err, &werr)
	if werr == nil || werr.Code != ErrCodeInvalidArgs {
		t.Errorf("expected invalid_args, got: %v", err)
	}
}

func TestUndoRestore_IfVersionRequiredWhenGuarded(t *testing.T) {
	e, _, _, id := restoreFixture(t, "v-required.txt")
	e.requireMutationPreconditions = true
	_, err := e.undoTool(args("action", "restore", "id", id))
	if err == nil {
		t.Fatal("expected precondition error without a guard")
	}
	if !strings.Contains(err.Error(), "if_checksum or if_version is required") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestUndoRestore_IfVersionSatisfiesGuardedPrecondition(t *testing.T) {
	e, _, p, id := restoreFixture(t, "v-satisfies.txt")
	e.requireMutationPreconditions = true
	setLive(t, e, p, "original")
	version := sha256HexBytes([]byte("original"))
	if _, err := e.undoTool(args("action", "restore", "id", id, "if_version", version)); err != nil {
		t.Fatalf("if_version must satisfy the guarded precondition: %v", err)
	}
}

// TestUndo_JournalTargetExcluded pins journal-target exclusion: when the
// history directory nests inside the workdir (e.g. config under the root),
// mutation entries must not reach it.
func TestUndo_JournalTargetExcluded(t *testing.T) {
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err == nil {
		dir = real
	}
	t.Setenv("JINN_CONFIG_DIR", filepath.Join(dir, "cfg"))
	e, err := NewWithConfig(dir, EngineConfig{Version: "dev", ShellMode: ShellModeUnsafe, UnsafeAllowMutationWithoutPreconditions: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })

	rel, err := filepath.Rel(dir, e.historyDir())
	if err != nil || rel == "" || strings.HasPrefix(rel, "..") {
		t.Fatalf("setup: historyDir %q is not nested under the workdir", e.historyDir())
	}
	target := filepath.Join(rel, "index.json")
	_, err = e.writeFile(args("path", target, "content", "evil"))
	if err == nil {
		t.Fatal("expected journal-target rejection")
	}
	if !strings.Contains(err.Error(), "undo history storage") {
		t.Errorf("wrong error: %v", err)
	}
}
