package jinn

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
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
	if err := atomicWriteBytes(path, data); err != nil {
		return fmt.Errorf("atomic write: %w", err)
	}
	return nil
}

// atomicWriteFile writes content to resolved via temp+rename and records the new mtime.
// It preserves existing file permissions and fsyncs before rename for crash safety.
// Returns a non-nil error on failure; caller is responsible for user-facing formatting.
func (e *Engine) atomicWriteFile(resolved, content string) error {
	_, err := e.atomicWriteFileStaged(resolved, content, nil)
	return err
}

// atomicWriteFileStaged writes content via a durable temp file and calls
// beforeRename after the staged data is durable but before replacing the target.
// The returned bool reports whether the target was replaced.
//
//nolint:gocyclo // The linear durability stages deliberately retain their distinct failure boundaries.
func (e *Engine) atomicWriteFileStaged(resolved, content string, beforeRename func() error) (bool, error) {
	// Capture existing file permissions before overwriting.
	perm := os.FileMode(0644)
	rel, relErr := e.rootRelative(resolved)
	if relErr != nil {
		return false, relErr
	}
	if info, statErr := e.root.Stat(rel); statErr == nil {
		perm = info.Mode().Perm()
	}
	parent := filepath.Dir(rel)
	if mkdirErr := e.root.MkdirAll(parent, 0o750); mkdirErr != nil {
		return false, mkdirErr
	}
	var nonce [8]byte
	if _, randErr := rand.Read(nonce[:]); randErr != nil {
		return false, randErr
	}
	tempName := filepath.Join(parent, ".jinn-"+hex.EncodeToString(nonce[:]))
	temp, err := e.root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return false, err
	}
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = e.root.Remove(tempName)
		}
	}()
	if _, writeErr := temp.Write([]byte(content)); writeErr != nil {
		return false, writeErr
	}
	if syncErr := temp.Sync(); syncErr != nil {
		return false, syncErr
	}
	if closeErr := temp.Close(); closeErr != nil {
		return false, closeErr
	}
	if beforeRename != nil {
		if callbackErr := beforeRename(); callbackErr != nil {
			return false, callbackErr
		}
	}
	if renameErr := e.root.Rename(tempName, rel); renameErr != nil {
		return false, renameErr
	}
	committed = true
	dir, err := e.root.Open(parent)
	if err != nil {
		return true, fmt.Errorf("open parent directory for durability: %w", err)
	}
	if syncErr := dir.Sync(); syncErr != nil {
		_ = dir.Close()
		return true, fmt.Errorf("sync parent directory for durability: %w", syncErr)
	}
	if closeErr := dir.Close(); closeErr != nil {
		return true, fmt.Errorf("close parent directory after durability sync: %w", closeErr)
	}

	// Record the post-write mtime so the staleness tracker stays consistent.
	if info, err := e.root.Stat(rel); err == nil {
		e.tracker.record(resolved, info.ModTime(), info.Size())
	}
	return true, nil
}

// removeFileDurable unlinks one rooted file then fsyncs its exact parent so a
// successful delete has the same crash-durability contract as atomic writes.
func (e *Engine) removeFileDurable(resolved string) error {
	rel, err := e.rootRelative(resolved)
	if err != nil {
		return err
	}
	parent := filepath.Dir(rel)
	dir, err := e.root.Open(parent)
	if err != nil {
		return fmt.Errorf("open parent directory for delete durability: %w", err)
	}
	defer func() { _ = dir.Close() }()
	if e.removeDurabilityHook != nil {
		if hookErr := e.removeDurabilityHook("open"); hookErr != nil {
			return hookErr
		}
	}
	if unlinkErr := unix.Unlinkat(int(dir.Fd()), filepath.Base(rel), 0); unlinkErr != nil {
		return unlinkErr
	}
	if e.removeDurabilityHook != nil {
		if err := e.removeDurabilityHook("sync"); err != nil {
			_ = dir.Close()
			return err
		}
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return fmt.Errorf("sync parent directory after delete: %w", err)
	}
	if e.removeDurabilityHook != nil {
		if err := e.removeDurabilityHook("close"); err != nil {
			_ = dir.Close()
			return err
		}
	}
	if err := dir.Close(); err != nil {
		return fmt.Errorf("close parent directory after delete sync: %w", err)
	}
	return nil
}

