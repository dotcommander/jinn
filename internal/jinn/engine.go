// Package jinn is a sandboxed tool executor bound to a working directory.
//
// File naming: tool_<name>.go = a tool handler plus its direct support;
// bare <domain>.go = generic infrastructure/helpers shared across tools.
// A few support files (read_window.go, search_parse.go, search_run.go) keep
// bare names despite being tool-specific — renaming would be churn.
package jinn

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sync"
)

var errRegisteredToolNotDispatched = errors.New("registered tool has no dispatcher")

// Engine is a sandboxed tool executor bound to a working directory.
type Engine struct {
	workDir       string
	version       string // ldflags-injected version ("dev" when un-set)
	tracker       *fileTracker
	rgPath        string     // path to rg binary, empty if unavailable
	fdPath        string     // path to fd binary, empty if unavailable
	LSPTimeoutSec int        // per-query LSP timeout; 0 uses default (10s)
	memMu         sync.Mutex // guards lazy memDB open + scope cache init
	memDB         *sql.DB    // lazily opened on first memory tool call; nil until then
	curScope      string     // cached auto-detected scope; "" until first currentProjectID call
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

// Close releases the engine's resources, closing the lazily-opened memory DB if
// it was ever opened. Safe to call when memDB is nil.
func (e *Engine) Close() error {
	e.memMu.Lock()
	defer e.memMu.Unlock()
	if e.memDB != nil {
		db := e.memDB
		e.memDB = nil
		return db.Close()
	}
	return nil
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
func (e *Engine) Dispatch(ctx context.Context, tool string, args map[string]interface{}) (*ToolResult, map[string]any, error) {
	if _, ok := lookupToolDescriptor(tool); !ok {
		return nil, nil, fmt.Errorf("unknown tool: %s", tool)
	}
	// run_shell is the only tool returning meta; handle it directly.
	if tool == "run_shell" {
		result, meta, err := e.runShell(ctx, args)
		return textResult(result), meta, err
	}
	if tool == "list_tools" {
		return e.dispatchListTools(args)
	}
	if res, ok, err := e.dispatchFileOps(args, tool); ok {
		return res, nil, err
	}
	if res, ok, err := e.dispatchSearchOps(ctx, args, tool); ok {
		return res, nil, err
	}
	if res, ok, err := e.dispatchMemoryMeta(ctx, args, tool); ok {
		return res, nil, err
	}
	if res, ok, err := e.dispatchPlanOps(ctx, args, tool); ok {
		return res, nil, err
	}
	return nil, nil, fmt.Errorf("%w: %s", errRegisteredToolNotDispatched, tool)
}

// dispatchListTools handles the list_tools capability/schema reporting case.
func (e *Engine) dispatchListTools(args map[string]interface{}) (*ToolResult, map[string]any, error) {
	if err := validateToolCatalogSchemaParity(); err != nil {
		return nil, nil, fmt.Errorf("validate tool catalog: %w", err)
	}
	caps := ToolCapabilities{
		JinnVersion: ResolveVersion(e.version),
		Tools:       registeredToolNames(),
		Features:    registeredToolFeatures(),
	}
	capsJSON, err := json.Marshal(caps)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal capabilities: %w", err)
	}
	includeSchema, _ := args["include_schema"].(bool)
	if !includeSchema {
		return textResult(string(capsJSON)), nil, nil
	}
	schema, err := LeanSchema()
	if err != nil {
		return nil, nil, fmt.Errorf("lean schema: %w", err)
	}
	return textResult(string(capsJSON) + "\n\n" + schema), nil, nil
}

// Handler return-shape rule (applies across all dispatch functions below):
// handlers returning a plain string are wrapped via textResult() at the call
// site; handlers that attach Meta/Content (truncation info, checksums, image
// blocks, diffs) return *ToolResult directly and are passed through unchanged.
//
// dispatchFileOps routes file-mutation/read tools whose handlers need no ctx.
// ok=false means "not in this group".
func (e *Engine) dispatchFileOps(args map[string]interface{}, tool string) (*ToolResult, bool, error) {
	switch tool {
	case "read_file":
		result, err := e.readFile(args)
		return result, true, err
	case "multi_read":
		result, err := e.multiRead(args)
		return result, true, err
	case "write_file":
		result, err := e.writeFile(args)
		return textResult(result), true, err
	case "edit_file":
		result, err := e.editFile(args)
		return result, true, err
	case "multi_edit":
		result, err := e.multiEdit(args)
		return result, true, err
	case "apply_patch":
		result, err := e.applyPatch(args)
		return result, true, err
	case "diff_files":
		result, err := e.diffFiles(args)
		return result, true, err
	default:
		return nil, false, nil
	}
}

// dispatchSearchOps routes search/navigation tools.
func (e *Engine) dispatchSearchOps(ctx context.Context, args map[string]interface{}, tool string) (*ToolResult, bool, error) {
	switch tool {
	case "search_files":
		result, err := e.searchFilesContext(ctx, args)
		return textResult(result), true, err
	case "stat_file":
		result, err := e.statFile(args)
		return textResult(result), true, err
	case "list_dir":
		result, err := e.listDir(args)
		return textResult(result), true, err
	case "find_files":
		result, err := e.findFiles(ctx, args)
		return textResult(result), true, err
	case "lsp_query":
		result, err := e.lspQuery(ctx, args)
		return textResult(result), true, err
	case "search_replace":
		result, err := e.searchReplace(ctx, args)
		return result, true, err
	default:
		return nil, false, nil
	}
}

// dispatchMemoryMeta routes memory/project/undo metadata tools.
func (e *Engine) dispatchMemoryMeta(ctx context.Context, args map[string]interface{}, tool string) (*ToolResult, bool, error) {
	switch tool {
	case "detect_project":
		result, err := e.detectProject(args)
		return textResult(result), true, err
	case "memory":
		result, err := e.memoryTool(ctx, args)
		return textResult(result), true, err
	case "undo":
		result, err := e.undoTool(args)
		return textResult(result), true, err
	default:
		return nil, false, nil
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

// strArg extracts an optional string arg.
func strArg(args map[string]interface{}, key string) string {
	s, _ := args[key].(string)
	return s
}

// boolArg extracts an optional bool arg.
func boolArg(args map[string]interface{}, key string) bool {
	b, _ := args[key].(bool)
	return b
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
