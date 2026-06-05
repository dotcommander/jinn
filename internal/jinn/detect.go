package jinn

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// isBinaryContent reports whether data contains a NUL byte. Callers cap the
// slice to the window they want to inspect (8192 for both read and search/replace).
func isBinaryContent(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

// peekFileBytes opens path and returns up to n bytes from the start.
// Returns nil (and the open error) if the file cannot be opened; a short read
// is not an error.
func peekFileBytes(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, n)
	read, _ := f.Read(buf)
	return buf[:read], nil
}

// detectIsImage is the single source of truth for "is this path an image, and
// what MIME". It peeks the first 512 bytes for MIME sniffing via imageMIME, and
// falls back to the .svg extension (DetectContentType reports text/xml for SVG).
// On the .svg fallback path the returned mime is empty; callers default it.
func detectIsImage(resolved, path string) (mime string, isImage bool) {
	if data, err := peekFileBytes(resolved, 512); err == nil && len(data) > 0 {
		mime, isImage = imageMIME(data)
	}
	if !isImage && strings.EqualFold(filepath.Ext(path), ".svg") {
		isImage = true
	}
	return mime, isImage
}

// imageMIME runs http.DetectContentType, strips any "; charset=..." suffix,
// and reports the clean MIME plus whether it is an image/* type.
func imageMIME(data []byte) (string, bool) {
	detected := http.DetectContentType(data)
	if i := strings.Index(detected, ";"); i != -1 {
		detected = strings.TrimSpace(detected[:i])
	}
	return detected, strings.HasPrefix(detected, "image/")
}
