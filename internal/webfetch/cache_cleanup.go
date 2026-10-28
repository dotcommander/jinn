package webfetch

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const cacheCleanupBatch = 64

// cleanup advances a bounded sweep on cache activity; Close owns the cursor.
func (c *urlCache) cleanup() {
	if c == nil {
		return
	}
	c.sweepMu.Lock()
	defer c.sweepMu.Unlock()
	if c.closed {
		return
	}
	if c.sweepDir == nil {
		var err error
		c.sweepDir, err = os.Open(c.dir)
		if err != nil {
			return
		}
	}
	entries, err := c.sweepDir.ReadDir(cacheCleanupBatch)
	if err != nil || len(entries) < cacheCleanupBatch {
		_ = c.sweepDir.Close()
		c.sweepDir = nil
	}
	_ = withCacheLock(filepath.Join(c.dir, ".cache.lock"), func() error {
		for _, entry := range entries {
			if entry.Type().IsRegular() && canonicalCacheName(entry.Name()) {
				c.removeExpired(filepath.Join(c.dir, entry.Name()))
			}
		}
		return nil
	})
}

func canonicalCacheName(name string) bool {
	stem, ok := strings.CutSuffix(name, ".json")
	if !ok || len(stem) != 64 {
		return false
	}
	_, err := hex.DecodeString(stem)
	return err == nil
}

// Called with the interprocess cache lock: re-read the current canonical entry
// so an earlier observation cannot delete a freshly replaced document.
func (c *urlCache) removeExpired(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	info, statErr := file.Stat()
	data, readErr := io.ReadAll(io.LimitReader(file, c.maxBytes+1))
	_ = file.Close()
	if statErr != nil || !info.Mode().IsRegular() || readErr != nil || int64(len(data)) > c.maxBytes {
		return
	}
	var envelope urlCacheEnvelope
	if json.Unmarshal(data, &envelope) != nil || envelope.Version != urlCacheVersion || envelope.StoredAt.IsZero() || time.Since(envelope.StoredAt) <= c.ttl {
		return
	}
	if filepath.Base(path) != cacheFileName(envelope.URL, envelope.Reader, envelope.Endpoint, envelope.Render, envelope.RenderWait) {
		return
	}
	// Avoid removing a path replaced by an uncoordinated external writer.
	current, err := os.Lstat(path)
	if err == nil && os.SameFile(info, current) {
		_ = os.Remove(path)
	}
}

func (c *urlCache) close() {
	if c == nil {
		return
	}
	c.sweepMu.Lock()
	defer c.sweepMu.Unlock()
	c.closed = true
	if c.sweepDir != nil {
		_ = c.sweepDir.Close()
		c.sweepDir = nil
	}
}
