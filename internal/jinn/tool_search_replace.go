package jinn

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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
	var result *ToolResult
	err := withFileLock(e.mutationLockPath(), func() error {
		var innerErr error
		result, innerErr = e.searchReplaceDiscovered(ctx, args)
		return innerErr
	})
	return result, err
}

func (e *Engine) searchReplaceDiscovered(ctx context.Context, args map[string]interface{}) (*ToolResult, error) {
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

	dryRun := boolArg(args, "dry_run")
	targets := make([]string, len(candidates))
	for i := range candidates {
		targets[i] = candidates[i].resolved
	}
	var result *ToolResult
	err = e.withTargetLocksOnly(targets, func() error {
		var innerErr error
		result, innerErr = e.searchReplaceLocked(args, candidates, re, replacement, dryRun)
		return innerErr
	})
	return result, err
}

func (e *Engine) searchReplaceLocked(args map[string]interface{}, candidates []searchReplaceCandidate, re *regexp.Regexp, replacement string, dryRun bool) (*ToolResult, error) {

	// --- Phase 1: Validate all files (collect-then-report) ---

	var pending []searchReplacePending
	var fileResults []searchReplaceFileResult

	for _, c := range candidates {
		checksum := checksumForTarget(args, c.path)
		p, fr, ok := e.processSRCandidate(c, re, replacement, checksum)
		switch {
		case p != nil:
			pending = append(pending, *p)
		case fr != nil:
			fileResults = append(fileResults, *fr)
		default:
			_ = ok // no match: skip silently (not an error)
		}
	}
	if e.requireMutationPreconditions && !dryRun {
		for _, p := range pending {
			if checksumForTarget(args, p.candidate.path) == "" {
				return nil, &ErrWithSuggestion{Err: fmt.Errorf("if_checksums[%q] is required", p.candidate.path), Suggestion: "read every changed file with include_checksum=true and supply its digest", Code: ErrCodeInvalidArgs}
			}
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

func checksumForTarget(args map[string]interface{}, path string) string {
	checksums, _ := args["if_checksums"].(map[string]interface{})
	want, _ := checksums[path].(string)
	return want
}
