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
	listDefaultMax = 500
	listCapMax     = 10000
	listVisitMax   = 100000
)

var listTimeout = 60 * time.Second

type listDirResult struct {
	Entries         []string `json:"entries"`
	Truncated       bool     `json:"truncated"`
	TotalCount      int      `json:"total_count"`
	TotalCountExact bool     `json:"total_count_exact"`
	Hint            string   `json:"hint,omitempty"`
}

type listParams struct {
	listPath     string
	depth        int
	maxEntries   int
	changedAfter time.Time
}

func parseListArgs(args map[string]interface{}) (listParams, error) {
	p := listParams{listPath: ".", depth: 3, maxEntries: intArg(args, "max_entries", listDefaultMax)}
	if value := strArg(args, "path"); value != "" {
		p.listPath = value
	}
	if value, ok := args["depth"].(float64); ok {
		p.depth = int(value)
	}
	p.depth = max(1, min(p.depth, 10))
	p.maxEntries = min(p.maxEntries, listCapMax)
	if raw, ok := args["changed_since"].(float64); ok && raw > 0 {
		seconds := int64(raw)
		nanos := int64((raw - float64(seconds)) * float64(time.Second))
		p.changedAfter = time.Unix(seconds, nanos)
	}
	if raw := strArg(args, "changed_after"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return listParams{}, &ErrWithSuggestion{Err: fmt.Errorf("invalid changed_after: %w", err), Suggestion: "use an RFC3339Nano timestamp", Code: ErrCodeInvalidArgs}
		}
		p.changedAfter = parsed
	}
	return p, nil
}

func (e *Engine) listDir(args map[string]interface{}) (string, error) {
	return e.listDirContext(context.Background(), args)
}

func (e *Engine) listDirContext(ctx context.Context, args map[string]interface{}) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	p, err := parseListArgs(args)
	if err != nil {
		return "", err
	}
	resolved, err := e.checkPath(p.listPath)
	if err != nil {
		return "", err
	}
	info, err := e.rootedStat(resolved)
	if err != nil {
		return "", &ErrWithSuggestion{Err: fmt.Errorf("path not found: %s", p.listPath), Suggestion: "check the directory path", Code: ErrCodeFileNotFound}
	}
	if !info.IsDir() {
		return "", &ErrWithSuggestion{Err: fmt.Errorf("not a directory: %s", p.listPath), Suggestion: "use stat_file for individual files", Code: ErrCodeInvalidArgs}
	}
	entries, total, exact, err := e.collectListEntries(ctx, resolved, p)
	if err != nil {
		return "", err
	}
	result := listDirResult{Entries: entries, Truncated: !exact || total > p.maxEntries, TotalCount: total, TotalCountExact: exact}
	if result.Entries == nil {
		result.Entries = []string{}
	}
	if result.Truncated {
		result.Hint = "[TRUNCATED: narrow path or depth, or increase max_entries]"
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("list_dir: marshal: %w", err)
	}
	return string(data), nil
}

//nolint:gocognit,gocyclo,revive // directory traversal limits and rendering decisions share one ordered walk callback and result shape.
func (e *Engine) collectListEntries(ctx context.Context, resolved string, p listParams) ([]string, int, bool, error) {
	rootPath, err := e.rootRelative(resolved)
	if err != nil {
		return nil, 0, false, err
	}
	baseDepth := strings.Count(filepath.ToSlash(rootPath), "/")
	entries := make([]string, 0, min(p.maxEntries, 128))
	total, visited := 0, 0
	exact := true
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
		if visited > listVisitMax {
			exact = false
			return fs.SkipAll
		}
		currentDepth := strings.Count(filepath.ToSlash(path), "/") - baseDepth
		if currentDepth > p.depth {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !p.changedAfter.IsZero() && !entry.IsDir() {
			info, infoErr := entry.Info()
			if infoErr != nil {
				return infoErr
			}
			if !info.ModTime().After(p.changedAfter) {
				return nil
			}
		}
		value := filepath.ToSlash(path)
		if entry.IsDir() {
			value += "/"
		}
		total++
		if len(entries) < p.maxEntries {
			entries = append(entries, value)
		}
		if total > p.maxEntries {
			exact = false
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, total, false, &ErrWithSuggestion{Err: fmt.Errorf("list_dir timed out after %s", listTimeout), Suggestion: "narrow path or depth", Code: ErrCodeTimeout}
		}
		if errors.Is(err, context.Canceled) {
			return nil, total, false, &ErrWithSuggestion{Err: errors.New("list_dir canceled"), Suggestion: "retry if cancellation was unintended", Code: ErrCodeCanceled}
		}
		return nil, total, exact, err
	}
	slices.Sort(entries)
	return entries, total, exact, nil
}
