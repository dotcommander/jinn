package jinn

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sync"
)

// Engine is a sandboxed tool executor bound to a working directory.
type Engine struct {
	workDir string
	version string     // ldflags-injected version ("dev" when un-set)
	tracker *fileTracker
	rgPath        string     // path to rg binary, empty if unavailable
	fdPath        string     // path to fd binary, empty if unavailable
	LSPTimeoutSec int        // per-query LSP timeout; 0 uses default (10s)
	memMu         sync.Mutex // guards memory file reads and writes
}

// New creates an Engine rooted at the given working directory.
// The workDir is resolved via EvalSymlinks so that path boundary checks
// work correctly on platforms where temp dirs are symlinks (e.g., macOS).
func New(workDir string, version string) *Engine {
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
		workDir = resolved
	}
	rgPath, _ := exec.LookPath("rg")
	fdPath, _ := exec.LookPath("fd")
	return &Engine{workDir: workDir, version: version, tracker: newFileTracker(), rgPath: rgPath, fdPath: fdPath, LSPTimeoutSec: 10}
}

// ToolResult is the structured output of a tool handler.
// Text results populate Text (and Content is nil).
// Image results populate Content with typed blocks (and Text is empty).
// Meta carries optional structured metadata (e.g. truncation info for read_file).
type ToolResult struct {
	Text    string         // human/LLM-readable text result
	Content []ContentBlock // structured content blocks (images, etc.)
	Meta    map[string]any // structured metadata for callers (truncation, etc.)
}

// textResult wraps a plain string as a ToolResult.
func textResult(s string) *ToolResult {
	return &ToolResult{Text: s}
}

// Dispatch routes a tool call to the appropriate handler and returns structured
// metadata alongside the result. Meta keys:
//   - "risk":           pre-execution risk level set by run_shell ("safe", "caution", "dangerous")
//   - "classification": exit-code class set by run_shell ("success", "expected_nonzero", "error", "timeout", "signal")
//
// Tools that don't set meta return a nil map. Callers should treat nil as empty.
func (e *Engine) Dispatch(ctx context.Context, tool string, args map[string]interface{}) (*ToolResult, map[string]string, error) {
	switch tool {
	case "run_shell":
		result, meta, err := e.runShell(ctx, args)
		return textResult(result), meta, err
	case "read_file":
		result, err := e.readFile(args)
		return result, nil, err
	case "write_file":
		result, err := e.writeFile(args)
		return textResult(result), nil, err
	case "edit_file":
		result, err := e.editFile(args)
		return result, nil, err
	case "multi_edit":
		result, err := e.multiEdit(args)
		return result, nil, err
	case "apply_patch":
		result, err := e.applyPatch(args)
		return result, nil, err
	case "diff_files":
		result, err := e.diffFiles(args)
		return result, nil, err
	case "search_files":
		result, err := e.searchFiles(args)
		return textResult(result), nil, err
	case "stat_file":
		result, err := e.statFile(args)
		return textResult(result), nil, err
	case "list_dir":
		result, err := e.listDir(args)
		return textResult(result), nil, err
	case "find_files":
		result, err := e.findFiles(args)
		return textResult(result), nil, err
	case "list_tools":
		tools := []string{
			"run_shell", "read_file", "write_file", "edit_file", "multi_edit",
			"apply_patch", "diff_files", "search_files", "stat_file", "list_dir",
			"find_files", "list_tools", "checksum_tree", "detect_project",
			"memory", "undo", "lsp_query",
		}
		caps := ToolCapabilities{
			JinnVersion: ResolveVersion(e.version),
			Tools:       tools,
			Features:    toolFeatures,
		}
		capsJSON, err := json.Marshal(caps)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal capabilities: %w", err)
		}
		return textResult(string(capsJSON) + "\n\n" + Schema), nil, nil
	case "checksum_tree":
		result, err := e.checksumTree(args)
		return textResult(result), nil, err
	case "detect_project":
		result, err := e.detectProject(args)
		return textResult(result), nil, err
	case "memory":
		result, err := e.memoryTool(args)
		return textResult(result), nil, err
	case "undo":
		result, err := e.undoTool(args)
		return textResult(result), nil, err
	case "lsp_query":
		result, err := e.lspQuery(args)
		return textResult(result), nil, err
	default:
		return nil, nil, fmt.Errorf("unknown tool: %s", tool)
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
