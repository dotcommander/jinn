package jinn

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

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

// currentProjectID returns the auto-detected project scope_id (repo root path)
// for the engine's working directory, computing it once and caching under memMu.
func (e *Engine) currentProjectID() string {
	e.memMu.Lock()
	defer e.memMu.Unlock()
	if e.curScope == "" {
		e.curScope = detectScope(e.workDir)
	}
	return e.curScope
}

// resolvedScope holds the (scope, scope_id) pair for a memory operation.
type resolvedScope struct {
	scope   string // global|project|task|agent
	scopeID string // ""=global, repo-path=project, task-id=task, agent-name=agent
}

// resolveMemoryScope maps caller-supplied scope+scopeID args to a canonical
// (scope, scope_id) pair per the design doc resolution table:
//
//	scope=""         → project, auto-detected repo root
//	scope="global"   → global, ""
//	scope="project"  → project, scopeID (or auto-detected if scopeID=="")
//	scope="task"     → task,    scopeID (caller-supplied; required)
//	scope="agent"    → agent,   scopeID (caller-supplied; required)
//
// Locking contract: acquires e.memMu via currentProjectID. Callers must NOT
// hold e.memMu when calling this function.
func (e *Engine) resolveMemoryScope(scope, scopeID string) (resolvedScope, error) {
	switch scope {
	case "", "project":
		id := scopeID
		if id == "" {
			id = e.currentProjectID()
		} else {
			id = normalizeScopePath(id)
		}
		return resolvedScope{"project", id}, nil

	case "global":
		if scopeID != "" {
			return resolvedScope{}, &ErrWithSuggestion{
				Err:        errors.New("global scope cannot have a scope_id"),
				Suggestion: `omit scope_id when using scope="global"`,
				Code:       ErrCodeInvalidArgs,
			}
		}
		return resolvedScope{"global", ""}, nil

	case "task":
		if scopeID == "" {
			return resolvedScope{}, &ErrWithSuggestion{
				Err:        errors.New("task scope requires a scope_id (task id)"),
				Suggestion: `pass scope_id="<task-id>" when using scope="task"`,
				Code:       ErrCodeInvalidArgs,
			}
		}
		return resolvedScope{"task", scopeID}, nil

	case "agent":
		if scopeID == "" {
			return resolvedScope{}, &ErrWithSuggestion{
				Err:        errors.New("agent scope requires a scope_id (agent name)"),
				Suggestion: `pass scope_id="<agent-name>" when using scope="agent"`,
				Code:       ErrCodeInvalidArgs,
			}
		}
		return resolvedScope{"agent", scopeID}, nil

	default:
		return resolvedScope{}, &ErrWithSuggestion{
			Err:        fmt.Errorf("invalid scope: %q — must be one of: global, project, task, agent", scope),
			Suggestion: `omit scope for project (default), or use "global", "task", or "agent"`,
			Code:       ErrCodeInvalidArgs,
		}
	}
}
