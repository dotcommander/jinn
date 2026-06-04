package jinn

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// editDetails carries structured metadata about an edit operation.
type editDetails struct {
	Diff             string `json:"diff"`
	FirstChangedLine int    `json:"firstChangedLine,omitempty"`
	LastChangedLine  int    `json:"lastChangedLine,omitempty"`
	MatchType        string `json:"matchType,omitempty"`
	FuzzyNormalized  string `json:"fuzzyNormalized,omitempty"`
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
		nums[i] = strconv.Itoa(l)
	}
	lineList := strings.Join(nums, ", ")
	msg := fmt.Sprintf("old_text matches %d locations (lines: %s)", count, lineList)
	if overflow > 0 {
		msg += fmt.Sprintf(" ... and %d more", overflow)
	}
	msg += " — must be unique. Add surrounding context to disambiguate"
	return fmt.Errorf("%s", msg)
}

func formatEditContext(content []byte, info matchInfo, newLines int, showContext int) string {
	lines := strings.Split(string(content), "\n")
	total := len(lines)
	if lines[total-1] == "" {
		total--
	}
	width := len(strconv.Itoa(total))

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

// mapApplyEditError converts an applyEdit error into a suggestion-bearing error,
// adding nearest-line hints for not-found and disambiguation hints for multi-match.
func mapApplyEditError(err error, path, oldText string, data []byte) error {
	if strings.Contains(err.Error(), "old_text not found") {
		raw, _ := stripBom(string(data))
		raw = normalizeToLF(raw)
		lineNum, lineText := closestLine(oldText, raw)
		if lineNum > 0 {
			return &ErrWithSuggestion{
				Err:        fmt.Errorf("old_text not found in %s (%d lines). Nearest match at line %d: %q — did you mean this?", path, countLines(raw), lineNum, lineText),
				Suggestion: "re-read the file to get current content, then retry with exact text",
				Code:       ErrCodeEditNotFound,
			}
		}
		return &ErrWithSuggestion{
			Err:        fmt.Errorf("old_text not found in %s (%d lines)", path, countLines(raw)),
			Suggestion: "re-read the file to get current content, then retry with exact text",
			Code:       ErrCodeEditNotFound,
		}
	}
	if strings.Contains(err.Error(), "matches") && strings.Contains(err.Error(), "locations") {
		return &ErrWithSuggestion{
			Err:        err,
			Suggestion: "add more surrounding lines to old_text to disambiguate the match",
			Code:       ErrCodeEditNotUnique,
		}
	}
	return err
}

// singleEditDetails constructs the structured edit metadata shared by the dry-run
// and live result envelopes.
func singleEditDetails(diff string, info matchInfo, fuzzy bool, newText string) editDetails {
	matchType := "exact"
	fuzzyNormalized := ""
	if fuzzy {
		matchType = "fuzzy"
		fuzzyNormalized = "whitespace_and_quotes"
	}
	newLineCount := strings.Count(newText, "\n") + 1
	return editDetails{
		Diff:             diff,
		FirstChangedLine: info.startLine,
		LastChangedLine:  info.startLine + newLineCount - 1,
		MatchType:        matchType,
		FuzzyNormalized:  fuzzyNormalized,
	}
}

// loadEditTarget validates old_text, resolves the path, checks staleness and
// reads the file, returning the resolved path and current content.
func (e *Engine) loadEditTarget(path, oldText string) (resolved string, data []byte, err error) {
	if oldText == "" {
		return "", nil, &ErrWithSuggestion{
			Err:        errors.New("old_text cannot be empty"),
			Suggestion: "provide a non-empty string to match — to insert at file start, include the existing first line in old_text and prepend in new_text",
			Code:       ErrCodeOldTextEmpty,
		}
	}

	resolved, err = e.checkPath(path)
	if err != nil {
		return "", nil, err
	}
	if staleErr := e.tracker.checkStale(resolved); staleErr != nil {
		return "", nil, staleErr
	}

	data, err = os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("file not found: %s", path),
				Suggestion: "verify the path exists with list_dir or check for typos",
				Code:       ErrCodeFileNotFound,
			}
		}
		return "", nil, err
	}
	return resolved, data, nil
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

	resolved, data, err := e.loadEditTarget(path, oldText)
	if err != nil {
		return nil, err
	}

	updated, fuzzy, info, err := applyEdit(data, oldText, newText, fuzzyIndent)
	if err != nil {
		return nil, mapApplyEditError(err, path, oldText, data)
	}

	if updated == string(data) {
		return nil, &ErrWithSuggestion{
			Err:        errors.New("edit produced no changes"),
			Suggestion: "old_text and new_text are equivalent (possibly after fuzzy normalization) — verify the intended change",
			Code:       ErrCodeEditNoChange,
		}
	}

	// Compute diff for structured metadata using fast-path (known region).
	dr := generateEditDiff(string(data), updated, path, info, oldText, newText, 3)
	details := singleEditDetails(dr.Diff, info, fuzzy, newText)

	if dryRun, ok := args["dry_run"].(bool); ok && dryRun {
		preview := formatEditPreview(string(data), updated, path, fuzzy)
		return &ToolResult{
			Text: preview,
			Meta: map[string]any{"edit": details},
		}, nil
	}

	_ = e.recordSnapshot(resolved, path, "edit_file", data)

	if writeErr := e.atomicWriteFile(resolved, updated); writeErr != nil {
		return nil, writeErr
	}

	result := editResultText(editResultParams{
		resolved: resolved, path: path, oldText: oldText, newText: newText,
		info: info, fuzzy: fuzzy, showContext: showContext,
	})
	return &ToolResult{
		Text: result,
		Meta: map[string]any{"edit": details},
	}, nil
}

// editResultParams bundles the inputs for the live-edit summary line.
type editResultParams struct {
	resolved    string
	path        string
	oldText     string
	newText     string
	info        matchInfo
	fuzzy       bool
	showContext int
}

// editResultText builds the human-readable summary for a live edit, optionally
// appending a re-read context snippet when showContext > 0.
func editResultText(p editResultParams) string {
	oldLines := strings.Count(p.oldText, "\n") + 1
	newLines := strings.Count(p.newText, "\n") + 1
	result := fmt.Sprintf("edited %s: lines %d-%d (%d lines) replaced with %d lines", p.path, p.info.startLine, p.info.endLine, oldLines, newLines)
	if p.fuzzy {
		result += " (fuzzy match: normalized whitespace/quotes)"
	}
	if p.showContext > 0 {
		if data, err := os.ReadFile(p.resolved); err == nil {
			result += fmt.Sprintf("\n--- context ---\n%s", formatEditContext(data, p.info, newLines, p.showContext))
		}
	}
	return result
}
