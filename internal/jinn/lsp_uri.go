package jinn

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

func pathToURI(abs string) string {
	// Drive and UNC forms identify Windows paths even in portable previews.
	if windowsDrivePath(abs) || strings.HasPrefix(abs, `\\`) {
		return pathToURIForOS(abs, "windows")
	}
	return pathToURIForOS(abs, runtime.GOOS)
}

func pathToURIForOS(abs, goos string) string {
	if goos == "windows" {
		value := strings.ReplaceAll(abs, `\`, "/")
		if strings.HasPrefix(value, "//") {
			host, rest, _ := strings.Cut(strings.TrimPrefix(value, "//"), "/")
			return (&url.URL{Scheme: "file", Host: host, Path: path.Clean("/" + rest)}).String()
		}
		if windowsDrivePath(value) {
			value = "/" + value
		}
		value = path.Clean(value)
		if len(value) == 3 && value[0] == '/' && value[2] == ':' {
			value += "/"
		}
		return (&url.URL{Scheme: "file", Path: value}).String()
	}
	return (&url.URL{Scheme: "file", Path: filepath.Clean(abs)}).String()
}

func fileURIPath(raw string) (string, error) {
	return fileURIPathForOS(raw, runtime.GOOS)
}

func fileURIPathForOS(raw, goos string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" || u.Opaque != "" || u.Path == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("unsupported file URI: %s", raw)
	}
	if goos == "windows" {
		value := path.Clean(u.Path)
		if u.Host != "" {
			if strings.ContainsAny(u.Host, ":\\") || !strings.HasPrefix(value, "/") || value == "/" {
				return "", fmt.Errorf("unsupported file URI: %s", raw)
			}
			return `\\` + u.Host + strings.ReplaceAll(value, "/", `\`), nil
		}
		value = strings.TrimPrefix(value, "/")
		if len(value) == 2 && value[1] == ':' {
			value += "/"
		}
		if !windowsDrivePath(value) {
			return "", fmt.Errorf("unsupported file URI: %s", raw)
		}
		return strings.ReplaceAll(value, "/", `\`), nil
	}
	if u.Host != "" || !strings.HasPrefix(u.Path, "/") {
		return "", fmt.Errorf("unsupported file URI: %s", raw)
	}
	return filepath.Clean(u.Path), nil
}

func windowsDrivePath(value string) bool {
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}
