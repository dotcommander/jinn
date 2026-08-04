package jinn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	historyMaxEntries    = 50
	historyMaxTotalBytes = 20 * 1024 * 1024 // 20 MiB
	historyMaxBlobBytes  = 5 * 1024 * 1024  // 5 MiB per file
)

const (
	historyStatePrepared  = "prepared"
	historyStateCommitted = "committed"
	historyStateAborted   = "aborted"
	historyStateUncertain = "uncertain"
)

type historyIdentity struct {
	Exists bool   `json:"exists"`
	Hash   string `json:"hash,omitempty"`
}

type snapshotTransition struct {
	preContent     []byte
	expectedAfter  []byte
	expectedExists bool
}

// historyEntry is one slot in the ring-buffer index.
type historyEntry struct {
	ID            string          `json:"id"`                  // sha256[:16] of (workdir+path+timestamp)
	Target        string          `json:"target,omitempty"`    // normalized workspace-relative target
	AbsPath       string          `json:"abs_path,omitempty"`  // legacy index compatibility
	DisplayPath   string          `json:"display_path"`        // user-visible relative path
	Op            string          `json:"op"`                  // write_file, edit_file, multi_edit
	BlobID        string          `json:"blob_id,omitempty"`   // history-root-relative blob identifier
	BlobPath      string          `json:"blob_path,omitempty"` // legacy index compatibility
	BlobSize      int64           `json:"blob_size"`           // pre-content byte count
	BlobHash      string          `json:"blob_hash"`           // sha256 hex of blob content
	Created       bool            `json:"created"`             // true when file didn't exist before op
	Timestamp     time.Time       `json:"timestamp"`
	Before        historyIdentity `json:"before,omitempty"`
	ExpectedAfter historyIdentity `json:"expected_after,omitempty"`
	State         string          `json:"state,omitempty"` // prepared, committed, aborted, uncertain
}

// historyFile is the on-disk index.
type historyFile struct {
	Version int            `json:"version"`
	Entries []historyEntry `json:"entries"` // oldest first
}

// historyDir returns the per-workdir history directory.
func (e *Engine) historyDir() string {
	hash := sha256.Sum256([]byte(e.workDir))
	wdHash := hex.EncodeToString(hash[:])[:16]
	base := os.Getenv("JINN_CONFIG_DIR")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err == nil {
			base = dir
		} else {
			base = os.TempDir()
		}
	}
	return filepath.Join(base, "jinn", "history", wdHash)
}

// indexPath returns the path to index.json within the history dir.
func (e *Engine) indexPath() string {
	return filepath.Join(e.historyDir(), "index.json")
}

// blobsDir returns the path to the blobs subdirectory.
func (e *Engine) blobsDir() string {
	return filepath.Join(e.historyDir(), "blobs")
}

// historyLockPath returns the cross-process lock file guarding this
// workdir's history store. It is a SIBLING of historyDir(), deliberately
// outside it: undoClear RemoveAll's the dir, and unlinking a held lock file
// would let two processes hold "the lock" on different inodes.
//
// The lock domain is the on-disk store shared by concurrent PROCESSES —
// jinn runs one process per tool call, so an in-process mutex protects
// nothing in production. flock also serializes goroutines within one
// process (each withFileLock call opens its own fd), so it fully subsumes
// the old package-level mutex this replaces.
func (e *Engine) historyLockPath() string {
	return e.historyDir() + ".lock"
}

// loadHistoryLocked reads the index under the cross-process history lock.
func (e *Engine) loadHistoryLocked() (historyFile, error) {
	var hf historyFile
	err := withFileLock(e.historyLockPath(), func() error {
		var loadErr error
		hf, loadErr = e.loadHistory()
		if loadErr != nil {
			return loadErr
		}
		return e.reconcileHistoryLocked(&hf)
	})
	return hf, err
}

