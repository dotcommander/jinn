package jinn

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// matchLeadingIndent returns the leading whitespace of the line containing idx.
func matchLeadingIndent(raw string, idx int) string {
	lineNum := strings.Count(raw[:idx], "\n")
	lines := strings.Split(raw, "\n")
	var b strings.Builder
	for _, ch := range lines[lineNum] {
		if ch == ' ' || ch == '\t' {
			b.WriteRune(ch)
		} else {
			break
		}
	}
	return b.String()
}

// reindentNewText re-indents newText to match the indentation found at the match
// site: it strips the minimum indentation common to non-empty lines, then
// prepends leading. When newText has no indented lines it is returned unchanged.
func reindentNewText(newText, leading string) string {
	newLines := strings.Split(newText, "\n")
	minIndent := -1
	for _, l := range newLines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		indent := len(l) - len(strings.TrimLeft(l, " \t"))
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent < 0 {
		return newText
	}
	for i, l := range newLines {
		switch {
		case strings.TrimSpace(l) == "":
			newLines[i] = ""
		case len(l) >= minIndent:
			newLines[i] = leading + l[minIndent:]
		default:
			newLines[i] = leading + l
		}
	}
	return strings.Join(newLines, "\n")
}

//nolint:revive // function-result-limit: signature (updated, fuzzy, matchInfo, err) is fixed by Dispatch + tests
func applyEdit(content []byte, oldText, newText string, fuzzyIndent bool) (string, bool, matchInfo, error) {
	var info matchInfo
	raw, bom := stripBom(string(content))
	matchText := oldText
	mapped := mapNormalizedText(raw, false)
	matchText = normalizeToLF(matchText)
	fuzzy := false
	count := strings.Count(mapped.text, matchText)
	if count == 0 {
		mapped = mapNormalizedText(raw, true)
		matchText = normalizeForFuzzyMatch(normalizeToLF(oldText))
		count = strings.Count(mapped.text, matchText)
		fuzzy = count > 0
	}
	if count == 0 {
		return "", false, info, errors.New("old_text not found in file")
	}
	if count > 1 {
		return "", false, info, multiMatchError(count, mapped.text, matchText)
	}
	idx := strings.Index(mapped.text, matchText)
	startByte := mapped.boundaries[idx]
	endByte := mapped.boundaries[idx+len(matchText)]
	info.startLine = strings.Count(mapped.text[:idx], "\n") + 1
	info.endLine = info.startLine + strings.Count(matchText, "\n")

	newText = normalizeToLF(newText)
	lineStart := strings.LastIndex(mapped.text[:idx], "\n") + 1
	if fuzzyIndent && strings.Trim(mapped.text[lineStart:idx], " \t") == "" {
		newText = reindentNewText(newText, matchLeadingIndent(mapped.text, idx))
	}
	ending := detectLineEnding(raw[startByte:endByte])
	if !strings.Contains(raw[startByte:endByte], "\n") && !strings.Contains(raw[startByte:endByte], "\r") {
		ending = detectLineEnding(raw)
	}
	replacement := restoreLineEndings(newText, ending)
	return bom + raw[:startByte] + replacement + raw[endByte:], fuzzy, info, nil
}

type normalizedTextMap struct {
	text       string
	boundaries []int
}

//nolint:gocognit,gocyclo,revive // byte-boundary preservation and fuzzy normalization are one inseparable mapping pass.
func mapNormalizedText(input string, fuzzy bool) normalizedTextMap {
	var out strings.Builder
	boundaries := []int{0}
	appendMapped := func(text string, start, end int) {
		out.WriteString(text)
		for range len(text) {
			boundaries = append(boundaries, end)
		}
		_ = start
	}
	for offset := 0; offset < len(input); {
		lineEnd := offset
		for lineEnd < len(input) && input[lineEnd] != '\n' && input[lineEnd] != '\r' {
			lineEnd++
		}
		contentEnd := lineEnd
		if fuzzy {
			for contentEnd > offset && (input[contentEnd-1] == ' ' || input[contentEnd-1] == '\t') {
				contentEnd--
			}
		}
		for pos := offset; pos < contentEnd; {
			r, size := utf8.DecodeRuneInString(input[pos:contentEnd])
			mapped := input[pos : pos+size]
			if fuzzy {
				if ascii, ok := normalizeRune(r); ok {
					mapped = string([]byte{ascii})
				}
			}
			appendMapped(mapped, pos, pos+size)
			pos += size
		}
		if fuzzy && contentEnd < lineEnd && len(boundaries) > 0 {
			boundaries[len(boundaries)-1] = lineEnd
		}
		if lineEnd == len(input) {
			break
		}
		newlineEnd := lineEnd + 1
		if input[lineEnd] == '\r' && newlineEnd < len(input) && input[newlineEnd] == '\n' {
			newlineEnd++
		}
		appendMapped("\n", lineEnd, newlineEnd)
		offset = newlineEnd
	}
	return normalizedTextMap{text: out.String(), boundaries: boundaries}
}

func countLines(s string) int {
	n := strings.Count(s, "\n")
	if n > 0 && strings.HasSuffix(s, "\n") {
		// Trailing newline terminates the last line but doesn't add one.
		// Split approach: "a\nb\n" -> ["a","b",""] -> 2 lines.
		// Count approach: 2 newlines - 0 = 2. Same result.
		return n
	}
	return n + 1
}
