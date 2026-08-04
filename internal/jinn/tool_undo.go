package jinn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
			Code:       ErrCodeInvalidArgs,
		}
	}
}

// undoList returns the snapshot history as JSON, newest-first, up to limit entries.
func (e *Engine) undoList(args map[string]interface{}) (string, error) {
	limit := historyMaxEntries
	if v, ok := args["limit"].(float64); ok && int(v) > 0 {
		limit = int(v)
	}

	hf, err := e.loadHistoryLocked()
	if err != nil {
		return "", err
	}

	// Reverse to newest-first for display.
	entries := hf.Entries
	n := len(entries)
	committed := 0
	for _, ent := range entries {
		if ent.State == historyStateCommitted {
			committed++
		}
	}
	reversed := make([]map[string]interface{}, 0, n)
	for i := n - 1; i >= 0 && len(reversed) < limit; i-- {
		ent := entries[i]
		if ent.State != historyStateCommitted {
			continue
		}
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
		"total":   committed,
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
			Err:        errors.New("id is required for preview"),
			Suggestion: `use action="list" to see available snapshot IDs`,
			Code:       ErrCodeInvalidArgs,
		}
	}

	ent, err := e.findEntry(id)
	if err != nil {
		return "", err
	}
	if ent.State != historyStateCommitted {
		return "", fmt.Errorf("snapshot %s is %s and cannot be previewed", ent.ID, ent.State)
	}
	resolved, err := e.resolveUndoTarget(ent)
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
	current, _, readErr := e.readRegularFile(resolved, maxFileSize)
	if readErr != nil {
		current = []byte{}
	}

	diff := unifiedDiff(string(current), string(preContent), ent.DisplayPath)
	return fmt.Sprintf("[undo preview] id=%s op=%s ts=%s\n%s",
		ent.ID, ent.Op, ent.Timestamp.Format(time.RFC3339), diff), nil
}

// undoRestore restores the file to the state captured in the snapshot.
func (e *Engine) undoRestore(args map[string]interface{}) (string, error) {
	id, _ := args["id"].(string)
	if id == "" {
		return "", &ErrWithSuggestion{
			Err:        errors.New("id is required for restore"),
			Suggestion: `use action="list" to see available snapshot IDs`,
			Code:       ErrCodeInvalidArgs,
		}
	}

	ent, err := e.findEntry(id)
	if err != nil {
		return "", err
	}
	if ent.State != historyStateCommitted {
		return "", fmt.Errorf("snapshot %s is %s and cannot be restored", ent.ID, ent.State)
	}

	resolved, err := e.resolveUndoTarget(ent)
	if err != nil {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("restore: path security check failed: %w", err),
			Suggestion: `use action="clear" to reset history if this entry is corrupt`,
		}
	}
	var result string
	err = e.withTargetLocks([]string{resolved}, func() error {
		var restoreErr error
		result, restoreErr = e.undoRestoreLocked(args, ent, resolved)
		return restoreErr
	})
	return result, err
}

//nolint:gocognit,gocyclo,revive // restore preconditions, stale checks, and created-file semantics are a single atomic decision.
func (e *Engine) undoRestoreLocked(args map[string]interface{}, ent historyEntry, resolved string) (string, error) {
	if strArg(args, "if_checksum") != "" && boolArg(args, "if_absent") {
		return "", &ErrWithSuggestion{Err: errors.New("if_checksum and if_absent:true are mutually exclusive"), Suggestion: "use if_checksum for a replacement or if_absent:true for a recreation", Code: ErrCodeInvalidArgs}
	}
	_, _, readErr := e.readRegularFile(resolved, maxFileSize)
	exists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return "", fmt.Errorf("restore: read current target: %w", readErr)
	}
	if e.requireMutationPreconditions {
		if exists && strArg(args, "if_checksum") == "" {
			return "", &ErrWithSuggestion{Err: fmt.Errorf("if_checksum is required to restore over %s", ent.DisplayPath), Suggestion: "read the current target with include_checksum=true and retry", Code: ErrCodeInvalidArgs}
		}
		if !exists && !boolArg(args, "if_absent") {
			return "", &ErrWithSuggestion{Err: fmt.Errorf("if_absent:true is required to recreate %s", ent.DisplayPath), Suggestion: "set if_absent:true after confirming the target should be absent", Code: ErrCodeInvalidArgs}
		}
	}
	// TOCTOU stale check before overwriting.
	if staleErr := e.tracker.checkStale(resolved); staleErr != nil {
		return "", staleErr
	}

	if ent.Created {
		// File was created by the op — undo means delete it.
		if checksumErr := e.verifyUndoChecksum(args, resolved); checksumErr != nil {
			return "", checksumErr
		}
		if rmErr := e.removeFileDurable(resolved); rmErr != nil && !os.IsNotExist(rmErr) {
			_ = e.markSnapshotState(ent.ID, historyStateUncertain)
			return "", fmt.Errorf("restore: remove %s: %w", ent.DisplayPath, rmErr)
		}
		return fmt.Sprintf("restored: deleted %s (undid %q)", ent.DisplayPath, ent.Op), nil
	}

	preContent, err := e.readBlob(ent)
	if err != nil {
		return "", err
	}

	if checksumErr := e.verifyUndoChecksum(args, resolved); checksumErr != nil {
		return "", checksumErr
	}
	// Restore via atomic write (preserves existing permissions, fsyncs).
	if err := e.atomicWriteFile(resolved, string(preContent)); err != nil {
		return "", fmt.Errorf("restore: write: %w", err)
	}
	return fmt.Sprintf("restored: %s to snapshot from %s (op %q, id=%s)",
		ent.DisplayPath, ent.Timestamp.Format(time.RFC3339), ent.Op, ent.ID), nil
}

