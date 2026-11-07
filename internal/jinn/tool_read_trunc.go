package jinn

import (
	"fmt"
	"strings"
)

// byteTruncateResult applies byte-size truncation. Sequential strategies also
// receive an exact source continuation; reordered strategies receive only the
// bounded content and a narrower-window hint. offsets carries absolute byte
// line starts recorded by the cursor pass so the continuation can be resumed
// by digest cursor; spill never produces a path.
// byteTruncateParams bundles the byte-truncation window context.
type byteTruncateParams struct {
	content    string
	startLine  int
	total      int
	sequential bool
	spill      func(int) (string, error)
	offsets    map[int]int64
}

func byteTruncateResult(params byteTruncateParams) *readContentResult {
	content, startLine, total, sequential, spill, offsets := params.content, params.startLine, params.total, params.sequential, params.spill, params.offsets
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

	var nextOffset int64
	nextLine := 0
	tmpPath := ""
	if sequential {
		nextLine = startLine + len(kept)
		tmpPath, _ = spill(nextLine)
		nextOffset = offsets[nextLine]
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
		NextOffset:  nextOffset,
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
