package webfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchJinaQuotaFallsBackToDefuddle(t *testing.T) {
	t.Parallel()
	jina := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte("InsufficientBalanceError"))
	}))
	t.Cleanup(jina.Close)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Marker Title</title></head><body><article><h1>Marker Title</h1><p>Fallback body text.</p></article></body></html>`))
	}))
	t.Cleanup(target.Close)

	service := NewService(Config{
		AllowPrivateNetworks: true,
		ReaderEndpoint:       jina.URL,
		URLCacheDir:          t.TempDir(),
	})
	doc, err := service.Fetch(context.Background(), FetchRequest{URL: target.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if doc.Source != ReaderModeDefuddle {
		t.Fatalf("source = %q, want defuddle", doc.Source)
	}
	if !strings.Contains(doc.Content, "Fallback body text") {
		t.Fatalf("content = %q", doc.Content)
	}
	found := false
	for _, warning := range doc.Warnings {
		if strings.Contains(warning, "fell back to defuddle") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want fallback note", doc.Warnings)
	}
}

func TestFetchJinaServerErrorDoesNotFallBack(t *testing.T) {
	t.Parallel()
	jina := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(jina.Close)

	service := NewService(Config{
		AllowPrivateNetworks: true,
		ReaderEndpoint:       jina.URL,
		URLCacheDir:          t.TempDir(),
	})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("should not be fetched"))
	}))
	t.Cleanup(target.Close)

	_, err := service.Fetch(context.Background(), FetchRequest{URL: target.URL})
	if err == nil {
		t.Fatal("Fetch succeeded, want the Jina 500 error")
	}
}

func TestIsQuotaReaderError(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusTooManyRequests} {
		err := newCodedError(&HTTPError{StatusCode: status}, ErrorCodeUpstreamHTTP, "x")
		if !isQuotaReaderError(err) {
			t.Fatalf("status %d not classified as quota error", status)
		}
	}
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		err := newCodedError(&HTTPError{StatusCode: status}, ErrorCodeUpstreamHTTP, "x")
		if isQuotaReaderError(err) {
			t.Fatalf("status %d misclassified as quota error", status)
		}
	}
}
