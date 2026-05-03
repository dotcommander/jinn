package jinn

import (
	"fmt"
	"os"
	"strings"
)

// editStatus records per-edit validation result for collect-then-report.
type editStatus struct {
	File      string `json:"file"`
	EditIndex int    `json:"edit_index"`
	Status    string `json:"status"`
	ErrorCode string `json:"error_code,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (e *Engine) multiEdit(args map[string]interface{}) (*ToolResult, error) {
	editsRaw, ok := args["edits"].([]interface{})
	if !ok || len(editsRaw) == 0 {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("edits must be a non-empty array"),
			Suggestion: "provide a JSON array of edit objects, each with path, old_text, and new_text",
			Code:       ErrCodeInvalidArgs,
		}
	}

	type pendingEdit struct {
		path        string
		resolved    string
		oldText     string // for fast-path diff (line count of removed region)
		newText     string // for fast-path diff (line count of added region)
		fuzzyIndent bool
		updated     string
		fuzzy       bool
		matchInfo   matchInfo
		showContext int
		preContent  []byte // pre-mutation bytes for undo snapshot
		// matchOffset/matchLength are set only when oldText was found in the
		// original (pre-any-edit) file; used for overlap detection.
		matchOffset     int
		matchLength     int
		matchInOriginal bool
	}

	// Phase 1a: parse inputs, check paths, read originals, detect overlaps.
	// Overlap detection requires all match offsets against the original before
	// any accumulated applyEdit runs — so we do a two-pass approach.

	type rawEntry struct {
		idx         int
		path        string
		resolved    string
		oldText     string
		newText     string
		fuzzyIndent bool
		showContext int
		origData    []byte // on-disk bytes (first read per file)
	}
	var rawEntries []rawEntry

	// originalContent stores normalized on-disk bytes for the first read of
	// each file; used for overlap detection.
	originalContent := make(map[string]string)
	// origData caches raw bytes per resolved path for the accumulated phase.
	origDataCache := make(map[string][]byte)

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

		if oldText == "" {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("edits[%d]: old_text cannot be empty", i),
				Suggestion: "provide a non-empty string to match — to insert at file start, include the existing first line in old_text and prepend in new_text",
				Code:       ErrCodeOldTextEmpty,
			}
		}

		resolved, err := e.checkPath(path)
		if err != nil {
			return nil, fmt.Errorf("edit[%d] %s: %w", i, path, err)
		}
		if err := e.tracker.checkStale(resolved); err != nil {
			return nil, fmt.Errorf("edit[%d] %s: %w", i, path, err)
		}

		if _, seen := origDataCache[resolved]; !seen {
			data, err := os.ReadFile(resolved)
			if err != nil {
				return nil, fmt.Errorf("edit[%d] %s: %w", i, path, err)
			}
			origDataCache[resolved] = data
			norm, _ := stripBom(string(data))
			originalContent[resolved] = normalizeToLF(norm)
		}

		rawEntries = append(rawEntries, rawEntry{
			idx:         i,
			path:        path,
			resolved:    resolved,
			oldText:     oldText,
			newText:     newText,
			fuzzyIndent: fuzzyIndent,
			showContext: showContext,
			origData:    origDataCache[resolved],
		})
	}

	// Overlap detection: locate each edit in the original content, then check
	// that no two edits for the same file target overlapping byte ranges.
	// Edits whose oldText does not appear in the original (dependent/chained
	// edits that rely on a prior edit's output) are skipped — they cannot
	// overlap with original-baseline edits by definition.
	type offsetEntry struct {
		editIdx     int
		matchOffset int
		matchLength int
	}
	fileOffsets := make(map[string][]offsetEntry)
	for _, re := range rawEntries {
		origNorm := originalContent[re.resolved]
		oldNorm := normalizeToLF(re.oldText)
		offset := strings.Index(origNorm, oldNorm)
		if offset < 0 {
			origFuzzy := normalizeForFuzzyMatch(origNorm)
			oldFuzzy := normalizeForFuzzyMatch(oldNorm)
			offset = strings.Index(origFuzzy, oldFuzzy)
		}
		if offset >= 0 {
			fileOffsets[re.resolved] = append(fileOffsets[re.resolved], offsetEntry{re.idx, offset, len(oldNorm)})
		}
		// offset < 0 means oldText only exists in accumulated (chained) content — skip overlap check.
	}
	for _, entries := range fileOffsets {
		// Sort by match offset ascending.
		for a := 0; a < len(entries)-1; a++ {
			for b := a + 1; b < len(entries); b++ {
				if entries[a].matchOffset > entries[b].matchOffset {
					entries[a], entries[b] = entries[b], entries[a]
				}
			}
		}
		for k := 0; k < len(entries)-1; k++ {
			prev, curr := entries[k], entries[k+1]
			if prev.matchOffset+prev.matchLength > curr.matchOffset {
				i, j := prev.editIdx, curr.editIdx
				if i > j {
					i, j = j, i
				}
				return nil, &ErrWithSuggestion{
					Err:        fmt.Errorf("edits[%d] and edits[%d] target overlapping regions", i, j),
					Suggestion: "split into separate multi_edit calls, or combine into a single edit covering the full region",
					Code:       ErrCodeEditOverlap,
				}
			}
		}
	}

	// Phase 1b: run applyEdit against accumulated content, validate results.
	// accumulatedContent tracks the evolving content per file so multiple
	// edits to the same file build on each other instead of overwriting.
	accumulatedContent := make(map[string]string)
	var edits []pendingEdit
	var statuses []editStatus

	for _, re := range rawEntries {
		var data []byte
		if accumulated, ok := accumulatedContent[re.resolved]; ok {
			data = []byte(accumulated)
		} else {
			data = re.origData
		}

		updated, fuzzy, info, err := applyEdit(data, re.oldText, re.newText, re.fuzzyIndent)
		if err != nil {
			statuses = append(statuses, editStatus{
				File:      re.path,
				EditIndex: re.idx,
				Status:    "error",
				ErrorCode: ErrCodeEditNotFound,
				Error:     err.Error(),
			})
			continue
		}

		if updated == string(data) {
			statuses = append(statuses, editStatus{
				File:      re.path,
				EditIndex: re.idx,
				Status:    "error",
				ErrorCode: ErrCodeEditNoChange,
				Error:     fmt.Sprintf("edits[%d] %s: edit produced no changes", re.idx, re.path),
			})
			continue
		}

		accumulatedContent[re.resolved] = updated

		edits = append(edits, pendingEdit{
			path:        re.path,
			resolved:    re.resolved,
			oldText:     re.oldText,
			newText:     re.newText,
			updated:     updated,
			fuzzy:       fuzzy,
			matchInfo:   info,
			showContext: re.showContext,
			preContent:  data,
		})
	}

	// If any edits failed validation, return collected statuses.
	if len(statuses) > 0 {
		var errMsgs []string
		for _, s := range statuses {
			errMsgs = append(errMsgs, fmt.Sprintf("  edits[%d] %s: %s", s.EditIndex, s.File, s.Error))
		}
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("%d of %d edits failed validation:\n%s", len(statuses), len(rawEntries), strings.Join(errMsgs, "\n")),
			Suggestion: "fix the reported edit(s) and retry — other edits in the batch were skipped",
			Code:       ErrCodeEditNotFound,
		}
	}

	// dry_run: return previews without writing.
	if dryRun, ok := args["dry_run"].(bool); ok && dryRun {
		var previews []string
		for _, ed := range edits {
			dr := generateEditDiff(string(ed.preContent), ed.updated, ed.path, ed.matchInfo, ed.oldText, ed.newText, 3)
			if dr.Diff != "" {
				previews = append(previews, dr.Diff)
			}
		}
		return &ToolResult{
			Text: fmt.Sprintf("[dry-run] %d edits validated:\n%s", len(edits), strings.Join(previews, "\n")),
		}, nil
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

	// Build aggregate editDetails from all edits.
	var lastLine int
	var matchType string
	var fuzzyNormalized string
	for _, ed := range edits {
		newLineCount := strings.Count(ed.newText, "\n") + 1
		if lcl := ed.matchInfo.startLine + newLineCount - 1; lcl > lastLine {
			lastLine = lcl
		}
		if ed.fuzzy {
			matchType = "fuzzy"
			fuzzyNormalized = "whitespace_and_quotes"
		} else if matchType == "" {
			matchType = "exact"
		}
	}

	meta := map[string]any{
		"edit": editDetails{
			Diff:             strings.Join(allDiffs, "\n"),
			FirstChangedLine: firstLine,
			LastChangedLine:  lastLine,
			MatchType:        matchType,
			FuzzyNormalized:  fuzzyNormalized,
		},
	}

	return &ToolResult{
		Text: fmt.Sprintf("applied %d edits:\n%s", len(edits), strings.Join(results, "\n")),
		Meta: meta,
	}, nil
}
