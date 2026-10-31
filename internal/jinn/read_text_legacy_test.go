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
	"strconv"
	"strings"
)

// Test-only baseline: the single-pass whole-file reader before window streaming.
// streamTextLines samples content before collecting all source lines in one pass.
// When requested, the checksum covers every byte, including rejected PDFs and
// binary files, without holding a whole-file byte slice.
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
	var source io.Reader = file
	if needChecksum {
		source = io.TeeReader(file, hasher)
	}
	reader := bufio.NewReaderSize(source, 32<<10)
	sample, sampleErr := reader.Peek(8192)
	if sampleErr != nil && !errors.Is(sampleErr, io.EOF) {
		return nil, info, "", sampleErr
	}
	checksum := func() string {
		if needChecksum {
			return hex.EncodeToString(hasher.Sum(nil))
		}
		return ""
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
		if needChecksum {
			if _, err := io.Copy(io.Discard, reader); err != nil {
				return nil, info, "", err
			}
		}
		return nil, info, checksum(), &ErrWithSuggestion{
			Err:        errors.New("pdf extraction not supported in zero-dep mode"),
			Suggestion: "convert the PDF to text first (pdftotext, pdftk, or a cloud OCR service) and read the text file",
			Code:       ErrCodeBinaryFile,
		}
	}

	// Binary detection: NUL byte in first 8KB (matches search/replace window).
	// Binary detection: return an error so the caller can decide how to present it.
	if isBinaryContent(sample) {
		if needChecksum {
			if _, err := io.Copy(io.Discard, reader); err != nil {
				return nil, info, "", err
			}
		}
		return nil, info, checksum(), &ErrWithSuggestion{
			Err:        fmt.Errorf("binary file: %d bytes", info.Size()),
			Suggestion: "use stat_file for metadata or skip content reads",
			Code:       ErrCodeBinaryFile,
		}
	}
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
			return nil, info, "", readErr
		}
	}
	finalInfo, err := file.Stat()
	if err != nil {
		return nil, info, checksum(), err
	}
	if finalInfo.Size() != info.Size() || !finalInfo.ModTime().Equal(info.ModTime()) {
		return nil, finalInfo, checksum(), &ErrWithSuggestion{Err: fmt.Errorf("file changed while reading: %s", resolved), Suggestion: "retry the read against stable bytes", Code: ErrCodeStaleFile}
	}
	return lines, finalInfo, checksum(), nil
}

// renderWindow formats lines[startLine-1:endLine] with optional line numbers
// and returns the content with the trailing newline trimmed.
func renderWindow(lines []string, startLine, endLine int, lineNumbers bool) string {
	width := len(strconv.Itoa(endLine))
	var b strings.Builder
	for i := startLine - 1; i < endLine && i < len(lines); i++ {
		if lineNumbers {
			fmt.Fprintf(&b, "%*d\t%s\n", width, i+1, lines[i])
		} else {
			fmt.Fprintf(&b, "%s\n", lines[i])
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeTruncationRemainder writes the lines from startLine onward to a temp file
// and returns the temp file path. Lines are written with line numbers. The temp
// file is registered with the spill registry so a follow-up read_file can read
// the exact path without widening the sandbox. Errors are swallowed by callers:
// the agent always has the start_line continuation fallback.
func writeTruncationRemainder(srcPath string, startLine int, remainderLines []string) (path string, err error) {
	if len(remainderLines) == 0 {
		return "", nil
	}
	base := filepath.Base(srcPath)
	tmpFile, err := os.CreateTemp("", spillFilePrefix+base+".*.txt")
	if err != nil {
		return "", err
	}
	// Close-before-return: a flush failure means the spill file is truncated, so
	// surface it via the named return (unless an earlier error already won).
	defer func() {
		if cerr := tmpFile.Close(); cerr != nil && err == nil {
			path, err = "", fmt.Errorf("close: %w", cerr)
		}
	}()

	endLine := startLine + len(remainderLines) - 1
	width := len(strconv.Itoa(endLine))
	for i, line := range remainderLines {
		if _, ferr := fmt.Fprintf(tmpFile, "%*d\t%s\n", width, startLine+i, line); ferr != nil {
			return "", fmt.Errorf("write spill: %w", ferr)
		}
	}

	registerShellSpill(tmpFile.Name())
	return tmpFile.Name(), nil
}
