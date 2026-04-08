package jinn

import (
	"context"
	"fmt"
	"runtime/debug"
)

// Engine is a sandboxed tool executor bound to a working directory.
type Engine struct {
	workDir string
	tracker *fileTracker
}

// New creates an Engine rooted at the given working directory.
func New(workDir string) *Engine {
	return &Engine{workDir: workDir, tracker: newFileTracker()}
}

// Dispatch routes a tool call to the appropriate handler.
func (e *Engine) Dispatch(ctx context.Context, tool string, args map[string]interface{}) (string, error) {
	switch tool {
	case "run_shell":
		return e.runShell(ctx, args), nil
	case "read_file":
		return e.readFile(args), nil
	case "write_file":
		return e.writeFile(args), nil
	case "edit_file":
		return e.editFile(args), nil
	case "multi_edit":
		return e.multiEdit(args), nil
	case "search_files":
		return e.searchFiles(args), nil
	case "stat_file":
		return e.statFile(args), nil
	case "list_dir":
		return e.listDir(args), nil
	default:
		return "", fmt.Errorf("unknown tool: %s", tool)
	}
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
