package jinn

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// srApplyResult holds the outcome of applying a regex replacement to one file.
// updated is empty when there were no matches or the replacement was a no-op.
type srApplyResult struct {
	updated   string
	matches   int
	replaced  int
	firstLine int
	lastLine  int
}

// srApplyOne applies the regex replacement to a single file.
// Returns the result content (if changed), match count, replace count, and any error.
func srApplyOne(content []byte, re *regexp.Regexp, replacement string) (srApplyResult, error) { //nolint:unparam // error return reserved: caller handles per-file failures uniformly via applyErr branch
	raw, bom := stripBom(string(content))
	ending := detectLineEnding(raw)
	raw = normalizeToLF(raw)

	// Count matches before replacing.
	locs := re.FindAllStringIndex(raw, -1)
	matches := len(locs)
	if matches == 0 {
		return srApplyResult{}, nil
	}

	// Apply replacement.
	result := re.ReplaceAllString(raw, replacement)
	replaced := matches // ReplaceAll replaces every match

	if result == raw {
		return srApplyResult{matches: matches}, nil // replacement was a no-op
	}

	// Find first and last changed lines.
	firstMatch := locs[0]
	lastMatch := locs[len(locs)-1]
	firstLine := strings.Count(raw[:firstMatch[0]], "\n") + 1
	lastLine := strings.Count(raw[:lastMatch[1]], "\n") + 1

	return srApplyResult{updated: bom + restoreLineEndings(result, ending), matches: matches, replaced: replaced, firstLine: firstLine, lastLine: lastLine}, nil
}

// processSRCandidate validates and applies the replacement to one candidate.
// Exactly one of the returns is meaningful:
//   - (*srPending, nil, _): a change ready to apply
//   - (nil, *srFileResult, _): a per-file error or no-op to report
//   - (nil, nil, _): no match in this file — skip silently
//
// srReadCandidate stats and reads a candidate file, returning its content. If the
// file cannot be read or should be skipped (missing, too large, binary), it returns
// nil content and a populated srFileResult describing the skip reason.
func (e *Engine) srReadCandidate(c srCandidate) ([]byte, *srFileResult) {
	info, statErr := os.Stat(c.resolved)
	if statErr != nil {
		return nil, &srFileResult{
			Path:      c.path,
			Error:     statErr.Error(),
			ErrorCode: ErrCodeFileNotFound,
		}
	}

	// Skip binary files.
	if info.Size() > srMaxFileSize {
		return nil, &srFileResult{
			Path:       c.path,
			Error:      fmt.Sprintf("file too large (%d bytes, max %d)", info.Size(), srMaxFileSize),
			ErrorCode:  ErrCodeFileTooLarge,
			Suggestion: "use edit_file with exact text for large files",
		}
	}

	data, readErr := os.ReadFile(c.resolved)
	if readErr != nil {
		return nil, &srFileResult{
			Path:      c.path,
			Error:     readErr.Error(),
			ErrorCode: ErrCodeFileNotFound,
		}
	}

	// Skip binary files (simple heuristic: null byte in first 8KB).
	checkLen := len(data)
	if checkLen > 8192 {
		checkLen = 8192
	}
	if isBinaryContent(data[:checkLen]) {
		return nil, &srFileResult{
			Path:       c.path,
			Error:      "binary file detected, skipping",
			ErrorCode:  ErrCodeBinaryFile,
			Suggestion: "search_replace only works on text files",
		}
	}

	return data, nil
}

func (e *Engine) processSRCandidate(c srCandidate, re *regexp.Regexp, replacement string) (*srPending, *srFileResult, bool) {
	// Stale check.
	if err := e.tracker.checkStale(c.resolved); err != nil {
		return nil, &srFileResult{
			Path:       c.path,
			Error:      err.Error(),
			ErrorCode:  ErrCodeStaleFile,
			Suggestion: "re-read the file and retry",
		}, false
	}

	data, skip := e.srReadCandidate(c)
	if skip != nil {
		return nil, skip, false
	}

	res, applyErr := srApplyOne(data, re, replacement)
	if applyErr != nil {
		return nil, &srFileResult{
			Path:      c.path,
			Error:     applyErr.Error(),
			ErrorCode: ErrCodeEditNotFound,
		}, false
	}

	if res.matches == 0 {
		// No match in this file — skip silently (not an error).
		return nil, nil, false
	}

	if res.replaced == 0 || res.updated == string(data) {
		// Replacement was a no-op.
		return nil, &srFileResult{
			Path:      c.path,
			Matches:   res.matches,
			Replaced:  0,
			Unchanged: true,
			MatchType: "regex",
			FirstLine: res.firstLine,
			LastLine:  res.lastLine,
		}, false
	}

	return &srPending{
		candidate: c,
		updated:   res.updated,
		matches:   res.matches,
		replaced:  res.replaced,
		firstLine: res.firstLine,
		lastLine:  res.lastLine,
		preData:   data,
	}, nil, true
}

