package jinn

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const (
	// srMaxFiles caps the number of files a single search_replace can touch.
	srMaxFiles = 50
	// srMaxFileSize refuses to process individual files larger than this.
	srMaxFileSize = 10 << 20 // 10 MiB
)

// srFileResult describes what happened in one file.
type srFileResult struct {
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

// srCandidate holds a file path that matched the glob (and optional path filter).
type srCandidate struct {
	path     string // display path
	resolved string // absolute path after security check
}

// srPending holds a validated, applied search-replace ready for atomic write.
type srPending struct {
	candidate srCandidate
	updated   string
	matches   int
	replaced  int
	firstLine int
	lastLine  int
	preData   []byte
}

// collectSRFiles resolves the files argument into a list of candidates.
// Supports: single path, glob pattern, or array of paths/globs.
func (e *Engine) collectSRFiles(ctx context.Context, args map[string]interface{}) ([]srCandidate, error) {
	// "files" can be a single string (path or glob) or an array of strings.
	var patterns []string
	switch v := args["files"].(type) {
	case string:
		patterns = []string{v}
	case []interface{}:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, &ErrWithSuggestion{
					Err:        fmt.Errorf("files array must contain only strings"),
					Suggestion: "provide file paths or glob patterns as strings",
					Code:       ErrCodeInvalidArgs,
				}
			}
			patterns = append(patterns, s)
		}
	default:
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("files is required (string or array of strings)"),
			Suggestion: "provide a file path, glob pattern, or array of paths",
			Code:       ErrCodeInvalidArgs,
		}
	}

	seen := make(map[string]bool)
	var candidates []srCandidate

	for _, pat := range patterns {
		resolved, err := e.checkPath(pat)
		if err == nil {
			// It's a real path — check if it's a directory.
			info, statErr := os.Stat(resolved)
			if statErr == nil {
				if !info.IsDir() {
					if !seen[resolved] {
						seen[resolved] = true
						candidates = append(candidates, srCandidate{path: pat, resolved: resolved})
					}
					continue
				}
				// Directory: treat as glob "**/*" within it.
				pat = strings.TrimRight(pat, "/") + "/**/*"
			} else if !looksLikeGlob(pat) {
				return nil, &ErrWithSuggestion{
					Err:        fmt.Errorf("cannot stat %s: %w", pat, statErr),
					Suggestion: "verify the path exists",
					Code:       ErrCodeFileNotFound,
				}
			}
		}

		// Treat as a glob pattern — use findFiles logic.
		found, err := e.globExpand(ctx, pat)
		if err != nil {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("no files matched %q", pat),
				Suggestion: "check the glob pattern or provide explicit file paths",
				Code:       ErrCodeFileNotFound,
			}
		}
		for _, f := range found {
			resolved, err := e.checkPath(f)
			if err != nil {
				continue // skip files outside sandbox
			}
			info, statErr := os.Stat(resolved)
			if statErr != nil || info.IsDir() {
				continue
			}
			if !seen[resolved] {
				seen[resolved] = true
				candidates = append(candidates, srCandidate{path: f, resolved: resolved})
			}
		}
	}

	if len(candidates) == 0 {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("no files matched"),
			Suggestion: "check file paths or glob patterns — use find_files to locate files first",
			Code:       ErrCodeFileNotFound,
		}
	}

	if len(candidates) > srMaxFiles {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("too many files matched (%d, max %d)", len(candidates), srMaxFiles),
			Suggestion: "narrow the glob pattern or provide fewer explicit paths",
			Code:       ErrCodeInvalidArgs,
		}
	}

	return candidates, nil
}

