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

// scanReadLines visits source lines without retaining skipped lines. A trailing
// newline terminates its line rather than creating an extra empty line.
func scanReadLines(r io.Reader, keep func(int) bool, visit func(int, string) error) (int, error) {
	reader := bufio.NewReaderSize(r, 32<<10)
	var line strings.Builder
	count := 0
	pending := false
	for {
		fragment, err := reader.ReadSlice('\n')
		if visit != nil && keep(count+1) && len(fragment) != 0 {
			if _, werr := line.Write(fragment); werr != nil {
				return count, werr
			}
		}
		if len(fragment) != 0 {
			pending = true
		}
		if err == nil {
			count++
			if visit != nil && keep(count) {
				text := line.String()
				if err := visit(count, text[:len(text)-1]); err != nil {
					return count, err
				}
			}
			line.Reset()
			pending = false
		} else if errors.Is(err, io.EOF) {
			if pending {
				count++
				if visit != nil && keep(count) {
					if err := visit(count, line.String()); err != nil {
						return count, err
					}
				}
			}
			return count, nil
		} else if !errors.Is(err, bufio.ErrBufferFull) {
			return count, err
		}
	}
}

func stableReadFile(file *os.File, initial os.FileInfo, resolved string) error {
	final, err := file.Stat()
	if err != nil {
		return err
	}
	if final.Size() != initial.Size() || !final.ModTime().Equal(initial.ModTime()) {
		return &ErrWithSuggestion{Err: fmt.Errorf("file changed while reading: %s", resolved), Suggestion: "retry the read against stable bytes", Code: ErrCodeStaleFile}
	}
	return nil
}

func (e *Engine) readTextWindow(resolved string, args map[string]interface{}, needChecksum bool) (*readContentResult, error) {
	file, info, err := e.openRegularFile(resolved, maxFileSize, true)
	if err != nil {
		if os.IsPermission(err) {
			return nil, permissionDeniedErr(resolved)
		}
		return nil, err
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
		return nil, sampleErr
	}
	checksum := func() string {
		if needChecksum {
			return hex.EncodeToString(hasher.Sum(nil))
		}
		return ""
	}
	ext := strings.ToLower(filepath.Ext(resolved))
	detected := http.DetectContentType(sample)
	if i := strings.Index(detected, ";"); i != -1 {
		detected = strings.TrimSpace(detected[:i])
	}
	if detected == "application/pdf" || ext == ".pdf" || isBinaryContent(sample) {
		if needChecksum {
			if _, err := io.Copy(io.Discard, reader); err != nil {
				return nil, err
			}
		}
		var rejection *ErrWithSuggestion
		if detected == "application/pdf" || ext == ".pdf" {
			rejection = &ErrWithSuggestion{Err: errors.New("pdf extraction not supported in zero-dep mode"), Suggestion: "convert the PDF to text first (pdftotext, pdftk, or a cloud OCR service) and read the text file", Code: ErrCodeBinaryFile}
		} else {
			rejection = &ErrWithSuggestion{Err: fmt.Errorf("binary file: %d bytes", info.Size()), Suggestion: "use stat_file for metadata or skip content reads", Code: ErrCodeBinaryFile}
		}
		return &readContentResult{Checksum: checksum()}, rejection
	}

	total, err := scanReadLines(reader, nil, nil)
	if err != nil {
		return nil, err
	}
	if err := stableReadFile(file, info, resolved); err != nil {
		return &readContentResult{Checksum: checksum()}, err
	}
	e.tracker.record(resolved, info.ModTime(), info.Size())
	mode, err := parseTruncateMode(args)
	if err != nil {
		return nil, err
	}
	if total == 0 {
		return &readContentResult{Checksum: checksum()}, nil
	}
	start, end, err := resolveReadWindow(args, mode, total)
	if err != nil {
		return nil, err
	}
	lineNumbers := true
	if value, ok := args["line_numbers"].(bool); ok {
		lineNumbers = value
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	// Explicit very large windows may require a large representation for middle,
	// tail and smart truncation; default and narrow windows retain only their slice.
	window := make([]string, 0, min(end-start+1, readDefaultLines))
	_, err = scanReadLines(file, func(n int) bool { return n >= start && n <= end }, func(_ int, line string) error {
		window = append(window, line)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := stableReadFile(file, info, resolved); err != nil {
		return nil, err
	}
	var spillErr error
	spill := func(next int) (string, error) {
		if next > total {
			return "", nil
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		path, err := writeStreamReadRemainder(resolved, next, total, file)
		if err != nil {
			return "", err
		}
		if err := stableReadFile(file, info, resolved); err != nil {
			_ = os.Remove(path)
			spillErr = err
			return "", err
		}
		return path, nil
	}
	if res := oversizedLineResult(resolved, window[0], start, total); res != nil {
		res.Checksum = checksum()
		return res, nil
	}
	tr := applyTruncateStrategy(renderSelectedWindow(window, start, end, lineNumbers), mode, ext)
	sequential := mode == "head" || mode == "none" || mode == "smart"
	if res := byteTruncateResult(tr.Content, start, total, sequential, spill); res != nil {
		if spillErr != nil {
			return nil, spillErr
		}
		res.Checksum = checksum()
		return res, nil
	}
	result := assembleReadResult(readWindow{startLine: start, endLine: end, total: total}, tr, mode, spill)
	if spillErr != nil {
		return nil, spillErr
	}
	result.Checksum = checksum()
	return result, nil
}

// Keep the original renderWindow alignment and trailing-newline behavior.
func renderSelectedWindow(lines []string, start, end int, numbered bool) string {
	width := len(fmt.Sprint(end))
	var b strings.Builder
	for i, line := range lines {
		if numbered {
			fmt.Fprintf(&b, "%*d\t%s\n", width, start+i, line)
		} else {
			fmt.Fprintf(&b, "%s\n", line)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