// srCheckAllFailed returns a terminal result/error when there is nothing to
// apply: either all candidates errored (error) or none matched (empty result).
// Returns (nil, nil) when there is pending work to continue with.
func srCheckAllFailed(fileResults []srFileResult, pending []srPending) (*ToolResult, error) {
	var errorFiles []srFileResult
	for _, fr := range fileResults {
		if fr.ErrorCode != "" && fr.ErrorCode != ErrCodeBinaryFile {
			errorFiles = append(errorFiles, fr)
		}
	}
	if len(errorFiles) > 0 && len(pending) == 0 {
		// All files failed.
		var msgs []string
		for _, ef := range errorFiles {
			msgs = append(msgs, fmt.Sprintf("  %s: %s", ef.Path, ef.Error))
		}
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("all %d files failed:\n%s", len(errorFiles), strings.Join(msgs, "\n")),
			Suggestion: "fix the reported errors and retry",
			Code:       errorFiles[0].ErrorCode,
		}
	}

	if len(pending) == 0 {
		return &ToolResult{
			Text: "no matches found across any target files",
			Meta: map[string]any{
				"files": fileResults,
			},
		}, nil
	}

	return nil, nil
}

// srDryRunResult renders the [dry-run] preview without touching the filesystem.
func srDryRunResult(fileResults []srFileResult, pending []srPending) *ToolResult {
	var previews []string
	for _, p := range pending {
		dr := generateDiff(string(p.preData), p.updated, p.candidate.path, 3)
		line := fmt.Sprintf("%s: %d matches → %d replacements (lines %d-%d)",
			p.candidate.path, p.matches, p.replaced, p.firstLine, p.lastLine)
		if dr.Diff != "" {
			line += "\n" + dr.Diff
		}
		previews = append(previews, line)
	}
	allResults := make([]srFileResult, 0, len(fileResults)+len(pending))
	allResults = append(allResults, fileResults...)
	allResults = append(allResults, srResultsFromPending(pending)...)
	return &ToolResult{
		Text: fmt.Sprintf("[dry-run] %d files would be changed:\n%s", len(pending), strings.Join(previews, "\n\n")),
		Meta: map[string]any{
			"files": allResults,
		},
	}
}

// srApplyWrites performs the per-file atomic writes (Phase 2) and builds the
// final success result.
func (e *Engine) srApplyWrites(fileResults []srFileResult, pending []srPending) (*ToolResult, error) {
	var applied []string
	for _, p := range pending {
		_ = e.recordSnapshot(p.candidate.resolved, p.candidate.path, "search_replace", p.preData)
		if err := e.atomicWriteFile(p.candidate.resolved, p.updated); err != nil {
			// Write failure — abort remaining but report what succeeded.
			return nil, fmt.Errorf("%s: %w", p.candidate.path, err)
		}
		applied = append(applied, fmt.Sprintf("%s: %d replacements (lines %d-%d)",
			p.candidate.path, p.replaced, p.firstLine, p.lastLine))
	}

	allResults := make([]srFileResult, 0, len(fileResults)+len(pending))
	allResults = append(allResults, fileResults...)
	allResults = append(allResults, srResultsFromPending(pending)...)
	totalReplaced := 0
	for _, p := range pending {
		totalReplaced += p.replaced
	}

	return &ToolResult{
		Text: fmt.Sprintf("replaced %d matches across %d files:\n%s",
			totalReplaced, len(pending), strings.Join(applied, "\n")),
		Meta: map[string]any{
			"files": allResults,
			"edit": editDetails{
				MatchType: "regex",
			},
		},
	}, nil
}

// srResultsFromPending converts pending edits to file results for the response.
func srResultsFromPending(pending []srPending) []srFileResult {
	results := make([]srFileResult, len(pending))
	for i, p := range pending {
		results[i] = srFileResult{
			Path:      p.candidate.path,
			Matches:   p.matches,
			Replaced:  p.replaced,
			MatchType: "regex",
			FirstLine: p.firstLine,
			LastLine:  p.lastLine,
		}
	}
	return results
}
