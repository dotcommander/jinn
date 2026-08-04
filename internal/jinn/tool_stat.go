package jinn

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

type contentStats struct {
	encoding   string
	lineEnding string
	bom        string
}

//nolint:gocyclo // content encoding and line-ending classification is a bounded decision table.
func detectContentStats(data []byte) contentStats {
	stats := contentStats{encoding: "utf-8", lineEnding: "lf", bom: "none"}
	if len(data) == 0 {
		return stats
	}
	sample := data
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	switch {
	case bytes.HasPrefix(sample, []byte{0xEF, 0xBB, 0xBF}):
		stats.bom = "utf-8-bom"
	case bytes.HasPrefix(sample, []byte{0xFF, 0xFE}):
		stats.bom = "utf-16-le"
	case bytes.HasPrefix(sample, []byte{0xFE, 0xFF}):
		stats.bom = "utf-16-be"
	}
	crlf := bytes.Count(sample, []byte{'\r', '\n'})
	lf := bytes.Count(sample, []byte{'\n'}) - crlf
	cr := bytes.Count(sample, []byte{'\r'}) - crlf
	switch {
	case crlf > 0 && lf == 0 && cr == 0:
		stats.lineEnding = "crlf"
	case cr > 0 && crlf == 0 && lf == 0:
		stats.lineEnding = "cr"
	case (crlf > 0 && (lf > 0 || cr > 0)) || (lf > 0 && cr > 0):
		stats.lineEnding = "mixed"
	}
	if !utf8.Valid(sample) {
		stats.encoding = "binary"
	}
	return stats
}

func countDataLines(data []byte) int {
	lines := strings.Count(string(data), "\n")
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	return lines
}

//nolint:revive // sampling intentionally shares the stat response construction flow.
func (e *Engine) statFile(args map[string]interface{}) (string, error) {
	path := strArg(args, "path")
	resolved, err := e.checkPath(path)
	if err != nil {
		return "", err
	}
	info, err := e.rootedStat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &ErrWithSuggestion{Err: fmt.Errorf("file not found: %s", path), Suggestion: "verify the path exists with list_dir or check for typos", Code: ErrCodeFileNotFound}
		}
		return "", err
	}
	fileType := "file"
	if info.IsDir() {
		fileType = "directory"
	} else if !info.Mode().IsRegular() {
		fileType = "special"
	}
	result := map[string]any{
		"path": path, "type": fileType, "size": info.Size(),
		"modified": info.ModTime().Format(time.RFC3339Nano),
	}
	summary := fmt.Sprintf("type: %s\nsize: %d\nmodified: %s", fileType, info.Size(), info.ModTime().Format(time.RFC3339Nano))
	//nolint:nestif // regular-file sampling intentionally keeps all result fields in one ownership block.
	if info.Mode().IsRegular() {
		rel, relErr := e.rootRelative(resolved)
		if relErr != nil {
			return "", relErr
		}
		file, openErr := e.root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if openErr != nil {
			result["sample_error"] = openErr.Error()
		} else {
			defer func() { _ = file.Close() }()
			data := make([]byte, 8192)
			n, readErr := file.Read(data)
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				result["sample_error"] = readErr.Error()
			} else {
				data = data[:n]
				stats := detectContentStats(data)
				result["sample_bytes"] = n
				result["sample_complete"] = int64(n) == info.Size()
				result["sample_lines"] = countDataLines(data)
				result["encoding"] = stats.encoding
				result["line_ending"] = stats.lineEnding
				result["bom"] = stats.bom
				if int64(n) == info.Size() {
					result["lines"] = countDataLines(data)
				}
				summary += fmt.Sprintf("\nlines: %d\nencoding: %s\nline_ending: %s\nbom: %s", countDataLines(data), stats.encoding, stats.lineEnding, stats.bom)
			}
		}
	}
	result["summary"] = summary
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("stat_file: marshal: %w", err)
	}
	return string(encoded), nil
}
