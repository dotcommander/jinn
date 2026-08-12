package jinn

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// byteTruncateResult applies byte-size truncation. Sequential strategies also
// receive an exact source continuation; reordered strategies receive only the
// bounded content and a narrower-window hint.
func byteTruncateResult(content, resolved string, lines []string, startLine, total int, sequential bool) *readContentResult {
	if len(content) <= readMaxBytes {
		return nil
	}

	outLines := strings.Split(content, "\n")
	if len(outLines) > 0 && outLines[len(outLines)-1] == "" {
		outLines = outLines[:len(outLines)-1]
	}
	var kept []string
	keptBytes := 0
	for _, l := range outLines {
		extra := len(l) + 1 // line + newline
		if keptBytes+extra > readMaxBytes {
			break
		}
		kept = append(kept, l)
		keptBytes += extra
	}

	nextLine := 0
	tmpPath := ""
	if sequential {
		nextLine = startLine + len(kept)
		var srcRemainder []string
		for i := nextLine - 1; i < total && i < len(lines); i++ {
			srcRemainder = append(srcRemainder, lines[i])
		}
		tmpPath, _ = writeTruncationRemainder(resolved, nextLine, srcRemainder)
	}
	hint := "\n[Output truncated by byte limit. Re-run with a narrower window.]"
	if sequential {
		hint = buildReadHint(startLine, startLine+len(kept)-1, total, nextLine, tmpPath)
	}

	return &readContentResult{
		Content:     strings.Join(kept, "\n"),
		TotalLines:  total,
		OutputLines: len(kept),
		Truncated:   true,
		ByteHint:    hint,
		TempFile:    tmpPath,
		NextLine:    nextLine,
	}
}

// buildReadHint formats the windowed-read continuation hint.
func buildReadHint(startLine, endLine, total, nextLine int, tmpPath string) string {
	hint := fmt.Sprintf("\n[Showing lines %d-%d of %d.", startLine, endLine, total)
	if nextLine > 0 {
		hint += fmt.Sprintf(" Use start_line=%d to continue.", nextLine)
	}
	if tmpPath != "" {
		hint += fmt.Sprintf(" Remainder saved to %s.", tmpPath)
	}
	hint += "]"
	return hint
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