// reconcileHistoryLocked resolves entries left between snapshot preparation
// and state commit. Legacy state-less entries predate this contract and remain
// committed. Invalid targets are never dereferenced: their outcome is simply
// uncertain and no blob is read or removed.
//
//nolint:gocognit,gocyclo,revive // each crash-state branch is explicit so reconciliation remains auditable.
func (e *Engine) reconcileHistoryLocked(hf *historyFile) error {
	changed := false
	for i := range hf.Entries {
		entry := &hf.Entries[i]
		if entry.State == "" {
			entry.State = historyStateCommitted
			changed = true
			continue
		}
		if entry.State == historyStateUncertain {
			// A post-mutation durability failure is not a crash window: preserve
			// explicit uncertainty for operator recovery rather than promoting it
			// merely because current bytes happen to match the expected state.
			continue
		}
		if entry.State != historyStatePrepared {
			continue
		}
		if entry.ExpectedAfter.Hash == "" && entry.ExpectedAfter.Exists {
			if entry.State != historyStateUncertain {
				entry.State = historyStateUncertain
				changed = true
			}
			continue
		}
		resolved, err := e.resolveUndoTarget(*entry)
		if err != nil {
			if entry.State != historyStateUncertain {
				entry.State = historyStateUncertain
				changed = true
			}
			continue
		}
		identity, err := e.historyTargetIdentity(resolved)
		if err != nil {
			if entry.State != historyStateUncertain {
				entry.State = historyStateUncertain
				changed = true
			}
			continue
		}
		state := historyStateUncertain
		switch identity {
		case entry.ExpectedAfter:
			state = historyStateCommitted
		case entry.Before:
			state = historyStateAborted
		}
		if entry.State != state {
			entry.State = state
			changed = true
		}
	}
	if changed {
		hf.Version = 2
		return e.saveHistory(*hf)
	}
	return nil
}

func (e *Engine) historyTargetIdentity(resolved string) (historyIdentity, error) {
	data, _, err := e.readRegularFile(resolved, maxFileSize)
	if os.IsNotExist(err) {
		return historyIdentity{}, nil
	}
	if err != nil {
		return historyIdentity{}, err
	}
	return historyIdentity{Exists: true, Hash: sha256HexBytes(data)}, nil
}

// loadHistory reads and unmarshals the history index.
// Returns an empty struct when the file does not exist.
func (e *Engine) loadHistory() (historyFile, error) {
	path := e.indexPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return historyFile{Version: 2, Entries: []historyEntry{}}, nil
	}
	if err != nil {
		return historyFile{}, fmt.Errorf("history: read index: %w", err)
	}
	var hf historyFile
	if err := json.Unmarshal(data, &hf); err != nil {
		return historyFile{}, fmt.Errorf("history: unmarshal index: %w", err)
	}
	if hf.Entries == nil {
		hf.Entries = []historyEntry{}
	}
	if hf.Version == 0 {
		hf.Version = 1
	}
	return hf, nil
}

// saveHistory atomically writes the history index via temp+fsync+rename.
func (e *Engine) saveHistory(hf historyFile) error {
	if err := atomicWriteJSON(e.indexPath(), hf); err != nil {
		return fmt.Errorf("history: %w", err)
	}
	return nil
}

func validateSnapshotSize(displayPath string, preContent []byte) error {
	if len(preContent) <= historyMaxBlobBytes {
		return nil
	}
	return &ErrWithSuggestion{
		Err: fmt.Errorf(
			"cannot mutate %s: undo snapshot limit is %d bytes (pre-content is %d bytes)",
			displayPath, historyMaxBlobBytes, len(preContent),
		),
		Suggestion: fmt.Sprintf("reduce the existing file below %d bytes before mutating it", historyMaxBlobBytes),
		Code:       ErrCodeFileTooLarge,
	}
}

// recordSnapshotForMutation enforces the undo contract at mutation boundaries
// while preserving recordSnapshot's best-effort behavior for internal callers.
func (e *Engine) recordSnapshotForMutation(absPath, displayPath, op string, transition snapshotTransition) (string, error) {
	if err := validateSnapshotSize(displayPath, transition.preContent); err != nil {
		return "", err
	}
	var id string
	err := withFileLock(e.historyLockPath(), func() error {
		var recordErr error
		id, recordErr = e.recordSnapshotLockedStrict(absPath, displayPath, op, transition)
		return recordErr
	})
	if err != nil {
		return "", fmt.Errorf("prepare undo snapshot: %w", err)
	}
	return id, nil
}

