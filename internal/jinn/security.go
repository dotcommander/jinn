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
		Code:       ErrCodePathOutsideSandbox,
	}
}

func hasSensitivePathSegment(p string) bool {
	clean := filepath.Clean(p)
	for _, seg := range sensitiveSegments {
		if strings.Contains(clean, seg) {
			return true
		}
	}
	base := filepath.Base(clean)
	for _, dir := range sensitiveDirs {
		if base == dir {
			return true
		}
	}
	return base == ".env" || strings.HasPrefix(base, ".env.")
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

// checkPathForRead is like checkPath but also allows reading jinn's own
// shell spill files. Spill files live in os.TempDir() and are named with
// spillFilePrefix — they are jinn's own output, equivalent to what run_shell
// can already return inline. Write and exec paths must use checkPath directly.
func (e *Engine) checkPathForRead(p string) (string, error) {
	resolved, err := e.resolvePath(p)
	if err != nil {
		return "", err
	}
	// Spill-file exemption: read-only access to jinn's own tmp output.
	// Both sides are cleaned to prevent symlink/.. escape.
	if filepath.Dir(resolved) == filepath.Clean(os.TempDir()) &&
		strings.HasPrefix(filepath.Base(resolved), spillFilePrefix) {
		return resolved, nil
	}
	return e.checkPath(p)
}

func (e *Engine) checkPath(p string) (string, error) {
	resolved, err := e.resolvePath(p)
	if err != nil {
		return "", err
	}

	real, err := resolveExistingPrefix(resolved)
	if err != nil {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("symlink target is outside the sandbox: %s", p),
			Suggestion: "symlink target is outside the sandbox; follow the symlink manually via its absolute path if authorized",
		}
	}

	// Check sensitive segments on the resolved path.
	if hasSensitivePathSegment(real) {
		return "", sensitivePathErr(p)
	}

	// Check workdir boundary on the resolved path.
	if !strings.HasPrefix(real, e.workDir+"/") && real != e.workDir {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("%s is outside working directory", p),
			Suggestion: "path resolves outside the sandbox root; supply a path inside the workdir",
			Code:       ErrCodePathOutsideSandbox,
		}
	}
	return real, nil
}

func resolveExistingPrefix(path string) (string, error) {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	var missing []string
	current := path
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(path), nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent
		real, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return real, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
	}
}
