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
		path        string
		resolved    string
		oldText     string // for fast-path diff (line count of removed region)
		newText     string // for fast-path diff (line count of added region)
		updated     string
		fuzzy       bool
		matchInfo   matchInfo
		showContext int
		preContent  []byte // pre-mutation bytes for undo snapshot
	}
	var edits []pendingEdit

	// accumulatedContent tracks the evolving content per file so multiple
	// edits to the same file build on each other instead of overwriting.
	accumulatedContent := make(map[string]string)

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

		// Use accumulated content if a previous edit touched this file,
		// otherwise read from disk.
		var data []byte
		if accumulated, ok := accumulatedContent[resolved]; ok {
			data = []byte(accumulated)
		} else {
			data, err = os.ReadFile(resolved)
			if err != nil {
				return nil, fmt.Errorf("edit[%d] %s: %w", i, path, err)
			}
		}

		updated, fuzzy, info, err := applyEdit(data, oldText, newText, fuzzyIndent)
		if err != nil {
			// applyEdit already formats line-number disambiguation via multiMatchError.
			return nil, fmt.Errorf("edit[%d] %s: %w", i, path, err)
		}

		accumulatedContent[resolved] = string(updated)

		edits = append(edits, pendingEdit{
			path:        path,
			resolved:    resolved,
			oldText:     oldText,
			newText:     newText,
			updated:     string(updated),
			fuzzy:       fuzzy,
			matchInfo:   info,
			showContext: showContext,
			preContent:  data,
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
				newLineCount := strings.Count(ed.newText, "\n") + 1
				line += fmt.Sprintf("\n--- context ---\n%s", formatEditContext(data, ed.matchInfo, newLineCount, ed.showContext))
			}
		}
		results = append(results, line)

		// Compute diff via fast-path using known matchInfo (avoids O(m×n) LCS
		// over the full file, which freezes on large files).
		dr := generateEditDiff(string(ed.preContent), ed.updated, ed.path, ed.matchInfo, ed.oldText, ed.newText, 3)
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
