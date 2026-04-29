package jinn

import (
	"fmt"
	"strings"
)

// generateEditDiff produces a unified diff from known line ranges, avoiding the
// O(m×n) LCS computation. It uses matchInfo from applyEdit to build the diff
// directly. Falls back to the full LCS-based generateDiff if the ranges look
// wrong (should never happen, but defensive).
func generateEditDiff(oldContent, newContent, label string, info matchInfo, oldText, newText string, contextLines int) DiffResult {
	if oldContent == newContent {
		return DiffResult{}
	}

	oldLinesRaw := strings.Split(oldContent, "\n")
	newLinesRaw := strings.Split(newContent, "\n")

	// Trim trailing empty from final newline.
	if len(oldLinesRaw) > 0 && oldLinesRaw[len(oldLinesRaw)-1] == "" && strings.HasSuffix(oldContent, "\n") {
		oldLinesRaw = oldLinesRaw[:len(oldLinesRaw)-1]
	}
	if len(newLinesRaw) > 0 && newLinesRaw[len(newLinesRaw)-1] == "" && strings.HasSuffix(newContent, "\n") {
		newLinesRaw = newLinesRaw[:len(newLinesRaw)-1]
	}

	oldCount := strings.Count(oldText, "\n") + 1
	newCount := strings.Count(newText, "\n") + 1

	// Sanity check: if info is out of bounds, fall back to LCS.
	if info.startLine < 1 || info.endLine > len(oldLinesRaw)+1 {
		return generateDiff(oldContent, newContent, label, contextLines)
	}

	// Build a simple diff script around the known change.
	start := info.startLine - 1 // 0-based index into oldLinesRaw
	oldEnd := start + oldCount
	newEnd := start + newCount

	ctxStart := start - contextLines
	if ctxStart < 0 {
		ctxStart = 0
	}

	// Hunk offsets are identical for old and new: the prefix before `start` is unchanged.
	offset := ctxStart
	var script []diffOp

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
	trailingEnd := oldEnd + contextLines
	if trailingEnd > len(oldLinesRaw) {
		trailingEnd = len(oldLinesRaw)
	}
	for i := trailingStart; i < trailingEnd; i++ {
		newIdx := newEnd + (i - oldEnd)
		if newIdx < len(newLinesRaw) {
			script = append(script, diffOp{' ', newLinesRaw[newIdx]})
		}
	}

	if len(script) == 0 {
		return DiffResult{}
	}

	// Count old/new lines in script for header.
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

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", label, label)
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", offset+1, oldScriptCount, offset+1, newScriptCount)

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

	return DiffResult{
		Diff:             strings.TrimRight(b.String(), "\n"),
		FirstChangedLine: firstChangedLine,
	}
}
