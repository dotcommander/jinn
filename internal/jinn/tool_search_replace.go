package jinn

import (
	"context"
	"errors"
)

const (
	// srMaxFiles caps the number of files a single search_replace can touch.
	srMaxFiles = 50
	// srMaxFileSize refuses to process individual files larger than this.
	srMaxFileSize = 10 << 20 // 10 MiB
)

// searchReplaceFileResult describes what happened in one file.
type searchReplaceFileResult struct {
	Path       string `json:"path"`
	Matches    int    `json:"matches"`
	Replaced   int    `json:"replaced"`
	Unchanged  bool   `json:"unchanged,omitempty"`
	MatchType  string `json:"matchType,omitempty"`
	FirstLine  int    `json:"firstLine,omitempty"`
	LastLine   int    `json:"lastLine,omitempty"`
	Error      string `json:"error,omitempty"`
	ErrorCode  string `json:"errorCode,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// searchReplaceCandidate holds a file path that matched the glob (and optional path filter).
type searchReplaceCandidate struct {
	path     string // display path
	resolved string // absolute path after security check
}

// searchReplacePending holds a validated, applied search-replace ready for atomic write.
type searchReplacePending struct {
	candidate searchReplaceCandidate
	updated   string
	matches   int
	replaced  int
	firstLine int
	lastLine  int
	preData   []byte
}

// searchReplace is the tool handler for search_replace.
func (e *Engine) searchReplace(ctx context.Context, args map[string]interface{}) (*ToolResult, error) {
	// --- Required arguments ---
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return nil, &ErrWithSuggestion{
			Err:        errors.New("pattern is required"),
			Suggestion: "provide a regex pattern to search for",
			Code:       ErrCodeInvalidArgs,
		}
	}

	replacement, _ := args["replacement"].(string)
	// replacement can be empty (deletion) — that's valid.

	// --- Compile regex with timeout protection ---
	re, err := compileSRRegex(pattern, args)
	if err != nil {
		return nil, err
	}

	// Backtracking protection: limit the regex complexity.
	// We rely on Go's re2 engine which doesn't have catastrophic backtracking,
	// but we still enforce a file size limit.

	// --- Collect target files ---
	candidates, err := e.collectSRFiles(ctx, args)
	if err != nil {
		return nil, err
	}

	// --- Optional: include glob filter ---
	candidates, err = filterSRInclude(candidates, args)
	if err != nil {
		return nil, err
	}

	dryRun := false
	if v, ok := args["dry_run"].(bool); ok && v {
		dryRun = true
	}

	// --- Phase 1: Validate all files (collect-then-report) ---

	var pending []searchReplacePending
	var fileResults []searchReplaceFileResult

	for _, c := range candidates {
		p, fr, ok := e.processSRCandidate(c, re, replacement)
		switch {
		case p != nil:
			pending = append(pending, *p)
		case fr != nil:
			fileResults = append(fileResults, *fr)
		default:
			_ = ok // no match: skip silently (not an error)
		}
	}

	// Report errors from failed files.
	if res, err := srCheckAllFailed(fileResults, pending); res != nil || err != nil {
		return res, err
	}

	// --- Dry run: return preview ---
	if dryRun {
		return srDryRunResult(fileResults, pending), nil
	}

	// --- Phase 2: Apply all changes with per-file atomic writes ---
	return e.srApplyWrites(fileResults, pending)
}
