package webfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLooksLikeHTML(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"<!DOCTYPE html><html>":      true,
		"  <html lang=en>":           true,
		"\n\n<head><title>x</title>": true,
		"<body>hi":                   true,
		"<?xml version=\"1.0\"?>":    true,
		"plain text":                 false,
		"":                           false,
		"{\"json\":1}":               false,
	}
	for body, want := range cases {
		if got := looksLikeHTML([]byte(body)); got != want {
			t.Fatalf("looksLikeHTML(%q) = %v, want %v", body, got, want)
		}
	}
}

func TestFetchDefuddleSniffsMislabeledHTML(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("<!DOCTYPE html><html><head><title>Hi</title></head><body><p>Hello sniffed</p></body></html>"))
	}))
	t.Cleanup(server.Close)

	service := NewService(Config{ReaderMode: ReaderModeDefuddle, AllowPrivateNetworks: true})
	doc, err := service.Fetch(context.Background(), FetchRequest{URL: server.URL + "/page"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(doc.Content, "Hello sniffed") {
		t.Fatalf("content = %q, want sniffed HTML extracted", doc.Content)
	}
}

func TestFetchDefuddleStillRejectsOpaqueBytes(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte{0, 1, 2, 3})
	}))
	t.Cleanup(server.Close)

	service := NewService(Config{ReaderMode: ReaderModeDefuddle, AllowPrivateNetworks: true})
	_, err := service.Fetch(context.Background(), FetchRequest{URL: server.URL + "/blob"})
	if err == nil {
		t.Fatal("Fetch succeeded, want not_html for opaque bytes")
	}
}

func TestRewriteGitHubBlobURL(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"https://github.com/owner/repo/blob/main/file.go?raw=true":         "https://raw.githubusercontent.com/owner/repo/main/file.go",
		"https://www.github.com/owner/repo/raw/branch/dir/nested.txt#frag": "https://raw.githubusercontent.com/owner/repo/branch/dir/nested.txt",
		"https://github.com/owner/repo/issues/1":                           "https://github.com/owner/repo/issues/1",
		"https://example.com/owner/repo/blob/main/file.go":                 "https://example.com/owner/repo/blob/main/file.go",
	}
	for raw, want := range cases {
		u, err := validateTarget(raw, false)
		if err != nil {
			t.Fatalf("validateTarget(%q): %v", raw, err)
		}
		if u.String() != want {
			t.Fatalf("validateTarget(%q) = %q, want %q", raw, u.String(), want)
		}
	}
}

func TestSanitizeText(t *testing.T) {
	t.Parallel()
	input := "Hello\x1b[31mred\x1b[0m \u202Eevil\tworld\nnext\x00\x07line"
	got := SanitizeText(input)
	if got != "Hellored evil\tworld\nnextline" {
		t.Fatalf("SanitizeText = %q, want stripped text preserving tab/newline", got)
	}
	if strings.Contains(got, "\x1b") || strings.ContainsRune(got, '\u202E') {
		t.Fatalf("SanitizeText left control/bidi characters: %q", got)
	}
}
