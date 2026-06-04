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
func truncateOutputSmart(raw string, limit int, ext string) truncateResult {
	result := truncateResult{}

	if raw == "" {
		return result
	}

	lines := splitLines(raw)
	count := len(lines)
	result.TotalLines = count

	// Non-C-syntax files fall back to head truncation.
	if !isCSyntaxExt(ext) {
		return truncateOutputHead(raw, limit)
	}

	if count <= limit {
		result.Content = raw
		result.ShownLines = count
		return result
	}

	cutLine, ok := braceCutLine(lines, limit)
	if !ok {
		// No clean boundary found — use head truncation as fallback.
		return truncateOutputHead(raw, limit)
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

// braceCutLine finds the exclusive cut point at or before limit that lands on
// a top-level (zero brace depth) boundary. It walks forward accumulating brace
// depth; if depth is zero at the limit it cuts there, otherwise it scans
// backward for the last zero-depth line. The bool reports whether a clean
// boundary was found.
func braceCutLine(lines []string, limit int) (int, bool) {
	depth := 0
	for i := 0; i < len(lines); i++ {
		depth += lineBraceDepth(lines[i])
		if i != limit-1 {
			continue
		}
		// We've reached the limit. If depth is 0, we can cut cleanly here.
		if depth == 0 {
			return limit, true
		}
		// Walk backward from current position to find the last zero-depth line.
		// Recompute depth from scratch to avoid accumulated state errors.
		for j := i; j >= 0; j-- {
			d := 0
			for k := 0; k <= j; k++ {
				d += lineBraceDepth(lines[k])
			}
			if d == 0 {
				return j + 1, true // cut after this line (exclusive)
			}
		}
		return 0, false
	}
	return limit, true
}

// lineBraceDepth computes the net brace depth change for a single line,
// ignoring braces inside double-quoted strings, backtick strings, and
// line comments.
func lineBraceDepth(line string) int {
	s := braceScanner{}
	for i := 0; i < len(line); i++ {
		if s.step(line, &i) {
			break
		}
	}
	return s.depth
}

// braceScanner carries the lexical state used to count brace depth on a single
// line while ignoring braces inside string/raw-string literals and comments.
type braceScanner struct {
	depth       int
	inString    bool
	inRawString bool
}

// step advances the scanner by the character at *i, mutating *i to consume
// extra characters (escapes, the second comment slash). It returns true when
// the rest of the line is a comment and scanning should stop.
func (s *braceScanner) step(line string, i *int) bool {
	ch := line[*i]

	if s.inRawString {
		if ch == '`' {
			s.inRawString = false
		}
		return false
	}
	if s.inString {
		switch ch {
		case '\\':
			*i++ // skip escaped char
		case '"':
			s.inString = false
		}
		return false
	}

	switch ch {
	case '"':
		s.inString = true
	case '`':
		s.inRawString = true
	case '/':
		if *i+1 < len(line) && line[*i+1] == '/' {
			// Line comments persist until newline; since we process per-line,
			// everything after // is a comment.
			return true
		}
	case '{':
		s.depth++
	case '}':
		s.depth--
	}
	return false
}
