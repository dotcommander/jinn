package jinn

import (
	"fmt"
	"strconv"
	"strings"
)

// readWindow holds the resolved 1-based line window and the file's total lines.
type readWindow struct {
	startLine int
	endLine   int
	total     int
}

// oversizedLineResult returns a hint result when the first windowed source line
// exceeds the byte cap (the byte-cap loop would otherwise keep nothing), or nil.
func oversizedLineResult(resolved string, lines []string, startLine, total int) *readContentResult {
	if startLine > total || len(lines[startLine-1]) <= readMaxBytes {
		return nil
	}
	srcLineLen := len(lines[startLine-1])
	return &readContentResult{
		Content: fmt.Sprintf(
			"[Line %d is %d KB, exceeds 50 KB limit. Use run_shell: sed -n '%dp' %s | head -c 50000]",
			startLine, srcLineLen/1024, startLine, resolved,
		),
		TotalLines:  total,
		OutputLines: 1,
	}
}

// assembleReadResult builds the final readContentResult, attaching a
// continuation hint and remainder temp file when the window or the truncation
// strategy dropped lines.
func assembleReadResult(resolved string, lines []string, w readWindow, tr truncateResult) *readContentResult {
	// Determine if the file itself is longer than the window requested.
	windowTruncated := w.total > w.endLine

	var hint string
	var tmpPath string
	if windowTruncated {
		// Write remainder to temp file for seamless continuation.
		tmpPath, _ = writeTruncationRemainder(resolved, w.endLine+1, lines[w.endLine:w.total])
		hint = buildReadHint(w.startLine, w.endLine, w.total, w.endLine+1, tmpPath)
	}

	// Build truncation metadata for callers.
	// Set when either the window didn't cover the whole file, or head+tail
	// collapse happened within the windowed chunk.
	if windowTruncated || tr.Truncated {
		totalShown := tr.ShownLines
		if !tr.Truncated {
			totalShown = w.endLine - w.startLine + 1
		}
		return &readContentResult{
			Content:     tr.Content,
			TotalLines:  w.total,
			OutputLines: totalShown,
			Truncated:   true,
			ByteHint:    hint,
			TempFile:    tmpPath,
		}
	}

	return &readContentResult{
		Content:     tr.Content,
		TotalLines:  w.total,
		OutputLines: w.total,
	}
}

// parseTruncateMode reads and validates the truncate strategy, defaulting to
// "head". "tail" mode later shifts the default window to the file's end.
func parseTruncateMode(args map[string]interface{}) (string, error) {
	truncateMode, _ := args["truncate"].(string)
	if truncateMode == "" {
		truncateMode = "head"
	}
	switch truncateMode {
	case "head", "tail", "middle", "none", "smart":
		return truncateMode, nil
	default:
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("invalid truncate value %q", truncateMode),
			Suggestion: `valid values are "head" (default), "tail", "middle", "none", "smart"`,
			Code:       ErrCodeInvalidArgs,
		}
	}
}

// resolveReadWindow computes the inclusive 1-based [startLine, endLine] window
// from the tail / start_line / end_line / truncate args against total lines.
func resolveReadWindow(args map[string]interface{}, truncateMode string, total int) (startLine, endLine int, err error) {
	tail := 0
	if t, ok := args["tail"].(float64); ok && int(t) > 0 {
		tail = int(t)
	}

	if tail > 0 {
		// Explicit tail= arg (number of lines from end) takes precedence.
		startLine = total - tail + 1
		if startLine < 1 {
			startLine = 1
		}
		endLine = total
	} else {
		startLine, endLine = explicitOrDefaultWindow(args, truncateMode, total)
	}

	if startLine > total {
		return 0, 0, &ErrWithSuggestion{
			Err:        fmt.Errorf("file has %d lines, start_line %d is past end", total, startLine),
			Suggestion: fmt.Sprintf("requested window starts beyond file length (%d lines); reduce start_line", total),
			Code:       ErrCodeInvalidArgs,
		}
	}
	if endLine > total {
		endLine = total
	}
	return startLine, endLine, nil
}

// explicitOrDefaultWindow computes the window from start_line/end_line. When
// truncate="tail" and the caller set no window, it pins to the file's end so
// truncateOutputTail receives the last readDefaultLines lines.
func explicitOrDefaultWindow(args map[string]interface{}, truncateMode string, total int) (startLine, endLine int) {
	startLine = 1
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
	return startLine, endLine
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

// applyTruncateStrategy collapses the windowed chunk per the chosen mode.
func applyTruncateStrategy(rawContent, truncateMode, ext string) truncateResult {
	var tr truncateResult
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
	return tr
}
