package jinn

import (
	"fmt"
	"strings"
)

// cSyntaxExtensions are file extensions that use brace-delimited blocks.
// Smart truncation walks brace depth to avoid cutting mid-block.
var cSyntaxExtensions = map[string]bool{
	".go":   true,
	".java": true,
	".c":    true,
	".cpp":  true,
	".h":    true,
	".hpp":  true,
	".rs":   true,
	".ts":   true,
	".tsx":  true,
	".js":   true,
	".jsx":  true,
}

// isCSyntaxExt reports whether the file extension (including dot) uses
// brace-delimited block syntax suitable for depth-aware truncation.
func isCSyntaxExt(ext string) bool {
	return cSyntaxExtensions[ext]
}

// truncateOutputSmart truncates content at brace-block boundaries for C-syntax
// files, falling back to head truncation for other extensions.
//
// The brace-depth heuristic scans backward from the line limit and finds the
// nearest line where brace depth returns to zero (top-level boundary). This
// ensures no function, struct, or other block is cut in half.
//
// Braces inside string literals (both double-quoted and backtick-quoted) and
// line comments (//) are ignored during depth counting.
func truncateOutputSmart(raw string, limit int, ext string) struct {
	Content    string
	Truncated  bool
	TotalLines int
	ShownLines int
} {
	result := struct {
		Content    string
		Truncated  bool
		TotalLines int
		ShownLines int
	}{}

	if raw == "" {
		return result
	}

	lines := splitLines(raw)
	count := len(lines)
	result.TotalLines = count

	if count <= limit {
		result.Content = raw
		result.ShownLines = count
		return result
	}

	// Non-C-syntax files fall back to head truncation.
	if !isCSyntaxExt(ext) {
		return truncateOutputHead(raw, limit)
	}

	// Walk forward accumulating lines. Track brace depth, ignoring braces
	// inside string literals and line comments. Stop at the limit and scan
	// backward to find the last line where depth == 0 (top-level boundary).
	depth := 0
	cutLine := limit // candidate cut point (exclusive)

	for i := 0; i < count; i++ {
		depth += lineBraceDepth(lines[i])
		if i == limit-1 {
			// We've reached the limit. If depth is 0, we can cut cleanly here.
			if depth == 0 {
				cutLine = limit
				break
			}
			// Walk backward from current position to find the last zero-depth line.
			// Recompute depth from scratch to avoid accumulated state errors.
			found := false
			for j := i; j >= 0; j-- {
				d := 0
				for k := 0; k <= j; k++ {
					d += lineBraceDepth(lines[k])
				}
				if d == 0 {
					cutLine = j + 1 // cut after this line (exclusive)
					found = true
					break
				}
			}
			if !found {
				// No clean boundary found — use head truncation as fallback.
				return truncateOutputHead(raw, limit)
			}
			break
		}
	}

	if cutLine <= 0 {
		cutLine = limit
	}

	kept := lines[:cutLine]
	var b strings.Builder
	for _, l := range kept {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "[... truncated: showing first %d of %d lines — use start_line=%d to continue ...]",
		cutLine, count, cutLine+1)

	result.Content = strings.TrimRight(b.String(), "\n")
	result.Truncated = true
	result.ShownLines = cutLine
	return result
}

// lineBraceDepth computes the net brace depth change for a single line,
// ignoring braces inside double-quoted strings, backtick strings, and
// line comments.
func lineBraceDepth(line string) int {
	depth := 0
	inString := false
	inRawString := false
	inComment := false

	for i := 0; i < len(line); i++ {
		ch := line[i]

		if inRawString {
			if ch == '`' {
				inRawString = false
			}
			continue
		}
		if inString {
			if ch == '\\' {
				i++ // skip escaped char
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if inComment {
			// Line comments persist until newline; since we process per-line,
			// everything after // is a comment.
			break
		}
		switch ch {
		case '"':
			inString = true
		case '`':
			inRawString = true
		case '/':
			if i+1 < len(line) && line[i+1] == '/' {
				inComment = true
				i++ // skip second slash
			}
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	return depth
}
