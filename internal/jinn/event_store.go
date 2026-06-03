package jinn

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// validateEventPayload enforces event payload constraints (ported from vybe).
func validateEventPayload(kind, agentName, message, metadata string) error {
	if kind == "" {
		return errInvalidArgs("event kind is required", "provide a non-empty kind")
	}
	if len(kind) > maxEventKind {
		return errInvalidArgs(fmt.Sprintf("event kind exceeds max length (%d)", maxEventKind), "shorten kind")
	}
	if agentName == "" {
		return errInvalidArgs("agent_name is required", "provide a non-empty agent name")
	}
	if len(agentName) > maxEventAgent {
		return errInvalidArgs(fmt.Sprintf("agent_name exceeds max length (%d)", maxEventAgent), "shorten agent_name")
	}
	if message == "" {
		return errInvalidArgs("event message is required", "provide a non-empty message")
	}
	if len(message) > maxEventMessage {
		return errInvalidArgs(fmt.Sprintf("message exceeds max length (%d chars)", maxEventMessage), "shorten message")
	}
	if metadata != "" {
		if len(metadata) > maxEventMetadata {
			return errInvalidArgs(fmt.Sprintf("metadata exceeds max length (%d bytes)", maxEventMetadata), "shorten metadata")
		}
		if !json.Valid([]byte(metadata)) {
			return errInvalidArgs("metadata must be valid JSON", "pass a JSON object or valid JSON string")
		}
	}
	return nil
}

// insertEventTx validates and inserts an event row inside a transaction.
func insertEventTx(ctx context.Context, tx *sql.Tx, kind, agentName, projectID, taskID, message, metadata string) (int64, error) {
	kind = strings.TrimSpace(kind)
	agentName = strings.TrimSpace(agentName)
	message = strings.TrimSpace(message)
	if err := validateEventPayload(kind, agentName, message, metadata); err != nil {
		return 0, err
	}
	var metaVal any
	if metadata != "" {
		metaVal = metadata
	}
	var taskIDVal any
	if taskID != "" {
		taskIDVal = taskID
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO events (kind, agent_name, project_id, task_id, message, metadata)
		VALUES (?, ?, ?, ?, ?, ?)
	`, kind, agentName, projectID, taskIDVal, message, metaVal)
	if err != nil {
		return 0, fmt.Errorf("insert event: %w", err)
	}
	return res.LastInsertId()
}

// listEventsDB returns events ascending by id; limit is capped at 100.
func listEventsDB(ctx context.Context, db *sql.DB, taskFilter, projectFilter, kindFilter string, limit int, sinceID int64) ([]*Event, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	q := `SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
		FROM events WHERE 1=1`
	var qargs []any
	if sinceID > 0 {
		q += ` AND id > ?`
		qargs = append(qargs, sinceID)
	}
	if taskFilter != "" {
		q += ` AND task_id = ?`
		qargs = append(qargs, taskFilter)
	}
	if projectFilter != "" {
		q += ` AND project_id = ?`
		qargs = append(qargs, projectFilter)
	}
	if kindFilter != "" {
		q += ` AND kind = ?`
		qargs = append(qargs, kindFilter)
	}
	q += ` ORDER BY id ASC LIMIT ?`
	qargs = append(qargs, limit)

	rows, err := db.QueryContext(ctx, q, qargs...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var events []*Event
	for rows.Next() {
		var ev Event
		var projectID, taskID, metadata sql.NullString
		var createdAt string
		if err := rows.Scan(
			&ev.ID, &ev.Kind, &ev.AgentName,
			&projectID, &taskID,
			&ev.Message, &metadata, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		ev.ProjectID = projectID.String
		ev.TaskID = taskID.String
		ev.Metadata = metadata.String
		ev.CreatedAt, _ = parseTimestamp(createdAt)
		events = append(events, &ev)
	}
	return events, rows.Err()
}
