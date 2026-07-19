package jinn

import (
	"fmt"
	"os"
	"strings"
)

// isRedundantNotFound reports whether an applyEdit "not found" error should be
// skipped because the same old_text→new_text pair was already applied to this file
// (the model likely over-counted occurrences).
func isRedundantNotFound(err error, pairs map[pairKey]int, key pairKey) bool {
	if !strings.Contains(err.Error(), "old_text not found") {
		return false
	}
	return pairs[key] > 0
}

// applyPendingEdits runs applyEdit against accumulated per-file content for
// each rawEntry, collects successes into pendingEdit slice and failures into
// editStatus slice, then returns an error if any validation failures occurred.
// It does NOT write files; all writes are deferred to the caller.
func applyPendingEdits(rawEntries []rawEntry) ([]pendingEdit, error) {
	accumulatedContent := make(map[string]string)
	var edits []pendingEdit
	var statuses []editStatus

	// Track applied old_text→new_text pairs per file for redundant edit detection.
	appliedPairs := make(map[string]map[pairKey]int) // resolved -> pair -> count applied

	for _, re := range rawEntries {
		var data []byte
		if accumulated, ok := accumulatedContent[re.resolved]; ok {
			data = []byte(accumulated)
		} else {
			data = re.origData
		}

		updated, fuzzy, info, err := applyEdit(data, re.oldText, re.newText, re.fuzzyIndent)
		if err != nil {
			// Redundant edit skip: if the same old_text→new_text pair was already
			// applied in this file, the model likely over-counted occurrences.
			// Skip gracefully instead of aborting the entire batch.
			if isRedundantNotFound(err, appliedPairs[re.resolved], pairKey{re.oldText, re.newText}) {
				continue
			}
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

		// Record applied pair for redundant detection.
		if appliedPairs[re.resolved] == nil {
			appliedPairs[re.resolved] = make(map[pairKey]int)
		}
		appliedPairs[re.resolved][pairKey{re.oldText, re.newText}]++

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

	return edits, nil
}

// writeResult aggregates the per-edit output of writePendingEdits.
type writeResult struct {
	results   []string
	firstLine int
	allDiffs  []string
}

// dryRunResult builds the [dry-run] preview ToolResult without writing files.
func dryRunResult(edits []pendingEdit) *ToolResult {
	var previews []string
	for _, ed := range edits {
		dr := generateEditDiff(string(ed.preContent), ed.updated, ed.path, ed.matchInfo, ed.oldText, ed.newText, 3)
		if dr.Diff != "" {
			previews = append(previews, dr.Diff)
		}
	}
	return &ToolResult{
		Text: fmt.Sprintf("[dry-run] %d edits validated:\n%s", len(edits), strings.Join(previews, "\n")),
	}
}

// writePendingEdits records snapshots and atomically writes each edit in order,
// returning per-edit result lines, the first changed line, and collected diffs.
func (e *Engine) writePendingEdits(edits []pendingEdit) (writeResult, error) {
	var wr writeResult
	var applied []appliedRef
	for _, ed := range edits {
		if err := verifyPreflightState(ed.resolved, ed.preContent, true); err != nil {
			return writeResult{}, partialApplyErr("multi_edit", applied, len(edits), fmt.Errorf("%s: %w", ed.path, err))
		}
		id, werr := e.snapshotAndWrite(ed.resolved, ed.path, "multi_edit", ed.preContent, ed.updated)
		if werr != nil {
			return writeResult{}, partialApplyErr("multi_edit", applied, len(edits), fmt.Errorf("%s: %w", ed.path, werr))
		}
		applied = append(applied, appliedRef{path: ed.path, undoID: id})
		line := fmt.Sprintf("edited %s", ed.path)
		if ed.fuzzy {
			line += " (fuzzy match)"
		}
		if ed.showContext > 0 {
			if data, rerr := os.ReadFile(ed.resolved); rerr == nil {
				newLineCount := strings.Count(ed.newText, "\n") + 1
				line += fmt.Sprintf("\n--- context ---\n%s", formatEditContext(data, ed.matchInfo, newLineCount, ed.showContext))
			}
		}
		wr.results = append(wr.results, line)

		// Compute diff via fast-path using known matchInfo (avoids O(m×n) LCS
		// over the full file, which freezes on large files).
		dr := generateEditDiff(string(ed.preContent), ed.updated, ed.path, ed.matchInfo, ed.oldText, ed.newText, 3)
		if dr.Diff != "" {
			wr.allDiffs = append(wr.allDiffs, dr.Diff)
		}
		if wr.firstLine == 0 && ed.matchInfo.startLine > 0 {
			wr.firstLine = ed.matchInfo.startLine
		}
	}
	return wr, nil
}

// buildEditDetails aggregates per-edit match info into the editDetails summary.
func buildEditDetails(edits []pendingEdit, firstLine int, allDiffs []string) editDetails {
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
	return editDetails{
		Diff:             strings.Join(allDiffs, "\n"),
		FirstChangedLine: firstLine,
		LastChangedLine:  lastLine,
		MatchType:        matchType,
		FuzzyNormalized:  fuzzyNormalized,
	}
}
