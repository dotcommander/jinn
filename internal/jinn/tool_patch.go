package jinn

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolvedOp pairs a parsed patch operation with its checked absolute path.
type resolvedOp struct {
	op       patchOperation
	resolved string
}

// preflightResult holds the validated old/new content for one operation,
// computed during the no-write preflight phase and reused at apply time.
type preflightResult struct {
	oldContent string
	newContent string
}

// applyPatch executes a parsed set of Codex-style patch operations.
// Phase 1: validate all operations (preflight) without writing.
// Phase 2: apply operations with per-file atomic writes and undo snapshots.
func (e *Engine) applyPatch(args map[string]interface{}) (*ToolResult, error) {
	patchText, _ := args["patch"].(string)
	if patchText == "" {
		return nil, errors.New("patch parameter is required")
	}

	ops, err := parsePatch(patchText)
	if err != nil {
		return nil, fmt.Errorf("parse patch: %w", err)
	}

	resolved, err := e.resolvePatchPaths(ops)
	if err != nil {
		return nil, err
	}

	preflights, err := preflightPatch(resolved)
	if err != nil {
		return nil, err
	}

	if boolArg(args, "dry_run") {
		return renderPatchDryRun(resolved, preflights), nil
	}

	return e.applyPatchOps(resolved, preflights)
}

// resolvePatchPaths checks every operation's path against the engine's
// path policy, returning operations paired with their resolved absolute paths.
func (e *Engine) resolvePatchPaths(ops []patchOperation) ([]resolvedOp, error) {
	resolved := make([]resolvedOp, 0, len(ops))
	for _, op := range ops {
		resolvedPath, err := e.checkPath(op.path)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", op.kind, op.path, err)
		}
		resolved = append(resolved, resolvedOp{op: op, resolved: resolvedPath})
	}
	return resolved, nil
}

// patchOpHandlers is the single source of truth mapping each patch op kind to
// its preflight (no-write validation) and apply (write) handlers. Unknown kinds
// have no entry; callers treat a missing entry as a no-op, matching the prior
// switch statements' default behavior.
var patchOpHandlers = map[string]struct {
	preflight func(resolvedOp) (preflightResult, error)
	apply     func(*Engine, resolvedOp, preflightResult) (applyOpResult, error)
}{
	"add":    {preflightAdd, (*Engine).applyAdd},
	"delete": {preflightDelete, (*Engine).applyDelete},
	"update": {preflightUpdate, (*Engine).applyUpdate},
}

// preflightPatch validates all operations without writing, returning the
// computed old/new content for each so the apply phase can reuse it.
func preflightPatch(resolved []resolvedOp) ([]preflightResult, error) {
	preflights := make([]preflightResult, len(resolved))

	for i, r := range resolved {
		h, ok := patchOpHandlers[r.op.kind]
		if !ok {
			continue
		}
		pre, err := h.preflight(r)
		if err != nil {
			return nil, err
		}
		preflights[i] = pre
	}

	return preflights, nil
}

// preflightAdd validates an add operation: the target must not already exist.
func preflightAdd(r resolvedOp) (preflightResult, error) {
	if _, err := os.Stat(r.resolved); err == nil {
		return preflightResult{}, fmt.Errorf("add %s: file already exists", r.op.path)
	} else if !os.IsNotExist(err) {
		return preflightResult{}, fmt.Errorf("add %s: %w", r.op.path, err)
	}
	return preflightResult{newContent: r.op.contents}, nil
}

// preflightDelete validates a delete operation and captures the file's
// current content for the undo snapshot.
func preflightDelete(r resolvedOp) (preflightResult, error) {
	if _, err := os.Stat(r.resolved); os.IsNotExist(err) {
		return preflightResult{}, fmt.Errorf("delete %s: file does not exist", r.op.path)
	}
	data, err := os.ReadFile(r.resolved)
	if err != nil {
		return preflightResult{}, fmt.Errorf("delete %s: %w", r.op.path, err)
	}
	return preflightResult{oldContent: string(data)}, nil
}

// preflightUpdate validates an update operation, deriving the new content
// from the chunks against the file's current content.
func preflightUpdate(r resolvedOp) (preflightResult, error) {
	data, err := os.ReadFile(r.resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return preflightResult{}, fmt.Errorf("update %s: file not found", r.op.path)
		}
		return preflightResult{}, fmt.Errorf("update %s: %w", r.op.path, err)
	}

	updated, err := deriveUpdatedContent(r.op.path, string(data), r.op.chunks)
	if err != nil {
		return preflightResult{}, fmt.Errorf("update %s: %w", r.op.path, err)
	}
	return preflightResult{oldContent: string(data), newContent: updated}, nil
}

