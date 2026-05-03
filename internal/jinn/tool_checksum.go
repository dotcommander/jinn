package jinn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

func (e *Engine) checksumTree(args map[string]interface{}) (string, error) {
	treePath := "."
	if p, ok := args["path"].(string); ok && p != "" {
		treePath = p
	}

	resolved, err := e.checkPath(treePath)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("path not found: %s", treePath),
			Suggestion: "Verify the path exists and is within the working directory.",
			Code:       ErrCodeFileNotFound,
		}
	}
	if !info.IsDir() {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("path is not a directory: %s", treePath),
			Suggestion: "checksum_tree requires a directory path.",
			Code:       ErrCodeInvalidArgs,
		}
	}

	pattern, _ := args["pattern"].(string)
	hashes := make(map[string]string)

	err = filepath.WalkDir(resolved, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			for _, skip := range grepExcludeDirs {
				if d.Name() == skip {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		if info, err2 := d.Info(); err2 != nil || info.Size() > maxFileSize {
			return nil
		}
		if pattern != "" {
			if matched, _ := filepath.Match(pattern, d.Name()); !matched {
				return nil
			}
		}
		h, err := e.hashFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(e.workDir, path)
		hashes[rel] = h
		return nil
	})
	if err != nil {
		return "", err
	}

	// Differential baseline mode: compare against provided baseline hashes
	baseline := make(map[string]string)
	if b, ok := args["baseline"].(map[string]interface{}); ok {
		for k, v := range b {
			if s, ok := v.(string); ok {
				baseline[k] = s
			}
		}
	}

	if len(baseline) > 0 {
		var changed, added, removed []string
		unchangedCount := 0
		diffHashes := make(map[string]string)

		for path, hash := range hashes {
			if baseHash, ok := baseline[path]; ok {
				if hash != baseHash {
					changed = append(changed, path)
					diffHashes[path] = hash
				} else {
					unchangedCount++
				}
			} else {
				added = append(added, path)
				diffHashes[path] = hash
			}
		}
		for path := range baseline {
			if _, ok := hashes[path]; !ok {
				removed = append(removed, path)
			}
		}

		sort.Strings(changed)
		sort.Strings(added)
		sort.Strings(removed)

		result := map[string]interface{}{
			"changed":         changed,
			"added":           added,
			"removed":         removed,
			"unchanged_count": unchangedCount,
			"hashes":          diffHashes,
		}
		data, err := json.Marshal(result)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	data, err := json.Marshal(hashes)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (e *Engine) hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxFileSize)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
