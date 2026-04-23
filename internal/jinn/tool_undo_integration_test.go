package jinn

import (
	"os"
	"testing"
)

// Integration tests: verify that write_file, edit_file, and multi_edit
// correctly snapshot pre-mutation state via recordSnapshot.

func TestWriteFile_SnapshotsBeforeWrite(t *testing.T) {
	e, workDir := undoEngine(t)
	undoMkFile(t, e, workDir, "g.txt", "pre-state\n")

	if _, err := e.writeFile(map[string]interface{}{"path": "g.txt", "content": "post-state\n"}); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	histMu.Lock()
	hf, _ := e.loadHistory()
	histMu.Unlock()
	if len(hf.Entries) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(hf.Entries))
	}
	blob, err := e.readBlob(hf.Entries[0])
	if err != nil {
		t.Fatalf("readBlob: %v", err)
	}
	if string(blob) != "pre-state\n" {
		t.Errorf("blob: got %q, want %q", blob, "pre-state\n")
	}
}

func TestEditFile_SnapshotsBeforeEdit(t *testing.T) {
	e, workDir := undoEngine(t)
	undoMkFile(t, e, workDir, "h.txt", "alpha\nbeta\ngamma\n")

	if _, err := e.editFile(map[string]interface{}{"path": "h.txt", "old_text": "beta", "new_text": "BETA"}); err != nil {
		t.Fatalf("editFile: %v", err)
	}

	histMu.Lock()
	hf, _ := e.loadHistory()
	histMu.Unlock()
	if len(hf.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(hf.Entries))
	}
	if hf.Entries[0].Op != "edit_file" {
		t.Errorf("op: got %q, want edit_file", hf.Entries[0].Op)
	}
}

func TestMultiEdit_SnapshotsEachFile(t *testing.T) {
	e, workDir := undoEngine(t)
	undoMkFile(t, e, workDir, "i1.txt", "foo bar\n")
	undoMkFile(t, e, workDir, "i2.txt", "baz qux\n")

	_, err := e.multiEdit(map[string]interface{}{
		"edits": []interface{}{
			map[string]interface{}{"path": "i1.txt", "old_text": "foo", "new_text": "FOO"},
			map[string]interface{}{"path": "i2.txt", "old_text": "baz", "new_text": "BAZ"},
		},
	})
	if err != nil {
		t.Fatalf("multiEdit: %v", err)
	}

	histMu.Lock()
	hf, _ := e.loadHistory()
	histMu.Unlock()
	if len(hf.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(hf.Entries))
	}
	for _, ent := range hf.Entries {
		if ent.Op != "multi_edit" {
			t.Errorf("op: got %q, want multi_edit", ent.Op)
		}
	}
}

func TestWriteFile_DryRunNoSnapshot(t *testing.T) {
	e, workDir := undoEngine(t)
	writeTestFile(t, workDir, "dry.txt", "original\n")

	if _, err := e.writeFile(map[string]interface{}{"path": "dry.txt", "content": "new\n", "dry_run": true}); err != nil {
		t.Fatalf("writeFile dry: %v", err)
	}

	histMu.Lock()
	hf, _ := e.loadHistory()
	histMu.Unlock()
	if len(hf.Entries) != 0 {
		t.Errorf("dry-run must not create snapshots, got %d entries", len(hf.Entries))
	}
}

// keep os import used
var _ = os.ReadFile
