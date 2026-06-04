package jinn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// loadOrCreateAgentStateTx ensures an agent_state row exists, then returns its
// cursor + focus pointers. Exists is always true on return.
func loadOrCreateAgentStateTx(ctx context.Context, tx *sql.Tx, agentName string) (AgentCursorFocus, error) {
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO agent_state (agent_name, last_seen_event_id, version, last_active_at)
		VALUES (?, 0, 1, ?)
	`, agentName, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return AgentCursorFocus{}, fmt.Errorf("ensure agent state: %w", err)
	}
	return loadAgentState(ctx, tx, agentName)
}

// rowQuerier is satisfied by both *sql.Tx and *sql.DB, letting tx and non-tx
// reads share one implementation.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// loadAgentState reads cursor + focus pointers without creating a row.
// Returns Exists=false (zero value otherwise) when no row exists.
func loadAgentState(ctx context.Context, q rowQuerier, agentName string) (AgentCursorFocus, error) {
	var out AgentCursorFocus
	var focusTaskID, focusProjectID sql.NullString
	err := q.QueryRowContext(ctx, `
		SELECT last_seen_event_id, focus_task_id, focus_project_id
		FROM agent_state WHERE agent_name = ?
	`, agentName).Scan(&out.Cursor, &focusTaskID, &focusProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentCursorFocus{Exists: false}, nil
	}
	if err != nil {
		return AgentCursorFocus{}, fmt.Errorf("load agent state: %w", err)
	}
	out.TaskID = focusTaskID.String
	out.ProjectID = focusProjectID.String
	out.Exists = true
	return out, nil
}

// persistAgentStateTx atomically advances cursor (monotonic), sets focus task +
// project, bumps version, and stamps last_active_at. Row must already exist.
// focusTaskID/focusProjectID "" -> stored NULL.
func persistAgentStateTx(ctx context.Context, tx *sql.Tx, agentName string, newCursor int64, focusTaskID, focusProjectID string) error {
	var taskVal, projVal any
	if focusTaskID != "" {
		taskVal = focusTaskID
	}
	if focusProjectID != "" {
		projVal = focusProjectID
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE agent_state
		SET last_seen_event_id = MAX(last_seen_event_id, ?),
		    focus_task_id      = ?,
		    focus_project_id   = ?,
		    version            = version + 1,
		    last_active_at     = ?
		WHERE agent_name = ?
	`, newCursor, taskVal, projVal, time.Now().UTC().Format(time.RFC3339), agentName)
	if err != nil {
		return fmt.Errorf("persist agent state: %w", err)
	}
	return nil
}
