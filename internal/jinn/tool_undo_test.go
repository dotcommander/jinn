package jinn

import (
	"encoding/json"
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

func TestUndoPreview_CreatedFile(t *testing.T) {
	e, workDir := undoEngine(t)

	newPath := filepath.Join(workDir, "newfile.txt")
	if err := e.recordSnapshot(newPath, "newfile.txt", "write_file", nil); err != nil {
		t.Fatalf("recordSnapshot: %v", err)
	}

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
	if err := e.recordSnapshot(absPath, "created.txt", "write_file", nil); err != nil {
		t.Fatalf("recordSnapshot: %v", err)
	}

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

	histMu.Lock()
	hf, _ := e.loadHistory()
	histMu.Unlock()
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