// recordSnapshot saves a pre-mutation snapshot of absPath and returns the
// new entry's undo id ("" when the snapshot was skipped).
// Never blocks a mutation — all recoverable failures are swallowed (best-effort).
// preContent == nil means the file did not exist before the operation.
//
// Blobs are compressed with adaptive gzip (adapted from agented's blob codec).
// This reduces disk usage for text-heavy edit histories without overhead on
// small edits or already-compressed content.
//
//nolint:unparam // legacy direct snapshot helper retains the operation test seam.
func (e *Engine) recordSnapshot(absPath, displayPath, op string, preContent []byte) string {
	if len(preContent) > historyMaxBlobBytes {
		// File too large to snapshot — skip silently, don't block the write.
		return ""
	}

	// Best-effort: a lock failure skips the snapshot, never blocks the write.
	var id string
	_ = withFileLock(e.historyLockPath(), func() error {
		id = e.recordSnapshotLocked(absPath, displayPath, op, preContent)
		return nil
	})
	if id != "" {
		_ = e.markSnapshotState(id, historyStateCommitted)
	}
	return id
}

// recordSnapshotLocked performs the load→blob-write→append→evict→save
// sequence and returns the entry id ("" on any skipped path). Caller holds
// the history file lock.
func (e *Engine) recordSnapshotLocked(absPath, displayPath, op string, preContent []byte) string {
	id, _ := e.recordSnapshotLockedStrict(absPath, displayPath, op, snapshotTransition{preContent: preContent})
	return id
}

func (e *Engine) recordSnapshotLockedStrict(absPath, displayPath, op string, transition snapshotTransition) (string, error) {
	hf, err := e.loadHistory()
	if err != nil {
		return "", err
	}

	// Build unique entry ID from workdir+path+timestamp.
	ts := time.Now().UTC()
	raw := e.workDir + absPath + ts.Format(time.RFC3339Nano)
	idHash := sha256.Sum256([]byte(raw))
	id := hex.EncodeToString(idHash[:])[:16]

	created := transition.preContent == nil

	// Write blob (compressed).
	blobHash := ""
	blobPath := ""
	var blobSize int64
	if !created {
		blob, werr := e.writeBlobForSnapshot(id, transition.preContent)
		if werr != nil {
			return "", werr
		}
		blobHash, blobPath, blobSize = blob.hash, blob.path, blob.size
	}

	target, err := e.rootRelative(absPath)
	if err != nil {
		return "", err
	}
	blobID := ""
	if blobPath != "" {
		blobID = filepath.Base(blobPath)
	}
	before := historyIdentity{Exists: !created}
	if before.Exists {
		before.Hash = sha256HexBytes(transition.preContent)
	}
	expected := historyIdentity{Exists: transition.expectedExists}
	if expected.Exists {
		expected.Hash = sha256HexBytes(transition.expectedAfter)
	}
	entry := historyEntry{
		ID:            id,
		Target:        target,
		DisplayPath:   displayPath,
		Op:            op,
		BlobID:        blobID,
		BlobHash:      blobHash,
		BlobSize:      blobSize,
		Created:       created,
		Timestamp:     ts,
		Before:        before,
		ExpectedAfter: expected,
		State:         historyStatePrepared,
	}

	hf.Entries = append(hf.Entries, entry)
	hf.Version = 2
	evicted := e.evictHistory(&hf)

	if err := e.saveHistory(hf); err != nil {
		// Index write failed — clean up orphaned blob (non-blocking).
		if blobPath != "" {
			_ = os.Remove(blobPath)
		}
		return "", err
	}
	for _, old := range evicted {
		e.removeBlob(old)
	}
	return id, nil
}

