package jinn

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// byteTruncateResult applies byte-size truncation. Sequential strategies also
// receive an exact source continuation; reordered strategies receive only the
// bounded content and a narrower-window hint.
func byteTruncateResult(content string, startLine, total int, sequential bool, spill func(int) (string, error)) *readContentResult {
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
		tmpPath, _ = spill(nextLine)
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

// writeStreamReadRemainder streams numbered source lines from an already
// rewound descriptor, without collecting the omitted file in memory.
func writeStreamReadRemainder(srcPath string, startLine, total int, source io.Reader) (path string, err error) {
	file, err := os.CreateTemp("", spillFilePrefix+filepath.Base(srcPath)+".*.txt")
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(file.Name())
			path = ""
		}
	}()
	width := len(strconv.Itoa(total))
	_, err = scanReadLines(source, func(n int) bool { return n >= startLine }, func(n int, line string) error {
		_, werr := fmt.Fprintf(file, "%*d\t%s\n", width, n, line)
		return werr
	})
	if err != nil {
		return "", err
	}
	registerShellSpill(file.Name())
	return file.Name(), nil
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
