package jinn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time" // time.Now() for updated_at, time.RFC3339 via parseTimestamp
)

// insertTaskTx inserts a new pending task and returns the populated Task.
func insertTaskTx(ctx context.Context, tx *sql.Tx, title, description, projectID string, priority int) (*Task, error) {
	id := newID("task")
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO tasks (id, title, description, status, priority, project_id, version, created_at, updated_at)
		VALUES (?, ?, ?, 'pending', ?, ?, 1, ?, ?)
	`, id, title, description, priority, projectID, now, now)
	if err != nil {
		return nil, fmt.Errorf("insert task: %w", err)
	}
	return getTaskTx(ctx, tx, id)
}

// getTaskTx fetches a single task by id within a transaction.
func getTaskTx(ctx context.Context, tx *sql.Tx, taskID string) (*Task, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, title, description, status, priority, project_id, blocked_reason, version, created_at, updated_at
		FROM tasks WHERE id = ?
	`, taskID)
	t, err := scanTaskRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	return t, err
}

// getTaskDB fetches a single task by id (outside transaction).
func getTaskDB(ctx context.Context, db *sql.DB, taskID string) (*Task, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, title, description, status, priority, project_id, blocked_reason, version, created_at, updated_at
		FROM tasks WHERE id = ?
	`, taskID)
	t, err := scanTaskRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	return t, err
}

// scanner is satisfied by both *sql.Row and *sql.Rows, letting single-row and
// row-loop scans share one implementation.
type scanner interface {
	Scan(dest ...any) error
}

// scanTaskRow scans one task row from a *sql.Row or *sql.Rows.
func scanTaskRow(s scanner) (*Task, error) {
	var t Task
	var desc, projectID, blockedReason sql.NullString
	var createdAt, updatedAt string
	if err := s.Scan(
		&t.ID, &t.Title, &desc,
		&t.Status, &t.Priority,
		&projectID, &blockedReason,
		&t.Version, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	t.Description = desc.String
	t.ProjectID = projectID.String
	t.BlockedReason = blockedReason.String
	t.CreatedAt, _ = parseTimestamp(createdAt)
	t.UpdatedAt, _ = parseTimestamp(updatedAt)
	return &t, nil
}

// updateTaskStatusTx performs an in-transaction read-modify-write status change.
// blocked_reason: cleared unless newStatus=="blocked" (preserved then).
func updateTaskStatusTx(ctx context.Context, tx *sql.Tx, taskID, newStatus string) (*Task, error) {
	var currentVersion int
	err := tx.QueryRowContext(ctx, `SELECT version FROM tasks WHERE id = ?`, taskID).Scan(&currentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if err != nil {
		return nil, fmt.Errorf("read task version: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?,
		    blocked_reason = CASE WHEN ? = 'blocked' THEN blocked_reason ELSE NULL END,
		    version = version + 1,
		    updated_at = ?
		WHERE id = ? AND version = ?
	`, newStatus, newStatus, time.Now().UTC().Format(time.RFC3339), taskID, currentVersion)
	if err != nil {
		return nil, fmt.Errorf("update task status: %w", err)
	}

	return getTaskTx(ctx, tx, taskID)
}

// setBlockedReasonTx sets blocked_reason (truncated at 256 runes).
func setBlockedReasonTx(ctx context.Context, tx *sql.Tx, taskID, reason string) error {
	runes := []rune(reason)
	if len(runes) > 256 {
		reason = string(runes[:256])
	}
	var val any
	if reason != "" {
		val = reason
	}
	_, err := tx.ExecContext(ctx, `UPDATE tasks SET blocked_reason = ? WHERE id = ?`, val, taskID)
	if err != nil {
		return fmt.Errorf("set blocked_reason: %w", err)
	}
	return nil
}

// listTasksDB returns tasks ordered by priority DESC, created_at ASC.
func listTasksDB(ctx context.Context, db *sql.DB, statusFilter, projectFilter string) ([]*Task, error) {
	q := `SELECT id, title, description, status, priority, project_id, blocked_reason, version, created_at, updated_at
		FROM tasks WHERE 1=1`
	var qargs []any
	if statusFilter != "" {
		q += ` AND status = ?`
		qargs = append(qargs, statusFilter)
	}
	if projectFilter != "" {
		q += ` AND project_id = ?`
		qargs = append(qargs, projectFilter)
	}
	q += ` ORDER BY priority DESC, created_at ASC`

	rows, err := db.QueryContext(ctx, q, qargs...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		t, err := scanTaskRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}
