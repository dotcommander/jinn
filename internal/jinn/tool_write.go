package jinn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		e.tracker.record(resolved, info.ModTime(), info.Size())
	}
	return nil
}

// snapshotAndWrite records an undo snapshot then atomically writes content.
// Combining them keeps the invariant structural: no mutating write skips
// history. Returns the undo id ("" when the snapshot was skipped).
func (e *Engine) snapshotAndWrite(resolved, displayPath, op string, preContent []byte, content string) (string, error) {
	id := e.recordSnapshot(resolved, displayPath, op, preContent)
	return id, e.atomicWriteFile(resolved, content)
}

// verifyIfChecksum enforces the optional if_checksum write precondition:
// when args["if_checksum"] is a non-empty hex digest, current must hash to
// it. exists=false means the target file is missing (always a mismatch).
// This is the cross-process staleness guard — the in-memory tracker only
// covers the persistent-Engine (--inspect) case, since production runs one
// process per tool call.
func verifyIfChecksum(args map[string]interface{}, path string, current []byte, exists bool) error {
	return verifyChecksum(strArg(args, "if_checksum"), path, current, exists)
}

// verifyChecksum enforces a caller-supplied checksum against the bytes read
// during a mutation preflight. Batch tools use this with per-target checksums.
func verifyChecksum(want, path string, current []byte, exists bool) error {
	if want == "" {
		return nil
	}
	if !exists {
		return &ErrWithSuggestion{
			Err:        fmt.Errorf("stale write rejected: %s no longer exists (if_checksum was set)", path),
			Suggestion: "re-read the file, then retry with the new checksum",
			Code:       ErrCodeStaleFile,
		}
	}
	h := sha256.Sum256(current)
	got := hex.EncodeToString(h[:])
	if !strings.EqualFold(got, want) {
		return &ErrWithSuggestion{
			Err:        fmt.Errorf("stale write rejected: %s changed since read (checksum %.12s… != expected %.12s…)", path, got, want),
			Suggestion: "re-read the file, then retry with the new checksum",
			Code:       ErrCodeStaleFile,
		}
	}
	return nil
}

// verifyPreflightState rejects a write when the target changed after phase-1
// validation. This closes the in-process preflight-to-write window even when
// the caller did not supply a cross-call checksum.
func verifyPreflightState(path string, expected []byte, expectedExists bool) error {
	current, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("re-read before write: %w", err)
	}
	if exists != expectedExists {
		change := "now exists"
		if !exists {
			change = "no longer exists"
		}
		return &ErrWithSuggestion{
			Err:        fmt.Errorf("stale write rejected: %s %s since preflight", path, change),
			Suggestion: "re-read the file, reconcile the external change, and retry",
			Code:       ErrCodeStaleFile,
		}
	}
	if !exists {
		return nil
	}
	return verifyChecksum(sha256HexBytes(expected), path, current, true)
}

func sha256HexBytes(content []byte) string {
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
}

func (e *Engine) writeFile(args map[string]interface{}) (string, error) {
	return e.writeFileWithPreCommitHook(args, nil)
}

// writeFileWithPreCommitHook exposes the preflight-to-commit boundary to
// deterministic race tests. Production callers always pass a nil hook through
// writeFile.
func (e *Engine) writeFileWithPreCommitHook(args map[string]interface{}, beforeCommit func()) (string, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)

	resolved, err := e.checkPath(path)
	if err != nil {
		return "", err
	}
	if staleErr := e.tracker.checkStale(resolved); staleErr != nil {
		return "", staleErr
	}

	// Read current state once: the if_checksum precondition, the dry-run
	// diff, and the undo snapshot all reuse these bytes. nil preContent =
	// file did not exist or was unreadable (skip snapshot, don't block write).
	preContent, readErr := os.ReadFile(resolved)
	exists := readErr == nil
	if readErr != nil {
		preContent = nil
	}
	if err := verifyIfChecksum(args, path, preContent, exists); err != nil {
		return "", err
	}

	if boolArg(args, "dry_run") {
		if !exists {
			return fmt.Sprintf("[dry-run] would create %s (%d bytes)", path, len(content)), nil
		}
		return unifiedDiff(string(preContent), content, path), nil
	}

	if beforeCommit != nil {
		beforeCommit()
	}
	if err := verifyPreflightState(resolved, preContent, exists); err != nil {
		return "", err
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
