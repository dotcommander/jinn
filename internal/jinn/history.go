package jinn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	historyMaxEntries    = 50
	historyMaxTotalBytes = 20 * 1024 * 1024 // 20 MiB
	historyMaxBlobBytes  = 5 * 1024 * 1024  // 5 MiB per file
)

// historyEntry is one slot in the ring-buffer index.
type historyEntry struct {
	ID          string    `json:"id"`           // sha256[:16] of (workdir+path+timestamp)
	AbsPath     string    `json:"abs_path"`     // resolved absolute path at snapshot time
	DisplayPath string    `json:"display_path"` // user-visible relative path
	Op          string    `json:"op"`           // write_file, edit_file, multi_edit
	BlobPath    string    `json:"blob_path"`    // absolute path to blob file
	BlobSize    int64     `json:"blob_size"`    // pre-content byte count
	BlobHash    string    `json:"blob_hash"`    // sha256 hex of blob content
	Created     bool      `json:"created"`      // true when file didn't exist before op
	Timestamp   time.Time `json:"timestamp"`
}

// historyFile is the on-disk index.
type historyFile struct {
	Version int            `json:"version"`
	Entries []historyEntry `json:"entries"` // oldest first
}

var histMu sync.Mutex // single global mutex — all engines share the same on-disk store

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

// loadHistory reads and unmarshals the history index.
// Returns an empty struct when the file does not exist.
func (e *Engine) loadHistory() (historyFile, error) {
	path := e.indexPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return historyFile{Version: 1, Entries: []historyEntry{}}, nil
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
	return hf, nil
}

// saveHistory atomically writes the history index via temp+rename.
func (e *Engine) saveHistory(hf historyFile) error {
	dir := e.historyDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("history: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(hf, "", "  ")
	if err != nil {
		return fmt.Errorf("history: marshal: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "index-*.json.tmp")
	if err != nil {
		return fmt.Errorf("history: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("history: write temp: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("history: chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("history: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, e.indexPath()); err != nil {
		return fmt.Errorf("history: rename: %w", err)
	}
	ok = true
	return nil
}

// recordSnapshot saves a pre-mutation snapshot of absPath.
// Never blocks a mutation — all recoverable failures return nil.
// preContent == nil means the file did not exist before the operation.
func (e *Engine) recordSnapshot(absPath, displayPath, op string, preContent []byte) error {
	if len(preContent) > historyMaxBlobBytes {
		// File too large to snapshot — skip silently, don't block the write.
		return nil
	}

	histMu.Lock()
	defer histMu.Unlock()

	hf, err := e.loadHistory()
	if err != nil {
		return nil // non-blocking
	}

	// Build unique entry ID from workdir+path+timestamp.
	ts := time.Now().UTC()
	raw := e.workDir + absPath + ts.Format(time.RFC3339Nano)
	idHash := sha256.Sum256([]byte(raw))
	id := hex.EncodeToString(idHash[:])[:16]

	created := preContent == nil

	// Write blob.
	blobHash := ""
	blobPath := ""
	var blobSize int64
	if !created {
		h := sha256.Sum256(preContent)
		blobHash = hex.EncodeToString(h[:])
		blobPath = filepath.Join(e.blobsDir(), id+".blob")
		if err := os.MkdirAll(e.blobsDir(), 0o700); err != nil {
			return nil // non-blocking
		}
		if err := atomicWriteBytes(blobPath, preContent, 0o600); err != nil {
			return nil // non-blocking
		}
		blobSize = int64(len(preContent))
	}

	entry := historyEntry{
		ID:          id,
		AbsPath:     absPath,
		DisplayPath: displayPath,
		Op:          op,
		BlobPath:    blobPath,
		BlobHash:    blobHash,
		BlobSize:    blobSize,
		Created:     created,
		Timestamp:   ts,
	}

	hf.Entries = append(hf.Entries, entry)
	e.evictHistory(&hf)

	if err := e.saveHistory(hf); err != nil {
		// Index write failed — clean up orphaned blob, then return nil (non-blocking).
		if blobPath != "" {
			os.Remove(blobPath)
		}
		return nil
	}
	return nil
}

// evictHistory trims the ring-buffer to satisfy entry count and total size limits.
// It removes blobs for evicted entries. Called with histMu held.
func (e *Engine) evictHistory(hf *historyFile) {
	// Trim by entry count (oldest first).
	for len(hf.Entries) > historyMaxEntries {
		e.removeBlob(hf.Entries[0])
		hf.Entries = hf.Entries[1:]
	}

	// Trim by total blob size.
	for {
		var total int64
		for _, ent := range hf.Entries {
			total += ent.BlobSize
		}
		if total <= historyMaxTotalBytes || len(hf.Entries) == 0 {
			break
		}
		e.removeBlob(hf.Entries[0])
		hf.Entries = hf.Entries[1:]
	}
}

// removeBlob deletes the blob file for an entry (best-effort, ignores errors).
func (e *Engine) removeBlob(ent historyEntry) {
	if ent.BlobPath != "" {
		os.Remove(ent.BlobPath)
	}
}

// atomicWriteBytes writes bytes to path via temp+rename.
func atomicWriteBytes(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".blob-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	ok = true
	return nil
}
