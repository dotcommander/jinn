package jinn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// applyPatch executes a parsed set of Codex-style patch operations.
// Phase 1: validate all operations (preflight) without writing.
// Phase 2: apply all operations atomically with undo snapshots.
func (e *Engine) applyPatch(args map[string]interface{}) (*ToolResult, error) {
	patchText, _ := args["patch"].(string)
	if patchText == "" {
		return nil, fmt.Errorf("patch parameter is required")
	}

	ops, err := parsePatch(patchText)
	if err != nil {
		return nil, fmt.Errorf("parse patch: %w", err)
	}

	// Resolve all paths upfront.
	type resolvedOp struct {
		op       patchOperation
		resolved string
	}
	var resolved []resolvedOp
	for _, op := range ops {
		resolvedPath, err := e.checkPath(op.path)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", op.kind, op.path, err)
		}
		resolved = append(resolved, resolvedOp{op: op, resolved: resolvedPath})
	}

	// Phase 1: preflight — validate all operations without writing.
	type preflightResult struct {
		oldContent string
		newContent string
	}
	preflights := make([]preflightResult, len(resolved))

	for i, r := range resolved {
		switch r.op.kind {
		case "add":
			preflights[i].newContent = r.op.contents

		case "delete":
			if _, err := os.Stat(r.resolved); os.IsNotExist(err) {
				return nil, fmt.Errorf("delete %s: file does not exist", r.op.path)
			}
			data, err := os.ReadFile(r.resolved)
			if err != nil {
				return nil, fmt.Errorf("delete %s: %w", r.op.path, err)
			}
			preflights[i].oldContent = string(data)

		case "update":
			data, err := os.ReadFile(r.resolved)
			if err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("update %s: file not found", r.op.path)
				}
				return nil, fmt.Errorf("update %s: %w", r.op.path, err)
			}
			preflights[i].oldContent = string(data)

			updated, err := deriveUpdatedContent(r.op.path, string(data), r.op.chunks)
			if err != nil {
				return nil, fmt.Errorf("update %s: %w", r.op.path, err)
			}
			preflights[i].newContent = updated
		}
	}

	// Phase 2: apply all operations.
	if dryRun, ok := args["dry_run"].(bool); ok && dryRun {
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
		}, nil
	}

	var results []string
	var allDiffs []string
	var firstLine int

	for i, r := range resolved {
		switch r.op.kind {
		case "add":
			dir := filepath.Dir(r.resolved)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("add %s: mkdir: %w", r.op.path, err)
			}
			preContent, _ := os.ReadFile(r.resolved)
			_ = e.recordSnapshot(r.resolved, r.op.path, "apply_patch", preContent)
			if err := e.atomicWriteFile(r.resolved, preflights[i].newContent); err != nil {
				return nil, fmt.Errorf("add %s: %w", r.op.path, err)
			}
			results = append(results, fmt.Sprintf("added %s", r.op.path))

		case "delete":
			preContent, _ := os.ReadFile(r.resolved)
			_ = e.recordSnapshot(r.resolved, r.op.path, "apply_patch", preContent)
			if err := os.Remove(r.resolved); err != nil {
				return nil, fmt.Errorf("delete %s: %w", r.op.path, err)
			}
			results = append(results, fmt.Sprintf("deleted %s", r.op.path))

		case "update":
			if err := e.tracker.checkStale(r.resolved); err != nil {
				return nil, fmt.Errorf("update %s: %w", r.op.path, err)
			}
			_ = e.recordSnapshot(r.resolved, r.op.path, "apply_patch", []byte(preflights[i].oldContent))
			if err := e.atomicWriteFile(r.resolved, preflights[i].newContent); err != nil {
				return nil, fmt.Errorf("update %s: %w", r.op.path, err)
			}
			dr := generateDiff(preflights[i].oldContent, preflights[i].newContent, r.op.path, 3)
			if dr.Diff != "" {
				allDiffs = append(allDiffs, dr.Diff)
			}
			if firstLine == 0 && dr.FirstChangedLine > 0 {
				firstLine = dr.FirstChangedLine
			}
			results = append(results, fmt.Sprintf("updated %s", r.op.path))
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
