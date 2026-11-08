package webfetch

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Durable result snapshots (slice 1): opt-in, immutable copies of search
// results or fetched documents that a later call can read verbatim via
// web_content — without refetching. Snapshots live privately outside the
// workspace, independent of the URL-document cache, and are bounded by
// entry count, aggregate bytes, per-entry bytes, and TTL. A namespace
// organizes data only; it is not authentication.

const (
	snapshotKindSearch   = "search"
	snapshotKindDocument = "document"
	snapshotFileSuffix   = ".json"
	snapshotIDBytes      = 16 // 128-bit random id, hex encoded (32 chars)
)

// Snapshot is one stored search or document result.
type Snapshot struct {
	ID        string           `json:"id"`
	Kind      string           `json:"kind"`
	CreatedAt time.Time        `json:"created_at"`
	ExpiresAt time.Time        `json:"expires_at"`
	Digest    string           `json:"digest"`
	Query     string           `json:"query,omitempty"`
	Provider  string           `json:"provider,omitempty"`
	Results   []SearchResult   `json:"results,omitempty"`
	Fallbacks []SearchFallback `json:"fallbacks,omitempty"`
	// Queries carries per-query outcomes for batched searches.
	Queries  []SearchQueryOutcome `json:"queries,omitempty"`
	Document *Document            `json:"document,omitempty"`
}

