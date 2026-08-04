package jinn

import (
	"net/http"
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

// detectIsImage is the single source of truth for "is this path an image, and
// what MIME". It peeks the first 512 bytes for MIME sniffing via imageMIME, and
// falls back to the .svg extension (DetectContentType reports text/xml for SVG).
// On the .svg fallback path the returned mime is empty; callers default it.
func (e *Engine) detectIsImage(resolved, path string) (mime string, isImage bool) {
	data, _, err := e.readRegularPrefix(resolved, 512)
	if err == nil && len(data) > 0 {
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
