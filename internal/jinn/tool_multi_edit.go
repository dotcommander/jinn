package jinn

import (
	"errors"
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

// rawEntry holds parsed, path-resolved, and file-read data for one edit before application.
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

// pendingEdit holds a validated, applied edit ready for atomic write.
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

// parseAndResolveEdits iterates the raw edits array, validates each entry
// (empty old_text guard, path security checks, stale check), reads each
// file's on-disk bytes once, and returns the resolved entries along with
// the normalized original content map used for overlap detection.
func (e *Engine) parseAndResolveEdits(editsRaw []interface{}) (
	entries []rawEntry,
	originalContent map[string]string,
	err error,
) {
	originalContent = make(map[string]string)
	origDataCache := make(map[string][]byte)

	for i, raw := range editsRaw {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			return nil, nil, fmt.Errorf("edit[%d]: invalid format", i)
		}
		path, _ := entry["path"].(string)
		oldText, _ := entry["old_text"].(string)
		newText, _ := entry["new_text"].(string)
		fuzzyIndent, _ := entry["fuzzy_indent"].(bool)
		showContext := intArg(entry, "show_context", 0)

		if oldText == "" {
			return nil, nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("edits[%d]: old_text cannot be empty", i),
				Suggestion: "provide a non-empty string to match — to insert at file start, include the existing first line in old_text and prepend in new_text",
				Code:       ErrCodeOldTextEmpty,
			}
		}

		resolved, err := e.checkPath(path)
		if err != nil {
			return nil, nil, fmt.Errorf("edit[%d] %s: %w", i, path, err)
		}
		if err := e.tracker.checkStale(resolved); err != nil {
			return nil, nil, fmt.Errorf("edit[%d] %s: %w", i, path, err)
		}

		if _, seen := origDataCache[resolved]; !seen {
			data, err := os.ReadFile(resolved)
			if err != nil {
				return nil, nil, fmt.Errorf("edit[%d] %s: %w", i, path, err)
			}
			origDataCache[resolved] = data
			norm, _ := stripBom(string(data))
			originalContent[resolved] = normalizeToLF(norm)
		}

		entries = append(entries, rawEntry{
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
	return entries, originalContent, nil
}

func (e *Engine) multiEdit(args map[string]interface{}) (*ToolResult, error) {
	editsRaw, ok := args["edits"].([]interface{})
	if !ok || len(editsRaw) == 0 {
		return nil, &ErrWithSuggestion{
			Err:        errors.New("edits must be a non-empty array"),
			Suggestion: "provide a JSON array of edit objects, each with path, old_text, and new_text",
			Code:       ErrCodeInvalidArgs,
		}
	}

	// Phase 1a: parse inputs, check paths, read originals.
	rawEntries, originalContent, err := e.parseAndResolveEdits(editsRaw)
	if err != nil {
		return nil, err
	}

	// Phase 1b: overlap detection + positional sorting.
	rawEntries, err = detectOverlaps(rawEntries, originalContent)
	if err != nil {
		return nil, err
	}

	// Phase 1c: run applyEdit against accumulated content, validate results.
	edits, err := applyPendingEdits(rawEntries)
	if err != nil {
		return nil, err
	}

	// dry_run: return previews without writing.
	if boolArg(args, "dry_run") {
		return dryRunResult(edits), nil
	}

	// Phase 2: apply all edits with per-file atomic writes.
	wr, err := e.writePendingEdits(edits)
	if err != nil {
		return nil, err
	}

	meta := map[string]any{
		"edit": buildEditDetails(edits, wr.firstLine, wr.allDiffs),
	}

	return &ToolResult{
		Text: fmt.Sprintf("applied %d edits:\n%s", len(edits), strings.Join(wr.results, "\n")),
		Meta: meta,
	}, nil
}
