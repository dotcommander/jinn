package jinn

import (
	"fmt"
	"strings"
)

// unifiedDiff generates a unified diff between two strings.
// contextLines controls how many unchanged lines surround each change.
// Returns "[dry-run] no changes" if old and new are identical.
func unifiedDiff(old, new_, label string, contextLines int) string {
	if old == new_ {
		return "[dry-run] no changes"
	}
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

	// Walk back to produce edit script: true = keep, false = delete (old) / insert (new).
	type op struct {
		tag   byte // ' ', '-', '+'
		value string
	}
	var script []op
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			script = append(script, op{' ', oldLines[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			script = append(script, op{'+', newLines[j-1]})
			j--
		} else {
			script = append(script, op{'-', oldLines[i-1]})
			i--
		}
	}
	// Reverse so script is in forward order.
	for l, r := 0, len(script)-1; l < r; l, r = l+1, r-1 {
		script[l], script[r] = script[r], script[l]
	}

	// Group into hunks separated by at least 2*contextLines+1 unchanged lines.
	type hunk struct {
		start int // 0-based index into script
		end   int // exclusive
	}
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
			// Count consecutive unchanged lines from here.
			consecutive := 0
			for k := idx; k < len(script) && script[k].tag == ' '; k++ {
				consecutive++
			}
			if consecutive > 2*contextLines {
				// End hunk before this context.
				hunks = append(hunks, hunk{hunkStart, idx})
				inHunk = false
			}
		}
	}
	if inHunk {
		hunks = append(hunks, hunk{hunkStart, len(script)})
	}
	if len(hunks) == 0 {
		return "[dry-run] no changes"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[dry-run] diff for %s:\n", label)

	for _, h := range hunks {
		// Expand hunk boundaries by contextLines.
		start := h.start - contextLines
		if start < 0 {
			start = 0
		}
		end := h.end + contextLines
		if end > len(script) {
			end = len(script)
		}

		// Count old/new line offsets for this hunk.
		oldOffset := 0
		newOffset := 0
		for k := 0; k < start; k++ {
			if script[k].tag == ' ' || script[k].tag == '-' {
				oldOffset++
			}
			if script[k].tag == ' ' || script[k].tag == '+' {
				newOffset++
			}
		}
		oldCount := 0
		newCount := 0
		for k := start; k < end; k++ {
			if script[k].tag == ' ' || script[k].tag == '-' {
				oldCount++
			}
			if script[k].tag == ' ' || script[k].tag == '+' {
				newCount++
			}
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", oldOffset+1, oldCount, newOffset+1, newCount)

		for k := start; k < end; k++ {
			b.WriteByte(script[k].tag)
			b.WriteByte(' ')
			b.WriteString(script[k].value)
			b.WriteByte('\n')
		}
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
