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
func stripBom(s string) (rest string, bom string) {
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
		if ascii, ok := normalizeRune(r); ok {
			b.WriteByte(ascii)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// runeASCIIMap holds the discrete 1:1 substitutions for normalizeRune: smart
// quotes \u2192 ASCII quote, Unicode dashes \u2192 '-', and the non-range special spaces
// \u2192 ' '. The contiguous space range U+2002..U+200A is handled separately in
// normalizeRune.
var runeASCIIMap = map[rune]byte{
	'\u2018': '\'', '\u2019': '\'', '\u201A': '\'', '\u201B': '\'',
	'\u201C': '"', '\u201D': '"', '\u201E': '"', '\u201F': '"',
	'\u2010': '-', '\u2011': '-', '\u2012': '-', '\u2013': '-',
	'\u2014': '-', '\u2015': '-', '\u2212': '-',
	'\u00A0': ' ', '\u202F': ' ', '\u205F': ' ', '\u3000': ' ',
}

// normalizeRune maps a Unicode smart quote, dash, or special space to its ASCII
// equivalent. The second return is false when r has no mapping (write r as-is).
func normalizeRune(r rune) (byte, bool) {
	if b, ok := runeASCIIMap[r]; ok {
		return b, true
	}
	if r >= '\u2002' && r <= '\u200A' {
		return ' ', true
	}
	return 0, false
}
