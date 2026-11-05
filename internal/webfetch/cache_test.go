package webfetch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sampleCachedDocument() Document {
	return Document{
		URL:         "https://example.com/article",
		FinalURL:    "https://example.com/article?final=1",
		StatusCode:  200,
		ContentType: "text/markdown",
		Title:       "Cached Article",
		Description: "A cached document.",
		Domain:      "example.com",
		Language:    "en",
		Author:      "Ada Lovelace",
		WordCount:   3,
		Extractor:   "defuddle",
		Source:      ReaderModeDefuddle,
		Content:     "# Cached Article\n\nCached content.\n",
	}
}

func TestNewURLCacheDisablesZeroTTL(t *testing.T) {
	t.Parallel()
	cacheDir := filepath.Join(t.TempDir(), "cache")
	if cache := newURLCache(0, cacheDir, 1024); cache != nil {
		t.Fatalf("newURLCache(0) = %+v, want nil", cache)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("cache directory state = %v, want absent", err)
	}
}

func TestDefaultURLCacheDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	got, err := defaultURLCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(os.Getenv("XDG_CACHE_HOME"), "jinn", "web", "urls"); got != want {
		t.Fatalf("XDG cache directory = %q, want %q", got, want)
	}

	t.Setenv("XDG_CACHE_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err = defaultURLCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".cache", "jinn", "web", "urls"); got != want {
		t.Fatalf("fallback cache directory = %q, want %q", got, want)
	}
}

func TestURLCacheRoundTripAndIdentity(t *testing.T) {
	t.Parallel()
	cache := newURLCache(time.Hour, filepath.Join(t.TempDir(), "cache"), 1024)
	doc := sampleCachedDocument()
	cache.store(doc.URL, ReaderModeDefuddle, "https://reader.example", doc)

	got, ok := cache.load(doc.URL, ReaderModeDefuddle, "https://reader.example")
	if !ok || got.Content != doc.Content || got.Title != doc.Title || got.Source != doc.Source {
		t.Fatalf("cache load = (%+v, %v), want document", got, ok)
	}
	if _, ok := cache.load(doc.URL, ReaderModeJina, "https://reader.example"); ok {
		t.Fatal("reader mode collision returned a cache hit")
	}
	if cacheFileName(doc.URL, ReaderModeDefuddle, "https://reader.example") == cacheFileName(doc.URL, ReaderModeJina, "https://reader.example") {
		t.Fatal("reader modes produced the same cache key")
	}
	cache.store(doc.URL, ReaderModeDefuddle, "https://reader.example", doc, RenderModeAlways, RenderWaitLoad)
	if _, ok := cache.load(doc.URL, ReaderModeDefuddle, "https://reader.example", RenderModeNever, RenderWaitLoad); !ok {
		t.Fatal("static cache entry was not retained alongside rendered identity")
	}
	if _, ok := cache.load(doc.URL, ReaderModeDefuddle, "https://reader.example", RenderModeAlways, RenderWaitNetworkIdle); ok {
		t.Fatal("render wait modes collided in cache")
	}
	if cacheFileName(doc.URL, ReaderModeDefuddle, "https://reader.example", RenderModeNever, RenderWaitLoad) == cacheFileName(doc.URL, ReaderModeDefuddle, "https://reader.example", RenderModeAlways, RenderWaitLoad) {
		t.Fatal("static and rendered requests produced the same cache key")
	}
}

func TestURLCacheRejectsStaleCorruptAndOversizedEntries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "stale",
			data: func() []byte {
				data, _ := json.Marshal(urlCacheEnvelope{
					Version:  urlCacheVersion,
					StoredAt: time.Now().Add(-2 * time.Hour),
					URL:      "https://example.com",
					Reader:   ReaderModeJina,
					Endpoint: "https://reader.example",
					Document: Document{URL: "https://example.com", Content: "stale"},
				})
				return data
			}(),
		},
		{name: "corrupt", data: []byte("not json")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cache := newURLCache(time.Hour, filepath.Join(t.TempDir(), "cache"), 32)
			if err := os.MkdirAll(cache.dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(cache.dir, cacheFileName("https://example.com", ReaderModeJina, "https://reader.example"))
			if err := os.WriteFile(path, tt.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, ok := cache.load("https://example.com", ReaderModeJina, "https://reader.example"); ok {
				t.Fatalf("%s entry returned a cache hit", tt.name)
			}
		})
	}

	cache := newURLCache(time.Hour, filepath.Join(t.TempDir(), "cache"), 32)
	if err := os.MkdirAll(cache.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cache.dir, cacheFileName("https://example.com", ReaderModeJina, "https://reader.example"))
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(cache.maxBytes)+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.load("https://example.com", ReaderModeJina, "https://reader.example"); ok {
		t.Fatal("oversized entry returned a cache hit")
	}
}

