package jinn

import (
	"fmt"
	"os"
	"strings"
)

func (e *Engine) multiEdit(args map[string]interface{}) string {
	editsRaw, ok := args["edits"].([]interface{})
	if !ok || len(editsRaw) == 0 {
		return "[error: edits must be a non-empty array]"
	}

	type pendingEdit struct {
		path     string
		resolved string
		updated  string
		fuzzy    bool
	}
	var edits []pendingEdit

	// Phase 1: validate all edits before applying any.
	for i, raw := range editsRaw {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Sprintf("[error: edit[%d]: invalid format]", i)
		}
		path, _ := entry["path"].(string)
		oldText, _ := entry["old_text"].(string)
		newText, _ := entry["new_text"].(string)

		resolved, err := e.checkPath(path)
		if err != nil {
			return fmt.Sprintf("[error: edit[%d] %s: %s]", i, path, err)
		}
		if err := e.tracker.checkStale(resolved); err != nil {
			return fmt.Sprintf("[error: edit[%d] %s: %s]", i, path, err)
		}

		data, err := os.ReadFile(resolved)
		if err != nil {
			return fmt.Sprintf("[error: edit[%d] %s: %s]", i, path, err)
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
			return fmt.Sprintf("[error: edit[%d] %s: old_text not found]", i, path)
		}
		if count > 1 {
			return fmt.Sprintf("[error: edit[%d] %s: old_text matches %d locations]", i, path, count)
		}

		updated := bom + restoreLineEndings(strings.Replace(content, oldText, newText, 1), ending)
		edits = append(edits, pendingEdit{
			path:     path,
			resolved: resolved,
			updated:  updated,
			fuzzy:    fuzzy,
		})
	}

	// Phase 2: apply all edits atomically.
	var results []string
	for _, ed := range edits {
		if err := e.atomicWriteFile(ed.resolved, ed.updated); err != nil {
			return fmt.Sprintf("[error: %s: %s]", ed.path, err)
		}
		if ed.fuzzy {
			results = append(results, fmt.Sprintf("edited %s (fuzzy match)", ed.path))
		} else {
			results = append(results, fmt.Sprintf("edited %s", ed.path))
		}
	}

	return fmt.Sprintf("applied %d edits:\n%s", len(edits), strings.Join(results, "\n"))
}
