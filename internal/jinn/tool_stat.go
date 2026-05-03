package jinn

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

func (e *Engine) statFile(args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	resolved, err := e.checkPath(path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &ErrWithSuggestion{
				Err:        fmt.Errorf("file not found: %s", path),
				Suggestion: "verify the path exists with list_dir or check for typos",
				Code:       ErrCodeFileNotFound,
			}
		}
		return "", err
	}

	ftype := "file"
	if info.IsDir() {
		ftype = "directory"
	} else if !info.Mode().IsRegular() {
		ftype = "special"
	}

	lines := 0
	var data []byte
	if info.Mode().IsRegular() && info.Size() <= maxFileSize {
		if d, err := os.ReadFile(resolved); err == nil {
			data = d
			lines = strings.Count(string(data), "\n")
			if len(data) > 0 && data[len(data)-1] != '\n' {
				lines++
			}
		}
	}

	encoding := "utf-8"
	lineEnding := "lf"
	bom := "none"
	if len(data) > 0 {
		sample := data
		if len(sample) > 8192 {
			sample = sample[:8192]
		}
		// BOM detection
		if bytes.HasPrefix(sample, []byte{0xEF, 0xBB, 0xBF}) {
			bom = "utf-8-bom"
		} else if bytes.HasPrefix(sample, []byte{0xFF, 0xFE}) {
			bom = "utf-16-le"
		} else if bytes.HasPrefix(sample, []byte{0xFE, 0xFF}) {
			bom = "utf-16-be"
		}
		// Line ending detection
		crlfCount := bytes.Count(sample, []byte{'\r', '\n'})
		lfCount := bytes.Count(sample, []byte{'\n'}) - crlfCount
		if crlfCount > 0 && lfCount == 0 {
			lineEnding = "crlf"
		} else if crlfCount > 0 && lfCount > 0 {
			lineEnding = "mixed"
		}
		// UTF-8 validity
		if !utf8.Valid(sample) {
			encoding = "binary"
		}
	}

	result := fmt.Sprintf("path: %s\ntype: %s\nsize: %d bytes\nlines: %d\nmodified: %s",
		path, ftype, info.Size(), lines, info.ModTime().Format(time.RFC3339))
	if info.Mode().IsRegular() {
		result += fmt.Sprintf("\nencoding: %s\nline_ending: %s\nbom: %s", encoding, lineEnding, bom)
	}
	return result, nil
}
