package jinn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var sensitiveSegments = []string{"/.git/", "/.ssh/", "/.aws/", "/.gnupg/"}
var sensitiveDirs = []string{".git", ".ssh", ".aws", ".gnupg"}

// sensitivePathErr builds the standard "blocked path" error with
// a single canonical suggestion string shared by all sensitive-path guards.
func sensitivePathErr(p string) error {
	return &ErrWithSuggestion{
		Err:        fmt.Errorf("sensitive path: %s", p),
		Suggestion: "this path is blocked for security; request the specific field or artifact from the user instead",
	}
}

func (e *Engine) resolvePath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		p = home + p[1:]
	}
	if !strings.HasPrefix(p, "/") {
		p = filepath.Join(e.workDir, p)
	}
	return filepath.Clean(p), nil
}

func (e *Engine) checkPath(p string) (string, error) {
	resolved, err := e.resolvePath(p)
	if err != nil {
		return "", err
	}

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
			// Symlink resolution failed for a non-existence reason — likely a
			// symlink whose target is outside the sandbox.
			return "", &ErrWithSuggestion{
				Err:        fmt.Errorf("symlink target is outside the sandbox: %s", p),
				Suggestion: "symlink target is outside the sandbox; follow the symlink manually via its absolute path if authorized",
			}
		}
	}

	// Check sensitive segments on the resolved path.
	for _, seg := range sensitiveSegments {
		if strings.Contains(real, seg) {
			return "", sensitivePathErr(p)
		}
	}
	base := filepath.Base(real)
	for _, dir := range sensitiveDirs {
		if base == dir {
			return "", sensitivePathErr(p)
		}
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return "", sensitivePathErr(p)
	}

	// Check workdir boundary on the resolved path.
	if !strings.HasPrefix(real, e.workDir+"/") && real != e.workDir {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("%s is outside working directory", p),
			Suggestion: "path resolves outside the sandbox root; supply a path inside the workdir",
		}
	}
	return real, nil
}