func (e *Engine) resolveUndoTarget(ent historyEntry) (string, error) {
	target := ent.Target
	if target == "" {
		target = ent.DisplayPath
	}
	resolved, err := e.checkPathForMutation(target)
	if err != nil {
		return "", &ErrWithSuggestion{Err: fmt.Errorf("restore: path security check failed: %w", err), Suggestion: `use action="clear" to reset history if this entry is corrupt`}
	}
	if ent.AbsPath != "" && resolved != ent.AbsPath {
		return "", &ErrWithSuggestion{Err: fmt.Errorf("restore: legacy path mismatch (index has %q, resolved to %q)", ent.AbsPath, resolved), Suggestion: `use action="clear" to reset history if this entry is corrupt`}
	}
	return resolved, nil
}

func (e *Engine) verifyUndoChecksum(args map[string]interface{}, path string) error {
	if strArg(args, "if_checksum") == "" {
		return nil
	}
	current, _, err := e.readRegularFile(path, maxFileSize)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("restore: read current target: %w", err)
	}
	return verifyIfChecksum(args, path, current, err == nil)
}

// undoClear deletes all history for this workdir.
func (e *Engine) undoClear() (string, error) {
	err := withFileLock(e.mutationLockPath(), func() error {
		return withFileLock(e.historyLockPath(), func() error {
			return os.RemoveAll(e.historyDir())
		})
	})
	if err != nil {
		return "", fmt.Errorf("undo clear: %w", err)
	}
	return "cleared history for this workdir", nil
}

// findEntry looks up a snapshot entry by exact ID or unambiguous ID prefix.
func (e *Engine) findEntry(id string) (historyEntry, error) {
	hf, err := e.loadHistoryLocked()
	if err != nil {
		return historyEntry{}, err
	}

	for _, ent := range hf.Entries {
		if ent.ID == id {
			return ent, nil
		}
	}

	var match historyEntry
	matches := 0
	for _, ent := range hf.Entries {
		if strings.HasPrefix(ent.ID, id) {
			match = ent
			matches++
		}
	}
	if matches == 1 {
		return match, nil
	}
	if matches > 1 {
		return historyEntry{}, &ErrWithSuggestion{
			Err:        fmt.Errorf("snapshot id prefix is ambiguous: %q matches %d entries", id, matches),
			Suggestion: `use a longer prefix or the full ID from action="list"`,
			Code:       ErrCodeInvalidArgs,
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
	blobPath := ent.BlobPath
	if ent.BlobID != "" {
		if filepath.Base(ent.BlobID) != ent.BlobID {
			return nil, &ErrWithSuggestion{Err: fmt.Errorf("invalid blob id for snapshot %s", ent.ID), Suggestion: `use action="clear" to reset history`}
		}
		blobPath = filepath.Join(e.blobsDir(), ent.BlobID)
	} else {
		clean := filepath.Clean(blobPath)
		if filepath.Dir(clean) != filepath.Clean(e.blobsDir()) || filepath.Base(clean) == "." {
			return nil, &ErrWithSuggestion{Err: fmt.Errorf("invalid legacy blob path for snapshot %s", ent.ID), Suggestion: `use action="clear" to reset history`}
		}
		blobPath = clean
	}
	blobsRoot := filepath.Clean(e.blobsDir()) + string(os.PathSeparator)
	if !strings.HasPrefix(blobPath, blobsRoot) {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("blob path outside history store for id=%s: %q", ent.ID, ent.BlobPath),
			Suggestion: `use action="clear" to reset history`,
		}
	}
	historyRoot, err := os.OpenRoot(e.historyDir())
	if err != nil {
		return nil, fmt.Errorf("open history root: %w", err)
	}
	defer func() { _ = historyRoot.Close() }()
	relBlob, err := filepath.Rel(e.historyDir(), blobPath)
	if err != nil {
		return nil, err
	}
	file, err := historyRoot.Open(relBlob)
	if err != nil {
		return nil, &ErrWithSuggestion{Err: fmt.Errorf("blob read failed for id=%s: %w", ent.ID, err), Suggestion: `use action="clear" to reset history`}
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > historyMaxBlobBytes+1<<20 {
		return nil, &ErrWithSuggestion{Err: fmt.Errorf("invalid blob file for id=%s", ent.ID), Suggestion: `use action="clear" to reset history`}
	}
	encoded, err := io.ReadAll(io.LimitReader(file, historyMaxBlobBytes+1<<20))
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
