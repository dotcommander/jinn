package jinn

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// multiReadMaxFiles is the maximum number of files accepted in a single call.
const multiReadMaxFiles = 20

// multiReadResult is the top-level JSON structure returned by multi_read.
type multiReadResult struct {
	Files      map[string]string         `json:"files"`
	Errors     map[string]multiReadError `json:"errors,omitempty"`
	Truncation map[string]truncationInfo `json:"truncation,omitempty"`
}

// multiReadError describes a per-file failure within a multi_read call.
type multiReadError struct {
	Error      string `json:"error"`
	Suggestion string `json:"suggestion,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
}

// TODO: global byte cap across all files (per-file 50KB cap from readFileContent is
// sufficient for v1; 20×50KB = 1MB max, within jinn's output capabilities).

func (e *Engine) multiRead(args map[string]interface{}) (*ToolResult, error) {
	// 1. Parse files array.
	rawFiles, ok := args["files"].([]interface{})
	if !ok || len(rawFiles) == 0 {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("files must be a non-empty array (1-%d items)", multiReadMaxFiles),
			Suggestion: "provide a 'files' array with 1-20 file request objects",
			Code:       ErrCodeInvalidArgs,
		}
	}
	if len(rawFiles) > multiReadMaxFiles {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("too many files requested: %d (max %d)", len(rawFiles), multiReadMaxFiles),
			Suggestion: fmt.Sprintf("split the request into batches of %d files", multiReadMaxFiles),
			Code:       ErrCodeInvalidArgs,
		}
	}

	result := multiReadResult{
		Files:      make(map[string]string, len(rawFiles)),
		Errors:     make(map[string]multiReadError),
		Truncation: make(map[string]truncationInfo),
	}

	for _, raw := range rawFiles {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		path, _ := entry["path"].(string)
		if path == "" {
			continue
		}

		// Build per-file args for readFileContent.
		perFileArgs := make(map[string]interface{})
		for _, key := range []string{"start_line", "end_line", "tail", "line_numbers", "truncate"} {
			if v, exists := entry[key]; exists {
				perFileArgs[key] = v
			}
		}

		// Sandbox validation.
		resolved, err := e.checkPath(path)
		if err != nil {
			result.Errors[path] = errToMultiRead(err)
			continue
		}

		// Image/binary detection via MIME sniff (same pattern as readFile).
		isImage := sniffIsImage(resolved)
		if !isImage && strings.HasSuffix(strings.ToLower(path), ".svg") {
			isImage = true
		}
		if isImage {
			result.Errors[path] = multiReadError{
				Error:      fmt.Sprintf("image file: %s", path),
				Suggestion: "use read_file for single-image viewing",
				ErrorCode:  ErrCodeBinaryFile,
			}
			continue
		}

		// Delegate to shared content reader.
		cr, err := e.readFileContent(resolved, perFileArgs)
		if err != nil {
			result.Errors[path] = errToMultiRead(err)
			continue
		}

		// Success: add content to files map.
		content := cr.Content
		if cr.ByteHint != "" {
			content += cr.ByteHint
		}
		result.Files[path] = content

		if cr.Truncated {
			result.Truncation[path] = truncationInfo{
				Truncated:   true,
				TotalLines:  cr.TotalLines,
				OutputLines: cr.OutputLines,
			}
		}
	}

	// If ALL files failed, return a top-level error.
	if len(result.Files) == 0 {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("all %d files failed", len(result.Errors)),
			Suggestion: "check file paths and permissions; use list_dir to verify files exist",
			Code:       ErrCodeInvalidArgs,
		}
	}

	// json.Marshal sorts map keys by default in Go, so output is deterministic.
	data, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal multi_read result: %w", err)
	}

	return textResult(string(data)), nil
}

// errToMultiRead converts an error (typically ErrWithSuggestion) to a multiReadError.
func errToMultiRead(err error) multiReadError {
	var sErr *ErrWithSuggestion
	if errors.As(err, &sErr) {
		return multiReadError{
			Error:      sErr.Err.Error(),
			Suggestion: sErr.Suggestion,
			ErrorCode:  sErr.Code,
		}
	}
	return multiReadError{
		Error: err.Error(),
	}
}

// sniffIsImage peeks at the first 512 bytes of a file to detect image MIME type.
func sniffIsImage(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	peek := make([]byte, 512)
	n, _ := f.Read(peek)
	if n == 0 {
		return false
	}
	detected := http.DetectContentType(peek[:n])
	return strings.HasPrefix(detected, "image/")
}
