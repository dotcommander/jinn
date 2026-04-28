package jinn

import (
	"fmt"
	"os"
	"strings"
)

func (e *Engine) multiEdit(args map[string]interface{}) (*ToolResult, error) {
	editsRaw, ok := args["edits"].([]interface{})
	if !ok || len(editsRaw) == 0 {
		return nil, fmt.Errorf("edits must be a non-empty array")
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
			return nil, fmt.Errorf("edit[%d]: invalid format", i)
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
			return nil, fmt.Errorf("edit[%d] %s: %w", i, path, err)
		}
		if err := e.tracker.checkStale(resolved); err != nil {
			return nil, fmt.Errorf("edit[%d] %s: %w", i, path, err)
		}

		data, err := os.ReadFile(resolved)
		if err != nil {
			return nil, fmt.Errorf("edit[%d] %s: %w", i, path, err)
		}

		updated, fuzzy, info, err := applyEdit(data, oldText, newText, fuzzyIndent)
		if err != nil {
			// applyEdit already formats line-number disambiguation via multiMatchError.
			return nil, fmt.Errorf("edit[%d] %s: %w", i, path, err)
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
	var allDiffs []string
	var firstLine int
	for _, ed := range edits {
		_ = e.recordSnapshot(ed.resolved, ed.path, "multi_edit", ed.preContent)
		if err := e.atomicWriteFile(ed.resolved, ed.updated); err != nil {
			return nil, fmt.Errorf("%s: %w", ed.path, err)
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

		// Compute diff for structured metadata.
		dr := generateDiff(string(ed.preContent), ed.updated, ed.path, 3)
		if dr.Diff != "" {
			allDiffs = append(allDiffs, dr.Diff)
		}
		if firstLine == 0 && ed.matchInfo.startLine > 0 {
			firstLine = ed.matchInfo.startLine
		}
	}

	meta := map[string]any{
		"edit": editDetails{
			Diff:             strings.Join(allDiffs, "\n"),
			FirstChangedLine: firstLine,
		},
	}

	return &ToolResult{
		Text: fmt.Sprintf("applied %d edits:\n%s", len(edits), strings.Join(results, "\n")),
		Meta: meta,
	}, nil
}
