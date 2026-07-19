package jinn

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- list ----

func TestUndoList_Empty(t *testing.T) {
	e, _ := undoEngine(t)

	result, err := e.undoTool(map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("json: %v", err)
	}
	if out["count"] != float64(0) {
		t.Errorf("count: got %v, want 0", out["count"])
	}
	if out["total"] != float64(0) {
		t.Errorf("total: got %v, want 0", out["total"])
	}
}

func TestUndoList_AfterWriteFile(t *testing.T) {
	e, workDir := undoEngine(t)
	undoMkFile(t, e, workDir, "a.txt", "original")

	if _, err := e.writeFile(map[string]interface{}{"path": "a.txt", "content": "updated"}); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	result, err := e.undoTool(map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("json: %v", err)
	}
	if out["count"] != float64(1) {
		t.Errorf("count: got %v, want 1", out["count"])
	}
	entries := out["entries"].([]interface{})
	entry := entries[0].(map[string]interface{})
	if entry["op"] != "write_file" {
		t.Errorf("op: got %v", entry["op"])
	}
	if entry["display_path"] != "a.txt" {
		t.Errorf("display_path: got %v", entry["display_path"])
	}
	if entry["id"] == "" {
		t.Error("id is empty")
	}
}

func TestUndoList_LimitParam(t *testing.T) {
	e, workDir := undoEngine(t)
	undoMkFile(t, e, workDir, "b.txt", "v0")

	for i := 1; i <= 5; i++ {
		if _, err := e.writeFile(map[string]interface{}{"path": "b.txt", "content": fmt.Sprintf("v%d", i)}); err != nil {
			t.Fatalf("writeFile %d: %v", i, err)
		}
	}

	result, err := e.undoTool(map[string]interface{}{"action": "list", "limit": float64(2)})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("json: %v", err)
	}
	if out["count"] != float64(2) {
		t.Errorf("count: got %v, want 2", out["count"])
	}
	if out["total"] != float64(5) {
		t.Errorf("total: got %v, want 5", out["total"])
	}
}

// ---- preview ----

func TestUndoPreview_ShowsDiff(t *testing.T) {
	e, workDir := undoEngine(t)
	undoMkFile(t, e, workDir, "c.txt", "line one\nline two\n")

	if _, err := e.writeFile(map[string]interface{}{"path": "c.txt", "content": "line one\nline three\n"}); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	id := firstSnapshotID(t, e)
	result, err := e.undoTool(map[string]interface{}{"action": "preview", "id": id})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !strings.Contains(result, "line two") {
		t.Errorf("expected 'line two' in preview, got: %s", result)
	}
	if !strings.Contains(result, "undo preview") {
		t.Errorf("expected 'undo preview' in result, got: %s", result)
	}
}

func TestUndoPreview_MissingID(t *testing.T) {
	e, _ := undoEngine(t)

	_, err := e.undoTool(map[string]interface{}{"action": "preview"})
	if err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Errorf("expected 'id is required' error, got: %v", err)
	}
}

