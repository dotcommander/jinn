package jinn

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// byteTruncateResult applies byte-size truncation: if the numbered output
// exceeds the byte cap, it keeps the head portion that fits and writes the full
// remainder to a temp file so the agent can pick up where it left off. It
// returns the truncated readContentResult, or nil when the output is within the
// byte cap and no truncation is needed.
func byteTruncateResult(content, resolved string, lines []string, startLine, total int) *readContentResult {
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

	// Collect source lines beyond the kept output lines for the remainder.
	remainingStart := startLine + len(kept)
	var srcRemainder []string
	for i := remainingStart - 1; i < total && i < len(lines); i++ {
		srcRemainder = append(srcRemainder, lines[i])
	}
	tmpPath, _ := writeTruncationRemainder(resolved, remainingStart, srcRemainder)

	nextLine := startLine + len(kept)
	hint := fmt.Sprintf("\n[Showing lines %d-%d of %d. Use start_line=%d to continue.",
		startLine, startLine+len(kept)-1, total, nextLine)
	if tmpPath != "" {
		hint += fmt.Sprintf(" Remainder saved to %s.", tmpPath)
	}
	hint += "]"

	return &readContentResult{
		Content:     strings.Join(kept, "\n"),
		TotalLines:  total,
		OutputLines: len(kept),
		Truncated:   true,
		ByteHint:    hint,
		TempFile:    tmpPath,
	}
}

// writeTruncationRemainder writes the lines from startLine onward to a temp file
// and returns the temp file path. Lines are written with line numbers. The temp
// file is placed in the XDG cache dir to avoid polluting the project tree.
// Errors are swallowed — the temp file is best-effort; the agent always has the
// start_line continuation fallback.
func writeTruncationRemainder(srcPath string, startLine int, remainderLines []string) (string, error) {
	if len(remainderLines) == 0 {
		return "", nil
	}
	base := filepath.Base(srcPath)
	userCache, _ := os.UserCacheDir()
	if userCache == "" {
		userCache = os.TempDir()
	}
	cacheDir := filepath.Join(userCache, "jinn", "truncated")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	tmpFile, err := os.CreateTemp(cacheDir, base+".*.txt")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	endLine := startLine + len(remainderLines) - 1
	width := len(strconv.Itoa(endLine))
	for i, line := range remainderLines {
		fmt.Fprintf(tmpFile, "%*d\t%s\n", width, startLine+i, line)
	}

	return tmpFile.Name(), nil
}