// SnapshotInfo reports the persistence outcome for one Save call.
type SnapshotInfo struct {
	ID        string `json:"id,omitempty"`
	Kind      string `json:"kind"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Digest    string `json:"digest,omitempty"`
	// Status is "stored" or "unavailable" (persistence failed; the
	// retrieval itself is preserved and never repeated).
	Status string `json:"status"`
}

type snapshotStore struct {
	dir           string
	ttl           time.Duration
	maxEntries    int
	maxBytes      int64
	maxEntryBytes int64
	mu            sync.Mutex
}

func newSnapshotStore(dir string, ttl time.Duration) *snapshotStore {
	if strings.TrimSpace(dir) == "" {
		var err error
		dir, err = defaultSnapshotDir()
		if err != nil {
			return nil
		}
	}
	if ttl <= 0 {
		ttl = DefaultSnapshotTTL
	}
	return &snapshotStore{
		dir:           dir,
		ttl:           ttl,
		maxEntries:    DefaultSnapshotMaxEntries,
		maxBytes:      DefaultSnapshotMaxBytes,
		maxEntryBytes: DefaultSnapshotMaxEntryBytes,
	}
}

// resolveSnapshotDir prefers an explicit snapshot directory, then derives
// one beside the URL cache directory.
func resolveSnapshotDir(cfg Config) string {
	if strings.TrimSpace(cfg.SnapshotDir) != "" {
		return strings.TrimSpace(cfg.SnapshotDir)
	}
	return snapshotDirFor(cfg.URLCacheDir)
}

func defaultSnapshotDir() (string, error) {
	if cacheHome := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); cacheHome != "" {
		return filepath.Join(cacheHome, "jinn", "web", "snapshots"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "jinn", "web", "snapshots"), nil
}

// snapshotDirFor lets the CLI derive the store location from an explicit
// URL cache directory so snapshots sit beside it.
func snapshotDirFor(urlCacheDir string) string {
	if strings.TrimSpace(urlCacheDir) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(urlCacheDir), "snapshots")
}

func validSnapshotID(id string) bool {
	if len(id) != snapshotIDBytes*2 || strings.ToLower(id) != id {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func newSnapshotID() (string, error) {
	buf := make([]byte, snapshotIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func snapshotDigest(kind string, payload []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// SaveSearchSnapshot persists one search response. The returned info never fails
// the retrieval: persistence problems surface as Status "unavailable".
func (s *Service) SaveSearchSnapshot(resp SearchResponse) SnapshotInfo {
	if s == nil || s.snapshots == nil {
		return SnapshotInfo{Kind: snapshotKindSearch, Status: "unavailable"}
	}
	payload, err := json.Marshal(struct {
		Query     string               `json:"query"`
		Provider  string               `json:"provider"`
		Results   []SearchResult       `json:"results"`
		Fallbacks []SearchFallback     `json:"fallbacks"`
		Queries   []SearchQueryOutcome `json:"queries"`
	}{resp.Query, resp.Provider, resp.Results, resp.Fallbacks, resp.Queries})
	if err != nil {
		return SnapshotInfo{Kind: snapshotKindSearch, Status: "unavailable"}
	}
	now := time.Now().UTC()
	snap := Snapshot{
		Kind:      snapshotKindSearch,
		CreatedAt: now,
		ExpiresAt: now.Add(s.snapshots.ttl),
		Digest:    snapshotDigest(snapshotKindSearch, payload),
		Query:     resp.Query,
		Provider:  resp.Provider,
		Results:   resp.Results,
		Fallbacks: resp.Fallbacks,
		Queries:   resp.Queries,
	}
	if err := s.snapshots.store(&snap); err != nil {
		return SnapshotInfo{Kind: snapshotKindSearch, Status: "unavailable"}
	}
	return SnapshotInfo{ID: snap.ID, Kind: snap.Kind, ExpiresAt: snap.ExpiresAt.Format(time.RFC3339), Digest: snap.Digest, Status: "stored"}
}

// SaveDocumentSnapshot persists one fetched document. See SaveSearchSnapshot.
func (s *Service) SaveDocumentSnapshot(doc Document) SnapshotInfo {
	if s == nil || s.snapshots == nil {
		return SnapshotInfo{Kind: snapshotKindDocument, Status: "unavailable"}
	}
	// Image bytes are not part of the text snapshot contract.
	doc.ImageBytes = nil
	payload, err := json.Marshal(doc)
	if err != nil {
		return SnapshotInfo{Kind: snapshotKindDocument, Status: "unavailable"}
	}
	now := time.Now().UTC()
	snap := Snapshot{
		Kind:      snapshotKindDocument,
		CreatedAt: now,
		ExpiresAt: now.Add(s.snapshots.ttl),
		Digest:    snapshotDigest(snapshotKindDocument, payload),
		Document:  &doc,
	}
	if err := s.snapshots.store(&snap); err != nil {
		return SnapshotInfo{Kind: snapshotKindDocument, Status: "unavailable"}
	}
	return SnapshotInfo{ID: snap.ID, Kind: snap.Kind, ExpiresAt: snap.ExpiresAt.Format(time.RFC3339), Digest: snap.Digest, Status: "stored"}
}

// GetSnapshot loads one snapshot by id. Reads never refetch. Absent,
// expired, and malformed snapshots produce distinct coded errors.
func (s *Service) GetSnapshot(id string) (Snapshot, error) {
	if s == nil || s.snapshots == nil {
		return Snapshot{}, newCodedError(errors.New("snapshot store is not configured"), ErrorCodeSnapshotInvalid, "configure a snapshot directory before reading snapshots")
	}
	return s.snapshots.load(id)
}

func (st *snapshotStore) store(snap *Snapshot) error {
	if st == nil {
		return errors.New("snapshot store is not configured")
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if int64(len(data)) > st.maxEntryBytes {
		return fmt.Errorf("snapshot exceeds %d bytes", st.maxEntryBytes)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if mkdirErr := os.MkdirAll(st.dir, urlCacheDirectoryMode); mkdirErr != nil {
		return mkdirErr
	}
	if chmodErr := os.Chmod(st.dir, urlCacheDirectoryMode); chmodErr != nil {
		return chmodErr
	}
	for attempt := 0; attempt < 4; attempt++ {
		id, idErr := newSnapshotID()
		if idErr != nil {
			return idErr
		}
		path := filepath.Join(st.dir, id+snapshotFileSuffix)
		if _, statErr := os.Stat(path); statErr == nil {
			continue // id collision; mint another
		}
		snap.ID = id
		// Re-marshal so the stored envelope carries the final id.
		data, err = json.Marshal(snap)
		if err != nil {
			return err
		}
		writeErr := withCacheLock(filepath.Join(st.dir, ".snapshot.lock"), func() error {
			return writeSnapshotFile(path, data)
		})
		if writeErr != nil {
			return writeErr
		}
		st.evict()
		return nil
	}
	return errors.New("could not mint a unique snapshot id")
}

func writeSnapshotFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".jinn-web-snapshot-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, urlCacheFileMode); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	keep = true
	return nil
}

func (st *snapshotStore) load(id string) (Snapshot, error) {
	if !validSnapshotID(id) {
		return Snapshot{}, newCodedError(fmt.Errorf("invalid snapshot id %q", id), ErrorCodeSnapshotInvalid, "use a 32-character lowercase hex snapshot id from a previous persist_snapshot result")
	}
	path := filepath.Join(st.dir, id+snapshotFileSuffix)
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{}, newCodedError(fmt.Errorf("snapshot %s not found", id), ErrorCodeSnapshotNotFound, "re-run the search or fetch with persist_snapshot to create a fresh snapshot")
		}
		return Snapshot{}, newCodedError(fmt.Errorf("read snapshot: %w", err), ErrorCodeSnapshotInvalid, "re-run the search or fetch with persist_snapshot")
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, st.maxEntryBytes+1))
	if err != nil || int64(len(data)) > st.maxEntryBytes {
		return Snapshot{}, newCodedError(errors.New("snapshot is unreadable or exceeds the entry limit"), ErrorCodeSnapshotInvalid, "re-run the search or fetch with persist_snapshot")
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return Snapshot{}, newCodedError(fmt.Errorf("decode snapshot: %w", err), ErrorCodeSnapshotInvalid, "re-run the search or fetch with persist_snapshot")
	}
	if snap.ID != id || snap.CreatedAt.IsZero() || snap.ExpiresAt.IsZero() {
		return Snapshot{}, newCodedError(errors.New("snapshot envelope is malformed"), ErrorCodeSnapshotInvalid, "re-run the search or fetch with persist_snapshot")
	}
	if time.Now().UTC().After(snap.ExpiresAt) {
		_ = withCacheLock(filepath.Join(st.dir, ".snapshot.lock"), func() error {
			_ = os.Remove(path)
			return nil
		})
		return Snapshot{}, newCodedError(fmt.Errorf("snapshot %s expired at %s", id, snap.ExpiresAt.Format(time.RFC3339)), ErrorCodeSnapshotExpired, "re-run the search or fetch with persist_snapshot to create a fresh snapshot")
	}
	return snap, nil
}

// evict removes oldest snapshots until count and aggregate bytes are within
// bounds. Best-effort: eviction failures never fail a save.
func (st *snapshotStore) evict() {
	entries, err := os.ReadDir(st.dir)
	if err != nil {
		return
	}
	type fileInfo struct {
		path    string
		size    int64
		modTime time.Time
	}
	var files []fileInfo
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), snapshotFileSuffix) {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		files = append(files, fileInfo{path: filepath.Join(st.dir, entry.Name()), size: info.Size(), modTime: info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
	for len(files) > st.maxEntries || total > st.maxBytes {
		oldest := files[0]
		files = files[1:]
		total -= oldest.size
		_ = os.Remove(oldest.path)
	}
}
