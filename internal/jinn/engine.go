package jinn

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sync"
)

// Engine is a sandboxed tool executor bound to a working directory.
type Engine struct {
	workDir string
	tracker *fileTracker
	rgPath  string     // path to rg binary, empty if unavailable
	memMu   sync.Mutex // guards memory file reads and writes
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

// Dispatch routes a tool call to the appropriate handler and returns structured
// metadata alongside the result string. Meta keys:
//   - "risk":           pre-execution risk level set by run_shell ("safe", "caution", "dangerous")
//   - "classification": exit-code class set by run_shell ("success", "expected_nonzero", "error", "timeout", "signal")
//
// Tools that don't set meta return a nil map. Callers should treat nil as empty.
// Option A: meta map in return signature keeps Dispatch pure and thread-safe.
func (e *Engine) Dispatch(ctx context.Context, tool string, args map[string]interface{}) (string, map[string]string, error) {
	switch tool {
	case "run_shell":
		result, meta, err := e.runShell(ctx, args)
		return result, meta, err
	case "read_file":
		result, err := e.readFile(args)
		return result, nil, err
	case "write_file":
		result, err := e.writeFile(args)
		return result, nil, err
	case "edit_file":
		result, err := e.editFile(args)
		return result, nil, err
	case "multi_edit":
		result, err := e.multiEdit(args)
		return result, nil, err
	case "search_files":
		result, err := e.searchFiles(args)
		return result, nil, err
	case "stat_file":
		result, err := e.statFile(args)
		return result, nil, err
	case "list_dir":
		result, err := e.listDir(args)
		return result, nil, err
	case "list_tools":
		return Schema, nil, nil
	case "checksum_tree":
		result, err := e.checksumTree(args)
		return result, nil, err
	case "detect_project":
		result, err := e.detectProject(args)
		return result, nil, err
	case "memory":
		result, err := e.memoryTool(args)
		return result, nil, err
	case "lsp_query":
		result, err := e.lspQuery(args)
		return result, nil, err
	default:
		return "", nil, fmt.Errorf("unknown tool: %s", tool)
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
