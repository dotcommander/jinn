package jinn

import (
	"strings"
	"unicode/utf8"
)

// closestLine finds the line in content that best matches the first line of oldText.
// Returns the 1-based line number and the matched line text.
// Uses character overlap ratio (shared runes / max runes) for scoring.
func closestLine(oldText, content string) (int, string) {
	firstLine := strings.SplitN(oldText, "\n", 2)[0]
	firstLine = strings.TrimSpace(firstLine)
	if firstLine == "" {
		return 0, ""
	}
	needles := []rune(firstLine)
	bestScore := -1.0
	bestIdx := 0
	bestText := ""
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		haystack := []rune(line)
		// Count shared runes using a set for haystack membership.
		haySet := make(map[rune]struct{}, len(haystack))
		for _, r := range haystack {
			haySet[r] = struct{}{}
		}
		shared := 0
		seen := make(map[rune]struct{}, len(needles))
		for _, r := range needles {
			if _, ok := haySet[r]; ok {
				if _, dup := seen[r]; !dup {
					shared++
					seen[r] = struct{}{}
				}
			}
		}
		maxRunes := utf8.RuneCountInString(firstLine)
		if len(haystack) > maxRunes {
			maxRunes = len(haystack)
		}
		if maxRunes == 0 {
			continue
		}
		score := float64(shared) / float64(maxRunes)
		if score > bestScore {
			bestScore = score
			bestIdx = i
			bestText = line
		}
	}
	return bestIdx + 1, bestText
}

// stripBom removes a UTF-8 BOM if present. Returns content and the BOM prefix.
func stripBom(s string) (string, string) {
	if strings.HasPrefix(s, "\xEF\xBB\xBF") {
		return s[3:], s[:3]
	}
	return s, ""
}

// detectLineEnding returns "\r\n" if the first newline is CRLF, else "\n".
func detectLineEnding(s string) string {
	crlfIdx := strings.Index(s, "\r\n")
	lfIdx := strings.Index(s, "\n")
	if lfIdx == -1 || crlfIdx == -1 {
		return "\n"
	}
	if crlfIdx < lfIdx {
		return "\r\n"
	}
	return "\n"
}

// normalizeToLF converts all line endings to LF for matching.
func normalizeToLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// restoreLineEndings converts LF back to the original ending.
func restoreLineEndings(s, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(s, "\n", "\r\n")
	}
	return s
}

// normalizeForFuzzyMatch strips trailing whitespace per line and normalizes
// Unicode smart quotes, dashes, and special spaces to ASCII equivalents.
func normalizeForFuzzyMatch(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	s = strings.Join(lines, "\n")

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\u2018' || r == '\u2019' || r == '\u201A' || r == '\u201B':
			b.WriteByte('\'')
		case r == '\u201C' || r == '\u201D' || r == '\u201E' || r == '\u201F':
			b.WriteByte('"')
		case r == '\u2010' || r == '\u2011' || r == '\u2012' || r == '\u2013' ||
			r == '\u2014' || r == '\u2015' || r == '\u2212':
			b.WriteByte('-')
		case r == '\u00A0' || (r >= '\u2002' && r <= '\u200A') ||
			r == '\u202F' || r == '\u205F' || r == '\u3000':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
