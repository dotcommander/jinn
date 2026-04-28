package jinn

import (
	"fmt"
	"os"
	"strings"
)

// editDetails carries structured metadata about an edit operation.
type editDetails struct {
	Diff             string `json:"diff"`
	FirstChangedLine int    `json:"firstChangedLine,omitempty"`
}

// matchInfo carries metadata about where old_text was found in the file.
type matchInfo struct {
	startLine int // 1-based line number where the match begins
	endLine   int // 1-based line number where the match ends
}

// collectMatchLines returns 1-based line numbers for every occurrence of
// needle in haystack, capped at maxMatches. If the total exceeds maxMatches
// the returned slice has exactly maxMatches entries and overflow > 0.
func collectMatchLines(haystack, needle string, maxMatches int) (lines []int, overflow int) {
	pos := 0
	total := 0
	for {
		idx := strings.Index(haystack[pos:], needle)
		if idx < 0 {
			break
		}
		absIdx := pos + idx
		total++
		if total <= maxMatches {
			lineNum := strings.Count(haystack[:absIdx], "\n") + 1
			lines = append(lines, lineNum)
		}
		pos = absIdx + len(needle)
		if pos >= len(haystack) {
			break
		}
	}
	overflow = total - len(lines)
	return lines, overflow
}

// multiMatchError builds the "matches N locations (lines: …)" error message.
// Cap at 10 line numbers; append "... and K more" when the total exceeds 10.
func multiMatchError(count int, haystack, needle string) error {
	const cap = 10
	lines, overflow := collectMatchLines(haystack, needle, cap)
	nums := make([]string, len(lines))
	for i, l := range lines {
		nums[i] = fmt.Sprintf("%d", l)
	}
	lineList := strings.Join(nums, ", ")
	msg := fmt.Sprintf("old_text matches %d locations (lines: %s)", count, lineList)
	if overflow > 0 {
		msg += fmt.Sprintf(" ... and %d more", overflow)
	}
	msg += " — must be unique. Add surrounding context to disambiguate"
	return fmt.Errorf("%s", msg)
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
		return "", false, info, multiMatchError(count, raw, oldText)
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

func (e *Engine) editFile(args map[string]interface{}) (*ToolResult, error) {
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
		return nil, err
	}
	if err := e.tracker.checkStale(resolved); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", path)
		}
		return nil, err
	}

	updated, fuzzy, info, err := applyEdit(data, oldText, newText, fuzzyIndent)
	if err != nil {
		if strings.Contains(err.Error(), "old_text not found") {
			raw, _ := stripBom(string(data))
			raw = normalizeToLF(raw)
			lineNum, lineText := closestLine(oldText, raw)
			if lineNum > 0 {
				return nil, fmt.Errorf("old_text not found in %s (%d lines). Nearest match at line %d: %q — did you mean this?", path, countLines(raw), lineNum, lineText)
			}
			return nil, fmt.Errorf("old_text not found in %s (%d lines)", path, countLines(raw))
		}
		return nil, err
	}

	// Compute diff for structured metadata.
	dr := generateDiff(string(data), updated, path, 3)

	if dryRun, ok := args["dry_run"].(bool); ok && dryRun {
		preview := formatEditPreview(string(data), updated, path, fuzzy)
		return &ToolResult{
			Text: preview,
			Meta: map[string]any{
				"edit": editDetails{
					Diff:             dr.Diff,
					FirstChangedLine: info.startLine,
				},
			},
		}, nil
	}

	_ = e.recordSnapshot(resolved, path, "edit_file", data)

	if err := e.atomicWriteFile(resolved, updated); err != nil {
		return nil, err
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

	return &ToolResult{
		Text: result,
		Meta: map[string]any{
			"edit": editDetails{
				Diff:             dr.Diff,
				FirstChangedLine: info.startLine,
			},
		},
	}, nil
}