// renderPatchDryRun produces the no-write summary of the operations.
func renderPatchDryRun(resolved []resolvedOp, preflights []preflightResult) *ToolResult {
	var parts []string
	for i, r := range resolved {
		switch r.op.kind {
		case "add":
			parts = append(parts, fmt.Sprintf("would add %s", r.op.path))
		case "delete":
			parts = append(parts, fmt.Sprintf("would delete %s", r.op.path))
		case "update":
			dr := generateDiff(preflights[i].oldContent, preflights[i].newContent, r.op.path, 3)
			parts = append(parts, fmt.Sprintf("would update %s:\n%s", r.op.path, dr.Diff))
		}
	}
	return &ToolResult{
		Text: fmt.Sprintf("[dry-run] patch with %d operation(s):\n%s", len(resolved), strings.Join(parts, "\n")),
	}
}

// applyOpResult holds the per-operation outcome accumulated during apply.
type applyOpResult struct {
	summary          string
	diff             string
	firstChangedLine int
	undoID           string
}

// applyPatchOps performs phase 2: apply all operations with per-file atomic
// writes and undo snapshots, accumulating diffs for the result metadata.
func (e *Engine) applyPatchOps(resolved []resolvedOp, preflights []preflightResult) (*ToolResult, error) {
	var results []string
	var allDiffs []string
	var firstLine int
	var applied []appliedRef

	for i, r := range resolved {
		h, ok := patchOpHandlers[r.op.kind]
		if !ok {
			continue
		}
		res, err := h.apply(e, r, preflights[i])
		if err != nil {
			return nil, partialApplyErr("apply_patch", applied, len(resolved), err)
		}
		applied = append(applied, appliedRef{path: r.op.path, undoID: res.undoID})
		results = append(results, res.summary)
		if res.diff != "" {
			allDiffs = append(allDiffs, res.diff)
		}
		if firstLine == 0 && res.firstChangedLine > 0 {
			firstLine = res.firstChangedLine
		}
	}

	meta := map[string]any{
		"edit": editDetails{
			Diff:             strings.Join(allDiffs, "\n"),
			FirstChangedLine: firstLine,
		},
	}

	return &ToolResult{
		Text: fmt.Sprintf("applied patch with %d operation(s):\n%s", len(resolved), strings.Join(results, "\n")),
		Meta: meta,
	}, nil
}

// applyAdd writes a new file, creating parent directories and recording an
// undo snapshot of any pre-existing content.
func (e *Engine) applyAdd(r resolvedOp, pre preflightResult) (applyOpResult, error) {
	dir := filepath.Dir(r.resolved)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return applyOpResult{}, fmt.Errorf("add %s: mkdir: %w", r.op.path, err)
	}
	preContent, _ := os.ReadFile(r.resolved)
	id, err := e.snapshotAndWrite(r.resolved, r.op.path, "apply_patch", preContent, pre.newContent)
	if err != nil {
		return applyOpResult{}, fmt.Errorf("add %s: %w", r.op.path, err)
	}
	return applyOpResult{summary: fmt.Sprintf("added %s", r.op.path), undoID: id}, nil
}

// applyDelete removes a file after recording an undo snapshot.
func (e *Engine) applyDelete(r resolvedOp, pre preflightResult) (applyOpResult, error) {
	_ = pre // pre unused: delete needs no preflight payload
	preContent, _ := os.ReadFile(r.resolved)
	id := e.recordSnapshot(r.resolved, r.op.path, "apply_patch", preContent)
	if err := os.Remove(r.resolved); err != nil {
		return applyOpResult{}, fmt.Errorf("delete %s: %w", r.op.path, err)
	}
	return applyOpResult{summary: fmt.Sprintf("deleted %s", r.op.path), undoID: id}, nil
}

// applyUpdate writes updated content after a staleness check, recording an
// undo snapshot and computing the diff for the result metadata.
func (e *Engine) applyUpdate(r resolvedOp, pre preflightResult) (applyOpResult, error) {
	if err := e.tracker.checkStale(r.resolved); err != nil {
		return applyOpResult{}, fmt.Errorf("update %s: %w", r.op.path, err)
	}
	id, err := e.snapshotAndWrite(r.resolved, r.op.path, "apply_patch", []byte(pre.oldContent), pre.newContent)
	if err != nil {
		return applyOpResult{}, fmt.Errorf("update %s: %w", r.op.path, err)
	}
	dr := generateDiff(pre.oldContent, pre.newContent, r.op.path, 3)
	return applyOpResult{
		summary:          fmt.Sprintf("updated %s", r.op.path),
		diff:             dr.Diff,
		firstChangedLine: dr.FirstChangedLine,
		undoID:           id,
	}, nil
}
