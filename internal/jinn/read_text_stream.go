package jinn

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// scanReadLines visits source lines without retaining skipped lines. A trailing
// newline terminates its line rather than creating an extra empty line.
//
//nolint:gocognit,gocyclo,revive // branchy by design; splitting would scatter one sequential algorithm

// cursorRow is one retained source line with its absolute byte offset.
// text holds at most readMaxBytes bytes: for an overlong line it is the
// capped prefix and exceeded reports that more bytes follow.
type cursorRow struct {
	start    int64
	text     string
	exceeded bool
}

// readCandidate is the pre-scan retention span. A rolling span keeps only
// the last rolling lines (tail-anchored windows, whose end is the file end
// and which never need a line cursor).
type readCandidate struct {
	start, end int
	rolling    int
}

func windowCandidateSpan(args map[string]interface{}) (start, end int, callerSet bool) {
	start = 1
	if s, ok := args["start_line"].(float64); ok && int(s) >= 1 {
		start = int(s)
		callerSet = true
	}
	if el, ok := args["end_line"].(float64); ok && int(el) >= start {
		end = int(el)
		callerSet = true
	} else {
		end = start + readDefaultLines - 1
	}
	return start, end, callerSet
}

func readCandidateSpan(args map[string]interface{}, mode string) readCandidate {
	if t, ok := args["tail"].(float64); ok && int(t) > 0 {
		return readCandidate{rolling: int(t)}
	}
	start, end, callerSet := windowCandidateSpan(args)
	if mode == "tail" && !callerSet {
		return readCandidate{rolling: readDefaultLines}
	}
	return readCandidate{start: start, end: end}
}

// cursorScanResult is the outcome of the single streaming pass: every byte
// is hashed by the tee, every line counted, only the candidate span
// retained. offsets maps 1-based line numbers to absolute byte offsets of
// line starts within the retained span plus the first line after it.
type cursorScanResult struct {
	total   int
	rows    map[int]cursorRow
	offsets map[int]int64
}

