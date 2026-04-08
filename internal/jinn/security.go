package jinn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var sensitiveSegments = []string{"/.git/", "/.ssh/", "/.aws/", "/.gnupg/"}
var sensitiveDirs = []string{".git", ".ssh", ".aws", ".gnupg"}

func (e *Engine) resolvePath(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = filepath.Join(e.workDir, p)
	}
	return filepath.Clean(p)
}

func (e *Engine) checkPath(p string) (string, error) {
	resolved := e.resolvePath(p)

	// Resolve symlinks to detect escape attempts.
	// If the file doesn't exist, try the parent directory.
	real, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			parentReal, err2 := filepath.EvalSymlinks(filepath.Dir(resolved))
			if err2 == nil {
				real = filepath.Join(parentReal, filepath.Base(resolved))
			} else {
				// Parent doesn't exist either; use the cleaned path.
				real = resolved
			}
		} else {
			real = resolved
		}
	}

	// Check sensitive segments on the resolved path.
	for _, seg := range sensitiveSegments {
		if strings.Contains(real, seg) {
			return "", fmt.Errorf("sensitive path: %s", p)
		}
	}
	base := filepath.Base(real)
	for _, dir := range sensitiveDirs {
		if base == dir {
			return "", fmt.Errorf("sensitive path: %s", p)
		}
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return "", fmt.Errorf("sensitive path: %s", p)
	}

	// Check workdir boundary on the resolved path.
	if !strings.HasPrefix(real, e.workDir+"/") && real != e.workDir {
		return "", fmt.Errorf("%s is outside working directory", p)
	}
	return real, nil
}
