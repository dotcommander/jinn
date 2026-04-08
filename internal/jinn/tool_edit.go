package jinn

import (
	"fmt"
	"os"
	"strings"
)

func (e *Engine) editFile(args map[string]interface{}) string {
	path, _ := args["path"].(string)
	oldText, _ := args["old_text"].(string)
	newText, _ := args["new_text"].(string)

	resolved, err := e.checkPath(path)
	if err != nil {
		return fmt.Sprintf("[blocked: %s]", err)
	}
	if err := e.tracker.checkStale(resolved); err != nil {
		return fmt.Sprintf("[blocked: %s]", err)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("[error: file not found: %s]", path)
		}
		return fmt.Sprintf("[error: %s]", err)
	}

	content, bom := stripBom(string(data))
	ending := detectLineEnding(content)
	content = normalizeToLF(content)
	oldText = normalizeToLF(oldText)
	newText = normalizeToLF(newText)

	count := strings.Count(content, oldText)
	fuzzy := false
	if count == 0 {
		normContent := normalizeForFuzzyMatch(content)
		normOld := normalizeForFuzzyMatch(oldText)
		count = strings.Count(normContent, normOld)
		if count == 1 {
			content = normContent
			oldText = normOld
			fuzzy = true
		}
	}

	if count == 0 {
		return "[error: old_text not found in file]"
	}
	if count > 1 {
		return fmt.Sprintf("[error: old_text matches %d locations — must be unique. Add surrounding context to disambiguate]", count)
	}

	updated := bom + restoreLineEndings(strings.Replace(content, oldText, newText, 1), ending)

	if err := e.atomicWriteFile(resolved, updated); err != nil {
		return fmt.Sprintf("[error: %s]", err)
	}

	oldLines := strings.Count(oldText, "\n") + 1
	newLines := strings.Count(newText, "\n") + 1
	result := fmt.Sprintf("edited %s: replaced %d lines with %d lines", path, oldLines, newLines)
	if fuzzy {
		result += " (fuzzy match: normalized whitespace/quotes)"
	}
	return result
}
