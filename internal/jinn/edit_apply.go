package jinn

import (
	"errors"
	"strings"
)

// editMatch carries the (possibly fuzzy-normalized) raw/oldText and whether the
// match required fuzzy normalization.
type editMatch struct {
	raw     string
	oldText string
	fuzzy   bool
}

// findEditMatch locates oldText in raw, falling back to fuzzy normalization when
// the exact match fails. It returns the (possibly normalized) raw/oldText and
// whether a fuzzy match was used, or a match-count error.
func findEditMatch(raw, oldText string) (editMatch, error) {
	count := strings.Count(raw, oldText)
	if count == 0 {
		normContent := normalizeForFuzzyMatch(raw)
		normOld := normalizeForFuzzyMatch(oldText)
		if strings.Count(normContent, normOld) == 1 {
			return editMatch{raw: normContent, oldText: normOld, fuzzy: true}, nil
		}
	}

	if count == 0 {
		return editMatch{}, errors.New("old_text not found in file")
	}
	if count > 1 {
		return editMatch{}, multiMatchError(count, raw, oldText)
	}
	return editMatch{raw: raw, oldText: oldText}, nil
}

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
	ending := detectLineEnding(raw)
	raw = normalizeToLF(raw)
	oldText = normalizeToLF(oldText)
	newText = normalizeToLF(newText)

	m, err := findEditMatch(raw, oldText)
	if err != nil {
		return "", false, info, err
	}
	raw, oldText, fuzzy := m.raw, m.oldText, m.fuzzy

	idx := strings.Index(raw, oldText)
	info.startLine = strings.Count(raw[:idx], "\n") + 1
	info.endLine = info.startLine + strings.Count(oldText, "\n")

	// fuzzyIndent: detect indentation at the match site and apply it to newText.
	if fuzzyIndent {
		newText = reindentNewText(newText, matchLeadingIndent(raw, idx))
	}

	return bom + restoreLineEndings(strings.Replace(raw, oldText, newText, 1), ending), fuzzy, info, nil
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
