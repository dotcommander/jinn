package jinn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		return "", fmt.Errorf("path not found: %s", treePath)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", treePath)
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
