package jinn

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicWriteFile writes content to resolved via temp+rename and records the new mtime.
// Returns a non-nil error on failure; caller is responsible for user-facing formatting.
func (e *Engine) atomicWriteFile(resolved, content string) error {
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
	if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
		return "", fmt.Errorf("mkdir: %s", err)
	}
	if err := e.atomicWriteFile(resolved, content); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(content), path), nil
}
