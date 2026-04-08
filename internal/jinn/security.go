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

	for _, seg := range sensitiveSegments {
		if strings.Contains(resolved, seg) {
			return "", fmt.Errorf("sensitive path: %s", p)
		}
	}
	base := filepath.Base(resolved)
	for _, dir := range sensitiveDirs {
		if base == dir {
			return "", fmt.Errorf("sensitive path: %s", p)
		}
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return "", fmt.Errorf("sensitive path: %s", p)
	}

	if strings.Contains(resolved, "/..") {
		return "", fmt.Errorf("unable to resolve path: %s", p)
	}
	fi, err := os.Lstat(resolved)
	if err == nil && fi.Mode()&os.ModeSymlink != 0 {
		target, _ := os.Readlink(resolved)
		target = e.resolvePath(target)
		if strings.Contains(target, "/..") {
			return "", fmt.Errorf("unable to resolve symlink target: %s", p)
		}
		if !strings.HasPrefix(target, e.workDir+"/") && target != e.workDir {
			return "", fmt.Errorf("%s is a symlink outside working directory", p)
		}
	}
	if !strings.HasPrefix(resolved, e.workDir+"/") && resolved != e.workDir {
		return "", fmt.Errorf("%s is outside working directory", p)
	}
	return resolved, nil
}
