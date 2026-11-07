package webfetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestFetchImageIfPresentReturnsSupportedImage(t *testing.T) {
	t.Parallel()
	body := []byte("fake-png-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	service := NewService(Config{AllowPrivateNetworks: true})
	target, err := url.Parse(server.URL + "/chart.png")
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	doc, ok, err := service.fetchImageIfPresent(context.Background(), target)
	if err != nil {
		t.Fatalf("fetchImageIfPresent: %v", err)
	}
	if !ok || doc.Source != sourceImage || doc.ImageMimeType != "image/png" || string(doc.ImageBytes) != string(body) {
		t.Fatalf("doc = %#v, ok=%v", doc, ok)
	}
}

func TestFetchRawReturnsExtensionlessImage(t *testing.T) {
	t.Parallel()
	body := []byte("fake-jpeg-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	service := NewService(Config{AllowPrivateNetworks: true})
	doc, err := service.Fetch(context.Background(), FetchRequest{URL: server.URL + "/download?id=42", Raw: true})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if doc.Source != sourceImage || doc.ImageMimeType != "image/jpeg" || string(doc.ImageBytes) != string(body) {
		t.Fatalf("doc = %#v", doc)
	}
}

func TestFetchImageIfPresentRejectsDeclaredOversize(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.FormatInt(DefaultMaxImageBytes+1, 10))
		_, _ = w.Write([]byte("small body, declared length is what matters"))
	}))
	t.Cleanup(server.Close)

	service := NewService(Config{AllowPrivateNetworks: true})
	target, err := url.Parse(server.URL + "/huge.jpg")
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	_, ok, err := service.fetchImageIfPresent(context.Background(), target)
	if ok {
		t.Fatal("oversize image reported present")
	}
	var coded *CodedError
	if err == nil || !errors.As(err, &coded) || coded.Code != ErrorCodeResponseTooLarge {
		t.Fatalf("err = %#v, coded = %#v, want response_too_large", err, coded)
	}
	if !strings.Contains(coded.Error(), "exceeding") {
		t.Fatalf("error message = %q", coded.Error())
	}
}

func TestFetchImageIfPresentIgnoresNonImage(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>page</body></html>"))
	}))
	t.Cleanup(server.Close)

	service := NewService(Config{AllowPrivateNetworks: true})
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	if _, ok, err := service.fetchImageIfPresent(context.Background(), target); ok || err != nil {
		t.Fatalf("fetchImageIfPresent = ok:%v err:%v, want not present", ok, err)
	}
}
