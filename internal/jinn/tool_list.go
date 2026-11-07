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

// Truncation reason tokens naming the binding discovery budget:
// "visited" (the visit cap), "max_entries" (the page cap), or "depth"
// (the depth cap at 10).
const (
	listTruncVisited    = "visited"
	listTruncMaxEntries = "max_entries"
	listTruncDepth      = "depth"
)

type listDirResult struct {
	Entries          []string          `json:"entries"`
	Truncated        bool              `json:"truncated"`
	TotalCount       int               `json:"total_count"`
	TotalCountExact  bool              `json:"total_count_exact"`
	Offset           int               `json:"offset"`
	Hint             string            `json:"hint,omitempty"`
	NextCall         *NextCall         `json:"next_call,omitempty"`
	TruncationReason string            `json:"truncation_reason,omitempty"`
	Symlinks         map[string]string `json:"symlinks,omitempty"`
}

type listParams struct {
	listPath     string
	depth        int
	maxEntries   int
	offset       int
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
	p.offset = max(0, min(intArg(args, "offset", 0), listVisitMax))
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
	entries, total, exact, reason, symlinks, err := e.collectListEntries(ctx, resolved, p)
	if err != nil {
		return "", err
	}
	// A non-empty reason means a discovery budget bound the walk (visited,
	// page cap, or depth), even when every visible entry was returned.
	result := listDirResult{Entries: entries, Truncated: !exact || reason != "" || total > p.offset+len(entries), TotalCount: total, TotalCountExact: exact, Offset: p.offset, Symlinks: symlinks}
	if result.Entries == nil {
		result.Entries = []string{}
	}
	if result.Truncated {
		result.TruncationReason = reason
		result.Hint = listTruncationHint(reason)
		if len(entries) > 0 {
			result.NextCall = listDirNextCall(args, p, p.offset+len(entries))
		}
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("list_dir: marshal: %w", err)
	}
	return string(data), nil
}

// listTruncationHint renders the actionable hint for the binding budget.
func listTruncationHint(reason string) string {
	switch reason {
	case listTruncVisited:
		return "[TRUNCATED: visit budget (100000) exhausted; narrow --path or add --changed_since]"
	case listTruncDepth:
		return "[TRUNCATED: depth budget (10) reached; narrow --path or lower --depth]"
	default:
		return "[TRUNCATED: narrow path or depth, or increase max_entries]"
	}
}

//nolint:gocognit,gocyclo,revive,funlen // directory traversal limits and rendering decisions share one ordered walk callback and result shape.
func (e *Engine) collectListEntries(ctx context.Context, resolved string, p listParams) (entries []string, total int, exact bool, reason string, symlinks map[string]string, err error) {
	rootPath, err := e.rootRelative(resolved)
	if err != nil {
		return nil, 0, false, "", nil, err
	}
	baseDepth := strings.Count(filepath.ToSlash(rootPath), "/")
	entries = make([]string, 0, min(p.maxEntries, 128))
	visited := 0
	exact = true
	depthLimited := false
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
			reason = listTruncVisited
			return fs.SkipAll
		}
		currentDepth := strings.Count(filepath.ToSlash(path), "/") - baseDepth
		if currentDepth > p.depth {
			depthLimited = true
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
		if entry.Type()&fs.ModeSymlink != 0 {
			if symlinks == nil {
				symlinks = make(map[string]string)
			}
			symlinks[value] = ""
			if target, linkErr := e.root.Readlink(path); linkErr == nil {
				symlinks[value] = filepath.ToSlash(target)
			}
		}
		if total > p.offset && len(entries) < p.maxEntries {
			entries = append(entries, value)
		}
		if total > p.offset+p.maxEntries {
			exact = false
			if reason == "" {
				reason = listTruncMaxEntries
			}
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, total, false, "", nil, &ErrWithSuggestion{Err: fmt.Errorf("list_dir timed out after %s", listTimeout), Suggestion: "narrow path or depth", Code: ErrCodeTimeout}
		}
		if errors.Is(err, context.Canceled) {
			return nil, total, false, "", nil, &ErrWithSuggestion{Err: errors.New("list_dir canceled"), Suggestion: "retry if cancellation was unintended", Code: ErrCodeCanceled}
		}
		return nil, total, exact, reason, symlinks, err
	}
	if reason == "" && depthLimited {
		reason = listTruncDepth
	}
	slices.Sort(entries)
	return entries, total, exact, reason, symlinks, nil
}

func listDirNextCall(args map[string]interface{}, p listParams, offset int) *NextCall {
	arguments := map[string]any{
		"path":        p.listPath,
		"depth":       p.depth,
		"max_entries": p.maxEntries,
		"offset":      offset,
	}
	for _, key := range []string{"changed_since", "changed_after"} {
		if value, ok := args[key]; ok {
			arguments[key] = value
		}
	}
	return &NextCall{Tool: "list_dir", Arguments: arguments}
}
