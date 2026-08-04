package jinn

import (
	"fmt"
	"net/url"
	"path/filepath"
)

func pathToURI(abs string) string {
	return (&url.URL{Scheme: "file", Path: filepath.Clean(abs)}).String()
}

func fileURIPath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" || u.Host != "" || u.Opaque != "" || u.Path == "" {
		return "", fmt.Errorf("unsupported file URI: %s", raw)
	}
	return filepath.Clean(u.Path), nil
}
