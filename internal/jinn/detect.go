package jinn

import (
	"net/http"
	"os"
	"strings"
)

// isBinaryContent reports whether data contains a NUL byte. Callers cap the
// slice to the window they want to inspect (512 for read, 8192 for search/replace).
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

// imageMIME runs http.DetectContentType, strips any "; charset=..." suffix,
// and reports the clean MIME plus whether it is an image/* type.
func imageMIME(data []byte) (string, bool) {
	detected := http.DetectContentType(data)
	if i := strings.Index(detected, ";"); i != -1 {
		detected = strings.TrimSpace(detected[:i])
	}
	return detected, strings.HasPrefix(detected, "image/")
}
