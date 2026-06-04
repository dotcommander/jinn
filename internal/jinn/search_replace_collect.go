package jinn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// collectSRFiles resolves the files argument into a list of candidates.
// Supports: single path, glob pattern, or array of paths/globs.
func (e *Engine) collectSRFiles(ctx context.Context, args map[string]interface{}) ([]srCandidate, error) {
	patterns, err := parseSRPatterns(args["files"])
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var candidates []srCandidate

	for _, pat := range patterns {
		if err := e.collectSRPattern(ctx, pat, seen, &candidates); err != nil {
			return nil, err
		}
	}

	if len(candidates) == 0 {
		return nil, &ErrWithSuggestion{
			Err:        errors.New("no files matched"),
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

// parseSRPatterns normalizes the "files" argument into a list of path/glob patterns.
// Accepts a single string or an array of strings.
func parseSRPatterns(filesArg interface{}) ([]string, error) {
	switch v := filesArg.(type) {
	case string:
		return []string{v}, nil
	case []interface{}:
		patterns := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, &ErrWithSuggestion{
					Err:        errors.New("files array must contain only strings"),
					Suggestion: "provide file paths or glob patterns as strings",
					Code:       ErrCodeInvalidArgs,
				}
			}
			patterns = append(patterns, s)
		}
		return patterns, nil
	default:
		return nil, &ErrWithSuggestion{
			Err:        errors.New("files is required (string or array of strings)"),
			Suggestion: "provide a file path, glob pattern, or array of paths",
			Code:       ErrCodeInvalidArgs,
		}
	}
}

// collectSRPattern resolves a single pattern (path, directory, or glob) and appends
// its matching, sandbox-safe, non-directory files to candidates (deduped via seen).
func (e *Engine) collectSRPattern(ctx context.Context, pat string, seen map[string]bool, candidates *[]srCandidate) error {
	resolved, err := e.checkPath(pat)
	if err == nil {
		// It's a real path — check if it's a directory.
		info, statErr := os.Stat(resolved)
		switch {
		case statErr == nil && !info.IsDir():
			if !seen[resolved] {
				seen[resolved] = true
				*candidates = append(*candidates, srCandidate{path: pat, resolved: resolved})
			}
			return nil
		case statErr == nil:
			// Directory: treat as glob "**/*" within it.
			pat = strings.TrimRight(pat, "/") + "/**/*"
		case !looksLikeGlob(pat):
			return &ErrWithSuggestion{
				Err:        fmt.Errorf("cannot stat %s: %w", pat, statErr),
				Suggestion: "verify the path exists",
				Code:       ErrCodeFileNotFound,
			}
		}
	}

	// Treat as a glob pattern — use findFiles logic.
	found, err := e.globExpand(ctx, pat)
	if err != nil {
		var sErr *ErrWithSuggestion
		if errors.As(err, &sErr) && (sErr.Code == ErrCodeCanceled || sErr.Code == ErrCodeTimeout) {
			return err
		}
		return &ErrWithSuggestion{
			Err:        fmt.Errorf("no files matched %q", pat),
			Suggestion: "check the glob pattern or provide explicit file paths",
			Code:       ErrCodeFileNotFound,
		}
	}
	for _, f := range found {
		e.appendSRCandidate(f, seen, candidates)
	}
	return nil
}

// appendSRCandidate adds f to candidates if it resolves inside the sandbox, is a
// regular (non-directory) file, and has not already been seen.
func (e *Engine) appendSRCandidate(f string, seen map[string]bool, candidates *[]srCandidate) {
	resolved, err := e.checkPath(f)
	if err != nil {
		return // skip files outside sandbox
	}
	info, statErr := os.Stat(resolved)
	if statErr != nil || info.IsDir() {
		return
	}
	if !seen[resolved] {
		seen[resolved] = true
		*candidates = append(*candidates, srCandidate{path: f, resolved: resolved})
	}
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

// compileSRRegex builds the effective regex from the pattern plus the
// case_insensitive/multiline flag arguments and compiles it.
func compileSRRegex(pattern string, args map[string]interface{}) (*regexp.Regexp, error) {
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
	return re, nil
}

// filterSRInclude applies the optional "include" glob filter to candidates.
// Returns candidates unchanged when no include filter is set.
func filterSRInclude(candidates []srCandidate, args map[string]interface{}) ([]srCandidate, error) {
	include, ok := args["include"].(string)
	if !ok || include == "" {
		return candidates, nil
	}
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
	return filtered, nil
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
