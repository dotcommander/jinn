package jinn

import (
	"fmt"
	"os"
	"strings"
)

func applyEdit(content []byte, oldText, newText string) (string, bool, error) {
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
		return "", false, fmt.Errorf("old_text not found in file")
	}
	if count > 1 {
		return "", false, fmt.Errorf("old_text matches %d locations — must be unique. Add surrounding context to disambiguate", count)
	}
	return bom + restoreLineEndings(strings.Replace(raw, oldText, newText, 1), ending), fuzzy, nil
}

func (e *Engine) editFile(args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	oldText, _ := args["old_text"].(string)
	newText, _ := args["new_text"].(string)

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

	updated, fuzzy, err := applyEdit(data, oldText, newText)
	if err != nil {
		return "", err
	}

	if err := e.atomicWriteFile(resolved, updated); err != nil {
		return "", err
	}

	oldLines := strings.Count(oldText, "\n") + 1
	newLines := strings.Count(newText, "\n") + 1
	result := fmt.Sprintf("edited %s: replaced %d lines with %d lines", path, oldLines, newLines)
	if fuzzy {
		result += " (fuzzy match: normalized whitespace/quotes)"
	}
	return result, nil
}
