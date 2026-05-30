package jinn

import (
	"fmt"
	"os"
	"path/filepath"
)

// globalScope is the reserved cross-project sentinel. Real scopes are absolute
// paths, so this string can never collide with one.
const globalScope = "global"

// normalizeScopePath resolves p to a canonical absolute path. EvalSymlinks is
// best-effort; on error it falls back to Clean(abs). Mirrors the resolution
// pattern in engine.go's New and security.go.
func normalizeScopePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return filepath.Clean(abs)
}

// detectScope walks up from workDir to the nearest ancestor containing a .git
// entry (file OR dir — worktrees use a .git file) and returns its normalized
// path. If no .git is found, it returns the normalized workDir.
func detectScope(workDir string) string {
	dir := workDir
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return normalizeScopePath(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return normalizeScopePath(workDir)
}

// currentScope returns the auto-detected scope for the engine's working
// directory, computing it once and caching under memMu. normalizeScopePath
// always returns non-empty, so "" reliably means "not yet computed".
func (e *Engine) currentScope() string {
	e.memMu.Lock()
	defer e.memMu.Unlock()
	if e.curScope == "" {
		e.curScope = detectScope(e.workDir)
	}
	return e.curScope
}

// resolveScope maps a caller-supplied scope argument to a canonical scope:
// empty → the current project, "global" → the cross-project bucket, an
// absolute path → its normalized form. Anything else is an error.
//
// Locking contract: resolveScope (via currentScope) acquires e.memMu itself.
// Callers must NOT hold e.memMu when calling it.
func (e *Engine) resolveScope(arg string) (string, error) {
	switch {
	case arg == "":
		return e.currentScope(), nil
	case arg == globalScope:
		return globalScope, nil
	case filepath.IsAbs(arg):
		return normalizeScopePath(arg), nil
	default:
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("invalid scope: %q — must be empty, %q, or an absolute path", arg, globalScope),
			Suggestion: `omit "scope" to use the current project, use "global" for the cross-project bucket, or pass an absolute path`,
			Code:       ErrCodeInvalidArgs,
		}
	}
}
