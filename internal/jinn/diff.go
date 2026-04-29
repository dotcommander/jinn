package jinn

import (
	"fmt"
	"strings"
)

// DiffResult holds structured diff output.
type DiffResult struct {
	Diff             string `json:"diff"`
	FirstChangedLine int    `json:"firstChangedLine,omitempty"`
}

// diffOp represents a single line in an edit script.
type diffOp struct {
	tag   byte // ' ', '-', '+'
	value string
}

// computeEditScript builds an LCS-based edit script between two strings,
// in forward order.
func computeEditScript(old, new_ string) []diffOp {
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new_, "\n")

	// Remove trailing empty element from final newline.
	if len(oldLines) > 0 && oldLines[len(oldLines)-1] == "" && strings.HasSuffix(old, "\n") {
		oldLines = oldLines[:len(oldLines)-1]
	}
	if len(newLines) > 0 && newLines[len(newLines)-1] == "" && strings.HasSuffix(new_, "\n") {
		newLines = newLines[:len(newLines)-1]
	}

	// Build LCS table.
	m, n := len(oldLines), len(newLines)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if oldLines[i-1] == newLines[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	// Walk back to produce edit script.
	var script []diffOp
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			script = append(script, diffOp{' ', oldLines[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			script = append(script, diffOp{'+', newLines[j-1]})
			j--
		} else {
			script = append(script, diffOp{'-', oldLines[i-1]})
			i--
		}
	}
	// Reverse so script is in forward order.
	for l, r := 0, len(script)-1; l < r; l, r = l+1, r-1 {
		script[l], script[r] = script[r], script[l]
	}
	return script
}

type hunkRange struct {
	start int // 0-based index into script
	end   int // exclusive
}

// computeHunks groups the edit script into hunks separated by enough context.
func computeHunks(script []diffOp, contextLines int) []hunkRange {
	type hunk = hunkRange
	var hunks []hunk
	inHunk := false
	hunkStart := 0
	for idx, s := range script {
		isChange := s.tag == '-' || s.tag == '+'
		if isChange && !inHunk {
			inHunk = true
			hunkStart = idx
		}
		if !isChange && inHunk {
			consecutive := 0
			for k := idx; k < len(script) && script[k].tag == ' '; k++ {
				consecutive++
			}
			if consecutive > 2*contextLines {
				hunks = append(hunks, hunk{hunkStart, idx})
				inHunk = false
			}
		}
	}
	if inHunk {
		hunks = append(hunks, hunk{hunkStart, len(script)})
	}
	return hunks
}

// formatHunks renders hunks from an edit script into a string builder.
// Returns the 1-based line number of the first changed line in the new file,
// or 0 if no changes.
func formatHunks(script []diffOp, hunks []hunkRange, contextLines int, b *strings.Builder) int {
	firstChangedLine := 0
	for _, h := range hunks {
		start := h.start - contextLines
		if start < 0 {
			start = 0
		}
		end := h.end + contextLines
		if end > len(script) {
			end = len(script)
		}

		oldOffset, newOffset := 0, 0
		for k := 0; k < start; k++ {
			if script[k].tag == ' ' || script[k].tag == '-' {
				oldOffset++
			}
			if script[k].tag == ' ' || script[k].tag == '+' {
				newOffset++
			}
		}
		oldCount, newCount := 0, 0
		for k := start; k < end; k++ {
			if script[k].tag == ' ' || script[k].tag == '-' {
				oldCount++
			}
			if script[k].tag == ' ' || script[k].tag == '+' {
				newCount++
			}
		}
		fmt.Fprintf(b, "@@ -%d,%d +%d,%d @@\n", oldOffset+1, oldCount, newOffset+1, newCount)

		for k := start; k < end; k++ {
			b.WriteByte(script[k].tag)
			b.WriteByte(' ')
			b.WriteString(script[k].value)
			b.WriteByte('\n')
			if firstChangedLine == 0 && (script[k].tag == '+' || script[k].tag == '-') {
				// Count the new-file line number up to this point.
				newLine := newOffset + 1
				for m := start; m <= k; m++ {
					if script[m].tag == ' ' || script[m].tag == '+' {
						newLine++
					}
				}
				firstChangedLine = newLine - 1
			}
		}
	}
	return firstChangedLine
}

// renderDiffBody runs the LCS pipeline and writes the unified-diff hunks
// (no "--- / +++" or "[dry-run]" header) to b. Callers prepend their own header.
// Returns true and the 1-based new-file line of the first change when there is
// at least one hunk; false (and 0) when old == new or no hunks survive context.
func renderDiffBody(old, new_ string, contextLines int, b *strings.Builder) (bool, int) {
	if old == new_ {
		return false, 0
	}
	script := computeEditScript(old, new_)
	hunks := computeHunks(script, contextLines)
	if len(hunks) == 0 {
		return false, 0
	}
	return true, formatHunks(script, hunks, contextLines, b)
}

// generateDiff computes a unified diff and returns structured output.
func generateDiff(old, new_, label string, contextLines int) DiffResult {
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", label, label)
	ok, firstChangedLine := renderDiffBody(old, new_, contextLines, &b)
	if !ok {
		return DiffResult{}
	}
	return DiffResult{
		Diff:             strings.TrimRight(b.String(), "\n"),
		FirstChangedLine: firstChangedLine,
	}
}

// unifiedDiff generates a unified diff with a "[dry-run]" prefix for tool previews.
// Returns "[dry-run] no changes" if old and new are identical.
func unifiedDiff(old, new_, label string, contextLines int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[dry-run] diff for %s:\n", label)
	ok, _ := renderDiffBody(old, new_, contextLines, &b)
	if !ok {
		return "[dry-run] no changes"
	}
	return b.String()
}

// formatEditPreview shows a before/after preview for an edit_file dry run.
// It uses unified diff on the full file content to show the change with context.
func formatEditPreview(old, updated, path string, fuzzy bool) string {
	diff := unifiedDiff(old, updated, path, 3)
	if fuzzy {
		diff += " (fuzzy match)\n"
	}
	return diff
}
