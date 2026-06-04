package jinn

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	listDefaultMax = 500
	listCapMax     = 10000
)

// listDirResult is the structured response for list_dir, including truncation metadata.
type listDirResult struct {
	Entries    []string `json:"entries"`
	Truncated  bool     `json:"truncated"`
	TotalCount int      `json:"total_count"`
}

// listParams holds the validated, clamped inputs for a list_dir call.
type listParams struct {
	listPath     string
	depth        int
	maxEntries   int
	changedSince int64
}

// parseListArgs extracts and clamps list_dir arguments.
func parseListArgs(args map[string]interface{}) listParams {
	p := listParams{listPath: ".", depth: 3}
	if v, ok := args["path"].(string); ok && v != "" {
		p.listPath = v
	}
	if d, ok := args["depth"].(float64); ok {
		p.depth = int(d)
	}
	if p.depth < 1 {
		p.depth = 1
	}
	if p.depth > 10 {
		p.depth = 10
	}
	p.maxEntries = intArg(args, "max_entries", listDefaultMax)
	if p.maxEntries > listCapMax {
		p.maxEntries = listCapMax
	}
	if v, ok := args["changed_since"].(float64); ok && v > 0 {
		p.changedSince = int64(v)
	}
	return p
}

func (e *Engine) listDir(args map[string]interface{}) (string, error) {
	p := parseListArgs(args)

	resolved, err := e.checkPath(p.listPath)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("path not found: %s", p.listPath),
			Suggestion: "check the directory path",
			Code:       ErrCodeFileNotFound,
		}
	}
	if !info.IsDir() {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("not a directory: %s", p.listPath),
			Suggestion: "use stat_file for individual files",
			Code:       ErrCodeInvalidArgs,
		}
	}

	all, err := e.collectListEntries(resolved, p)
	if err != nil {
		return "", err
	}

	return formatListResult(all, p.maxEntries)
}

// collectListEntries walks resolved and returns sorted, filtered display entries.
func (e *Engine) collectListEntries(resolved string, p listParams) ([]string, error) {
	// Compute the depth of the resolved base path to enforce maxdepth.
	baseDepth := strings.Count(resolved, string(os.PathSeparator))

	var all []string
	err := filepath.WalkDir(resolved, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip unreadable entry, continue walking the rest of the tree
		}
		entry, walkErr := listWalkEntry(path, d, baseDepth, p)
		if walkErr != nil {
			return walkErr
		}
		if entry != "" {
			rel, _ := filepath.Rel(e.workDir, path)
			if rel == "." {
				rel = p.listPath
			}
			out := rel
			if d.IsDir() {
				out += "/"
			}
			all = append(all, out)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(all)
	return all, nil
}

// listWalkEntry applies hidden/depth/mtime filters for one walk entry.
// Returns (path, nil) to include it, ("", nil) to skip it, or ("", SkipDir) to prune.
func listWalkEntry(path string, d fs.DirEntry, baseDepth int, p listParams) (string, error) {
	// Skip hidden entries (starting with '.')
	if d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
		if d.IsDir() {
			return "", filepath.SkipDir
		}
		return "", nil
	}
	// Enforce depth limit
	currentDepth := strings.Count(path, string(os.PathSeparator)) - baseDepth
	if currentDepth > p.depth {
		if d.IsDir() {
			return "", filepath.SkipDir
		}
		return "", nil
	}
	// Mtime filter: skip listing entries older than threshold.
	// Always descend into directories regardless of their own mtime.
	if p.changedSince > 0 {
		if fi, fiErr := d.Info(); fiErr == nil && fi.ModTime().Unix() < p.changedSince {
			return "", nil
		}
	}
	return path, nil
}

// formatListResult applies truncation and marshals the list_dir response.
func formatListResult(all []string, maxEntries int) (string, error) {
	total := len(all)
	truncated := total > maxEntries
	shown := all
	if truncated {
		shown = all[:maxEntries]
	}

	res := listDirResult{
		Entries:    shown,
		Truncated:  truncated,
		TotalCount: total,
	}
	if res.Entries == nil {
		res.Entries = []string{}
	}
	b, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("listDir: marshal: %w", err)
	}
	result := string(b)
	if truncated {
		result += "\n" + formatTruncatedHint(maxEntries, total, "'max_entries' or 'depth'")
	}
	return result, nil
}
