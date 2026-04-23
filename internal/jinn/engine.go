package jinn

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime/debug"
)

// Engine is a sandboxed tool executor bound to a working directory.
type Engine struct {
	workDir string
	tracker *fileTracker
	rgPath  string // path to rg binary, empty if unavailable
}

// New creates an Engine rooted at the given working directory.
// The workDir is resolved via EvalSymlinks so that path boundary checks
// work correctly on platforms where temp dirs are symlinks (e.g., macOS).
func New(workDir string) *Engine {
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
		workDir = resolved
	}
	rgPath, _ := exec.LookPath("rg")
	return &Engine{workDir: workDir, tracker: newFileTracker(), rgPath: rgPath}
}

// Dispatch routes a tool call to the appropriate handler.
// When the returned error wraps *ErrWithSuggestion, callers can extract
// the suggestion via errors.As for inclusion in their response envelope.
func (e *Engine) Dispatch(ctx context.Context, tool string, args map[string]interface{}) (string, error) {
	switch tool {
	case "run_shell":
		return e.runShell(ctx, args)
	case "read_file":
		return e.readFile(args)
	case "write_file":
		return e.writeFile(args)
	case "edit_file":
		return e.editFile(args)
	case "multi_edit":
		return e.multiEdit(args)
	case "search_files":
		return e.searchFiles(args)
	case "stat_file":
		return e.statFile(args)
	case "list_dir":
		return e.listDir(args)
	case "list_tools":
		return Schema, nil
	case "checksum_tree":
		return e.checksumTree(args)
	case "detect_project":
		return e.detectProject(args)
	default:
		return "", fmt.Errorf("unknown tool: %s", tool)
	}
}

// intArg reads an int-valued argument from the JSON args map.
// Returns def when the key is absent, non-numeric, or <= 0.
func intArg(args map[string]interface{}, key string, def int) int {
	if v, ok := args[key].(float64); ok && int(v) > 0 {
		return int(v)
	}
	return def
}

// ResolveVersion returns a human-readable version string, preferring
// ldflags-injected version, then VCS revision, then module version.
func ResolveVersion(ldVersion string) string {
	if ldVersion != "dev" {
		return ldVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return s.Value[:7]
			}
		}
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	return ldVersion
}
