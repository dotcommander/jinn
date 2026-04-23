package jinn

import (
	"fmt"
	"os"
	"strings"
)

func (e *Engine) multiEdit(args map[string]interface{}) (string, error) {
	editsRaw, ok := args["edits"].([]interface{})
	if !ok || len(editsRaw) == 0 {
		return "", fmt.Errorf("edits must be a non-empty array")
	}

	type pendingEdit struct {
		path         string
		resolved     string
		updated      string
		fuzzy        bool
		matchInfo    matchInfo
		showContext  int
		newLineCount int    // lines in new_text, for show_context marker
		preContent   []byte // pre-mutation bytes for undo snapshot
	}
	var edits []pendingEdit

	// Phase 1: validate all edits before applying any.
	for i, raw := range editsRaw {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("edit[%d]: invalid format", i)
		}
		path, _ := entry["path"].(string)
		oldText, _ := entry["old_text"].(string)
		newText, _ := entry["new_text"].(string)
		fuzzyIndent, _ := entry["fuzzy_indent"].(bool)
		showContext := 0
		if v, ok := entry["show_context"].(float64); ok && v > 0 {
			showContext = int(v)
		}

		resolved, err := e.checkPath(path)
		if err != nil {
			return "", fmt.Errorf("edit[%d] %s: %s", i, path, err)
		}
		if err := e.tracker.checkStale(resolved); err != nil {
			return "", fmt.Errorf("edit[%d] %s: %s", i, path, err)
		}

		data, err := os.ReadFile(resolved)
		if err != nil {
			return "", fmt.Errorf("edit[%d] %s: %s", i, path, err)
		}

		updated, fuzzy, info, err := applyEdit(data, oldText, newText, fuzzyIndent)
		if err != nil {
			// applyEdit already formats line-number disambiguation via multiMatchError.
			return "", fmt.Errorf("edit[%d] %s: %w", i, path, err)
		}

		edits = append(edits, pendingEdit{
			path:         path,
			resolved:     resolved,
			updated:      updated,
			fuzzy:        fuzzy,
			matchInfo:    info,
			showContext:  showContext,
			newLineCount: strings.Count(newText, "\n") + 1,
			preContent:   data,
		})
	}

	// Phase 2: apply all edits atomically.
	var results []string
	for _, ed := range edits {
		_ = e.recordSnapshot(ed.resolved, ed.path, "multi_edit", ed.preContent)
		if err := e.atomicWriteFile(ed.resolved, ed.updated); err != nil {
			return "", fmt.Errorf("%s: %s", ed.path, err)
		}
		line := fmt.Sprintf("edited %s", ed.path)
		if ed.fuzzy {
			line += " (fuzzy match)"
		}
		if ed.showContext > 0 {
			if data, err := os.ReadFile(ed.resolved); err == nil {
				line += fmt.Sprintf("\n--- context ---\n%s", formatEditContext(data, ed.matchInfo, ed.newLineCount, ed.showContext))
			}
		}
		results = append(results, line)
	}

	return fmt.Sprintf("applied %d edits:\n%s", len(edits), strings.Join(results, "\n")), nil
}