func TestURLCacheStoreIsAtomicAndPermissionRestricted(t *testing.T) {
	t.Parallel()
	cacheDir := filepath.Join(t.TempDir(), "cache")
	cache := newURLCache(time.Hour, cacheDir, 1024)
	doc := sampleCachedDocument()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cache.store(doc.URL, ReaderModeDefuddle, "https://reader.example", doc)

	dirInfo, err := os.Stat(cacheDir)
	if err != nil {
		t.Fatalf("cache directory: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("cache directory mode = %o, want 700", dirInfo.Mode().Perm())
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("read cache directory: %v", err)
	}
	if len(entries) != 2 || entries[0].Name() != ".cache.lock" || !canonicalCacheName(entries[1].Name()) {
		t.Fatalf("cache entries = %+v, want one final file and cache lock", entries)
	}
	fileInfo, err := os.Stat(filepath.Join(cacheDir, entries[1].Name()))
	if err != nil {
		t.Fatalf("cache file: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("cache file mode = %o, want 600", fileInfo.Mode().Perm())
	}
}

func TestServiceFetchUsesURLCache(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	readerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Cached\n\nProvider content\n"))
	}))
	t.Cleanup(readerServer.Close)

	service := NewService(Config{
		AllowPrivateNetworks: true,
		ReaderEndpoint:       readerServer.URL,
		URLCacheTTL:          time.Hour,
		URLCacheDir:          filepath.Join(t.TempDir(), "cache"),
	})
	first, err := service.Fetch(context.Background(), FetchRequest{URL: "http://example.com/article"})
	if err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	second, err := service.Fetch(context.Background(), FetchRequest{URL: "http://example.com/article"})
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if first.Content != second.Content || first.Title != second.Title {
		t.Fatalf("cached documents differ: first=%+v second=%+v", first, second)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("reader requests = %d, want one", got)
	}
}

func TestCachedFinalURLRespectsCallerPolicy(t *testing.T) {
	t.Parallel()
	const target = "https://example.com/article"
	const endpoint = "https://reader.example"
	cacheDir := filepath.Join(t.TempDir(), "cache")
	seed := NewService(Config{AllowPrivateNetworks: true, ReaderEndpoint: endpoint, URLCacheTTL: time.Hour, URLCacheDir: cacheDir})
	private := sampleCachedDocument()
	private.URL = target
	private.FinalURL = "http://127.0.0.1/private"
	private.Content = "private body"
	seed.cache.store(target, ReaderModeDefuddle, endpoint, private)

	permissive := NewService(Config{AllowPrivateNetworks: true, ReaderEndpoint: endpoint, URLCacheTTL: time.Hour, URLCacheDir: cacheDir})
	got, err := permissive.Fetch(context.Background(), FetchRequest{URL: target, Reader: ReaderModeDefuddle})
	if err != nil || got.Content != private.Content {
		t.Fatalf("permissive cache hit = (%+v, %v)", got, err)
	}

	strict := NewService(Config{ReaderEndpoint: endpoint, URLCacheTTL: time.Hour, URLCacheDir: cacheDir})
	calls := 0
	strict.client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       io.NopCloser(strings.NewReader("<html><body><p>public content</p></body></html>")),
			Request:    req,
		}, nil
	})
	// Service.client and Service.reader share the same *http.Client, so the
	// transport injected above is already visible to the reader.
	got, err = strict.Fetch(context.Background(), FetchRequest{URL: target, Reader: ReaderModeDefuddle})
	if err != nil || got.Content == private.Content || calls != 1 {
		t.Fatalf("strict fetch = (%+v, %v), network calls = %d", got, err, calls)
	}
	got, err = strict.Fetch(context.Background(), FetchRequest{URL: target, Reader: ReaderModeDefuddle})
	if err != nil || calls != 1 || got.Content == private.Content {
		t.Fatalf("strict public cache hit = (%+v, %v), network calls = %d", got, err, calls)
	}

	// A legacy or malformed entry without a final URL must also miss.
	missing := private
	missing.FinalURL = ""
	strict.cache.store(target, ReaderModeDefuddle, endpoint, missing)
	_, err = strict.Fetch(context.Background(), FetchRequest{URL: target, Reader: ReaderModeDefuddle})
	if err != nil || calls != 2 {
		t.Fatalf("missing final URL: error = %v, network calls = %d", err, calls)
	}
}

func TestServiceFetchRawBypassesURLCache(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	rawServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("raw content"))
	}))
	t.Cleanup(rawServer.Close)

	service := NewService(Config{
		AllowPrivateNetworks: true,
		URLCacheTTL:          time.Hour,
		URLCacheDir:          filepath.Join(t.TempDir(), "cache"),
	})
	for range 2 {
		if _, err := service.Fetch(context.Background(), FetchRequest{URL: rawServer.URL, Raw: true}); err != nil {
			t.Fatalf("raw Fetch: %v", err)
		}
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("raw requests = %d, want two", got)
	}
}

func TestServiceFetchCacheWriteFailureFailsOpen(t *testing.T) {
	t.Parallel()
	readerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Successful\n\nProvider content\n"))
	}))
	t.Cleanup(readerServer.Close)

	cachePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(cachePath, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewService(Config{
		AllowPrivateNetworks: true,
		ReaderEndpoint:       readerServer.URL,
		URLCacheTTL:          time.Hour,
		URLCacheDir:          cachePath,
	})
	if _, err := service.Fetch(context.Background(), FetchRequest{URL: "http://example.com/article"}); err != nil {
		t.Fatalf("Fetch with unwritable cache: %v", err)
	}
}
