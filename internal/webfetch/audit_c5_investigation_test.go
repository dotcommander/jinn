package webfetch

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditC5R01ExpiredEntryIsRemoved(t *testing.T) {
	t.Parallel()
	cache := newURLCache(time.Hour, t.TempDir(), 1024)
	t.Cleanup(cache.close)
	doc := sampleCachedDocument()
	cache.store(doc.URL, ReaderModeDefuddle, "", doc)
	path := filepath.Join(cache.dir, cacheFileName(doc.URL, ReaderModeDefuddle, ""))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var envelope urlCacheEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.StoredAt = time.Now().Add(-2 * time.Hour)
	data, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.load(doc.URL, ReaderModeDefuddle, ""); ok {
		t.Fatal("expired entry was served")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired file retained: %v", err)
	}
}

func TestAuditC5R02StaticAndRenderedHTTPStatusAgree(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			body := `<html><head><title>Upstream error</title></head><body><article><h1>Upstream error</h1><p>` + strings.Repeat("Controlled error response content. ", 30) + `</p></article></body></html>`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			service := NewService(Config{AllowPrivateNetworks: true})
			t.Cleanup(func() { _ = service.Close(t.Context()) })
			_, err := service.Fetch(t.Context(), FetchRequest{URL: server.URL, Reader: ReaderModeDefuddle, Render: RenderModeNever})
			var httpErr *HTTPError
			if !errors.As(err, &httpErr) || httpErr.StatusCode != status || httpErr.Body == "" {
				t.Fatalf("static error = %v", err)
			}
			service.renderer = &fakePageRenderer{page: renderedPage{HTML: body, FinalURL: server.URL, StatusCode: status, ContentType: "text/html"}}
			_, err = service.Fetch(t.Context(), FetchRequest{URL: server.URL, Reader: ReaderModeDefuddle, Render: RenderModeAlways})
			httpErr = nil
			if !errors.As(err, &httpErr) || httpErr.StatusCode != status || httpErr.Body == "" || len(httpErr.Body) > maxErrorBody+len("...") || httpErr.URL != server.URL {
				t.Fatalf("rendered error = %v", err)
			}
			service.renderer = &fakePageRenderer{page: renderedPage{FinalURL: server.URL, StatusCode: status}}
			_, err = service.Fetch(t.Context(), FetchRequest{URL: server.URL, Reader: ReaderModeDefuddle, Render: RenderModeAlways})
			httpErr = nil
			if !errors.As(err, &httpErr) || httpErr.StatusCode != status || httpErr.Body != "" {
				t.Fatalf("empty rendered error = %v", err)
			}
		})
	}
}
