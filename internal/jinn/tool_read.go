package jinn

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	maxFileSize      = 50 << 20  // 50 MB absolute file limit
	readDefaultLines = 2000      // default window when no start_line/end_line given
	readMaxBytes     = 50 * 1024 // 50 KB output cap per chunk
	readTruncLines   = 2000      // head+tail collapse threshold
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
}

// readFileContent reads and processes a file's text content. It handles stat
// checks, reading, PDF/binary detection, line splitting, windowing, and
// truncation. The caller is responsible for sandbox validation (checkPath),
// image detection, checksum computation, and ToolResult wrapping.
func (e *Engine) readFileContent(resolved string, args map[string]interface{}) (*readContentResult, error) {
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
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("permission denied: %s", resolved),
				Suggestion: "file is not readable by the sandbox; check ownership or choose a different file",
				Code:       ErrCodePermissionDenied,
			}
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

	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsPermission(err) {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("permission denied: %s", resolved),
				Suggestion: "file is not readable by the sandbox; check ownership or choose a different file",
				Code:       ErrCodePermissionDenied,
			}
		}
		return nil, err
	}

	e.tracker.record(resolved, info.ModTime())

	ext := strings.ToLower(filepath.Ext(resolved))

	// Content-based detection: DetectContentType handles <512 bytes automatically.
	detected := http.DetectContentType(data)
	// Strip "; charset=..." suffix for a clean MIME.
	if i := strings.Index(detected, ";"); i != -1 {
		detected = strings.TrimSpace(detected[:i])
	}

	// PDF: reject before binary checks — pdftotext is a better tool.
	// Either the content detector or the extension is sufficient evidence.
	if detected == "application/pdf" || ext == ".pdf" {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("pdf extraction not supported in zero-dep mode"),
			Suggestion: "convert the PDF to text first (pdftotext, pdftk, or a cloud OCR service) and read the text file",
			Code:       ErrCodeBinaryFile,
		}
	}

	check := data
	if len(check) > 512 {
		check = check[:512]
	}
	// Binary detection: return an error so the caller can decide how to present it.
	if strings.ContainsRune(string(check), 0) {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("binary file: %d bytes", len(data)),
			Suggestion: "use stat_file for metadata or skip content reads",
			Code:       ErrCodeBinaryFile,
		}
	}

	tail := 0
	if t, ok := args["tail"].(float64); ok && int(t) > 0 {
		tail = int(t)
	}

	// Parse truncate strategy early — "tail" mode shifts the default window to
	// the end of the file so the truncation function sees the final lines.
	truncateMode, _ := args["truncate"].(string)
	if truncateMode == "" {
		truncateMode = "head"
	}
	switch truncateMode {
	case "head", "tail", "middle", "none", "smart":
		// valid
	default:
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("invalid truncate value %q", truncateMode),
			Suggestion: `valid values are "head" (default), "tail", "middle", "none", "smart"`,
			Code:       ErrCodeInvalidArgs,
		}
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

	// Explicit tail= arg (number of lines from end) takes precedence.
	startLine := 1
	endLine := startLine + readDefaultLines - 1
	if tail > 0 {
		startLine = total - tail + 1
		if startLine < 1 {
			startLine = 1
		}
		endLine = total
	} else {
		// When truncate="tail" and no explicit window, pin window to end so
		// truncateOutputTail receives the last readDefaultLines lines.
		callerSetWindow := false
		if s, ok := args["start_line"].(float64); ok && int(s) >= 1 {
			startLine = int(s)
			callerSetWindow = true
		}
		if el, ok := args["end_line"].(float64); ok && int(el) >= startLine {
			endLine = int(el)
			callerSetWindow = true
		} else {
			endLine = startLine + readDefaultLines - 1
		}
		if truncateMode == "tail" && !callerSetWindow && total > readDefaultLines {
			startLine = total - readDefaultLines + 1
			endLine = total
		}
	}

	if startLine > total {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("file has %d lines, start_line %d is past end", total, startLine),
			Suggestion: fmt.Sprintf("requested window starts beyond file length (%d lines); reduce start_line", total),
			Code:       ErrCodeInvalidArgs,
		}
	}
	if endLine > total {
		endLine = total
	}

	lineNumbers := true
	if v, ok := args["line_numbers"].(bool); ok {
		lineNumbers = v
	}

	width := len(strconv.Itoa(endLine))
	var b strings.Builder
	for i := startLine - 1; i < endLine && i < len(lines); i++ {
		if lineNumbers {
			fmt.Fprintf(&b, "%*d\t%s\n", width, i+1, lines[i])
		} else {
			fmt.Fprintf(&b, "%s\n", lines[i])
		}
	}

	// Single oversized line guard: if the first source line exceeds the byte cap,
	// the byte-cap loop below would keep nothing. Return a hint instead.
	if startLine <= total && len(lines[startLine-1]) > readMaxBytes {
		srcLineLen := len(lines[startLine-1])
		return &readContentResult{
			Content: fmt.Sprintf(
				"[Line %d is %d KB, exceeds 50 KB limit. Use run_shell: sed -n '%dp' %s | head -c 50000]",
				startLine, srcLineLen/1024, startLine, resolved,
			),
			TotalLines:  total,
			OutputLines: 1,
		}, nil
	}

	// Apply truncation strategy if windowed chunk exceeds the line limit.
	rawContent := strings.TrimRight(b.String(), "\n")
	var tr struct {
		Content    string
		Truncated  bool
		TotalLines int
		ShownLines int
	}
	switch truncateMode {
	case "head":
		tr = truncateOutputHead(rawContent, readTruncLines)
	case "tail":
		tr = truncateOutputTail(rawContent, readTruncLines)
	case "middle":
		tr = truncateOutputDetailed(rawContent, readTruncLines)
	case "smart":
		tr = truncateOutputSmart(rawContent, readTruncLines, ext)
	case "none":
		tr.Content = rawContent
		lines2 := strings.Split(rawContent, "\n")
		tr.TotalLines = len(lines2)
		tr.ShownLines = len(lines2)
	}

	// Apply byte-size truncation: if the numbered output exceeds 50KB,
	// keep the head portion that fits and write the full remainder to a
	// temp file so the agent can pick up where it left off.
	if res := byteTruncateResult(tr.Content, resolved, lines, startLine, total); res != nil {
		return res, nil
	}

	// Determine if the file itself is longer than the window requested.
	windowTruncated := total > endLine

	content := tr.Content
	var hint string
	var tmpPath string
	if windowTruncated {
		// Write remainder to temp file for seamless continuation.
		tmpPath, _ = writeTruncationRemainder(resolved, endLine+1, lines[endLine:total])
		hint = fmt.Sprintf("\n[Showing lines %d-%d of %d. Use start_line=%d to continue.",
			startLine, endLine, total, endLine+1)
		if tmpPath != "" {
			hint += fmt.Sprintf(" Remainder saved to %s.", tmpPath)
		}
		hint += "]"
	}

	// Build truncation metadata for callers.
	// Set when either the window didn't cover the whole file, or head+tail
	// collapse happened within the windowed chunk.
	if windowTruncated || tr.Truncated {
		totalShown := tr.ShownLines
		if !tr.Truncated {
			totalShown = endLine - startLine + 1
		}
		return &readContentResult{
			Content:     content,
			TotalLines:  total,
			OutputLines: totalShown,
			Truncated:   true,
			ByteHint:    hint,
			TempFile:    tmpPath,
		}, nil
	}

	return &readContentResult{
		Content:     content,
		TotalLines:  total,
		OutputLines: total,
	}, nil
}
