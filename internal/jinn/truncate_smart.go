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
// a top-level boundary outside a literal or comment. One forward scan retains
// lexical state and remembers the last safe boundary within the line allowance.
func braceCutLine(lines []string, limit int) (int, bool) {
	s := braceScanner{}
	lastBoundary := 0
	for line := 0; line < len(lines) && line < limit; line++ {
		for i := 0; i < len(lines[line]); i++ {
			if s.step(lines[line], &i) {
				break
			}
		}
		if s.depth == 0 && s.quote == 0 && s.rawClose == "" && s.blockDepth == 0 {
			lastBoundary = line + 1
		}
	}
	return lastBoundary, lastBoundary > 0
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

// braceScanner retains lexical state across lines while counting braces outside
// quoted/raw literals and block or line comments.
type braceScanner struct {
	depth      int
	quote      byte
	rawClose   string
	blockDepth int
}

// step advances the scanner by the character at *i, mutating *i to consume
// extra characters (escapes, the second comment slash). It returns true when
// the rest of the line is a comment and scanning should stop.
func (s *braceScanner) step(line string, i *int) bool {
	ch := line[*i]

	if s.rawClose != "" {
		if strings.HasPrefix(line[*i:], s.rawClose) {
			*i += len(s.rawClose) - 1
			s.rawClose = ""
		}
		return false
	}
	if s.blockDepth > 0 {
		if strings.HasPrefix(line[*i:], "*/") {
			s.blockDepth--
			*i++
		} else if strings.HasPrefix(line[*i:], "/*") {
			s.blockDepth++
			*i++
		}
		return false
	}
	if s.quote != 0 {
		if ch == '\\' {
			*i++
		} else if ch == s.quote {
			s.quote = 0
		}
		return false
	}
	if strings.HasPrefix(line[*i:], `"""`) {
		s.rawClose = `"""`
		*i += 2
		return false
	}
	// Rust raw strings allow zero or more hashes around their quotes.
	if ch == 'r' {
		end := *i + 1
		for end < len(line) && line[end] == '#' {
			end++
		}
		if end < len(line) && line[end] == '"' {
			s.rawClose = `"` + line[*i+1:end]
			*i = end
			return false
		}
	}
	// C++ raw strings close with the opening delimiter after a ')'.
	if strings.HasPrefix(line[*i:], `R"`) {
		if end := strings.IndexByte(line[*i+2:], '('); end >= 0 && end <= 16 {
			s.rawClose = ")" + line[*i+2:*i+2+end] + `"`
			*i += 2 + end
			return false
		}
	}

	switch ch {
	case '"':
		s.quote = ch
	case '\'':
		// Rust lifetimes such as 'a are identifiers, not character literals.
		if charLiteralEnd(line[*i:]) {
			s.quote = ch
		}
	case '`':
		s.rawClose = "`"
	case '/':
		if *i+1 < len(line) && line[*i+1] == '/' {
			// Line comments persist until newline; since we process per-line,
			// everything after // is a comment.
			return true
		}
		if *i+1 < len(line) && line[*i+1] == '*' {
			s.blockDepth = 1
			*i++
		}
	case '{':
		s.depth++
	case '}':
		s.depth--
	}
	return false
}

func charLiteralEnd(text string) bool {
	for i := 1; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		if text[i] == '\'' {
			return true
		}
		if text[i] == ' ' || text[i] == '\t' {
			return i == 1 && i+1 < len(text) && text[i+1] == '\''
		}
	}
	return false
}
