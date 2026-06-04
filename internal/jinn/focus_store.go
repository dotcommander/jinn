package jinn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	statusInProgress = "in_progress"
	statusBlocked    = "blocked"
	statusPending    = "pending"
)

// blockedReasonIsFailure reports whether a blocked_reason marks a failure block.
func blockedReasonIsFailure(reason string) bool {
	return strings.HasPrefix(reason, "failure:")
}

// FocusResult is the outcome of determineFocusTx: the chosen task id + the rule
// that fired (rule1..rule5) for observability.
type FocusResult struct {
	TaskID string
	Rule   string
}

// determineFocusTx applies the deterministic 5-rule focus selection.
// deltas are events since the old cursor (used for rule 2). projectID scopes
// rule 4 (project-first, then global fallback).
func determineFocusTx(ctx context.Context, tx *sql.Tx, currentFocusID string, deltas []*Event, projectID string) (FocusResult, error) {
	// Rule 1 / 1.5: keep current focus.
	if currentFocusID != "" {
		t, err := getTaskTx(ctx, tx, currentFocusID)
		if err == nil {
			if t.Status == statusInProgress {
				return FocusResult{currentFocusID, fmt.Sprintf("rule1: kept in_progress focus on %s", currentFocusID)}, nil
			}
			if t.Status == statusBlocked && !blockedReasonIsFailure(t.BlockedReason) {
				return FocusResult{currentFocusID, fmt.Sprintf("rule1.5: kept non-failure blocked focus on %s", currentFocusID)}, nil
			}
		}
	}

	// Rule 2: task_assigned delta whose task is pending (and in project if scoped).
	// First pass: collect ordered, de-duplicated candidate task ids (first occurrence wins).
	var candidates []string
	seen := make(map[string]bool)
	for _, ev := range deltas {
		if ev.Kind != "task_assigned" || ev.TaskID == "" || seen[ev.TaskID] {
			continue
		}
		seen[ev.TaskID] = true
		candidates = append(candidates, ev.TaskID)
	}
	if len(candidates) > 0 {
		// One batched query for status + project_id of all candidate ids.
		placeholders := strings.Repeat("?,", len(candidates))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]any, len(candidates))
		for i, id := range candidates {
			args[i] = id
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT id, status, project_id FROM tasks WHERE id IN (`+placeholders+`)`, args...)
		if err != nil {
			return FocusResult{}, fmt.Errorf("batch fetch rule2 candidates: %w", err)
		}
		type taskMeta struct {
			status    string
			projectID string
		}
		metas := make(map[string]taskMeta, len(candidates))
		for rows.Next() {
			var id, status string
			var pid sql.NullString
			if err := rows.Scan(&id, &status, &pid); err != nil {
				rows.Close()
				return FocusResult{}, fmt.Errorf("scan rule2 candidate: %w", err)
			}
			metas[id] = taskMeta{status: status, projectID: pid.String}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return FocusResult{}, fmt.Errorf("iterate rule2 candidates: %w", err)
		}
		rows.Close()
		// Second pass: first candidate (in delta order) that is pending and in-scope.
		for _, id := range candidates {
			m, ok := metas[id]
			if !ok || m.status != statusPending {
				continue
			}
			if projectID != "" && m.projectID != projectID {
				continue
			}
			return FocusResult{id, fmt.Sprintf("rule2: assigned via task_assigned event for %s", id)}, nil
		}
	}

	// Rule 3: previously-blocked focus now pending.
	if currentFocusID != "" {
		t, err := getTaskTx(ctx, tx, currentFocusID)
		if err == nil && t.Status == statusPending {
			return FocusResult{currentFocusID, fmt.Sprintf("rule3: resumed now-pending focus on %s", currentFocusID)}, nil
		}
	}

	// Rule 4: highest-priority pending task, project-first then global fallback.
	if projectID != "" {
		var id string
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM tasks WHERE status = 'pending' AND project_id = ?
			ORDER BY priority DESC, created_at ASC LIMIT 1
		`, projectID).Scan(&id)
		if err == nil {
			return FocusResult{id, fmt.Sprintf("rule4: selected highest-priority pending task %s", id)}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return FocusResult{}, fmt.Errorf("select project focus: %w", err)
		}
	}
	var id string
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM tasks WHERE status = 'pending'
		ORDER BY priority DESC, created_at ASC LIMIT 1
	`).Scan(&id)
	if err == nil {
		return FocusResult{id, fmt.Sprintf("rule4: selected highest-priority pending task %s", id)}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return FocusResult{}, fmt.Errorf("select global focus: %w", err)
	}

	// Rule 5: no work.
	return FocusResult{"", "rule5: no pending tasks available"}, nil
}
