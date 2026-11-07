package jinn

import (
	"errors"
	"fmt"
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

// batchStatusError aggregates per-edit statuses into one preflight rejection
// that preserves every member's code and reason. Publishing never begins
// with a non-empty status list.
func batchStatusError(statuses []editStatus, total int) error {
	if len(statuses) == 0 {
		return nil
	}
	messages := make([]string, len(statuses))
	for i, s := range statuses {
		messages[i] = fmt.Sprintf("  edits[%d] %s [%s]: %s", s.EditIndex, s.File, s.ErrorCode, s.Error)
	}
	return &ErrWithSuggestion{
		Err:        fmt.Errorf("%d of %d edits failed validation:\n%s", len(statuses), total, strings.Join(messages, "\n")),
		Suggestion: "fix the reported edit(s) and retry — no edits in the batch were written",
		Code:       statuses[0].ErrorCode,
	}
}

// parseAndResolveEdits iterates the raw edits array, validates each entry
// (empty old_text guard, path security checks, stale check), reads each
// file's on-disk bytes once, and returns the resolved entries along with
// the normalized original content map used for overlap detection.
//
// Validation is exhaustive: every member's failure is collected into
// statuses (one code per member) instead of failing fast, so callers can
// reject the whole batch with per-file reasons and publish nothing.
func (e *Engine) parseAndResolveEdits(editsRaw []interface{}, requireChecksums bool) (
	entries []rawEntry,
	originalContent map[string]string,
	statuses []editStatus,
) {
	originalContent = make(map[string]string)
	origDataCache := make(map[string][]byte)
	fail := func(idx int, file string, err error) {
		code := ErrCodeConflict
		var sErr *ErrWithSuggestion
		if errors.As(err, &sErr) && sErr.Code != "" {
			code = sErr.Code
		}
		statuses = append(statuses, editStatus{
			File: file, EditIndex: idx, Status: "error", ErrorCode: code, Error: err.Error(),
		})
	}

	for i, raw := range editsRaw {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			fail(i, "", fmt.Errorf("edit[%d]: invalid format", i))
			continue
		}
		path, _ := entry["path"].(string)
		oldText, _ := entry["old_text"].(string)
		newText, _ := entry["new_text"].(string)
		fuzzyIndent, _ := entry["fuzzy_indent"].(bool)
		showContext := intArg(entry, "show_context", 0)

		if oldText == "" {
			fail(i, path, &ErrWithSuggestion{
				Err:        fmt.Errorf("edits[%d]: old_text cannot be empty", i),
				Suggestion: "provide a non-empty string to match — to insert at file start, include the existing first line in old_text and prepend in new_text",
				Code:       ErrCodeOldTextEmpty,
			})
			continue
		}

		resolved, err := e.checkPathForMutation(path)
		if err != nil {
			fail(i, path, fmt.Errorf("edit[%d] %s: %w", i, path, err))
			continue
		}
		if err := e.tracker.checkStale(resolved); err != nil {
			fail(i, path, fmt.Errorf("edit[%d] %s: %w", i, path, err))
			continue
		}

		if _, seen := origDataCache[resolved]; !seen {
			data, _, err := e.readRegularFile(resolved, maxFileSize)
			if err != nil {
				fail(i, path, fmt.Errorf("edit[%d] %s: %w", i, path, err))
				continue
			}
			origDataCache[resolved] = data
			norm, _ := stripBom(string(data))
			originalContent[resolved] = norm
		}
		if err := verifyChecksum(strArg(entry, "if_checksum"), path, origDataCache[resolved], true); err != nil {
			fail(i, path, err)
			continue
		}
		if requireChecksums && strArg(entry, "if_checksum") == "" {
			fail(i, path, &ErrWithSuggestion{Err: fmt.Errorf("edits[%d]: if_checksum is required for %s", i, path), Suggestion: "read each file with include_checksum=true and include its digest in every edit entry", Code: ErrCodeInvalidArgs})
			continue
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
	return entries, originalContent, statuses
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
	var targets []string
	for i, raw := range editsRaw {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("edit[%d]: invalid format", i)
		}
		resolved, err := e.checkPathForMutation(strArg(entry, "path"))
		if err != nil {
			return nil, fmt.Errorf("edit[%d]: %w", i, err)
		}
		targets = append(targets, resolved)
	}
	var result *ToolResult
	err := e.withTargetLocks(targets, func() error {
		var innerErr error
		result, innerErr = e.multiEditLocked(args, editsRaw)
		return innerErr
	})
	return result, err
}

func (e *Engine) multiEditLocked(args map[string]interface{}, editsRaw []interface{}) (*ToolResult, error) {

	// Phase 1a: parse inputs, check paths, read originals. Every member is
	// validated; any failure rejects the whole batch publishing nothing.
	rawEntries, originalContent, statuses := e.parseAndResolveEdits(editsRaw, e.requireMutationPreconditions && !boolArg(args, "dry_run"))
	if err := batchStatusError(statuses, len(editsRaw)); err != nil {
		return nil, err
	}

	// Phase 1b: overlap detection + positional sorting.
	rawEntries, err := detectOverlaps(rawEntries, originalContent)
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