// scanCursorStream consumes the reader once, tracking line starts as
// absolute byte offsets and retaining capped row text only within the
// candidate span (or the last rolling lines).
//
//nolint:gocognit,gocyclo,revive // branchy by design; splitting would scatter one sequential algorithm
func scanCursorStream(r io.Reader, cand readCandidate) (cursorScanResult, error) {
	var res cursorScanResult
	res.rows = map[int]cursorRow{}
	res.offsets = map[int]int64{}
	reader := bufio.NewReaderSize(r, 32<<10)
	var line strings.Builder
	var ring []int
	var ringRows map[int]cursorRow
	if cand.rolling > 0 {
		ringRows = map[int]cursorRow{}
	}
	var lineStart int64
	var lineLen int
	var absOffset int64
	count := 0
	pending := false
	emit := func() {
		n := count
		text := line.String()
		if cand.rolling > 0 {
			ringRows[n] = cursorRow{start: lineStart, text: text, exceeded: lineLen > readMaxBytes}
			ring = append(ring, n)
			for len(ring) > cand.rolling {
				delete(ringRows, ring[0])
				ring = ring[1:]
			}
		} else {
			if n >= cand.start && n <= cand.end+1 {
				res.offsets[n] = lineStart
			}
			if n >= cand.start && n <= cand.end {
				res.rows[n] = cursorRow{start: lineStart, text: text, exceeded: lineLen > readMaxBytes}
			}
		}
		line.Reset()
	}
scanLoop:
	for {
		fragment, rerr := reader.ReadSlice('\n')
		//nolint:nestif // nested guards express one validation/state ladder
		if len(fragment) != 0 {
			content := fragment
			if rerr == nil {
				// The fragment closing the line carries the terminator;
				// row text never includes it (lineLen counts only content).
				content = fragment[:len(fragment)-1]
			}
			n := count + 1
			kept := cand.rolling > 0 || (n >= cand.start && n <= cand.end)
			if kept && lineLen < readMaxBytes {
				take := content
				if rem := readMaxBytes - lineLen; rem < len(take) {
					take = take[:rem]
				}
				_, _ = line.Write(take)
			}
			lineLen += len(content)
			absOffset += int64(len(fragment))
			pending = true
		}
		switch {
		case rerr == nil:
			count++
			emit()
			lineStart = absOffset
			lineLen = 0
			pending = false
		case errors.Is(rerr, io.EOF):
			if pending {
				count++
				emit()
			}
			break scanLoop
		case errors.Is(rerr, bufio.ErrBufferFull):
		default:
			return res, rerr
		}
	}
	res.total = count
	if cand.rolling > 0 {
		res.rows = ringRows
	}
	return res, nil
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

//nolint:gocognit,gocyclo,revive,funlen // branchy by design; splitting would scatter one sequential algorithm
func (e *Engine) readTextWindow(resolved string, args map[string]interface{}, needChecksum bool) (*readContentResult, error) {
	file, info, err := e.openRegularFile(resolved, maxFileSize, true)
	if err != nil {
		if os.IsPermission(err) {
			return nil, permissionDeniedErr(resolved)
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()

	// The full-file digest is computed only when requested: one streaming
	// pass hashes every byte while counting lines. Retained memory stays
	// independent of input size (window rows plus a capped current line).
	var source io.Reader = file
	var hasher hash.Hash
	if needChecksum {
		hasher = sha256.New()
		source = io.TeeReader(file, hasher)
	}
	reader := bufio.NewReaderSize(source, 32<<10)
	sample, sampleErr := reader.Peek(8192)
	if sampleErr != nil && !errors.Is(sampleErr, io.EOF) {
		return nil, sampleErr
	}
	checksum := func() string {
		if hasher == nil {
			return ""
		}
		return hex.EncodeToString(hasher.Sum(nil))
	}
	ext := strings.ToLower(filepath.Ext(resolved))
	detected := http.DetectContentType(sample)
	if i := strings.Index(detected, ";"); i != -1 {
		detected = strings.TrimSpace(detected[:i])
	}
	if detected == "application/pdf" || ext == ".pdf" || isBinaryContent(sample) {
		if _, copyErr := io.Copy(io.Discard, reader); copyErr != nil {
			return nil, copyErr
		}
		var rejection *ErrWithSuggestion
		if detected == "application/pdf" || ext == ".pdf" {
			rejection = &ErrWithSuggestion{Err: errors.New("pdf extraction not supported in zero-dep mode"), Suggestion: "convert the PDF to text first (pdftotext, pdftk, or a cloud OCR service) and read the text file", Code: ErrCodeBinaryFile}
		} else {
			rejection = &ErrWithSuggestion{Err: fmt.Errorf("binary file: %d bytes", info.Size()), Suggestion: "use stat_file for metadata or skip content reads", Code: ErrCodeBinaryFile}
		}
		return &readContentResult{Checksum: checksum()}, rejection
	}

	mode, err := parseTruncateMode(args)
	if err != nil {
		return nil, err
	}
	scan, err := scanCursorStream(reader, readCandidateSpan(args, mode))
	if err != nil { //nolint:staticcheck // scanCursorStream returns a real error for unreadable input; SA4023 misfires on the call
		return nil, err
	}
	if stableErr := stableReadFile(file, info, resolved); stableErr != nil {
		return &readContentResult{Checksum: checksum()}, stableErr
	}
	e.tracker.record(resolved, info.ModTime(), info.Size())
	total := scan.total
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

	// Gather the resolved window from the retained rows.
	window := make([]string, 0, max(0, min(end-start+1, total)))
	for n := start; n <= end; n++ {
		row, ok := scan.rows[n]
		if !ok {
			continue
		}
		window = append(window, row.text)
	}
	if len(window) == 0 {
		return &readContentResult{Checksum: checksum()}, nil
	}

	// An overlong first line is consumed in bounded memory and reported
	// through the cursor: the prefix is the content and NextColumn says
	// how many bytes of the line were already shown.
	if first, ok := scan.rows[start]; ok && first.exceeded {
		prefix := utf8Prefix(first.text)
		res := &readContentResult{
			Content:     prefix,
			TotalLines:  total,
			OutputLines: 1,
			Truncated:   true,
			NextLine:    start,
			NextOffset:  first.start,
			NextColumn:  int64(len(prefix)),
			Checksum:    checksum(),
		}
		return res, nil
	}

	tr := applyTruncateStrategy(renderSelectedWindow(window, start, end, lineNumbers), mode, ext)
	sequential := mode == "head" || mode == "none" || mode == "smart"
	if res := byteTruncateResult(byteTruncateParams{content: tr.Content, startLine: start, total: total, sequential: sequential, spill: noSpill, offsets: scan.offsets}); res != nil {
		res.Checksum = checksum()
		return res, nil
	}
	result := assembleReadResult(readWindow{startLine: start, endLine: end, total: total}, tr, mode, noSpill, scan.offsets)
	result.Checksum = checksum()
	return result, nil
}

// noSpill is the cursor-era continuation: resume is digest-backed and the
// result carries NextOffset/NextColumn instead of a spilled remainder file,
// so spill never produces a path.
func noSpill(next int) (string, error) {
	return "", nil
}

// utf8Prefix trims a capped byte prefix to a valid UTF-8 boundary.
func utf8Prefix(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// Keep the original renderWindow alignment and trailing-newline behavior.
func renderSelectedWindow(lines []string, start, end int, numbered bool) string {
	width := len(strconv.Itoa(end))
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
