package jinn

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// contentStats holds the encoding/line-ending/BOM detected from a file sample.
type contentStats struct {
	encoding   string
	lineEnding string
	bom        string
}

// detectContentStats inspects up to the first 8 KB of data to classify encoding,
// line ending and BOM. Empty data yields the defaults (utf-8 / lf / none).
func detectContentStats(data []byte) contentStats {
	cs := contentStats{encoding: "utf-8", lineEnding: "lf", bom: "none"}
	if len(data) == 0 {
		return cs
	}
	sample := data
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	switch {
	case bytes.HasPrefix(sample, []byte{0xEF, 0xBB, 0xBF}):
		cs.bom = "utf-8-bom"
	case bytes.HasPrefix(sample, []byte{0xFF, 0xFE}):
		cs.bom = "utf-16-le"
	case bytes.HasPrefix(sample, []byte{0xFE, 0xFF}):
		cs.bom = "utf-16-be"
	}
	crlfCount := bytes.Count(sample, []byte{'\r', '\n'})
	lfCount := bytes.Count(sample, []byte{'\n'}) - crlfCount
	if crlfCount > 0 && lfCount == 0 {
		cs.lineEnding = "crlf"
	} else if crlfCount > 0 && lfCount > 0 {
		cs.lineEnding = "mixed"
	}
	if !utf8.Valid(sample) {
		cs.encoding = "binary"
	}
	return cs
}

// countDataLines returns the number of lines in data (a final non-newline-
// terminated line still counts as a line). Empty data yields 0.
func countDataLines(data []byte) int {
	lines := strings.Count(string(data), "\n")
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	return lines
}

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

	var data []byte
	if info.Mode().IsRegular() && info.Size() <= maxFileSize {
		if d, err := os.ReadFile(resolved); err == nil {
			data = d
		}
	}
	lines := countDataLines(data)
	cs := detectContentStats(data)

	result := fmt.Sprintf("path: %s\ntype: %s\nsize: %d bytes\nlines: %d\nmodified: %s",
		path, ftype, info.Size(), lines, info.ModTime().Format(time.RFC3339))
	if info.Mode().IsRegular() {
		result += fmt.Sprintf("\nencoding: %s\nline_ending: %s\nbom: %s", cs.encoding, cs.lineEnding, cs.bom)
	}
	return result, nil
}
