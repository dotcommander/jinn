package jinn

import (
	"fmt"
	"os"
	"strings"
)

// matchInfo carries metadata about where old_text was found in the file.
type matchInfo struct {
	startLine int // 1-based line number where the match begins
	endLine   int // 1-based line number where the match ends
}

func applyEdit(content []byte, oldText, newText string, fuzzyIndent bool) (string, bool, matchInfo, error) {
	var info matchInfo
	raw, bom := stripBom(string(content))
	ending := detectLineEnding(raw)
	raw = normalizeToLF(raw)
	oldText = normalizeToLF(oldText)
	newText = normalizeToLF(newText)

	count := strings.Count(raw, oldText)
	fuzzy := false
	if count == 0 {
		normContent := normalizeForFuzzyMatch(raw)
		normOld := normalizeForFuzzyMatch(oldText)
		count = strings.Count(normContent, normOld)
		if count == 1 {
			raw = normContent
			oldText = normOld
			fuzzy = true
		}
	}

	if count == 0 {
		return "", false, info, fmt.Errorf("old_text not found in file")
	}
	if count > 1 {
		return "", false, info, fmt.Errorf("old_text matches %d locations — must be unique. Add surrounding context to disambiguate", count)
	}

	idx := strings.Index(raw, oldText)
	info.startLine = strings.Count(raw[:idx], "\n") + 1
	info.endLine = info.startLine + strings.Count(oldText, "\n")

	// fuzzyIndent: detect indentation at the match site and apply it to newText.
	if fuzzyIndent {
		lineNum := strings.Count(raw[:idx], "\n")
		lines := strings.Split(raw, "\n")
		// Leading whitespace of the line containing the match start.
		leading := ""
		for _, ch := range lines[lineNum] {
			if ch == ' ' || ch == '\t' {
				leading += string(ch)
			} else {
				break
			}
		}
		// Find minimum indentation of non-empty newText lines.
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
		// Re-indent: strip minIndent from each line, prepend leading whitespace.
		if minIndent >= 0 {
			for i, l := range newLines {
				if strings.TrimSpace(l) == "" {
					newLines[i] = ""
				} else if len(l) >= minIndent {
					newLines[i] = leading + l[minIndent:]
				} else {
					newLines[i] = leading + l
				}
			}
			newText = strings.Join(newLines, "\n")
		}
	}

	return bom + restoreLineEndings(strings.Replace(raw, oldText, newText, 1), ending), fuzzy, info, nil
}

func formatEditContext(content []byte, info matchInfo, newLines int, showContext int) string {
	lines := strings.Split(string(content), "\n")
	total := len(lines)
	if lines[total-1] == "" {
		total--
	}
	width := len(fmt.Sprintf("%d", total))

	start := info.startLine - showContext
	if start < 1 {
		start = 1
	}
	end := info.startLine + newLines - 1 + showContext
	if end > total {
		end = total
	}

	var b strings.Builder
	for i := start; i <= end; i++ {
		marker := " "
		if i >= info.startLine && i < info.startLine+newLines {
			marker = "* "
		}
		fmt.Fprintf(&b, "%*d%s| %s\n", width, i, marker, lines[i-1])
	}
	return b.String()
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

func (e *Engine) editFile(args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	oldText, _ := args["old_text"].(string)
	newText, _ := args["new_text"].(string)
	fuzzyIndent, _ := args["fuzzy_indent"].(bool)
	showContext := 0
	if v, ok := args["show_context"].(float64); ok && v > 0 {
		showContext = int(v)
	}

	resolved, err := e.checkPath(path)
	if err != nil {
		return "", err
	}
	if err := e.tracker.checkStale(resolved); err != nil {
		return "", err
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", path)
		}
		return "", err
	}

	updated, fuzzy, info, err := applyEdit(data, oldText, newText, fuzzyIndent)
	if err != nil {
		if strings.Contains(err.Error(), "old_text not found") {
			raw, _ := stripBom(string(data))
			raw = normalizeToLF(raw)
			lineNum, lineText := closestLine(oldText, raw)
			if lineNum > 0 {
				return "", fmt.Errorf("old_text not found in %s (%d lines). Nearest match at line %d: %q — did you mean this?", path, countLines(raw), lineNum, lineText)
			}
			return "", fmt.Errorf("old_text not found in %s (%d lines)", path, countLines(raw))
		}
		return "", err
	}

	if dryRun, ok := args["dry_run"].(bool); ok && dryRun {
		return formatEditPreview(string(data), updated, path, fuzzy), nil
	}

	if err := e.atomicWriteFile(resolved, updated); err != nil {
		return "", err
	}

	oldLines := strings.Count(oldText, "\n") + 1
	newLines := strings.Count(newText, "\n") + 1
	result := fmt.Sprintf("edited %s: lines %d-%d (%d lines) replaced with %d lines", path, info.startLine, info.endLine, oldLines, newLines)
	if fuzzy {
		result += " (fuzzy match: normalized whitespace/quotes)"
	}
	if showContext > 0 {
		data, err = os.ReadFile(resolved)
		if err == nil {
			result += fmt.Sprintf("\n--- context ---\n%s", formatEditContext(data, info, newLines, showContext))
		}
	}
	return result, nil
}
