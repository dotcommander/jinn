package jinn

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// atomicWriteJSON marshals v as indented JSON and atomically writes it to path
// (temp+chmod+fsync+rename). perm is the target file mode. Returns a descriptive
// wrapped error on any failure.
func atomicWriteJSON(path string, v any, perm os.FileMode) error {
	data, merr := json.MarshalIndent(v, "", "  ")
	if merr != nil {
		return fmt.Errorf("marshal: %w", merr)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".json-")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err = tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	ok = true
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

	dir := filepath.Dir(resolved)
	tmp, err := os.CreateTemp(dir, ".jinn-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("chmod: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmpPath, resolved); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	if info, err := os.Stat(resolved); err == nil {
		e.tracker.record(resolved, info.ModTime())
	}
	return nil
}

func (e *Engine) writeFile(args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)

	resolved, err := e.checkPath(path)
	if err != nil {
		return "", err
	}
	if err := e.tracker.checkStale(resolved); err != nil {
		return "", err
	}

	if dryRun, ok := args["dry_run"].(bool); ok && dryRun {
		existing, err := os.ReadFile(resolved)
		if err != nil {
			return fmt.Sprintf("[dry-run] would create %s (%d bytes)", path, len(content)), nil
		}
		return unifiedDiff(string(existing), content, path, 3), nil
	}

	// Capture pre-state for undo history (nil = file did not exist).
	preContent, err := os.ReadFile(resolved)
	if os.IsNotExist(err) {
		preContent = nil
	} else if err != nil {
		preContent = nil // unreadable — skip snapshot, don't block write
	}
	_ = e.recordSnapshot(resolved, path, "write_file", preContent)

	if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	if err := e.atomicWriteFile(resolved, content); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(content), path), nil
}