// globExpand expands a glob pattern into matching file paths.
func (e *Engine) globExpand(ctx context.Context, pattern string) ([]string, error) {
	// Delegate to the existing findFiles infrastructure.
	res, err := e.findFiles(ctx, map[string]interface{}{
		"pattern": pattern,
		"limit":   float64(srMaxFiles + 1),
	})
	if err != nil {
		return nil, err
	}
	if res == "" {
		return nil, fmt.Errorf("no matches for %q", pattern)
	}
	raw := res
	// Strip any truncation hint appended by findFiles.
	if idx := strings.Index(raw, "\n[TRUNCATED"); idx >= 0 {
		raw = raw[:idx]
	}
	var found findFilesResult
	if err := json.Unmarshal([]byte(raw), &found); err != nil {
		return nil, fmt.Errorf("parse find_files result: %w", err)
	}
	if len(found.Files) == 0 {
		return nil, fmt.Errorf("no matches for %q", pattern)
	}
	return found.Files, nil
}

func looksLikeGlob(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

// srApplyOne applies the regex replacement to a single file.
// Returns the result content (if changed), match count, replace count, and any error.
func srApplyOne(content []byte, re *regexp.Regexp, replacement string, multiline bool) (updated string, matches int, replaced int, firstLine int, lastLine int, err error) {
	raw, bom := stripBom(string(content))
	ending := detectLineEnding(raw)
	raw = normalizeToLF(raw)

	// Count matches before replacing.
	locs := re.FindAllStringIndex(raw, -1)
	matches = len(locs)
	if matches == 0 {
		return "", 0, 0, 0, 0, nil
	}

	// Apply replacement.
	result := re.ReplaceAllString(raw, replacement)
	replaced = matches // ReplaceAll replaces every match

	if result == raw {
		return "", matches, 0, 0, 0, nil // replacement was a no-op
	}

	// Find first and last changed lines.
	firstMatch := locs[0]
	lastMatch := locs[len(locs)-1]
	firstLine = strings.Count(raw[:firstMatch[0]], "\n") + 1
	lastLine = strings.Count(raw[:lastMatch[1]], "\n") + 1

	return bom + restoreLineEndings(result, ending), matches, replaced, firstLine, lastLine, nil
}

// searchReplace is the tool handler for search_replace.
func (e *Engine) searchReplace(ctx context.Context, args map[string]interface{}) (*ToolResult, error) {
	// --- Required arguments ---
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("pattern is required"),
			Suggestion: "provide a regex pattern to search for",
			Code:       ErrCodeInvalidArgs,
		}
	}

	replacement, _ := args["replacement"].(string)
	// replacement can be empty (deletion) — that's valid.

	// --- Compile regex with timeout protection ---
	flags := ""
	if v, ok := args["case_insensitive"].(bool); ok && v {
		flags += "i"
	}
	multiline := true // default: ^/$ match line boundaries
	if v, ok := args["multiline"].(bool); ok && !v {
		multiline = false
	}
	if multiline {
		flags += "m"
	}
	if flags != "" {
		pattern = "(?" + flags + ")" + pattern
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("invalid regex: %w", err),
			Suggestion: "check regex syntax — use literal:true in search_files first to verify the pattern matches",
			Code:       ErrCodeInvalidRegex,
		}
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
	if include, ok := args["include"].(string); ok && include != "" {
		var filtered []srCandidate
		for _, c := range candidates {
			// Simple suffix/glob match on the display path.
			if globMatch(include, c.path) {
				filtered = append(filtered, c)
			}
		}
		if len(filtered) == 0 {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("no files matched include filter %q", include),
				Suggestion: "broaden the include glob or check file extensions",
				Code:       ErrCodeFileNotFound,
			}
		}
		candidates = filtered
	}

	dryRun := false
	if v, ok := args["dry_run"].(bool); ok && v {
		dryRun = true
	}

	// --- Phase 1: Validate all files (collect-then-report) ---

	var pending []srPending
	var fileResults []srFileResult

	for _, c := range candidates {
		// Stale check.
		if err := e.tracker.checkStale(c.resolved); err != nil {
			fileResults = append(fileResults, srFileResult{
				Path:       c.path,
				Error:      err.Error(),
				ErrorCode:  ErrCodeStaleFile,
				Suggestion: "re-read the file and retry",
			})
			continue
		}

		info, statErr := os.Stat(c.resolved)
		if statErr != nil {
			fileResults = append(fileResults, srFileResult{
				Path:      c.path,
				Error:     statErr.Error(),
				ErrorCode: ErrCodeFileNotFound,
			})
			continue
		}

		// Skip binary files.
		if info.Size() > srMaxFileSize {
			fileResults = append(fileResults, srFileResult{
				Path:       c.path,
				Error:      fmt.Sprintf("file too large (%d bytes, max %d)", info.Size(), srMaxFileSize),
				ErrorCode:  ErrCodeFileTooLarge,
				Suggestion: "use edit_file with exact text for large files",
			})
			continue
		}

		data, readErr := os.ReadFile(c.resolved)
		if readErr != nil {
			fileResults = append(fileResults, srFileResult{
				Path:      c.path,
				Error:     readErr.Error(),
				ErrorCode: ErrCodeFileNotFound,
			})
			continue
		}

		// Skip binary files (simple heuristic: null byte in first 8KB).
		checkLen := len(data)
		if checkLen > 8192 {
			checkLen = 8192
		}
		if isBinary(data[:checkLen]) {
			fileResults = append(fileResults, srFileResult{
				Path:       c.path,
				Error:      "binary file detected, skipping",
				ErrorCode:  ErrCodeBinaryFile,
				Suggestion: "search_replace only works on text files",
			})
			continue
		}

		updated, matches, replaced, firstLine, lastLine, applyErr := srApplyOne(data, re, replacement, multiline)
		if applyErr != nil {
			fileResults = append(fileResults, srFileResult{
				Path:      c.path,
				Error:     applyErr.Error(),
				ErrorCode: ErrCodeEditNotFound,
			})
			continue
		}

		if matches == 0 {
			// No match in this file — skip silently (not an error).
			continue
		}

		if replaced == 0 || updated == string(data) {
			// Replacement was a no-op.
			fileResults = append(fileResults, srFileResult{
				Path:      c.path,
				Matches:   matches,
				Replaced:  0,
				Unchanged: true,
				MatchType: "regex",
				FirstLine: firstLine,
				LastLine:  lastLine,
			})
			continue
		}

		pending = append(pending, srPending{
			candidate: c,
			updated:   updated,
			matches:   matches,
			replaced:  replaced,
			firstLine: firstLine,
			lastLine:  lastLine,
			preData:   data,
		})
	}

	// Report errors from failed files.
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

	// --- Dry run: return preview ---
	if dryRun {
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
		allResults := append(fileResults, srResultsFromPending(pending)...)
		return &ToolResult{
			Text: fmt.Sprintf("[dry-run] %d files would be changed:\n%s", len(pending), strings.Join(previews, "\n\n")),
			Meta: map[string]any{
				"files": allResults,
			},
		}, nil
	}

	// --- Phase 2: Apply all changes with per-file atomic writes ---
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

	allResults := append(fileResults, srResultsFromPending(pending)...)
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

// isBinary returns true if the data contains null bytes (simple heuristic).
func isBinary(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

// globMatch does a simple glob match where '*' matches any non-separator.
func globMatch(pattern, name string) bool {
	// Simple implementation: handle common cases like "*.go", "*.ts"
	if pattern == "" {
		return true
	}
	if pattern[0] == '*' && !strings.Contains(pattern[1:], "*") {
		suffix := pattern[1:]
		return strings.HasSuffix(name, suffix)
	}
	if strings.Contains(pattern, "*") {
		// For complex globs, compile as regex.
		regex := globToRegex(pattern)
		re, err := regexp.Compile(regex)
		if err != nil {
			return false
		}
		return re.MatchString(name)
	}
	return name == pattern
}

// globToRegex converts a simple glob to a regex pattern.
func globToRegex(glob string) string {
	var b strings.Builder
	b.WriteString("^")
	for _, ch := range glob {
		switch ch {
		case '*':
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '.', '(', ')', '|', '+', '^', '$', '[', ']', '{', '}':
			b.WriteByte('\\')
			b.WriteRune(ch)
		default:
			b.WriteRune(ch)
		}
	}
	b.WriteString("$")
	return b.String()
}
