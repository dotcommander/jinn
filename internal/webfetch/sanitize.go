package webfetch

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	ansiEscapePattern  = regexp.MustCompile("\x1b(?:[@-Z\\-_]|\\[[0-?]*[ -/]*[@-~])|\u009b[0-?]*[ -/]*[@-~]")
	bidiControlPattern = regexp.MustCompile("[\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]")
)

// SanitizeText strips terminal escape sequences, bidi controls, and C0/C1
// control characters (except tab and newline) from untrusted fetched text.
func SanitizeText(text string) string {
	if text == "" {
		return ""
	}
	text = ansiEscapePattern.ReplaceAllString(text, "")
	text = bidiControlPattern.ReplaceAllString(text, "")
	var builder strings.Builder
	builder.Grow(len(text))
	for _, r := range text {
		if r == '\t' || r == '\n' {
			builder.WriteRune(r)
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
