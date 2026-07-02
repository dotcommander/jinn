package jinn

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxFileSize      = 50 << 20  // 50 MB absolute file limit
	readDefaultLines = 2000      // default read window when no start_line/end_line given; distinct knob from readTruncLines (window size, not collapse point). tunable: config candidate
	readMaxBytes     = 50 * 1024 // 50 KB output cap per chunk. tunable: config candidate
	readTruncLines   = 2000      // head+tail collapse threshold; distinct knob from readDefaultLines (when to collapse output, not how much to read)
)

// readContentResult holds the output of readFileContent.
// The caller is responsible for wrapping this into a ToolResult.
type readContentResult struct {
	Content     string // processed, line-numbered (and possibly truncated) text
	TotalLines  int    // total lines in the source file
	OutputLines int    // lines actually included in Content
	Truncated   bool   // true if content was truncated in any way
	ByteHint    string // truncation hint appended after Content (byte or window)
	TempFile    string // path to spilled remainder file, if any
	Checksum    string // SHA-256 checksum of full file bytes when requested
}

// readFileContent reads and processes a file's text content. It handles stat
// checks, reading, PDF/binary detection, line splitting, windowing, and
// truncation. The caller is responsible for sandbox validation (checkPath),
// image detection, checksum computation, and ToolResult wrapping.
func (e *Engine) readFileContent(resolved string, args map[string]interface{}, needChecksum bool) (*readContentResult, error) {
	info, err := statForRead(resolved)
	if err != nil {
		return nil, err
	}

	data, checksum, err := e.readAndClassify(resolved, info, needChecksum)
	if err != nil {
		return &readContentResult{Checksum: checksum}, err
	}

	ext := strings.ToLower(filepath.Ext(resolved))

	truncateMode, err := parseTruncateMode(args)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	total := len(lines)
	if lines[total-1] == "" {
		total--
	}
	if total == 0 {
		return &readContentResult{
			Content:     "",
			TotalLines:  0,
			OutputLines: 0,
		}, nil
	}

	startLine, endLine, err := resolveReadWindow(args, truncateMode, total)
	if err != nil {
		return nil, err
	}

	lineNumbers := true
	if v, ok := args["line_numbers"].(bool); ok {
		lineNumbers = v
	}

	rawContent := renderWindow(lines, startLine, endLine, lineNumbers)

	// Single oversized line guard: if the first source line exceeds the byte cap,
	// the byte-cap loop below would keep nothing. Return a hint instead.
	if res := oversizedLineResult(resolved, lines, startLine, total); res != nil {
		res.Checksum = checksum
		return res, nil
	}

	// Apply truncation strategy if windowed chunk exceeds the line limit.
	tr := applyTruncateStrategy(rawContent, truncateMode, ext)

	// Apply byte-size truncation: if the numbered output exceeds 50KB,
	// keep the head portion that fits and write the full remainder to a
	// temp file so the agent can pick up where it left off.
	if res := byteTruncateResult(tr.Content, resolved, lines, startLine, total); res != nil {
		res.Checksum = checksum
		return res, nil
	}

	result := assembleReadResult(resolved, lines, readWindow{startLine: startLine, endLine: endLine, total: total}, tr)
	result.Checksum = checksum
	return result, nil
}

// statForRead stats resolved and verifies it is a readable, regular file
// within the size cap. Errors carry suggestions for the caller to surface.
func statForRead(resolved string) (os.FileInfo, error) {
	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("file not found: %s", resolved),
				Suggestion: "verify the path exists with list_dir on the parent, or check for typos",
				Code:       ErrCodeFileNotFound,
			}
		}
		if os.IsPermission(err) {
			return nil, permissionDeniedErr(resolved)
		}
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("not a regular file: %s", resolved),
			Suggestion: "target a regular file, not a directory — use list_dir to enumerate entries",
			Code:       ErrCodeInvalidArgs,
		}
	}
	if info.Size() > maxFileSize {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("file too large: %d MB (max 50 MB)", info.Size()>>20),
			Suggestion: "file is too large to read in one shot; use start_line/end_line to window, or search_files for a pattern",
			Code:       ErrCodeFileTooLarge,
		}
	}
	return info, nil
}

// readFileForOp reads resolved (the sandbox-resolved path) and maps the common
// filesystem errors onto the canonical jinn errors, using path (the display
// path) in messages. Not-found and permission-denied share their suggestion
// text with statForRead and the other readable-file guards.
func readFileForOp(path, resolved string) ([]byte, error) {
	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("file not found: %s", path),
				Suggestion: "verify the path exists with list_dir on the parent, or check for typos",
				Code:       ErrCodeFileNotFound,
			}
		}
		if os.IsPermission(err) {
			return nil, permissionDeniedErr(path)
		}
		return nil, err
	}
	return data, nil
}

// readAndClassify reads the file, records it in the tracker, and rejects PDF
// and binary content before any text processing.
func (e *Engine) readAndClassify(resolved string, info os.FileInfo, needChecksum bool) ([]byte, string, error) {
	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsPermission(err) {
			return nil, "", permissionDeniedErr(resolved)
		}
		return nil, "", err
	}

	e.tracker.record(resolved, info.ModTime(), info.Size())

	ext := strings.ToLower(filepath.Ext(resolved))

	// Content-based detection: DetectContentType handles <512 bytes automatically.
	detected := http.DetectContentType(data)
	// Strip "; charset=..." suffix for a clean MIME.
	if i := strings.Index(detected, ";"); i != -1 {
		detected = strings.TrimSpace(detected[:i])
	}

	checksum := ""
	if needChecksum {
		h := sha256.Sum256(data)
		checksum = hex.EncodeToString(h[:])
	}

	// PDF: reject before binary checks — pdftotext is a better tool.
	// Either the content detector or the extension is sufficient evidence.
	if detected == "application/pdf" || ext == ".pdf" {
		return nil, checksum, &ErrWithSuggestion{
			Err:        errors.New("pdf extraction not supported in zero-dep mode"),
			Suggestion: "convert the PDF to text first (pdftotext, pdftk, or a cloud OCR service) and read the text file",
			Code:       ErrCodeBinaryFile,
		}
	}

	// Binary detection: NUL byte in first 8KB (matches search/replace window).
	check := data
	if len(check) > 8192 {
		check = check[:8192]
	}
	// Binary detection: return an error so the caller can decide how to present it.
	if isBinaryContent(check) {
		return nil, checksum, &ErrWithSuggestion{
			Err:        fmt.Errorf("binary file: %d bytes", len(data)),
			Suggestion: "use stat_file for metadata or skip content reads",
			Code:       ErrCodeBinaryFile,
		}
	}
	return data, checksum, nil
}
