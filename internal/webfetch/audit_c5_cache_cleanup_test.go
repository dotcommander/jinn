package webfetch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeExpiredAuditEntry(t *testing.T, cache *urlCache, index int) string {
	t.Helper()
	doc := sampleCachedDocument()
	doc.URL = fmt.Sprintf("https://example.com/%d", index)
	envelope := urlCacheEnvelope{Version: urlCacheVersion, StoredAt: time.Now().Add(-2 * cache.ttl), URL: doc.URL,
		Reader: ReaderModeDefuddle, Render: RenderModeNever, RenderWait: RenderWaitLoad, RenderPolicyVersion: renderPolicyVersion, Document: doc}
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cache.dir, cacheFileName(doc.URL, ReaderModeDefuddle, ""))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAuditC5R01BoundedSweepAdvancesAndCloses(t *testing.T) {
	t.Parallel()
	cache := newURLCache(time.Hour, t.TempDir(), 1024)
	t.Cleanup(cache.close)
	var paths []string
	for i := range 2 * cacheCleanupBatch {
		paths = append(paths, writeExpiredAuditEntry(t, cache, i))
	}
	cache.cleanup()
	removed := 0
	for _, path := range paths {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			removed++
		}
	}
	if removed == 0 || removed > cacheCleanupBatch {
		t.Fatalf("first sweep removed %d entries", removed)
	}
	cache.cleanup()
	cache.cleanup()
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expired entry retained: %s: %v", path, err)
		}
	}
	cache.close()
	if cache.sweepDir != nil {
		t.Fatal("cleanup directory handle retained")
	}
}

func TestCleanupFreshPrefixBoundedAndExpiredFallback(t *testing.T) {
	t.Parallel()
	cache := newURLCache(time.Hour, t.TempDir(), 128<<10)
	t.Cleanup(cache.close)
	doc := sampleCachedDocument()
	doc.Content = strings.Repeat("content", 10000)
	cache.store(doc.URL, ReaderModeDefuddle, "", doc)
	path := filepath.Join(cache.dir, cacheFileName(doc.URL, ReaderModeDefuddle, ""))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader := bytes.NewReader(data)
	if !freshCanonicalCachePrefix(reader, cache.ttl) {
		t.Fatal("fresh canonical entry did not match prefix")
	}
	if consumed := len(data) - reader.Len(); consumed != cacheCleanupProbeBytes || consumed >= len(data) {
		t.Fatalf("probe consumed %d of %d bytes", consumed, len(data))
	}
	cache.cleanup()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fresh entry removed: %v", err)
	}

	// A reordered, valid envelope still receives full validation and deletion.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	freshStoredAt := fields["stored_at"]
	fields["stored_at"], err = json.Marshal(time.Now().Add(-2 * cache.ttl))
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := json.Marshal(fields) // map keys sort; document precedes stored_at.
	if err != nil {
		t.Fatal(err)
	}
	if freshCanonicalCachePrefix(bytes.NewReader(reordered), cache.ttl) {
		t.Fatal("reordered expired entry matched fresh prefix")
	}
	if err := os.WriteFile(path, reordered, 0o600); err != nil {
		t.Fatal(err)
	}
	cache.cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired reordered entry retained: %v", err)
	}

	// A parseable expired prefix is not deletion authority if the document is corrupt.
	oldStamp := append([]byte(`"stored_at":`), freshStoredAt...)
	newStamp := append([]byte(`"stored_at":`), fields["stored_at"]...)
	if !bytes.Contains(data, oldStamp) {
		t.Fatal("canonical timestamp not found")
	}
	corrupt := append(bytes.Replace(data, oldStamp, newStamp, 1), '!')
	if freshCanonicalCachePrefix(bytes.NewReader(corrupt), cache.ttl) {
		t.Fatal("expired corrupt entry matched fresh prefix")
	}
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	cache.cleanup()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("corrupt expired entry removed: %v", err)
	}
}

func TestAuditC5R01FreshReplacementSurvivesStaleCleanup(t *testing.T) {
	t.Parallel()
	cache := newURLCache(time.Hour, t.TempDir(), 1024)
	t.Cleanup(cache.close)
	path := writeExpiredAuditEntry(t, cache, 0)
	// The scanner may have observed the path before another cache owner replaces it.
	other := newURLCache(time.Hour, cache.dir, 1024)
	t.Cleanup(other.close)
	doc := sampleCachedDocument()
	doc.URL = "https://example.com/0"
	doc.Content = "fresh replacement"
	other.store(doc.URL, ReaderModeDefuddle, "", doc)
	if err := withCacheLock(filepath.Join(cache.dir, ".cache.lock"), func() error { cache.removeExpired(path); return nil }); err != nil {
		t.Fatal(err)
	}
	got, ok := cache.load(doc.URL, ReaderModeDefuddle, "")
	if !ok || got.Content != doc.Content {
		t.Fatalf("fresh replacement removed: %+v, %v", got, ok)
	}
}
