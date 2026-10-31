package webfetch

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// BenchmarkFreshCacheCleanup compares a full bounded sweep of 16 fresh,
// canonical local entries (~256 KiB of content each). HEAD reads and decodes
// each entry; current cleanup retains it after a 128-byte prefix probe. This
// measures warm local filesystem behavior, not expiry, cache misses, or network.
func BenchmarkFreshCacheCleanup(b *testing.B) {
	dir := b.TempDir()
	cache := newURLCache(time.Hour, dir, 512<<10)
	b.Cleanup(cache.close)
	content := strings.Repeat("x", 256<<10)
	for i := 0; i < 16; i++ {
		url := fmt.Sprintf("https://example.test/cache/%d", i)
		data, err := json.Marshal(urlCacheEnvelope{
			Version: urlCacheVersion, StoredAt: time.Now().UTC(), URL: url,
			Reader: ReaderModeDefuddle, Render: RenderModeNever, RenderWait: RenderWaitLoad,
			RenderPolicyVersion: renderPolicyVersion, Document: Document{URL: url, Content: content},
		})
		if err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, cacheFileName(url, ReaderModeDefuddle, "")), data, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	for _, variant := range []struct {
		name string
		run  func() error
	}{
		{name: "HEAD_full_read_decode", run: func() error {
			entries, err := os.ReadDir(dir)
			if err != nil {
				return err
			}
			return withCacheLock(filepath.Join(dir, ".cache.lock"), func() error {
				for _, entry := range entries {
					if entry.Type().IsRegular() && canonicalCacheName(entry.Name()) {
						if err := legacyFreshCacheRead(cache, filepath.Join(dir, entry.Name())); err != nil {
							return err
						}
					}
				}
				return nil
			})
		}},
		{name: "current_prefix_probe", run: func() error { cache.cleanup(); return nil }},
	} {
		b.Run(variant.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := variant.run(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// legacyFreshCacheRead retains the HEAD removeExpired read/decode path for
// fresh entries; no deletion is possible in this fixture.
func legacyFreshCacheRead(c *urlCache, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(file, c.maxBytes+1))
	if err != nil || int64(len(data)) > c.maxBytes {
		return err
	}
	var envelope urlCacheEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if envelope.Version != urlCacheVersion || envelope.StoredAt.IsZero() || time.Since(envelope.StoredAt) <= c.ttl {
		return nil
	}
	return fmt.Errorf("unexpected expired fixture: %s", path)
}