// snapshotAndWrite records an undo snapshot then atomically writes content.
// Combining them keeps the invariant structural: no mutating write skips
// history. Returns the undo id ("" when the snapshot was skipped).
func (e *Engine) snapshotAndWrite(resolved, displayPath, op string, preContent []byte, content string) (string, error) {
	id, err := e.recordSnapshotForMutation(resolved, displayPath, op, snapshotTransition{preContent: preContent, expectedAfter: []byte(content), expectedExists: true})
	if err != nil {
		return "", err
	}
	if e.snapshotPreparedHook != nil {
		e.snapshotPreparedHook()
	}
	mutated, err := e.atomicWriteFileStaged(resolved, content, func() error {
		return e.verifyPreflightState(resolved, preContent, preContent != nil)
	})
	if err != nil {
		state := historyStateAborted
		if mutated {
			state = historyStateUncertain
		}
		_ = e.markSnapshotState(id, state)
		return id, err
	}
	if err := e.markSnapshotState(id, historyStateCommitted); err != nil {
		_ = e.markSnapshotState(id, historyStateUncertain)
		return id, fmt.Errorf("mutation committed but history state is uncertain: %w", err)
	}
	return id, nil
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
func (e *Engine) verifyPreflightState(path string, expected []byte, expectedExists bool) error {
	current, _, err := e.readRegularFile(path, maxFileSize)
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

	resolved, err := e.checkPathForMutation(path)
	if err != nil {
		return "", err
	}
	var result string
	err = e.withTargetLocks([]string{resolved}, func() error {
		var innerErr error
		result, innerErr = e.writeFileLocked(args, path, content, resolved, beforeCommit)
		return innerErr
	})
	return result, err
}

//nolint:gocyclo // preflight, dry-run, assertion, and commit branches must share one target lock.
func (e *Engine) writeFileLocked(args map[string]interface{}, path, content, resolved string, beforeCommit func()) (string, error) {
	if strArg(args, "if_checksum") != "" && boolArg(args, "if_absent") {
		return "", &ErrWithSuggestion{Err: errors.New("if_checksum and if_absent:true are mutually exclusive"), Suggestion: "use if_checksum for a replacement or if_absent:true for a creation", Code: ErrCodeInvalidArgs}
	}
	if staleErr := e.tracker.checkStale(resolved); staleErr != nil {
		return "", staleErr
	}

	// Read current state once: the if_checksum precondition, the dry-run
	// diff, and the undo snapshot all reuse these bytes. nil preContent =
	// file did not exist or was unreadable (skip snapshot, don't block write).
	preContent, _, readErr := e.readRegularFile(resolved, maxFileSize)
	exists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return "", fmt.Errorf("read current target: %w", readErr)
	}
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
	if e.requireMutationPreconditions {
		if exists && strArg(args, "if_checksum") == "" {
			return "", &ErrWithSuggestion{Err: fmt.Errorf("if_checksum is required to replace existing file %s", path), Suggestion: "read the file with include_checksum=true and retry with that digest", Code: ErrCodeInvalidArgs}
		}
		if !exists && !boolArg(args, "if_absent") {
			return "", &ErrWithSuggestion{Err: fmt.Errorf("if_absent:true is required to create %s", path), Suggestion: "set if_absent:true after confirming the target should not exist", Code: ErrCodeInvalidArgs}
		}
	}

	if beforeCommit != nil {
		beforeCommit()
	}
	if err := e.verifyPreflightState(resolved, preContent, exists); err != nil {
		return "", err
	}
	if _, err := e.snapshotAndWrite(resolved, path, "write_file", preContent, content); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(content), path), nil
}
