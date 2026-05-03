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

func (e *Engine) listDir(args map[string]interface{}) (string, error) {
	listPath := "."
	if p, ok := args["path"].(string); ok && p != "" {
		listPath = p
	}
	depth := 3
	if d, ok := args["depth"].(float64); ok {
		depth = int(d)
	}
	if depth < 1 {
		depth = 1
	}
	if depth > 10 {
		depth = 10
	}

	maxEntries := intArg(args, "max_entries", listDefaultMax)
	if maxEntries > listCapMax {
		maxEntries = listCapMax
	}

	var changedSince int64
	if v, ok := args["changed_since"].(float64); ok && v > 0 {
		changedSince = int64(v)
	}

	resolved, err := e.checkPath(listPath)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("path not found: %s", listPath),
			Suggestion: "check the directory path",
			Code:       ErrCodeFileNotFound,
		}
	}
	if !info.IsDir() {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("not a directory: %s", listPath),
			Suggestion: "use stat_file for individual files",
			Code:       ErrCodeInvalidArgs,
		}
	}

	// Compute the depth of the resolved base path to enforce maxdepth.
	baseDepth := strings.Count(resolved, string(os.PathSeparator))

	var all []string
	err = filepath.WalkDir(resolved, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// Skip hidden entries (starting with '.')
		if d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Enforce depth limit
		currentDepth := strings.Count(path, string(os.PathSeparator)) - baseDepth
		if currentDepth > depth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Mtime filter: skip listing entries older than threshold.
		// Always descend into directories regardless of their own mtime.
		skipForMtime := false
		if changedSince > 0 {
			fi, fiErr := d.Info()
			if fiErr == nil && fi.ModTime().Unix() < changedSince {
				skipForMtime = true
			}
		}

		if !skipForMtime {
			rel, _ := filepath.Rel(e.workDir, path)
			if rel == "." {
				rel = listPath
			}
			entry := rel
			if d.IsDir() {
				entry += "/"
			}
			all = append(all, entry)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	slices.Sort(all)

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
