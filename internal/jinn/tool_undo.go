package jinn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// undoTool dispatches undo sub-actions: list, preview, restore, clear.
func (e *Engine) undoTool(args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	switch action {
	case "list":
		return e.undoList(args)
	case "preview":
		return e.undoPreview(args)
	case "restore":
		return e.undoRestore(args)
	case "clear":
		return e.undoClear()
	default:
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("unknown action: %q", action),
			Suggestion: `use action="list", "preview", "restore", or "clear"`,
		}
	}
}

// undoList returns the snapshot history as JSON, newest-first, up to limit entries.
func (e *Engine) undoList(args map[string]interface{}) (string, error) {
	limit := historyMaxEntries
	if v, ok := args["limit"].(float64); ok && int(v) > 0 {
		limit = int(v)
	}

	histMu.Lock()
	hf, err := e.loadHistory()
	histMu.Unlock()
	if err != nil {
		return "", err
	}

	// Reverse to newest-first for display.
	entries := hf.Entries
	n := len(entries)
	reversed := make([]map[string]interface{}, 0, n)
	for i := n - 1; i >= 0 && len(reversed) < limit; i-- {
		ent := entries[i]
		reversed = append(reversed, map[string]interface{}{
			"id":           ent.ID,
			"display_path": ent.DisplayPath,
			"op":           ent.Op,
			"timestamp":    ent.Timestamp.Format(time.RFC3339),
			"blob_size":    ent.BlobSize,
			"created":      ent.Created,
		})
	}

	result := map[string]interface{}{
		"entries": reversed,
		"count":   len(reversed),
		"total":   n,
	}
	if result["entries"] == nil {
		result["entries"] = []interface{}{}
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("undo list: marshal: %w", err)
	}
	return string(data), nil
}

// undoPreview shows a unified diff of what restoring an entry would do.
func (e *Engine) undoPreview(args map[string]interface{}) (string, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("id is required for preview"),
			Suggestion: `use action="list" to see available snapshot IDs`,
		}
	}

	ent, err := e.findEntry(id)
	if err != nil {
		return "", err
	}

	if ent.Created {
		return fmt.Sprintf("[undo preview] restoring would delete %s (it was created by op %q)", ent.DisplayPath, ent.Op), nil
	}

	preContent, err := e.readBlob(ent)
	if err != nil {
		return "", err
	}

	// Read current file for diff.
	current, readErr := os.ReadFile(ent.AbsPath)
	if readErr != nil {
		current = []byte{}
	}

	diff := unifiedDiff(string(current), string(preContent), ent.DisplayPath, 3)
	return fmt.Sprintf("[undo preview] id=%s op=%s ts=%s\n%s",
		ent.ID, ent.Op, ent.Timestamp.Format(time.RFC3339), diff), nil
}

// undoRestore restores the file to the state captured in the snapshot.
func (e *Engine) undoRestore(args map[string]interface{}) (string, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("id is required for restore"),
			Suggestion: `use action="list" to see available snapshot IDs`,
		}
	}

	ent, err := e.findEntry(id)
	if err != nil {
		return "", err
	}

	// Security: checkPath on the entry's abs_path.
	resolved, err := e.checkPath(ent.DisplayPath)
	if err != nil {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("restore: path security check failed: %w", err),
			Suggestion: `use action="clear" to reset history if this entry is corrupt`,
		}
	}
	// Double-check resolved path matches stored abs_path exactly.
	if resolved != ent.AbsPath {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("restore: path mismatch (index has %q, resolved to %q)", ent.AbsPath, resolved),
			Suggestion: `use action="clear" to reset history if this entry is corrupt`,
		}
	}

	// TOCTOU stale check before overwriting.
	if err := e.tracker.checkStale(resolved); err != nil {
		return "", err
	}

	if ent.Created {
		// File was created by the op — undo means delete it.
		if err := os.Remove(resolved); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("restore: remove %s: %w", ent.DisplayPath, err)
		}
		return fmt.Sprintf("restored: deleted %s (undid %q)", ent.DisplayPath, ent.Op), nil
	}

	preContent, err := e.readBlob(ent)
	if err != nil {
		return "", err
	}

	// Restore via atomic write (preserves existing permissions, fsyncs).
	if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
		return "", fmt.Errorf("restore: mkdir: %w", err)
	}
	if err := e.atomicWriteFile(resolved, string(preContent)); err != nil {
		return "", fmt.Errorf("restore: write: %w", err)
	}
	return fmt.Sprintf("restored: %s to snapshot from %s (op %q, id=%s)",
		ent.DisplayPath, ent.Timestamp.Format(time.RFC3339), ent.Op, ent.ID), nil
}

// undoClear deletes all history for this workdir.
func (e *Engine) undoClear() (string, error) {
	histMu.Lock()
	defer histMu.Unlock()

	dir := e.historyDir()
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("undo clear: %w", err)
	}
	return "cleared history for this workdir", nil
}

// findEntry looks up a snapshot entry by ID prefix. Returns ErrWithSuggestion when not found.
func (e *Engine) findEntry(id string) (historyEntry, error) {
	histMu.Lock()
	hf, err := e.loadHistory()
	histMu.Unlock()
	if err != nil {
		return historyEntry{}, err
	}

	for _, ent := range hf.Entries {
		if strings.HasPrefix(ent.ID, id) {
			return ent, nil
		}
	}
	return historyEntry{}, &ErrWithSuggestion{
		Err:        fmt.Errorf("snapshot not found: %q", id),
		Suggestion: `use action="list" to see available snapshot IDs`,
	}
}

// readBlob reads, decompresses, and SHA-256-verifies the blob for an entry.
// Blobs are stored with adaptive compression (see blob_codec.go); decode is
// transparent to callers.
func (e *Engine) readBlob(ent historyEntry) ([]byte, error) {
	encoded, err := os.ReadFile(ent.BlobPath)
	if err != nil {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("blob read failed for id=%s: %w", ent.ID, err),
			Suggestion: `use action="clear" to reset history`,
		}
	}
	data, err := decodeBlob(encoded)
	if err != nil {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("blob decode failed for id=%s: %w", ent.ID, err),
			Suggestion: `use action="clear" to reset history`,
		}
	}
	h := sha256.Sum256(data)
	got := hex.EncodeToString(h[:])
	if got != ent.BlobHash {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("blob integrity check failed for id=%s (got %s, want %s)", ent.ID, got, ent.BlobHash),
			Suggestion: `use action="clear" to reset history`,
		}
	}
	return data, nil
}
