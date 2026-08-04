package jinn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	findDefaultLimit = 1000
	findVisitLimit   = 100000
)

var findTimeout = 60 * time.Second

var findExcludeDirs = []string{".git", ".ssh", ".aws", ".gnupg", "node_modules", "vendor", "__pycache__", ".cache", "dist", "build"}

type findFilesResult struct {
	Files           []string `json:"files"`
	Truncated       bool     `json:"truncated"`
	TotalCount      int      `json:"total_count"`
	TotalCountExact bool     `json:"total_count_exact"`
	LimitUsed       int      `json:"limit_used"`
	Backend         string   `json:"backend"`
	Hint            string   `json:"hint,omitempty"`
}

func (e *Engine) findFiles(ctx context.Context, args map[string]interface{}) (string, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "", &ErrWithSuggestion{Err: errors.New("pattern is required"), Suggestion: "provide a glob pattern like '*.go' or '**/*.test.ts'", Code: ErrCodeInvalidArgs}
	}
	searchPath := strArg(args, "path")
	if searchPath == "" {
		searchPath = "."
	}
	limit := intArg(args, "limit", findDefaultLimit)
	ctx, cancel := context.WithTimeout(ctx, findTimeout)
	defer cancel()
	files, total, exact, err := e.walkNativeFiles(ctx, pattern, searchPath, limit)
	if err != nil {
		return "", classifyNativeFindErr(err)
	}
	result := findFilesResult{
		Files: files, Truncated: !exact || total > limit, TotalCount: total,
		TotalCountExact: exact, LimitUsed: limit, Backend: "native",
	}
	if result.Truncated {
		result.Hint = "TRUNCATED: use a more specific pattern or increase limit"
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("find_files: marshal: %w", err)
	}
	return string(data), nil
}

//nolint:funlen,gocognit,gocyclo,revive // traversal policy checks are deliberately co-located with WalkDir control flow and result shape.
func (e *Engine) walkNativeFiles(ctx context.Context, pattern, searchPath string, limit int) ([]string, int, bool, error) {
	resolved, err := e.checkPath(searchPath)
	if err != nil {
		return nil, 0, false, err
	}
	info, err := e.rootedStat(resolved)
	if err != nil {
		return nil, 0, false, err
	}
	if !info.IsDir() {
		return nil, 0, false, &ErrWithSuggestion{Err: fmt.Errorf("not a directory: %s", searchPath), Suggestion: "choose a directory for path", Code: ErrCodeInvalidArgs}
	}
	stopAfter := limit + 1
	if stopAfter > findVisitLimit {
		stopAfter = findVisitLimit
	}
	visited := 0
	total := 0
	exact := true
	files := make([]string, 0, min(limit, 128))
	rootPath, err := e.rootRelative(resolved)
	if err != nil {
		return nil, 0, false, err
	}
	err = fs.WalkDir(e.root.FS(), rootPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return walkErr
		}
		relBase, relErr := filepath.Rel(rootPath, path)
		if relErr != nil {
			return relErr
		}
		if relBase == "." {
			return nil
		}
		if shouldPruneTraversal(relBase, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		if visited > findVisitLimit {
			exact = false
			return fs.SkipAll
		}
		if entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relWork := filepath.ToSlash(path)
		candidate := filepath.ToSlash(relBase)
		if !strings.Contains(pattern, "/") {
			candidate = filepath.Base(candidate)
		}
		if !globMatch(pattern, candidate) {
			return nil
		}
		total++
		if len(files) < limit {
			files = append(files, relWork)
		}
		if total >= stopAfter {
			exact = false
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, total, exact, err
	}
	slices.Sort(files)
	return files, total, exact, nil
}

func shouldPruneTraversal(rel string, isDir bool) bool {
	segments := strings.Split(filepath.ToSlash(rel), "/")
	for _, segment := range segments {
		if strings.HasPrefix(segment, ".") {
			return true
		}
		if isDir && slices.Contains(findExcludeDirs, segment) {
			return true
		}
	}
	return false
}

func globMatch(pattern, candidate string) bool {
	patternParts := strings.Split(filepath.ToSlash(pattern), "/")
	candidateParts := strings.Split(filepath.ToSlash(candidate), "/")
	var match func(int, int) bool
	match = func(pi, ci int) bool {
		if pi == len(patternParts) {
			return ci == len(candidateParts)
		}
		if patternParts[pi] == "**" {
			return match(pi+1, ci) || (ci < len(candidateParts) && match(pi, ci+1))
		}
		if ci >= len(candidateParts) {
			return false
		}
		ok, err := filepath.Match(patternParts[pi], candidateParts[ci])
		return err == nil && ok && match(pi+1, ci+1)
	}
	return match(0, 0)
}

func classifyNativeFindErr(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &ErrWithSuggestion{Err: fmt.Errorf("find_files timed out after %s (backend=native)", findTimeout), Suggestion: "narrow path or use a more specific glob", Code: ErrCodeTimeout}
	case errors.Is(err, context.Canceled):
		return &ErrWithSuggestion{Err: errors.New("find_files canceled (backend=native)"), Suggestion: "retry the file search if cancellation was unintended", Code: ErrCodeCanceled}
	default:
		return err
	}
}
