package jinn

import (
	"fmt"
	"strings"
)

// splitDiffLines splits content into lines, trimming the trailing empty
// element produced by a final newline.
func splitDiffLines(content string) []string {
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" && strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// editRegion describes a known change region for the fast-path diff builder.
type editRegion struct {
	start        int // 0-based index into oldLinesRaw where the change begins
	oldCount     int // number of replaced old lines
	newCount     int // number of inserted new lines
	contextLines int
}

// buildEditScript constructs the diff script (leading context, removed, added,
// trailing context) for a known change region. offset is the 0-based start of
// the rendered span (== ctxStart).
func buildEditScript(oldLinesRaw, newLinesRaw []string, r editRegion) (script []diffOp, offset int) {
	start := r.start
	oldEnd := start + r.oldCount
	newEnd := start + r.newCount

	ctxStart := start - r.contextLines
	if ctxStart < 0 {
		ctxStart = 0
	}
	offset = ctxStart

	// Leading context lines (same in both old and new).
	for i := ctxStart; i < start; i++ {
		script = append(script, diffOp{' ', oldLinesRaw[i]})
	}

	// Removed lines.
	for i := start; i < oldEnd && i < len(oldLinesRaw); i++ {
		script = append(script, diffOp{'-', oldLinesRaw[i]})
	}

	// Added lines.
	for i := start; i < newEnd && i < len(newLinesRaw); i++ {
		script = append(script, diffOp{'+', newLinesRaw[i]})
	}

	// Trailing context lines (same in both, taken from new file shifted by newEnd).
	trailingStart := oldEnd
	trailingEnd := oldEnd + r.contextLines
	if trailingEnd > len(oldLinesRaw) {
		trailingEnd = len(oldLinesRaw)
	}
	for i := trailingStart; i < trailingEnd; i++ {
		newIdx := newEnd + (i - oldEnd)
		if newIdx < len(newLinesRaw) {
			script = append(script, diffOp{' ', newLinesRaw[newIdx]})
		}
	}
	return script, offset
}

// renderEditScript writes the --- / +++ / @@ header and body lines for a
// single-hunk script to b, returning the 1-based new-file line of the first
// change, or 0 if none.
func renderEditScript(script []diffOp, offset int, label string, b *strings.Builder) int {
	oldScriptCount := 0
	newScriptCount := 0
	for _, s := range script {
		if s.tag == ' ' || s.tag == '-' {
			oldScriptCount++
		}
		if s.tag == ' ' || s.tag == '+' {
			newScriptCount++
		}
	}

	fmt.Fprintf(b, "--- %s\n+++ %s\n", label, label)
	fmt.Fprintf(b, "@@ -%d,%d +%d,%d @@\n", offset+1, oldScriptCount, offset+1, newScriptCount)

	firstChangedLine := 0
	for k, s := range script {
		b.WriteByte(s.tag)
		b.WriteByte(' ')
		b.WriteString(s.value)
		b.WriteByte('\n')
		if firstChangedLine == 0 && (s.tag == '+' || s.tag == '-') {
			lineNum := offset + 1
			for m := 0; m <= k; m++ {
				if script[m].tag == ' ' || script[m].tag == '+' {
					lineNum++
				}
			}
			firstChangedLine = lineNum - 1
		}
	}
	return firstChangedLine
}

// generateEditDiff produces a unified diff from known line ranges, avoiding the
// O(m×n) LCS computation. It uses matchInfo from applyEdit to build the diff
// directly. Falls back to the full LCS-based generateDiff if the ranges look
// wrong (should never happen, but defensive).
//
//nolint:revive // argument-limit: signature is fixed by callers in tool_edit.go and tests; cannot collapse params without changing the public contract.
func generateEditDiff(oldContent, newContent, label string, info matchInfo, oldText, newText string, contextLines int) DiffResult {
	if oldContent == newContent {
		return DiffResult{}
	}

	oldLinesRaw := splitDiffLines(oldContent)
	newLinesRaw := splitDiffLines(newContent)

	oldCount := strings.Count(oldText, "\n") + 1
	newCount := strings.Count(newText, "\n") + 1

	// Sanity check: if info is out of bounds, fall back to LCS.
	if info.startLine < 1 || info.endLine > len(oldLinesRaw)+1 {
		return generateDiff(oldContent, newContent, label, contextLines)
	}

	// Build a simple diff script around the known change.
	start := info.startLine - 1 // 0-based index into oldLinesRaw
	script, offset := buildEditScript(oldLinesRaw, newLinesRaw, editRegion{
		start:        start,
		oldCount:     oldCount,
		newCount:     newCount,
		contextLines: contextLines,
	})

	if len(script) == 0 {
		return DiffResult{}
	}

	var b strings.Builder
	firstChangedLine := renderEditScript(script, offset, label, &b)

	return DiffResult{
		Diff:             strings.TrimRight(b.String(), "\n"),
		FirstChangedLine: firstChangedLine,
	}
}