func (e *Engine) markSnapshotState(id, state string) error {
	if id == "" {
		return nil
	}
	if state != historyStatePrepared && state != historyStateCommitted && state != historyStateAborted && state != historyStateUncertain {
		return fmt.Errorf("invalid history state %q", state)
	}
	return withFileLock(e.historyLockPath(), func() error {
		hf, err := e.loadHistory()
		if err != nil {
			return err
		}
		for index := range hf.Entries {
			if hf.Entries[index].ID == id {
				hf.Entries[index].State = state
				return e.saveHistory(hf)
			}
		}
		return fmt.Errorf("history entry %s disappeared before %s", id, state)
	})
}

// snapshotBlob is the result of writing a pre-edit blob to disk.
type snapshotBlob struct {
	hash, path string
	size       int64
}

// writeBlobForSnapshot encodes and atomically writes the pre-edit content to a
// blob file for snapshot id. On any failure (mkdir, encode, write) it returns a
// non-nil error and a zero snapshotBlob, so recordSnapshot aborts the snapshot
// exactly as the inline early-returns did (best-effort, non-blocking).
func (e *Engine) writeBlobForSnapshot(id string, preContent []byte) (snapshotBlob, error) {
	h := sha256.Sum256(preContent)
	path := filepath.Join(e.blobsDir(), id+".blob")
	if mkErr := os.MkdirAll(e.blobsDir(), 0o700); mkErr != nil {
		return snapshotBlob{}, mkErr
	}
	encoded, cerr := encodeBlob(preContent)
	if cerr != nil {
		return snapshotBlob{}, cerr
	}
	if wErr := atomicWriteBytes(path, encoded); wErr != nil {
		return snapshotBlob{}, wErr
	}
	return snapshotBlob{
		hash: hex.EncodeToString(h[:]),
		path: path,
		size: int64(len(preContent)), // track original size for eviction
	}, nil
}

// evictHistory trims the ring-buffer to satisfy entry count and total size limits.
// It removes blobs for evicted entries. Caller holds the history file lock.
func (e *Engine) evictHistory(hf *historyFile) []historyEntry {
	var evicted []historyEntry
	// Trim by entry count (oldest first).
	for len(hf.Entries) > historyMaxEntries {
		evicted = append(evicted, hf.Entries[0])
		hf.Entries = hf.Entries[1:]
	}

	// Trim by total blob size (compute once, subtract as entries are removed).
	var total int64
	for _, ent := range hf.Entries {
		total += ent.BlobSize
	}
	for total > historyMaxTotalBytes && len(hf.Entries) > 0 {
		total -= hf.Entries[0].BlobSize
		evicted = append(evicted, hf.Entries[0])
		hf.Entries = hf.Entries[1:]
	}
	return evicted
}

// removeBlob deletes the blob file for an entry (best-effort, ignores errors).
func (e *Engine) removeBlob(ent historyEntry) {
	if ent.BlobID != "" && filepath.Base(ent.BlobID) == ent.BlobID {
		_ = os.Remove(filepath.Join(e.blobsDir(), ent.BlobID))
		return
	}
	legacy := filepath.Clean(ent.BlobPath)
	if filepath.Dir(legacy) == filepath.Clean(e.blobsDir()) && filepath.Base(legacy) != "." {
		_ = os.Remove(legacy)
	}
}

// atomicWriteBytes writes bytes to path via temp+rename.
func atomicWriteBytes(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".blob-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, writeErr := tmp.Write(data); writeErr != nil {
		_ = tmp.Close()
		return writeErr
	}
	if chmodErr := tmp.Chmod(0o600); chmodErr != nil {
		_ = tmp.Close()
		return chmodErr
	}
	if syncErr := tmp.Sync(); syncErr != nil {
		_ = tmp.Close()
		return syncErr
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return closeErr
	}
	if renameErr := os.Rename(tmpPath, path); renameErr != nil {
		return renameErr
	}
	dirFile, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open parent directory for durability: %w", err)
	}
	if syncErr := dirFile.Sync(); syncErr != nil {
		_ = dirFile.Close()
		return fmt.Errorf("sync parent directory for durability: %w", syncErr)
	}
	if closeErr := dirFile.Close(); closeErr != nil {
		return fmt.Errorf("close parent directory after durability sync: %w", closeErr)
	}
	ok = true
	return nil
}
