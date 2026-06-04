package jinn

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// atomicWriteJSON marshals v as indented JSON and atomically writes it to path
// (temp+chmod+fsync+rename) with mode 0o600. Returns a descriptive
// wrapped error on any failure.
func atomicWriteJSON(path string, v any) error {
	data, merr := json.MarshalIndent(v, "", "  ")
	if merr != nil {
		return fmt.Errorf("marshal: %w", merr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := atomicWriteBytes(path, data, 0o600); err != nil {
		return fmt.Errorf("atomic write: %w", err)
	}
	return nil
}

// atomicWriteFile writes content to resolved via temp+rename and records the new mtime.
// It preserves existing file permissions and fsyncs before rename for crash safety.
// Returns a non-nil error on failure; caller is responsible for user-facing formatting.
func (e *Engine) atomicWriteFile(resolved, content string) error {
	// Capture existing file permissions before overwriting.
	perm := os.FileMode(0644)
	if info, err := os.Stat(resolved); err == nil {
		perm = info.Mode().Perm()
	}

	if err := atomicWriteBytes(resolved, []byte(content), perm); err != nil {
		return err
	}

	// Record the post-write mtime so the staleness tracker stays consistent.
	if info, err := os.Stat(resolved); err == nil {
		e.tracker.record(resolved, info.ModTime())
	}
	return nil
}

// snapshotAndWrite records an undo snapshot then atomically writes content.
// Combining them keeps the invariant structural: no mutating write skips history.
func (e *Engine) snapshotAndWrite(resolved, displayPath, op string, preContent []byte, content string) error {
	e.recordSnapshot(resolved, displayPath, op, preContent)
	return e.atomicWriteFile(resolved, content)
}

func (e *Engine) writeFile(args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)

	resolved, err := e.checkPath(path)
	if err != nil {
		return "", err
	}
	if staleErr := e.tracker.checkStale(resolved); staleErr != nil {
		return "", staleErr
	}

	if boolArg(args, "dry_run") {
		existing, rErr := os.ReadFile(resolved)
		if rErr != nil {
			return fmt.Sprintf("[dry-run] would create %s (%d bytes)", path, len(content)), nil //nolint:nilerr // unreadable/missing file in dry-run means the write would create it
		}
		return unifiedDiff(string(existing), content, path), nil
	}

	// Capture pre-state for undo history (nil = file did not exist).
	preContent, err := os.ReadFile(resolved)
	if os.IsNotExist(err) {
		preContent = nil
	} else if err != nil {
		preContent = nil // unreadable — skip snapshot, don't block write
	}
	e.recordSnapshot(resolved, path, "write_file", preContent)

	if err := os.MkdirAll(filepath.Dir(resolved), 0o750); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	if err := e.atomicWriteFile(resolved, content); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(content), path), nil
}
