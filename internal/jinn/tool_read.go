package jinn

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
	lines, info, checksum, err := e.streamTextLines(resolved, needChecksum)
	if err != nil {
		return &readContentResult{Checksum: checksum}, err
	}
	e.tracker.record(resolved, info.ModTime(), info.Size())

	ext := strings.ToLower(filepath.Ext(resolved))

	truncateMode, err := parseTruncateMode(args)
	if err != nil {
		return nil, err
	}

	total := len(lines)
	if total == 0 {
		return &readContentResult{
			Content:     "",
			TotalLines:  0,
			OutputLines: 0,
			Checksum:    checksum,
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
func (e *Engine) statForRead(resolved string) (os.FileInfo, error) {
	var info os.FileInfo
	var err error
	if _, relErr := e.rootRelative(resolved); relErr == nil {
		info, err = e.rootedStat(resolved)
	} else {
		info, err = os.Stat(resolved)
	}
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

// streamTextLines computes the checksum and content classification in a bounded
// streaming pass, then seeks the same descriptor to collect source lines. It
// avoids holding both the full byte slice and a whole-file strings.Split copy.
//
//nolint:funlen,gocognit,gocyclo,revive // streaming checksum, MIME gates, and line collection must share one bounded descriptor and result shape.
func (e *Engine) streamTextLines(resolved string, needChecksum bool) ([]string, os.FileInfo, string, error) {
	file, info, err := e.openRegularFile(resolved, maxFileSize, true)
	if err != nil {
		if os.IsPermission(err) {
			return nil, info, "", permissionDeniedErr(resolved)
		}
		return nil, info, "", err
	}
	defer func() { _ = file.Close() }()

	hasher := sha256.New()
	buffer := make([]byte, 32<<10)
	sample := make([]byte, 0, 8192)
	for {
		n, readErr := file.Read(buffer)
		//nolint:nestif // bounded sampling must be updated alongside the checksum for each read chunk.
		if n > 0 {
			chunk := buffer[:n]
			if needChecksum {
				_, _ = hasher.Write(chunk)
			}
			if len(sample) < cap(sample) {
				remaining := cap(sample) - len(sample)
				if remaining > n {
					remaining = n
				}
				sample = append(sample, chunk[:remaining]...)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, info, "", readErr
		}
	}
	checksum := ""
	if needChecksum {
		checksum = hex.EncodeToString(hasher.Sum(nil))
	}
	ext := strings.ToLower(filepath.Ext(resolved))
	detected := http.DetectContentType(sample)
	// Strip "; charset=..." suffix for a clean MIME.
	if i := strings.Index(detected, ";"); i != -1 {
		detected = strings.TrimSpace(detected[:i])
	}

	// PDF: reject before binary checks — pdftotext is a better tool.
	// Either the content detector or the extension is sufficient evidence.
	if detected == "application/pdf" || ext == ".pdf" {
		return nil, info, checksum, &ErrWithSuggestion{
			Err:        errors.New("pdf extraction not supported in zero-dep mode"),
			Suggestion: "convert the PDF to text first (pdftotext, pdftk, or a cloud OCR service) and read the text file",
			Code:       ErrCodeBinaryFile,
		}
	}

	// Binary detection: NUL byte in first 8KB (matches search/replace window).
	// Binary detection: return an error so the caller can decide how to present it.
	if isBinaryContent(sample) {
		return nil, info, checksum, &ErrWithSuggestion{
			Err:        fmt.Errorf("binary file: %d bytes", info.Size()),
			Suggestion: "use stat_file for metadata or skip content reads",
			Code:       ErrCodeBinaryFile,
		}
	}
	if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
		return nil, info, checksum, seekErr
	}
	reader := bufio.NewReaderSize(file, 32<<10)
	lines := make([]string, 0, min(int(info.Size()/40)+1, readDefaultLines))
	for {
		line, readErr := reader.ReadString('\n')
		if strings.HasSuffix(line, "\n") {
			line = strings.TrimSuffix(line, "\n")
			lines = append(lines, line)
		} else if line != "" {
			lines = append(lines, line)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, info, checksum, readErr
		}
	}
	finalInfo, err := file.Stat()
	if err != nil {
		return nil, info, checksum, err
	}
	if finalInfo.Size() != info.Size() || !finalInfo.ModTime().Equal(info.ModTime()) {
		return nil, finalInfo, checksum, &ErrWithSuggestion{Err: fmt.Errorf("file changed while reading: %s", resolved), Suggestion: "retry the read against stable bytes", Code: ErrCodeStaleFile}
	}
	return lines, finalInfo, checksum, nil
}