func TestUndoPreview_NotFound(t *testing.T) {
	e, _ := undoEngine(t)

	_, err := e.undoTool(map[string]interface{}{"action": "preview", "id": "doesnotexist"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestFindEntry_AmbiguousPrefixRequiresExactID(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	t.Cleanup(func() { _ = os.RemoveAll(e.historyDir()) })
	want := historyEntry{ID: "abc1230000000000", DisplayPath: "first.txt"}
	hf := historyFile{
		Version: 1,
		Entries: []historyEntry{
			want,
			{ID: "abc123fffffffff", DisplayPath: "second.txt"},
		},
	}
	if err := e.saveHistory(hf); err != nil {
		t.Fatalf("saveHistory: %v", err)
	}

	_, err := e.findEntry("abc123")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous prefix error = %v, want ambiguity error", err)
	}

	got, err := e.findEntry(want.ID)
	if err != nil {
		t.Fatalf("exact ID lookup: %v", err)
	}
	if got.ID != want.ID {
		t.Errorf("exact ID lookup = %q, want %q", got.ID, want.ID)
	}
}

func TestUndoPreview_CreatedFile(t *testing.T) {
	e, workDir := undoEngine(t)

	newPath := filepath.Join(workDir, "newfile.txt")
	e.recordSnapshot(newPath, "newfile.txt", "write_file", nil)

	id := firstSnapshotID(t, e)
	result, err := e.undoTool(map[string]interface{}{"action": "preview", "id": id})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !strings.Contains(result, "delete") {
		t.Errorf("expected 'delete' in preview for created file, got: %s", result)
	}
}

// ---- restore ----

func TestUndoRestore_RevertContent(t *testing.T) {
	e, workDir := undoEngine(t)
	undoMkFile(t, e, workDir, "d.txt", "original content\n")

	if _, err := e.writeFile(map[string]interface{}{"path": "d.txt", "content": "modified content\n"}); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	id := firstSnapshotID(t, e)
	result, err := e.undoTool(map[string]interface{}{"action": "restore", "id": id})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !strings.Contains(result, "restored") {
		t.Errorf("expected 'restored' in result, got: %s", result)
	}
	got, err := os.ReadFile(filepath.Join(workDir, "d.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "original content\n" {
		t.Errorf("content: got %q, want %q", got, "original content\n")
	}
}

func TestUndoRestore_CreatedFileDeleted(t *testing.T) {
	e, workDir := undoEngine(t)

	absPath := filepath.Join(workDir, "created.txt")
	if err := os.WriteFile(absPath, []byte("brand new"), 0644); err != nil {
		t.Fatal(err)
	}
	e.recordSnapshot(absPath, "created.txt", "write_file", nil)

	id := firstSnapshotID(t, e)
	result, err := e.undoTool(map[string]interface{}{"action": "restore", "id": id})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !strings.Contains(result, "deleted") {
		t.Errorf("expected 'deleted' in result, got: %s", result)
	}
	if _, statErr := os.Stat(absPath); !os.IsNotExist(statErr) {
		t.Errorf("file should have been deleted")
	}
}

func TestUndoRestore_MissingID(t *testing.T) {
	e, _ := undoEngine(t)

	_, err := e.undoTool(map[string]interface{}{"action": "restore"})
	if err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Errorf("expected 'id is required' error, got: %v", err)
	}
}

func TestUndoRestore_BlobIntegrityFails(t *testing.T) {
	e, workDir := undoEngine(t)
	undoMkFile(t, e, workDir, "e.txt", "data\n")

	if _, err := e.writeFile(map[string]interface{}{"path": "e.txt", "content": "new\n"}); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	hf, _ := e.loadHistoryLocked()
	if len(hf.Entries) == 0 {
		t.Fatal("expected entry in history")
	}
	if err := os.WriteFile(hf.Entries[0].BlobPath, append([]byte{blobTagRaw}, []byte("corrupted")...), 0600); err != nil {
		t.Fatalf("corrupt blob: %v", err)
	}

	id := firstSnapshotID(t, e)
	_, err := e.undoTool(map[string]interface{}{"action": "restore", "id": id})
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Errorf("expected integrity error, got: %v", err)
	}
}

func TestUndoRestore_RejectsChecksumMismatch(t *testing.T) {
	t.Parallel()
	e, workDir := testEngine(t)
	t.Cleanup(func() { _ = os.RemoveAll(e.historyDir()) })
	undoMkFile(t, e, workDir, "checksum.txt", "original\n")

	if _, err := e.writeFile(map[string]interface{}{"path": "checksum.txt", "content": "modified\n"}); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	id := firstSnapshotID(t, e)

	_, err := e.undoTool(map[string]interface{}{
		"action":      "restore",
		"id":          id,
		"if_checksum": strings.Repeat("0", 64),
	})
	if err == nil {
		t.Fatal("expected checksum mismatch, got nil")
	}
	var structured *ErrWithSuggestion
	if !errors.As(err, &structured) {
		t.Fatalf("error type = %T, want *ErrWithSuggestion", err)
	}
	if structured.Code != ErrCodeStaleFile {
		t.Errorf("error code = %q, want %q", structured.Code, ErrCodeStaleFile)
	}
	got, readErr := os.ReadFile(filepath.Join(workDir, "checksum.txt"))
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(got) != "modified\n" {
		t.Errorf("content after rejected restore = %q, want %q", got, "modified\n")
	}
}

func TestUndoRestore_RejectsMissingTargetBeforeCreatingParent(t *testing.T) {
	t.Parallel()
	e, workDir := testEngine(t)
	t.Cleanup(func() { _ = os.RemoveAll(e.historyDir()) })
	parent := filepath.Join(workDir, "removed", "nested")
	target := filepath.Join(parent, "file.txt")
	if err := os.MkdirAll(parent, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(target, []byte("original\n"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	e.recordSnapshot(target, filepath.Join("removed", "nested", "file.txt"), "write_file", []byte("original\n"))
	id := firstSnapshotID(t, e)
	if err := os.RemoveAll(filepath.Join(workDir, "removed")); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	_, err := e.undoTool(map[string]interface{}{
		"action":      "restore",
		"id":          id,
		"if_checksum": sha256Hex([]byte("modified\n")),
	})
	requireStaleFileError(t, err)
	if _, statErr := os.Stat(parent); !os.IsNotExist(statErr) {
		t.Fatalf("stale restore created parent directory: stat error = %v", statErr)
	}
}

func TestUndoRestore_AcceptsMatchingChecksum(t *testing.T) {
	t.Parallel()
	e, workDir := testEngine(t)
	t.Cleanup(func() { _ = os.RemoveAll(e.historyDir()) })
	undoMkFile(t, e, workDir, "checksum-match.txt", "original\n")

	if _, err := e.writeFile(map[string]interface{}{"path": "checksum-match.txt", "content": "modified\n"}); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	id := firstSnapshotID(t, e)
	_, err := e.undoTool(map[string]interface{}{
		"action":      "restore",
		"id":          id,
		"if_checksum": sha256Hex([]byte("modified\n")),
	})
	if err != nil {
		t.Fatalf("matching checksum restore: %v", err)
	}
	got, readErr := os.ReadFile(filepath.Join(workDir, "checksum-match.txt"))
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(got) != "original\n" {
		t.Errorf("restored content = %q, want original", got)
	}
}

// ---- clear ----

func TestUndoClear_EmptiesHistory(t *testing.T) {
	e, workDir := undoEngine(t)
	undoMkFile(t, e, workDir, "f.txt", "hello")

	if _, err := e.writeFile(map[string]interface{}{"path": "f.txt", "content": "world"}); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if _, err := e.undoTool(map[string]interface{}{"action": "clear"}); err != nil {
		t.Fatalf("clear: %v", err)
	}

	result, err := e.undoTool(map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("json: %v", err)
	}
	if out["count"] != float64(0) {
		t.Errorf("count after clear: got %v, want 0", out["count"])
	}
}

// ---- unknown action ----

func TestUndoTool_UnknownAction(t *testing.T) {
	e, _ := undoEngine(t)

	_, err := e.undoTool(map[string]interface{}{"action": "explode"})
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("expected 'unknown action' error, got: %v", err)
	}
}
